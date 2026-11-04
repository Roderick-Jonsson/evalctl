package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"evalctl/internal/diff"
	"evalctl/internal/runner"
)

// Step 1 of the guide's loop, with an agent doing the writing and a human doing the deciding.
//
// The dojo has always ended with "revise SKILL.md"; that sentence was the operator's own work in
// their editor. `p` now asks the revise sandbox for a proposal from the three things the guide names
// — the failed assertions with the judge's evidence, the feedback, and the current SKILL.md — and
// lands it on a screen where the diff is the whole point. Nothing is written until `a`: a revision is
// a hypothesis about the next iteration, and the one thing this screen must never do is apply a
// hypothesis silently.

type revisionState struct {
	proposal runner.Revision
	inputs   runner.RevisionInputs
	lines    []diff.Line
	hunks    []diff.Hunk
	// off holds the hunks the operator unticked. It is a set of exceptions rather than of selections
	// because the default is "the reviser's whole proposal": a screen that starts with nothing
	// accepted makes the common case (accept it all) the long way round.
	off map[int]bool
	// cursor and top are rows of the rendered block, not source lines: a hunk header and a wrapped
	// tail are rows a reader can stand on, and the selection follows the row they are standing on.
	// view is the last frame's row count, for the scroll arithmetic.
	cursor, top, view int
	// tmp is the edit buffer handed to $EDITOR by `e`. The proposal stays the text that gets applied;
	// the file exists so a person can change it in the editor they already use.
	tmp string
}

// selected reports whether a hunk is part of what `a` would write.
func (st *revisionState) selected(i int) bool { return !st.off[i] }

// selectedCount is how many hunks survive.
func (st *revisionState) selectedCount() int {
	n := 0
	for i := range st.hunks {
		if st.selected(i) {
			n++
		}
	}
	return n
}

// accepted is the text `a` writes: the before-side of the hunks that were unticked, the after-side of
// the rest. With nothing ticked off it is the reviser's file; with everything off it is the original.
func (st *revisionState) accepted() string {
	return diff.Apply(st.lines, st.hunks, st.selected)
}

// startRevision runs one revision call for the iteration on screen.
func (m *Model) startRevision() tea.Cmd {
	q := m.quality
	if q == nil || q.lab == nil || q.iter == 0 {
		m.setStatus("a revision answers a finished iteration — R runs one first", true)
		return nil
	}
	if len(q.fails) == 0 && reviewedCases(q.cases) == 0 {
		m.setStatus("nothing to revise from — no failed assertion and no feedback on this iteration", true)
		return nil
	}
	skill, iter := m.skill, q.iter
	m.run = &runState{kind: runRevise, scope: fmt.Sprintf("revision · %s@%d", skill, iter),
		hint: "one call to " + runner.ReviseProfile + " — nothing is written until you accept it"}
	m.run.live = newLiveRun(m.run.scope, false)
	m.mode = screenRunLive
	ch := make(chan runner.Event, 16)
	m.runEvents = ch
	ctx, cancel := context.WithCancel(context.Background())
	m.run.live.cancel = cancel
	ch <- runner.Event{Kind: runner.EventStart, Message: fmt.Sprintf("reading iteration %d: %s failed, %d with feedback, and SKILL.md",
		iter, plural(len(q.fails), "assertion"), reviewedCases(q.cases))}

	r := runner.New(m.store)
	r.Emit = func(e runner.Event) {
		select {
		case ch <- e:
		default:
		}
	}
	go func() {
		defer close(ch)
		if _, err := r.Revise(ctx, skill, iter, runner.ReviseOptions{Budget: 5 * time.Minute}); err != nil {
			ch <- runner.Event{Kind: "error", Err: err, Message: err.Error()}
		}
	}()
	return tea.Batch(waitForEvent(ch), tickEvery())
}

// openProposal puts the newest proposal for an iteration on screen. It is what a finished call lands
// on, and it is also what a screen reopened later shows: a proposal is on disk, so reading it again
// costs nothing.
func (m *Model) openProposal(rev runner.Revision, in runner.RevisionInputs) {
	lines := diff.Lines(rev.Baseline, rev.Text)
	m.revision = &revisionState{proposal: rev, inputs: in, lines: lines,
		hunks: diff.Hunks(lines, diffContext), off: map[int]bool{}}
	// The screen opens on the first change: a diff that starts on the file's unchanged preamble makes
	// the reader scroll to find out what it is being asked to accept.
	rows := m.revisionRows(m.width)
	for i, r := range rows {
		if r.kind == rowHunk {
			m.revision.cursor = i
			m.revision.top = max(0, i)
			break
		}
	}
	m.mode = screenRevision
	m.setStatus("proposal ready — a applies · e edits · esc discards", false)
}

// revisionRunBody is the live screen for one revision call: what it is reading, and how long it has
// been reading it. There are no cases to tick here — the call is one agent reading and answering.
func (m *Model) revisionRunBody(h int) []string {
	p := m.run.live
	d := &docWriter{w: m.width, label: 10}
	d.head("REVISION · "+m.skill, false)
	d.rule()
	d.lead(fmt.Sprintf("one call to %s · %s elapsed", runner.ReviseProfile, shortDuration(time.Since(p.started))))
	d.para("It reads the failed assertions with the judge's evidence, the feedback on this iteration, and SKILL.md — then answers with a whole new skill. Nothing is written until you accept the proposal.")
	if p.err != nil {
		d.lines = append(d.lines, "", "  "+statusBad.Render("! "+p.err.Error()))
	}
	if p.done {
		d.lines = append(d.lines, "", "  "+styleOn.Render("the proposal is on disk — enter reads it"))
	}
	for _, line := range p.log {
		d.lines = append(d.lines, "  "+dimStyle.Render(ellipsis(line, max(10, d.body()))))
	}
	return fitRows(d.lines, h)
}

// revisionBody is the proposal: why, and the diff against the skill as it stands. The diff is the
// screen, because the sentence a reviser writes about its own change is the least reliable thing it
// produces and the lines are the most.
func (m *Model) revisionBody(h int) []string {
	st := m.revision
	if st == nil {
		return fitRows([]string{"", "  " + dimStyle.Render("no proposal open")}, h)
	}
	rev := st.proposal
	added, removed := diff.StatOf(st.hunks, st.selected)
	w := m.width
	right := fmt.Sprintf("+%d −%d · %s", added, removed, plural(len(st.hunks), "hunk"))
	if !diff.Changed(st.lines) {
		right = "no change"
	} else if n := st.selectedCount(); n < len(st.hunks) {
		right += fmt.Sprintf(" · %d of %d kept", n, len(st.hunks))
	}
	out := []string{"", regionHead(w, "PROPOSED REVISION · SKILL.md", right, true), gateRule(w)}

	// The action is reserved first: on a short window the diff loses lines, the key does not.
	act := &docWriter{w: w}
	switch {
	case !diff.Changed(st.lines):
		act.action("esc", "discard — this proposal changes nothing")
	case st.selectedCount() == 0:
		act.action("space", "tick a hunk — nothing is selected, so `a` would write the file unchanged")
	default:
		act.action("a", fmt.Sprintf("apply %s to SKILL.md — space ticks a hunk off, n jumps, e edits",
			plural(st.selectedCount(), "hunk")))
	}
	room := max(1, h-len(out)-len(act.lines))

	d := &docWriter{w: w, label: 10}
	d.para(fmt.Sprintf("from iteration %d · read %s failed · %d with feedback · %s",
		rev.Iteration, plural(st.inputs.Failed, "case"), st.inputs.Feedbacks, runner.ReviseProfile))
	if strings.TrimSpace(rev.Why) != "" {
		d.card("why")
		// The reviser's own account is the least reliable thing it produces; it gets three lines above
		// the diff and no more, because the screen is the lines.
		why := wrapPlain(strings.Join(strings.Fields(rev.Why), " "), max(10, d.body()))
		if len(why) > 3 {
			why = append(why[:3], "…")
		}
		for _, line := range why {
			d.lines = append(d.lines, "  "+colText.Render(line))
		}
	}
	d.card("the diff")
	out = append(out, d.lines...)
	if len(out) >= room {
		return fitRows(append(out, act.lines...), h)
	}

	// The diff scrolls by row; the marks are the same three the rest of the app uses — a tick for what
	// stays, an accent for what arrives, red for what goes — and a line of a skill you cannot read is a
	// line you cannot approve, so a long one wraps under its mark instead of being cut.
	rows := m.revisionRows(w)
	view := room - len(out)
	st.view = max(1, view)
	st.cursor = min(st.cursor, max(0, len(rows)-1))
	st.top = min(max(0, st.top), max(0, len(rows)-view))
	if st.cursor < st.top {
		st.top = st.cursor
	}
	if st.cursor >= st.top+view {
		st.top = st.cursor - view + 1
	}
	for i := st.top; i < len(rows); i++ {
		if len(out) >= room {
			out = append(out, "  "+dimStyle.Render(fmt.Sprintf("… %d more rows — j walks, n jumps",
				max(0, len(rows)-st.top-view))))
			break
		}
		out = append(out, m.renderRow(rows[i], w, i == st.cursor))
	}
	return fitRows(append(out, act.lines...), h)
}

// The diff is read as hunks, not as a file: the changes with three lines around them, the untouched
// middle folded away, a header per neighbourhood, and a tick per neighbourhood that `a` writes. It is
// the shape lazygit and every other diff reader converged on, and the reason is the one that matters
// here — a revision is judged hunk by hunk ("the arithmetic, yes; the terseness rule, no"), and a
// single "apply everything" is a decision nobody can make on a 200-line skill.
const diffContext = 3

type revRowKind int

const (
	rowFold revRowKind = iota // "… 24 unchanged lines"
	rowHunk                   // "@@ -8,4 +8,6 @@  [x]  2 changes"
	rowLine                   // one line of a hunk, wrapped tails included
)

type revRow struct {
	kind revRowKind
	text string
	line diff.Kind // rowLine only: what happened to this line
	hunk int       // the hunk this row belongs to; -1 when it belongs to none
	lead bool      // the first row of a wrapped source line, which carries the mark and the numbers
	// oldNo and newNo are the two files' line numbers, so a reader can point at a line in an editor.
	oldNo, newNo int
}

// revisionRows is the whole block the screen scrolls: hunk headers, their lines, and a fold between
// neighbourhoods. It is rebuilt each frame from the state, which is cheap and keeps the selection the
// only thing that persists.
func (m *Model) revisionRows(w int) []revRow {
	st := m.revision
	if st == nil {
		return nil
	}
	width := max(10, w-gutterWidth-2)
	var out []revRow
	prev := 0
	for hi, h := range st.hunks {
		if h.From > prev {
			out = append(out, revRow{kind: rowFold, hunk: -1,
				text: fmt.Sprintf("%d unchanged", h.From-prev)})
		}
		out = append(out, revRow{kind: rowHunk, hunk: hi,
			text: fmt.Sprintf("- %d,%d + %d,%d", h.OldStart, h.OldCount, h.NewStart, h.NewCount),
			lead: true})
		oldNo, newNo := h.OldStart, h.NewStart
		for _, l := range h.Lines {
			parts := wrapPlain(l.Text, width)
			if len(parts) == 0 {
				parts = []string{""}
			}
			for i, part := range parts {
				row := revRow{kind: rowLine, line: l.Kind, hunk: hi, text: part, lead: i == 0}
				if i == 0 {
					switch l.Kind {
					case diff.Del:
						row.oldNo = oldNo
					case diff.Add:
						row.newNo = newNo
					default:
						row.oldNo, row.newNo = oldNo, newNo
					}
				}
				out = append(out, row)
			}
			switch l.Kind {
			case diff.Del:
				oldNo++
			case diff.Add:
				newNo++
			default:
				oldNo, newNo = oldNo+1, newNo+1
			}
		}
		prev = h.To
	}
	if prev < len(st.lines) {
		out = append(out, revRow{kind: rowFold, hunk: -1, text: fmt.Sprintf("%d unchanged", len(st.lines)-prev)})
	}
	return out
}

const gutterWidth = 12 // two 4-wide line-number columns, a separator and the mark

// renderRow is one row as the frame draws it. An unticked hunk goes dim, header included: the screen
// has to show what will NOT be written as plainly as what will.
func (m *Model) renderRow(r revRow, w int, isCursor bool) string {
	st := m.revision
	on := r.hunk < 0 || st.selected(r.hunk)
	switch r.kind {
	case rowFold:
		label := "⋯ " + r.text
		if isCursor {
			return "  " + selected(dimStyle.Render(label))
		}
		return "  " + dimStyle.Render(label)
	case rowHunk:
		box := "[x]"
		if !on {
			box = "[ ]"
		}
		// The hunk's own weight, in the same +A −R the head uses: "2 changes" for one edited line is
		// the kind of count that makes a reader distrust the numbers.
		changes := ""
		if r.hunk >= 0 && r.hunk < len(st.hunks) {
			h := st.hunks[r.hunk]
			added, removed := diff.StatOf([]diff.Hunk{h}, nil)
			changes = fmt.Sprintf("+%d −%d", added, removed)
			if h.Changes == 0 {
				changes = "context only"
			}
		}
		left := "  " + colSplit.Render("@@ "+r.text+" @@") + "  " + box + "  " + dimStyle.Render(changes)
		if isCursor {
			return selected(left)
		}
		if !on {
			return dimStyle.Render(left)
		}
		return left
	}
	gutter := " " + padCell(numOr(r.oldNo), 4, true) + " " + padCell(numOr(r.newNo), 4, true) + " "
	if !r.lead {
		gutter = strings.Repeat(" ", gutterWidth-2)
	}
	text := ellipsis(r.text, max(10, w-gutterWidth-2))
	mark := "  "
	switch r.line {
	case diff.Add:
		mark = "+ "
	case diff.Del:
		mark = "- "
	}
	if !r.lead {
		mark = "  "
	}
	// Three registers: the numbers and context are quiet, additions carry the accent, removals the
	// error colour. An unticked hunk keeps its marks and its numbers and goes entirely quiet — the
	// point of unticking is to have read it, and a line stripped of its sign cannot be read as a
	// rejection.
	numStyle, markStyle, textStyle := dimStyle, styleOn, colText
	if r.line == diff.Del {
		markStyle, textStyle = statusBad, dimStyle
	}
	if !on {
		numStyle, markStyle, textStyle = dimStyle, dimStyle, dimStyle
	}
	line := numStyle.Render(gutter) + markStyle.Render(mark) + textStyle.Render(text)
	if isCursor {
		return selected(line)
	}
	return line
}

func numOr(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}

// updateRevision is the proposal's keys: the diff walks and one key writes. `space` is the operator's
// veto on a hunk, which is the whole reason the diff is cut into hunks.
func (m *Model) updateRevision(msg tea.KeyMsg) tea.Cmd {
	st := m.revision
	if st == nil {
		return nil
	}
	rows := m.revisionRows(m.width)
	switch msg.String() {
	case "j", "down":
		st.cursor = min(st.cursor+1, max(0, len(rows)-1))
		m.scrollToCursor(len(rows))
	case "k", "up":
		st.cursor = max(0, st.cursor-1)
		m.scrollToCursor(len(rows))
	case "n":
		for i := st.cursor + 1; i < len(rows); i++ {
			if rows[i].kind == rowHunk {
				st.cursor = i
				break
			}
		}
		m.scrollToCursor(len(rows))
	case "N":
		for i := st.cursor - 1; i >= 0; i-- {
			if rows[i].kind == rowHunk {
				st.cursor = i
				break
			}
		}
		m.scrollToCursor(len(rows))
	case " ":
		if st.cursor < len(rows) {
			h := rows[st.cursor].hunk
			if h >= 0 {
				if st.off[h] {
					delete(st.off, h)
				} else {
					st.off[h] = true
				}
			}
		}
	case "g", "home":
		st.cursor, st.top = 0, 0
	case "G", "end":
		st.cursor = max(0, len(rows)-1)
		m.scrollToCursor(len(rows))
	case "a":
		return m.applyRevision()
	case "e":
		return m.editProposal()
	case "enter", "esc":
		m.revision = nil
		m.setQualitySection(qDojo)
		m.mode = screenQuality
		m.setStatus("the proposal is still on disk — p opens it again, and SKILL.md is untouched", false)
	}
	return nil
}

// scrollToCursor keeps the cursor row inside the window it was moved in.
func (m *Model) scrollToCursor(total int) {
	st := m.revision
	if st == nil {
		return
	}
	view := m.revision.view
	if view <= 0 {
		view = 10
	}
	if st.cursor < st.top {
		st.top = st.cursor
	}
	if st.cursor >= st.top+view {
		st.top = st.cursor - view + 1
	}
	st.top = min(max(0, st.top), max(0, total-view))
}

// applyRevision is the only path that lets a machine's answer overwrite a skill body.
func (m *Model) applyRevision() tea.Cmd {
	st := m.revision
	if st == nil || !diff.Changed(st.lines) {
		return nil
	}
	text := st.accepted()
	if strings.TrimSpace(text) == "" {
		m.setStatus("nothing is selected — tick a hunk with space before applying", true)
		return nil
	}
	rev := st.proposal
	rev.Text = text
	if err := runner.New(m.store).ApplyRevision(m.skill, rev); err != nil {
		m.setStatus(err.Error(), true)
		return nil
	}
	m.revision = nil
	m.stamp = ""
	m.syncTree()
	m.setQualitySection(qDojo)
	m.mode = screenQuality
	m.reload()
	m.setStatus(fmt.Sprintf("%s revised — R runs iteration %d against it", m.skill, rev.Iteration+1), false)
	return nil
}

// editProposal hands the proposal to $EDITOR. The buffer is a temp file, not the proposal's own
// record: what gets applied is the text this screen shows, and an abandoned edit must not leave a
// half-revised document beside the iteration.
func (m *Model) editProposal() tea.Cmd {
	st := m.revision
	if st == nil {
		return nil
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("evalctl-revision-%d.md", time.Now().UnixNano()))
	if err := os.WriteFile(path, []byte(st.proposal.Text), 0o644); err != nil {
		m.setStatus(err.Error(), true)
		return nil
	}
	st.tmp = path
	m.setStatus("editing the proposal — the editor's file is what a will apply", false)
	return m.editFile(path, "revision")
}

// reloadProposal re-reads the edit buffer after the editor exits.
func (m *Model) reloadProposal() {
	st := m.revision
	if st == nil || st.tmp == "" {
		return
	}
	b, err := os.ReadFile(st.tmp)
	if err != nil {
		return
	}
	if strings.TrimSpace(string(b)) == "" {
		return
	}
	st.proposal.Text = string(b)
	st.lines = diff.Lines(st.proposal.Baseline, st.proposal.Text)
	st.tmp = ""
	m.setStatus("the edited proposal is on screen — a applies it", false)
}

// reviewedCases counts the cases whose feedback is not empty: a case with a complaint, as opposed to
// a case whose output looked fine.
func reviewedCases(cases []runner.QualityCase) int {
	n := 0
	for _, c := range cases {
		if strings.TrimSpace(c.Feedback) != "" {
			n++
		}
	}
	return n
}
