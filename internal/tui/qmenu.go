package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"evalctl/internal/runner"
)

// The skill's own screen: the three views the lane has, and which one to walk into.
//
// It is the same split as the door and the skill chooser — the choice on the left, the row under
// the cursor said properly on the right — because the lane's three jobs are a choice, not a step:
// you come back to the set to add an assertion, to an iteration to read what failed, to the table to
// see whether it is getting better. Landing in one of them (which is what the old rail did, picking
// the section its disk last touched) answers a question nobody asked.
//
// The cursor is the one thing the disk decides: the view that is asking for work is where the lane
// opens, and the pane on the right says why in the view's own terms.

func (m *Model) qualityMenuBody(h int) []string {
	listW := qualityMenuListWidth(m.width)
	paneW := min(96, m.width-listW-3)
	if paneW < 38 {
		return m.qualityMenuNarrow(h)
	}
	return m.splitBody(h, listW, paneW, m.qualityMenuList(listW), m.qualityMenuPane(paneW, h))
}

// qualityMenuListWidth is the list's column: wide enough for "testing quality" and its state under
// it, and no wider — the pane is where the sentence about the view lives.
func qualityMenuListWidth(w int) int { return min(40, max(32, w/3)) }

// qualityMenuListName is what the list calls itself, in the register every region uses. It is the
// same word the trigger lane's chooser uses, because it is the same screen with three rows instead
// of two.
const qualityMenuListName = "SECTIONS"

// qualityMenuList is the three views: the name, and under it what that view currently holds. A row
// with a state line is two lines and no divider — the gutter between rows is the blank one. A view
// the closed loop cannot reach is drawn dim, with the reason in place of its numbers.
func (m *Model) qualityMenuList(w int) []string {
	q := m.quality
	lines := []string{"", regionHead(w, qualityMenuListName, "", true),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, w-4)))}
	for i, v := range qualitySections {
		mark := "  "
		if i == q.vcursor {
			mark = cursorMark.Render(glyphs.Cursor)
		}
		title := titleStyle.Render(ellipsis(v.name, w-4))
		if m.viewLocked(v.sec) {
			title = dimStyle.Render(ellipsis(v.name, w-4))
		}
		line := mark + title
		sub := "    " + dimStyle.Render(ellipsis(m.menuRowState(v.sec), w-6))
		if i == q.vcursor {
			line, sub = selected(line), selected(sub)
		}
		lines = append(lines, line, sub, "")
	}
	return lines
}

// qualityMenuNarrow is the menu without a pane: the three rows, and the one under the cursor
// explained under itself. Below the width that buys a pane, the explanation cannot live beside the
// thing it explains.
func (m *Model) qualityMenuNarrow(h int) []string {
	q := m.quality
	lines := []string{"", regionHead(m.width, qualityMenuListName, "", true),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, m.width-4)))}
	for i, v := range qualitySections {
		mark := "  "
		if i == q.vcursor {
			mark = cursorMark.Render(glyphs.Cursor)
		}
		line := mark + titleStyle.Render(v.name)
		if m.viewLocked(v.sec) {
			line = mark + dimStyle.Render(v.name)
		}
		line += "  " + dimStyle.Render(m.menuRowState(v.sec))
		if i == q.vcursor {
			line = selected(line)
		}
		lines = append(lines, line)
		if i == q.vcursor {
			blurb := v.blurb
			if m.viewLocked(v.sec) {
				blurb = "locked — " + m.lockedReason()
			}
			for _, l := range wrapPlain(blurb, max(20, m.width-6)) {
				lines = append(lines, "    "+dimStyle.Render(l))
			}
		}
		lines = append(lines, "")
	}
	return fitRows(lines, h)
}

// qualityMenuPane is the row under the cursor, said properly: what the view holds, what it is for,
// and the one key that walks in. The blocks are placed from the top down with the action reserved
// first, so a short window loses the explanation and never the key.
func (m *Model) qualityMenuPane(w, h int) []string {
	q := m.quality
	v := qualitySections[q.vcursor]
	d := &docWriter{w: w, label: 14}
	var blocks [][]string
	from := len(d.lines)
	block := func() {
		blocks = append(blocks, append([]string(nil), d.lines[from:]...))
		from = len(d.lines)
	}

	d.head(strings.ToUpper(v.name), false)
	d.rule()
	// The rule the view works under, not the row's numbers: the list already carries the state, and
	// a pane that repeats it has said one thing twice on one screen.
	d.lead(v.blurb)
	block()

	// A locked view gets its description and not its key list: the keys of a screen that will not
	// open are a promise the menu cannot keep.
	locked := m.viewLocked(v.sec)
	switch v.sec {
	case qSetup:
		d.para("A case is a prompt, the expected output, the fixtures it stages and the assertions a judge reads — and an assertion is a sentence a PASS or a FAIL can be written against, which a total is not. Everything a run and a grade read comes from this file.")
		block()
		d.card("It writes")
		d.fact("the set", m.skill+"/evals/evals.json")
		d.fact("fixtures", m.skill+"/evals/files/<slug>/")
		block()
	case qDojo:
		d.para("The assertions that failed with the judge's evidence for each, the arm's reply and its transcript, and the feedback you write about the output. The revision is asked for from here, because this is where the failure is.")
		block()
		if locked {
			break
		}
		d.card("The keys")
		d.fact("f", "the feedback for the case under the cursor")
		d.fact("p", "a revision of SKILL.md — the diff lands on its own screen")
		d.fact("E", "SKILL.md, in $EDITOR")
		block()
	default:
		d.para("The delta between the two arms, and which complete iteration is the best. A rate exists only where the judge left grades, and a run that skipped part of the set never ranks.")
		block()
		if locked {
			break
		}
		d.card("The keys")
		d.fact("enter", "that iteration's evidence, in the iteration view")
		d.fact("r", "the run's own report.md")
		d.fact("g", "grade · re-grade the iteration on screen")
		block()
	}

	act := &docWriter{w: w}
	var action []string
	if locked {
		// A locked row has no action line: the key that opens this view is in the set's view, and an
		// `enter` offered for a screen that will not open is the one lie this menu could tell.
		action = append(action, "")
		for _, line := range wrapPlain("locked — "+m.lockedReason(), w-4) {
			action = append(action, "  "+dimStyle.Render(line))
		}
	} else {
		switch v.sec {
		case qSetup:
			act.action("enter", "the cases · S opens evals.json, e writes the assertions")
		case qDojo:
			if q.iter > 0 {
				act.action("enter", fmt.Sprintf("iteration %d — read the failures, f the feedback, p a revision", q.iter))
			} else {
				act.action("enter", "the iteration view · nothing has run yet, R runs iteration 1")
			}
		default:
			act.action("enter", "the iterations table")
		}
		action = act.lines
	}
	room := h - len(action)
	var out []string
	for _, b := range blocks {
		if len(out)+len(b) > room {
			break
		}
		out = append(out, b...)
	}
	return fitRows(append(out, action...), h)
}

// viewLocked is whether the closed loop keeps a view shut. Until a graded run exists there is nothing
// in the iteration or in the table that is not a blank, and the only way to open the loop is the
// declaration — so the set is the one view a closed loop offers, and the other two say why they are
// shut instead of opening on a screen that answers "nothing measured yet".
func (m *Model) viewLocked(sec int) bool {
	q := m.quality
	return q != nil && q.lab != nil && q.lab.InitPending && sec != qSetup
}

// lockedReason is what opens the loop, in the band's own words: one sentence, because the status row
// and the pane both print it and neither has room for the whole checklist. The sentence already names
// the key it wants; a second "press e" on the end of it would be the same fact twice.
func (m *Model) lockedReason() string {
	q := m.quality
	if q == nil || q.lab == nil {
		return "the loop is not open yet"
	}
	if _, text := gateNext(q.lab); text != "" {
		return text
	}
	return "the loop is not open yet"
}

// menuRowState is a row's second line: the view's own numbers, or the one fact that keeps it shut. A
// locked row says only *that* it is locked — the errand that opens it is a sentence, and a sentence in
// a list column is a column of ellipses; the pane on the right carries it whole.
func (m *Model) menuRowState(sec int) string {
	if m.viewLocked(sec) {
		return "locked until the loop opens"
	}
	return m.viewState(sec)
}

// viewState is one view's state in the menu's own terms: the numbers that decide which of the three
// you would walk into. It is the same reading the rail's facts give, split by view instead of pooled,
// because the menu is answering "where is the work" and the rail is answering "how far has it got".
func (m *Model) viewState(sec int) string {
	q := m.quality
	if q == nil || q.lab == nil {
		return ""
	}
	lab := q.lab
	switch sec {
	case qSetup:
		switch {
		case lab.Cases == 0:
			return "no cases yet — S opens evals.json"
		case lab.Unasserted > 0:
			return fmt.Sprintf("%d of %s carry no assertion", lab.Unasserted, plural(lab.Cases, "case"))
		}
		return fmt.Sprintf("%s · %s", plural(lab.Cases, "case"), plural(lab.Assertions, "assertion"))
	case qDojo:
		if n := len(lab.Iterations); n == 0 {
			return "nothing run yet"
		}
		last := lab.Iterations[len(lab.Iterations)-1]
		switch {
		case len(last.Failed) > 0:
			return fmt.Sprintf("iteration #%d · %s failed", last.N, plural(len(last.Failed), "assertion"))
		case !last.Complete:
			return fmt.Sprintf("iteration #%d not fully graded", last.N)
		}
		return fmt.Sprintf("iteration #%d · every assertion passed", last.N)
	case qQuality:
		n := len(lab.Iterations)
		if n == 0 {
			return "nothing to rank yet"
		}
		state := plural(n, "iteration") + " on disk"
		if lab.Best > 0 {
			state = fmt.Sprintf("best #%d · %s", lab.Best, state)
		}
		return state
	}
	return ""
}

// menuRowFor is which view the disk is asking for: the set while the loop is closed, the iteration
// when its newest run failed, and the table when there is a trend to read — a grade that owes
// verdicts is a trend question, because the answer to "is this better" is what `g` unlocks.
func menuRowFor(lab *runner.QualityLab) int {
	if lab == nil || lab.InitPending || len(lab.Iterations) == 0 {
		return qSetup
	}
	if last := lab.Iterations[len(lab.Iterations)-1]; len(last.Failed) > 0 {
		return qDojo
	}
	return qQuality
}

// openQualityView walks into one of the three — from the menu, by enter or by number. A view the
// closed loop keeps shut refuses here rather than opening, and the status row says what opens it: the
// menu is the only door, so it is also the only place a locked door can be tried.
func (m *Model) openQualityView(sec int) {
	if m.quality == nil || m.quality.lab == nil {
		return
	}
	sec = min(max(qSetup, sec), qQuality)
	if m.viewLocked(sec) {
		m.setStatus(qualitySections[sec].name+" is locked until the loop opens — "+m.lockedReason(), true)
		return
	}
	m.quality.vcursor = sec
	m.mode = screenQuality
	m.setQualitySection(sec)
	if sec == qDojo {
		// The iteration view is read case by case, and it opens on the first case that failed: a
		// view that opens on a passing case answers "what went wrong" with "nothing".
		m.quality.selectFirstFailed()
	}
}

// moveQualityCursor steps the menu's cursor onto the next view the loop offers, in step's direction,
// and stays put when there is none. The shut rows keep their place in the list — seeing what the loop
// will open is why the menu draws them at all — but the cursor is only ever on a view `enter` would
// actually open: parking it on a row that can only refuse is the same dead key the rail stopped
// drawing numbers for.
func (m *Model) moveQualityCursor(step int) {
	q := m.quality
	for i := q.vcursor + step; i >= 0 && i < len(qualitySections); i += step {
		if !m.viewLocked(qualitySections[i].sec) {
			q.vcursor = i
			return
		}
	}
}

// edgeQualityCursor is `g` and `G`: the first or last view the loop offers, not the first or last
// row — a closed loop then leaves the cursor on the set, where it already was.
func (m *Model) edgeQualityCursor(last bool) {
	q := m.quality
	if last {
		for i := len(qualitySections) - 1; i >= 0; i-- {
			if !m.viewLocked(qualitySections[i].sec) {
				q.vcursor = i
				return
			}
		}
		return
	}
	for i := range qualitySections {
		if !m.viewLocked(qualitySections[i].sec) {
			q.vcursor = i
			return
		}
	}
}

// openRows is how many of the three the loop offers right now. Only the key band reads it: one open
// row means the move keys have nowhere to go, and a hint is a promise the screen has to keep.
func (m *Model) openRows() int {
	n := 0
	for _, v := range qualitySections {
		if !m.viewLocked(v.sec) {
			n++
		}
	}
	return n
}

func (m *Model) updateQualityMenu(msg tea.KeyMsg) tea.Cmd {
	q := m.quality
	if q == nil {
		return nil
	}
	switch msg.String() {
	case "j", "down":
		m.moveQualityCursor(1)
	case "k", "up":
		m.moveQualityCursor(-1)
	case "g", "home":
		m.edgeQualityCursor(false)
	case "G", "end":
		m.edgeQualityCursor(true)
	case "enter":
		m.openQualityView(qualitySections[q.vcursor].sec)
	case "1", "2", "3":
		// The rail's numbers are offered here too: the menu is the one screen that lists the views,
		// so pressing the number the rail would use must not do nothing.
		m.openQualityView(int(msg.String()[0] - '1'))
	}
	return nil
}

// qualityMenuHints is the menu's key band: the move, the open, and the level above. The move keys are
// banded only while there is somewhere to move to — a closed loop offers one row, and a `j k` or a
// `1 2 3` that cannot change the row is the same dead key the rail stopped drawing numbers for.
func (m *Model) qualityMenuHints() []hint {
	if m.openRows() < 2 {
		return []hint{{"enter", "open"}, {"esc", "skills"}}
	}
	return []hint{{"j k", "the three views"}, {"enter", "open"}, {"1 2 3", "open one"}, {"esc", "skills"}}
}

// qualityMenuPosition is the status row's right corner on the menu: which of the three the cursor is
// on. The menu is one area and its three rows are short, so the region is not named — naming it
// would say the obvious and cost the position its room.
func (m *Model) qualityMenuPosition() string {
	if m.quality == nil {
		return ""
	}
	return fmt.Sprintf("%d / %d", m.quality.vcursor+1, len(qualitySections))
}
