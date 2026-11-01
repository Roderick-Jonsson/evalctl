package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

// iterBody is the workbench, or the stacked fallback when the window cannot hold four panes.
func (m *Model) iterBody(h int) []string {
	m.ensureDraft()
	if !m.itersTableDrawn() || h < 18 {
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

// itersTableDrawn reports whether the window can hold the workbench's four quadrants. It is asked in
// two places — the body, which stacks when it cannot, and the top row, which keeps the loop's numbers
// only where the iterations table is gone — so it asks about the window, never about the body height,
// which is what the top row decides.
func (m *Model) itersTableDrawn() bool {
	// 90 cells is the width at which the two columns stop squeezing their queries; 23 rows is the
	// height that leaves the quadrants 18 under the bar, its rule and the two footer rows.
	return m.width >= 90 && m.height >= 23
}

// ---------------------------------------------------------------- quadrants

// quadFailure is the failure the next revision is written from, in three moves: the ask that failed,
// the way it failed, and the guide's hints for that kind of failure. Nothing else.
//
// It used to carry the row's why, the skill that loaded instead, the verdict as a sentence and a
// warning about pasting a query into the wording. All four are true and none of them changes the next
// edit — this pane sits beside the description you are about to rewrite, so what it owes you is a
// work order, not a report. The full record is still one key away (`f`), and the why and the
// competitor belong to the set, where the row is edited.
//
// Which row it is follows the cursor: if the row you are standing on failed, that is the one; if it
// passed, the first failure is, because the quadrant is about what to fix.
func (m *Model) quadFailure(w, h int) []string {
	f := m.flow
	row := f.failedRow()
	d := &docWriter{w: w}
	if row == nil {
		// A quadrant that takes no keys names itself in the quiet register, so the one that has
		// them is the only one in the accent.
		d.head("FAILED QUERY", false)
		switch {
		case f.pending() != nil:
			d.lead(fmt.Sprintf("candidate #%d has not been run yet — run the train set", f.pending().ID))
		case f.latest() != nil:
			d.lead(fmt.Sprintf("nothing failed — all %d train queries pass", f.latest().Train.Train.Judged))
		default:
			d.lead("nothing measured yet — run the train set")
		}
		return fitRows(d.lines, h)
	}
	d.head(fmt.Sprintf("FAILED QUERY · q%d", row.ID), false)
	// The ask, in the register of a heading: it is what every line under it is about, and the one
	// line here you might recognise from the set. It comes from the set and not from the score row,
	// because the set is what is on screen beside it and what the next run will measure.
	ask := f.askOf(row.ID)
	if ask == "" {
		ask = row.Query
	}
	for _, line := range wrapPlain(ask, max(10, w-2)) {
		d.lines = append(d.lines, "  "+titleStyle.Render(line))
	}
	// A row of its own, because the ask can wrap and a wrapped ask ends mid-sentence: the failure
	// line has to start the eye at the left edge again, or the reading runs the last line of the
	// query into the verdict.
	d.lines = append(d.lines, "")
	// How it failed, in the order the run is read: what it should have done, what it did, and the
	// two words for the gap. Squeezed, it keeps the words and loses the colours rather than the
	// other way round: a cut verdict is unreadable, a colourless one is not.
	label := colYes
	if !row.ShouldTrigger {
		label = colNo
	}
	kind := "should " + triggerWord(row.ShouldTrigger) + fmt.Sprintf(" · fired %d of %d · ", row.Triggers, row.Runs)
	if lipgloss.Width(kind+verdictWord(*row)) > max(10, w-2) {
		d.lines = append(d.lines, "  "+colText.Render(ellipsis(kind+verdictWord(*row), max(10, w-2))))
	} else {
		d.lines = append(d.lines, "  "+label.Render("should "+triggerWord(row.ShouldTrigger))+
			dimStyle.Render(fmt.Sprintf(" · fired %d of %d", row.Triggers, row.Runs))+
			" · "+failMark.Render(verdictWord(*row)))
	}
	d.lines = append(d.lines, "")
	// The advice, numbered so a revision can be argued with one hint at a time.
	for i, hint := range adaptHints(*row) {
		d.hint(i+1, hint)
	}
	return fitRows(d.lines, h)
}

// adaptHints is the guide's advice, narrowed to the failure in front of you. It is deterministic on
// purpose: the same failure always earns the same advice, so it stays checkable against the page it
// came from instead of being an opinion at 2am.
//
// Each hint is one clause, because the pane sits beside the field you are about to rewrite: a hint
// that needs a second line is a paragraph about the fix instead of the fix. The order is the order to
// try them in — the wording first, the row last.
func adaptHints(row runner.ScoreRow) []string {
	switch {
	case row.ShouldTrigger && row.Triggers == 0:
		return []string{
			"Name the context this ask belongs to.",
			"Say it applies even unnamed — be pushy.",
			"Describe the intent, not the tool.",
		}
	case !row.ShouldTrigger && row.Triggers > 0:
		return []string{
			"Something here overlaps a neighbouring skill.",
			"Say what this skill is for, and where it ends.",
			"Check which skill did load — your rival.",
		}
	}
	return []string{
		"The line between yes and no is not explicit.",
		"Say what makes this a yes, in so many words.",
		"If it keeps happening, the row may be wrong.",
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

// askOf is a row's query as the set spells it. The score row carries its own copy, but the set is
// what is on screen beside the failure pane and what the next run measures, so a query edited since
// the run is read here as it will be run — and an old measurement is never shown above new text
// without the table's own verdict column saying the two disagree.
func (f *flowState) askOf(id int) string {
	for _, e := range f.rows {
		if e.ID == id {
			return e.Text
		}
	}
	return ""
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
