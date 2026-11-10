package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"evalctl/internal/diff"
	"evalctl/internal/llm"
	"evalctl/internal/runner"
)

// The description, rewritten by a model, on the pane it is edited in.
//
// `p` on the workbench asks one provider call for a new paragraph, built from the only evidence the
// guide allows a revision to learn from: the train failures and their verdicts. What comes back is
// not applied anywhere. It lands on the description pane as a character-level diff — the paragraph as
// it reads now, with the words that go in red and the words that arrive in green — and `y` puts it
// into the editable field, where it is a draft like any other: edit it, save it, run it.
//
// The screen is the pane and not a screen of its own on purpose. The revision of SKILL.md has hunks
// to tick and a file to write, so it earns a full screen; a description is one paragraph, and a
// paragraph belongs where the caret already is. The cost of a different layout is real though, and it
// is paid in the same coin as everything else on this workbench: when the proposal is up, the row it
// lives in grows — a 600-character paragraph wrapped to a 40-cell pane is 15 lines, and a diff you
// cannot read is a diff you cannot decide on.

// describeState is one proposal, as the pane draws it.
//
// segs is the whole diff, computed once when the proposal lands: the pane wraps it per frame instead,
// because the width it has is the window's and not the call's.
type describeState struct {
	cand     int
	baseline string
	text     string
	why      string
	model    string
	chars    int
	segs     []diff.Seg
	// issues are the checks the paragraph failed even after its repair: shown, not hidden, because a
	// proposal a person can see the flaw in is worth more than no proposal at all.
	issues []string
	// top and view are the diff's scroll: the paragraph is rarely one pane tall, and a reader who
	// cannot see the end of it cannot say whether to keep it.
	top, view int
}

// startDescribe asks for one proposal for the wording on screen.
//
// Every refusal below is a state the call cannot be built from, said before anything is spent: a
// proposal answers failures, and failures describe a text that a run measured. A draft the operator
// is in the middle of writing is not that text yet, which is the one refusal worth spelling out —
// "run the set" would otherwise read as an obstacle rather than a step.
func (m *Model) startDescribe() tea.Cmd {
	f := m.flow
	if f == nil || f.lab == nil {
		return nil
	}
	if f.sec != secIteration {
		m.setStatus("a description is revised in the iteration half — s goes back to the train set", true)
		return nil
	}
	if pend := f.pending(); pend != nil {
		m.setStatus(fmt.Sprintf("candidate #%d is written but not measured — p answers a run's failures, so press enter to run it first", pend.ID), true)
		return nil
	}
	it := f.latest()
	if it == nil {
		m.setStatus("nothing measured yet — a proposal is written from the failures of a wording that ran", true)
		return nil
	}
	if len(it.Cand.FailedTrain()) == 0 {
		m.setStatus(fmt.Sprintf("candidate #%d passes every train query — there is no failure to revise from", it.Cand.ID), true)
		return nil
	}
	if f.draftChanged() {
		m.setStatus("the field holds edits no run has seen — ctrl+s saves them as the next candidate, enter runs it", true)
		return nil
	}
	cfg, err := llm.FromEnv()
	if err != nil {
		m.setStatus(err.Error(), true)
		return nil
	}
	skill, cand, iter := m.skill, it.Cand.ID, it.Train.Iteration
	m.run = &runState{
		kind: runDescribe, cand: cand, describeIter: iter,
		scope: fmt.Sprintf("description · candidate #%d", cand),
		hint:  fmt.Sprintf("one call to %s · key from %s — the paragraph lands in the description pane", cfg.Model, cfg.KeyFrom),
	}
	m.run.live = newLiveRun(m.run.scope, false)
	m.mode = screenRunLive

	ch := make(chan runner.Event, 16)
	m.runEvents = ch
	ctx, cancel := context.WithCancel(context.Background())
	m.run.live.cancel = cancel
	ch <- runner.Event{Kind: runner.EventStart, Message: fmt.Sprintf(
		"reading candidate #%d: %d failed train quer%s and the wording they were measured against",
		cand, len(it.Cand.FailedTrain()), plural(len(it.Cand.FailedTrain()), "y"))}

	r := runner.New(m.store)
	r.Emit = func(e runner.Event) {
		select {
		case ch <- e:
		default:
		}
	}
	go func() {
		defer close(ch)
		prop, err := r.ReviseDescription(ctx, skill, cand, runner.DescribeOptions{
			Budget: runner.DescriptionBudget, Config: &cfg,
		})
		if err != nil {
			ch <- runner.Event{Kind: "error", Err: err, Message: err.Error()}
			return
		}
		added, removed := diff.SegmentsStat(diff.Segments(prop.Baseline, prop.Text))
		ch <- runner.Event{Kind: runner.EventMessage, Message: fmt.Sprintf(
			"the proposal is on disk · %d chars · +%d −%d", prop.Chars, added, removed)}
	}()
	return tea.Batch(waitForEvent(ch), tickEvery())
}

// openDescriptionProposal puts the answer on the description pane. The keys go back to the set first:
// the paragraph owns the caret only after `y`, so typing can never land in a text that is about to be
// replaced.
func (m *Model) openDescriptionProposal(p runner.DescriptionProposal) {
	f := m.flow
	if f == nil {
		return
	}
	f.proposal = &describeState{
		cand: p.Candidate, baseline: p.Baseline, text: p.Text, why: p.Why,
		model: p.Model, chars: p.Chars, segs: diff.Segments(p.Baseline, p.Text),
		issues: p.Issues,
	}
	f.focus = focusSet
	f.blur()
	msg := fmt.Sprintf("a proposal from %s — y keeps it in the field, p asks again, esc discards it", p.Model)
	if len(p.Issues) > 0 {
		msg = fmt.Sprintf("a proposal from %s with %s — read them before you keep it", p.Model, plural(len(p.Issues), "problem"))
	}
	m.setStatus(msg, len(p.Issues) > 0)
}

// keepDescription is the one path from a machine's paragraph into the field. It writes no file: the
// text becomes a draft, and the draft is saved by the key that has always saved it.
func (m *Model) keepDescription() tea.Cmd {
	f := m.flow
	p := f.proposal
	if p == nil || f.draft == nil {
		return nil
	}
	f.draft.text.SetValue(p.text)
	f.proposal = nil
	f.focus = focusWording
	f.applyFocus()
	m.setStatus(fmt.Sprintf("the proposal is in the field (%d chars) — edit it, ctrl+s stages it as the next draft", p.chars), false)
	return nil
}

// discardDescription drops the proposal. It costs nothing to get back — p asks again — and it says so,
// because a discarded answer that needed a call should not read as a mistake.
func (m *Model) discardDescription() {
	if m.flow == nil || m.flow.proposal == nil {
		return
	}
	m.flow.proposal = nil
	m.setStatus("the proposal is dropped — the field is unchanged, and p asks for another", false)
}

// updateProposal is the pane's own keys while a proposal is up: keep it, ask again, or read it.
func (m *Model) updateProposal(msg tea.KeyMsg) tea.Cmd {
	f := m.flow
	p := f.proposal
	switch msg.String() {
	case "y":
		return m.keepDescription()
	case "p":
		// Asking again overwrites the proposal on disk: it answers the same failures, so the second
		// answer is a second opinion and not a second record.
		return m.startDescribe()
	case "j", "down":
		p.top++
	case "k", "up":
		p.top = max(0, p.top-1)
	case "g", "home":
		p.top = 0
	case "G", "end":
		p.top = 1 << 20 // clamped against the wrapped length when the pane is drawn
	}
	return nil
}

// describeRunBody is the live screen for one proposal call: what it is reading, from where, and how
// long it has been reading it.
func (m *Model) describeRunBody(h int) []string {
	p := m.run.live
	d := &docWriter{w: m.width, label: 10}
	d.head("DESCRIPTION PROPOSAL · "+m.skill, false)
	d.rule()
	d.lead(fmt.Sprintf("%s · %s elapsed", m.run.hint, shortDuration(time.Since(p.started))))
	d.para("It reads the failures of one measured wording and the description that produced them, and answers with one paragraph. Nothing is written: the paragraph lands in the description pane as a diff, and y puts it in the field.")
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

// quadProposal is the description pane while a proposal is in it: the paragraph as it would read,
// with what leaves it in red and what arrives in green, and the one key that accepts it.
func (m *Model) quadProposal(w, h int) []string {
	f := m.flow
	p := f.proposal
	added, removed := diff.SegmentsStat(p.segs)
	head := endsLine(blockMark.Render(glyphs.Block)+" "+regionOn.Render("THE DESCRIPTION"),
		dimStyle.Render(fmt.Sprintf("+%d −%d", added, removed))+" ", w)
	lines := []string{"", head, "  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, w-4)))}
	lines = append(lines, "  "+dimStyle.Render(ellipsis(
		fmt.Sprintf("%s · %d chars · red gone, green new", p.model, p.chars), max(10, w-4))))
	if why := strings.TrimSpace(p.why); why != "" {
		lines = append(lines, "  "+paneLabel.Render("why ")+colText.Render(ellipsis(oneLine(why), max(8, w-10))))
	}
	// The checks the paragraph failed after its repair. They are not a footnote: a description that
	// lifts a request's words is the one failure this lane can catch mechanically, and the reader is
	// the last person who can decide whether to accept it anyway.
	for _, issue := range p.issues {
		lines = append(lines, "  "+badgeWarn.Render("! ")+colText.Render(ellipsis(oneLine(issue), max(8, w-10))))
	}

	// The action line is reserved before the diff: a paragraph that loses its last two lines can
	// still be accepted, one that loses its keys cannot. It is short on purpose — `j k scroll` is in
	// the band under the frame, which has the room, and this pane has 44 cells on a small window.
	act := "  " + footKey.Render("y") + footAct.Render(" keep") +
		footAct.Render(" · ") + footKey.Render("p") + footAct.Render(" another") +
		footAct.Render(" · ") + footKey.Render("esc") + footAct.Render(" drop")

	all := diff.WrapSegs(p.segs, max(12, w-4))
	total := len(all)
	// The diff's own room: everything left after the head, minus the blank and the action line.
	room := max(1, h-len(lines)-2)
	// When the paragraph does not fit, one of those rows is the marker: the difference between the
	// end of the text and the end of the pane is the difference between "read it" and "keep reading".
	marker := total > room
	if marker {
		room--
	}
	room = max(1, room)
	p.view = room
	p.top = min(max(0, p.top), max(0, total-room))
	for i := p.top; i < total && i < p.top+room; i++ {
		lines = append(lines, "  "+renderSegLine(all[i]))
	}
	if marker {
		above, below := p.top, total-(p.top+room)
		label := fmt.Sprintf("… %d line%s to go · j scrolls", below, pluralS(below))
		switch {
		case above > 0 && below > 0:
			label = fmt.Sprintf("… %d above, %d to go · j k scrolls", above, below)
		case above > 0:
			label = fmt.Sprintf("… %d line%s above · k scrolls", above, pluralS(above))
		}
		lines = append(lines, "  "+dimStyle.Render(label))
	}
	lines = append(lines, "", act)
	return fitRows(lines, h)
}

// renderSegLine paints one wrapped line of the diff: a removal is red and struck through, an addition
// is green, and what both versions share is the palette's text colour. The strike is load-bearing —
// the old text and the new text sit adjacent in this kind of diff, and a colour alone would leave
// "dont" and "do" reading as one word.
func renderSegLine(segs []diff.Seg) string {
	var b strings.Builder
	for _, s := range segs {
		switch s.Kind {
		case diff.Del:
			b.WriteString(goneStyle.Render(s.Text))
		case diff.Add:
			b.WriteString(newStyle.Render(s.Text))
		default:
			b.WriteString(colText.Render(s.Text))
		}
	}
	return b.String()
}

// pluralS is the plural marker for a count already printed: "1 line" / "2 lines".
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
