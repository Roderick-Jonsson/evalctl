package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// The iteration workbench: four quadrants, because the loop is one decision seen from four sides.
//
//	tl  THE FAILED QUERY   one failing row in full, and what the guide says to do about it
//	tr  TRAIN SET          the queries and the last run's verdict beside each one — read, not edited
//	bl  THE DESCRIPTION    SKILL.md's wording, editable — the thing you actually change
//	br  ITERATIONS         every run so far, with the rate it scored
//
// The failure says what is wrong, the set says how wrong (one row, or half of them), the iteration
// list says whether the last five changes bought anything, and the description is where the answer is
// written. A rung-by-rung screen hid three of those while you worked on the fourth.
//
// The description is the only thing this screen edits, because it is the only thing a revision is:
// the set is written on its own step screen and beside the two sections, and a second editor here
// made the loop's one editable thing look like one of five.

const (
	focusSet = iota
	focusWording
	focusNote
	focusCount
)

// iterDraft is the wording being edited, seeded from the candidate under test.
type iterDraft struct {
	parent   int    // the candidate this text was seeded from
	loaded   string // the text it was seeded with, for "has anything changed"
	loadedIn string // and its note
	text     textarea.Model
	note     textinput.Model
}

func (m *Model) iterBody(h int) []string {
	m.ensureDraft()
	if m.width < 90 || h < 18 {
		return m.iterStacked(h)
	}
	leftW := min(96, max(44, m.width*40/100))
	rightW := m.workbenchPaneWidth()
	topH := max(8, h/2-1)
	botH := max(6, h-topH-1)

	// Two rows of two: what went wrong beside how much of the set it cost, and — under them — where
	// the answer is written beside how far that has got the loop.
	top := m.splitBody(topH, leftW, rightW, m.quadFailure(leftW, topH), m.setPane(rightW, topH))
	bot := m.splitBody(botH, leftW, rightW, m.quadWording(leftW, botH), m.quadIterations(rightW, botH))
	rule := dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, m.width)))
	return fitRows(append(append(top, rule), bot...), h)
}

// workbenchPaneWidth is the width of the workbench's right column. The set's table and the iteration
// list are drawn to it, so the two rows line up on one seam.
func (m *Model) workbenchPaneWidth() int {
	leftW := min(96, max(44, m.width*40/100))
	return m.width - leftW - 3
}

// ---------------------------------------------------------------- quadrants

// quadFailure is one failing row in full and what the guide says to do about that kind of failure.
// Which row it is follows the cursor: if the row you are standing on failed, that is the one; if it
// passed, the first failure is, because the quadrant is about what to fix.
func (m *Model) quadFailure(w, h int) []string {
	f := m.flow
	row := f.failedRow()
	build := func(full bool) []string {
		d := &docWriter{w: w}
		if row == nil {
			// A quadrant that takes no keys names itself in the quiet register, so the one
			// that has them is the only one in the accent.
			d.head("FAILED QUERY", false)
			switch {
			case f.pending() != nil:
				d.lead(fmt.Sprintf("candidate #%d has not been run yet", f.pending().ID))
				d.para("Run the train set: the failures are what the next revision is written from, and there is nothing to read until one exists.")
			case f.latest() != nil:
				it := f.latest()
				d.lead("nothing failed")
				d.para(fmt.Sprintf("This wording passes all %d train queries. A revision now is a hunch — the loop has nothing to tell you, and generalization is where the next real answer is.", it.Train.Train.Judged))
			default:
				d.lead("nothing measured yet")
				d.para("Run the train set against the wording in the description, and its failures appear here.")
			}
			return d.lines
		}
		d.head(fmt.Sprintf("FAILED QUERY · q%d", row.ID), false)
		d.rule()
		d.lead(fmt.Sprintf("should %s · fired %d of %d runs", triggerWord(row.ShouldTrigger), row.Triggers, row.Runs))
		if row.Query != "" {
			d.fact("the ask", row.Query)
		}
		if full && row.Why != "" {
			d.fact("why it is here", row.Why)
		}
		if full {
			d.fact("what loaded", loadedInstead(*row))
		}
		d.fact("verdict", verdictOf(*row))
		d.card("What to adapt")
		for _, line := range adaptHints(*row) {
			d.bullet(line)
		}
		d.para("Never paste a query's words into the description: that teaches the wording the test, not the job.")
		return d.lines
	}
	lines := build(true)
	// The advice is the quadrant's reason to exist: a short column loses the why and the loaded
	// skill before it loses a line of what to do about it.
	if len(lines) > h {
		lines = build(false)
	}
	return fitRows(lines, h)
}

// adaptHints is the guide's advice, narrowed to the failure in front of you. It is deterministic on
// purpose: the same failure always earns the same advice, so it stays checkable against the page it
// came from instead of being an opinion at 2am.
func adaptHints(row runner.ScoreRow) []string {
	switch {
	case row.ShouldTrigger && row.Triggers == 0:
		return []string{
			"Name this context: the ask is the skill's job and the wording never says so.",
			"Err on the side of pushy — say it applies even when the user does not name it.",
			"Describe the intent the user has, not the tool they happen to mention.",
		}
	case !row.ShouldTrigger && row.Triggers > 0:
		return []string{
			"Something in the wording overlaps work that is not this skill's.",
			"Narrow it: say what this skill is for, and where it hands off.",
			"Check which skill did load — that is the wording you are competing with.",
		}
	}
	return []string{
		"The wording is ambiguous here: it fires on some runs and not others.",
		"Make the line between yes and no explicit — say what makes this a yes.",
		"If it keeps happening, the row may be the problem, not the description.",
	}
}

func (m *Model) quadSetRow(i, idW, trigW, resW, queryW int) string {
	f := m.flow
	e := f.rows[i]
	mark := "  "
	// Only the region with the keys draws the cursor, and the band goes with it: a row that is
	// marked in a pane nobody is driving reads as a second cursor, which is the thing this screen
	// had two of.
	on := i == f.sel && f.focus == focusSet
	if on {
		mark = cursorMark.Render(glyphs.Cursor)
	}
	trig := colYes.Render(padCell("yes", trigW, false))
	if e.Label == "no" {
		trig = colNo.Render(padCell("no", trigW, false))
	}
	result := dimStyle.Render(padCell("—", resW, false))
	if p := f.passOf(e.ID); p != nil {
		if *p {
			result = styleOn.Render(padCell("pass", resW, false))
		} else {
			result = statusBad.Render(padCell("FAIL", resW, false))
		}
	}
	line := mark + colID.Render(padCell(fmt.Sprint(e.ID), idW, true)) + "   " + trig + " " +
		result + "  " + colText.Render(padCell(oneLine(e.Text), queryW, false))
	if on {
		line = selected(line)
	}
	return line
}

// quadWording is the description, editable in place: the loop ends here, with a caret, because a
// revision is an edit and not a screen you navigate to.
func (m *Model) quadWording(w, h int) []string {
	f := m.flow
	d := f.draft
	d.text.SetWidth(max(20, w-4))
	d.text.SetHeight(max(2, h-4))
	n := len([]rune(strings.TrimSpace(d.text.Value())))
	counter := fmt.Sprintf("%d / %d", n, store.DescriptionLimit)
	style := dimStyle
	if n > store.DescriptionLimit {
		style = statusBad
	} else if n > store.DescriptionLimit*4/5 {
		style = badgeWarn
	}
	lines := []string{"", endsLine("  "+regionHead(w, "THE DESCRIPTION", "", f.focus == focusWording),
		style.Render(counter)+" ", w)}
	for _, line := range strings.Split(strings.TrimRight(d.text.View(), "\n"), "\n") {
		lines = append(lines, "  "+line)
	}
	// The note is the reason this wording exists; the ledger keeps it beside the text. It is its own
	// region — the caret goes into it on the next tab — so it carries its own mark rather than
	// borrowing the description's.
	label := regionOff.Render("note")
	prefix := "  "
	if f.focus == focusNote {
		label, prefix = regionOn.Render("note"), blockMark.Render(glyphs.Block)+" "
	}
	lines = append(lines, prefix+label+"  "+d.note.View())
	return fitRows(lines, h)
}

// quadIterations is the loop's own history: every run, and what it scored on the train set. It is
// the only place you can see whether the last four revisions bought anything.
//
// It takes no keys, so its name says so: no block, no accent, and no band on the newest row. A band
// here used to mark "the wording you are revising", and beside the set's own cursor it made two
// rows on one screen look selected.
func (m *Model) quadIterations(w, h int) []string {
	f := m.flow
	head := "  " + dimStyle.Render(padCell("ITER", 5, true)) + " " +
		dimStyle.Render(padCell("TRAIN", 12, true)) + " " +
		dimStyle.Render(padCell("CHARS", 6, true)) + "  " + dimStyle.Render("NOTE")
	lines := []string{"", regionHead(w, "ITERATIONS", fmt.Sprintf("%d of %d", len(f.iters), iterationsRequired), false), head}
	room := max(1, h-len(lines)-2)
	start := max(0, len(f.iters)-room)
	for i := start; i < len(f.iters); i++ {
		it := f.iters[i]
		rate := pct(it.Train.Train.Passed, it.Train.Train.Judged)
		row := "  " + colID.Render(padCell(fmt.Sprint(it.N), 5, true)) + " " +
			colText.Render(padCell(rate, 12, true)) + " " +
			colSplit.Render(padCell(fmt.Sprint(it.Cand.Chars), 6, true)) + "  " +
			colText.Render(ellipsis(oneLine(it.Cand.Note), max(6, w-32)))
		lines = append(lines, row)
	}
	if len(f.iters) == 0 {
		lines = append(lines, "  "+dimStyle.Render("nothing measured yet"))
	}
	// The gate is the one thing the table is counting towards.
	if !f.gateOpen() {
		lines = append(lines, "  "+dimStyle.Render(fmt.Sprintf("%s to go before generalization",
			plural(max(0, iterationsRequired-len(f.iters)), "iteration"))))
	}
	return fitRows(lines, h)
}

// ---------------------------------------------------------------- the narrow fallback

// iterStacked is the workbench without columns: the set with its verdicts and its fields, then the
// description. A window too narrow for two columns still edits where the row is listed.
func (m *Model) iterStacked(h int) []string {
	setH := max(6, h*60/100)
	lines := m.setPane(m.width, setH)
	lines = append(lines, dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, m.width))))
	lines = append(lines, m.quadWording(m.width, h-setH-1)...)
	return fitRows(lines, h)
}

// ---------------------------------------------------------------- state

// ensureDraft seeds the wording from the candidate under test the first time the section is opened.
// A draft already being typed survives: leaving the section and coming back must not throw away the
// sentences you were in the middle of writing.
func (m *Model) ensureDraft() {
	f := m.flow
	if f == nil || f.lab == nil || f.draft != nil {
		return
	}
	parent, text, note := 0, "", ""
	if pend := f.pending(); pend != nil {
		parent, text, note = pend.ID, pend.Text, pend.Note
	} else if it := f.latest(); it != nil {
		parent, text, note = it.Cand.ID, it.Cand.Text, it.Cand.Note
	} else if n := len(f.lab.Candidates); n > 0 {
		c := f.lab.Candidates[n-1]
		parent, text, note = c.ID, c.Text, c.Note
	}

	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetValue(text)
	ta.SetWidth(max(20, m.width-6))
	ta.SetHeight(6)

	ni := textinput.New()
	ni.Prompt = ""
	ni.Placeholder = "why this change — one line"
	ni.CharLimit = 240
	ni.SetValue(note)

	f.draft = &iterDraft{parent: parent, loaded: text, loadedIn: note, text: ta, note: ni}
	f.applyFocus()
}

// draftChanged reports whether the wording or its note differs from what the draft was seeded from.
func (f *flowState) draftChanged() bool {
	if f.draft == nil {
		return false
	}
	return strings.TrimSpace(f.draft.text.Value()) != strings.TrimSpace(f.draft.loaded) ||
		strings.TrimSpace(f.draft.note.Value()) != strings.TrimSpace(f.draft.loadedIn)
}

// passOf is one query's verdict in the latest train run, or nil when there was no run.
func (f *flowState) passOf(id int) *bool {
	it := f.latest()
	if it == nil || f.pending() != nil {
		return nil
	}
	for _, row := range it.Train.Rows {
		if row.ID == id {
			return row.Passed
		}
	}
	return nil
}

// failedRow is the row the failure quadrant shows: the one under the cursor when it failed,
// otherwise the first failure, because the quadrant is about what to fix next.
func (f *flowState) failedRow() *runner.ScoreRow {
	it := f.latest()
	if it == nil || f.pending() != nil {
		return nil
	}
	fails := it.Cand.FailedTrain()
	if len(fails) == 0 {
		return nil
	}
	if f.sel < len(f.rows) {
		want := f.rows[f.sel].ID
		for i := range fails {
			if fails[i].ID == want {
				return &fails[i]
			}
		}
	}
	return &fails[0]
}

func triggerWord(yes bool) string {
	if yes {
		return "trigger"
	}
	return "not trigger"
}

// typing reports whether the iteration screen's keystrokes belong to a text field.
func (m *Model) typing() bool {
	return m.mode == screenFlow && m.flow != nil && m.flow.sec == secIteration && m.flow.focus != focusSet
}

// ---------------------------------------------------------------- keys

// updateIter is the workbench's keys: walk the set, run, read the failures — and type the wording.
// The set is not written here. `n`, `e`, `d` and `space` belong to the screen that owns the set, and
// a second editor on the screen whose one editable thing is the description cost the loop its focus.
func (m *Model) updateIter(msg tea.KeyMsg) tea.Cmd {
	f := m.flow
	switch msg.String() {
	case "tab":
		return m.cycleFocus(1)
	case "shift+tab":
		return m.cycleFocus(-1)
	case "j", "down":
		f.sel = min(f.sel+1, max(0, len(f.rows)-1))
	case "k", "up":
		f.sel = max(0, f.sel-1)
	case "g", "home":
		f.sel = 0
	case "G", "end":
		f.sel = max(0, len(f.rows)-1)
	case "R":
		return m.openRunAsk()
	case "f":
		if it := f.latest(); it != nil && f.pending() == nil && len(it.Cand.FailedTrain()) > 0 {
			return m.openFailures(it)
		}
	case "enter":
		// The loop in one key: save the wording being written (if it changed), then measure it.
		return m.runOrSave()
	}
	return nil
}

// typingKey sends a keystroke to the focused field. Only tab, ctrl+s and esc mean anything else here,
// because every other character is one the description might need.
func (m *Model) typingKey(msg tea.KeyMsg) tea.Cmd {
	f := m.flow
	switch msg.String() {
	case "esc":
		f.blur()
		f.focus = focusSet
		return nil
	case "tab":
		return m.cycleFocus(1)
	case "shift+tab":
		return m.cycleFocus(-1)
	case "ctrl+s":
		return m.saveDraft(false)
	case "enter":
		// In the note a newline is not a thing: enter saves and measures, like the set's enter.
		if f.focus == focusNote {
			return m.saveDraft(true)
		}
	}
	var cmd tea.Cmd
	if f.focus == focusWording {
		f.draft.text, cmd = f.draft.text.Update(msg)
	} else {
		f.draft.note, cmd = f.draft.note.Update(msg)
	}
	return cmd
}

// cycleFocus walks the three panes. The set takes letters as commands, so a field that has the keys
// has to say so: the footer changes, and the note's label turns into the caret.
func (m *Model) cycleFocus(dir int) tea.Cmd {
	f := m.flow
	m.ensureDraft()
	if f.draft == nil {
		return nil
	}
	f.focus = (f.focus + dir + focusCount) % focusCount
	f.applyFocus()
	return nil
}

// applyFocus puts the caret where the focus says, and nowhere else.
func (f *flowState) applyFocus() {
	if f.draft == nil {
		return
	}
	switch f.focus {
	case focusWording:
		f.draft.text.Focus()
		f.draft.note.Blur()
	case focusNote:
		f.draft.text.Blur()
		f.draft.note.Focus()
	default:
		f.blur()
	}
}

func (f *flowState) blur() {
	if f.draft != nil {
		f.draft.text.Blur()
		f.draft.note.Blur()
	}
}

// runOrSave is the enter key on the set: write the wording you have been editing, then measure it.
func (m *Model) runOrSave() tea.Cmd {
	f := m.flow
	if f.draftChanged() {
		if cmd := m.saveDraft(true); cmd != nil || m.mode != screenFlow {
			return cmd
		}
	}
	return m.openRunAsk()
}

// saveDraft writes the wording into the ledger as the next candidate. It does not touch SKILL.md —
// the run is what puts it there, because the file is what the router reads and a wording nobody has
// measured should not be in it.
func (m *Model) saveDraft(thenRun bool) tea.Cmd {
	f := m.flow
	if f.draft == nil {
		return nil
	}
	text := strings.TrimSpace(f.draft.text.Value())
	if text == "" {
		m.setStatus("the description cannot be empty", true)
		return nil
	}
	if len([]rune(text)) > store.DescriptionLimit {
		m.setStatus(fmt.Sprintf("%d characters — the format's ceiling is %d",
			len([]rune(text)), store.DescriptionLimit), true)
		return nil
	}
	if !f.draftChanged() {
		m.setStatus("the wording is unchanged — nothing to save", false)
		if thenRun {
			return m.openRunAsk()
		}
		return nil
	}
	cand, err := runner.New(m.store).SaveDescriptionCandidate(m.skill, text,
		strings.TrimSpace(f.draft.note.Value()), f.draft.parent)
	if err != nil {
		m.setStatus(err.Error(), true)
		return nil
	}
	m.reload()
	// Seed again from the candidate just written: the draft is clean, and the pane says which
	// candidate the next run measures.
	f.draft = nil
	m.ensureDraft()
	m.setStatus(fmt.Sprintf("candidate #%d saved — enter runs the train set against it", cand.ID), false)
	if thenRun {
		return m.openRunAsk()
	}
	return nil
}
