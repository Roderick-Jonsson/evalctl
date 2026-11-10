package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
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
	focusCount
)

// iterDraft is the wording being edited, seeded from the candidate under test. There is no note
// field: what a wording changes is computed from the text itself, and the reason a losing wording
// existed is not something a file has to remember.
type iterDraft struct {
	parent int    // the candidate this text was seeded from
	loaded string // the text it was seeded with, for "has anything changed"
	text   textarea.Model
}

// iterBody is the workbench, or the stacked fallback when the window cannot hold four panes.
func (m *Model) iterBody(h int) []string {
	m.ensureDraft()
	if !m.itersTableDrawn() || h < 18 {
		return m.iterStacked(h)
	}
	leftW := min(96, max(44, m.width*40/100))
	rightW := m.workbenchPaneWidth()
	// The top row takes the larger half: the failure pane has to fit a wrapped ask, the two labelled
	// facts and three hints, while the description and the iteration list below pad with blanks when
	// they have room to spare. A row given to the top is a row the bottom does not miss.
	//
	// A proposal in the description pane turns that around: a 600-character paragraph wrapped to this
	// column is fifteen lines, and a diff whose end cannot be read cannot be decided on. The bottom
	// row takes two thirds while one is up, and gives them back when it is kept or dropped.
	topH := max(8, h/2)
	if m.flow != nil && m.flow.proposal != nil {
		topH = max(8, h/3)
	}
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
		d.rule()
		switch {
		case len(f.rows) == 0:
			d.lead("the " + setRegionWord(f.sec == secIteration) + " is empty — n writes the first query here, and it lands in the file")
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
	d.rule()
	// The ask, labelled and hung like a hint: the label says what the sentence is, the sentence is
	// the thing every line under it is about. It comes from the set and not from the score row,
	// because the set is what is on screen beside it and what the next run will measure.
	ask := f.askOf(row.ID)
	if ask == "" {
		ask = row.Query
	}
	d.labelled("Query", paneLabel, ask)
	// A row of its own, because the ask can wrap and a wrapped ask ends mid-sentence: the failure
	// line has to start the eye at the left edge again, or the reading runs the last line of the
	// query into the verdict.
	d.lines = append(d.lines, "")
	// How it failed, as two labelled rows: the label names the axis, the value answers it. The old
	// line said "should not trigger · fired 3 of 3", and the reader had to work out which half was
	// the wish and which was the fact — "not" made it worse by turning the sentence inside out. This
	// is the fix a table makes with a column called "is true": name what the boolean means, and the
	// boolean stops being a puzzle. Squeezed, the pair keeps its words and loses its colours; a cut
	// phrase is unreadable, a colourless one is not.
	expect := "to trigger"
	if !row.ShouldTrigger {
		expect = "not to trigger"
	}
	d.labelled("Expected", paneLabel, expect)
	// The run's own numbers, and nothing else on this row: how many of the runs fired. What those
	// numbers mean is a verdict, and a verdict is a reading rather than a measurement — it goes with
	// the advice, not with the evidence.
	d.labelled("Fired", paneLabel, fmt.Sprintf("%d of %d runs", row.Triggers, row.Runs))
	// The advice sits one row clear of the verdict line's block, and it opens with the verdict itself:
	// the two words that name the failure, then the moves that answer it. The verdict is the guide's
	// reading of the numbers above — "too broad" is not a measurement, it is what the guide says those
	// measurements mean — so it belongs with what to do, and on a row of its own so that the run's
	// numbers never carry an opinion.
	d.lines = append(d.lines, "")
	verdict := verdictWord(*row)
	gap := d.paneGap("Verdict", paneLabel)
	if lipgloss.Width(gap)+lipgloss.Width(verdict) > d.w {
		d.lines = append(d.lines, gap+colText.Render(ellipsis(verdict, max(10, d.w-lipgloss.Width(gap)))))
	} else {
		d.lines = append(d.lines, gap+failMark.Render(verdict))
	}
	for i, hint := range adaptHints(*row) {
		d.hint(i+1, hint)
	}
	return fitRows(d.lines, h)
}

// adaptHints is the guide's advice, narrowed to the failure in front of you. It is deterministic on
// purpose: the same failure always earns the same advice, so it stays checkable against the page it
// came from instead of being an opinion at 2am.
//
// Each hint is one clause of a fixed size — thirty-six cells, the room a workbench column gives the
// text beside its label — because the pane is a fixed height and a hint that wraps costs the third
// one its place. That is also the order to try them in: the wording first, the row last.
func adaptHints(row runner.ScoreRow) []string {
	switch {
	case row.ShouldTrigger && row.Triggers == 0:
		return []string{
			"Name the context this belongs to.",
			"Be pushy: say it applies unnamed.",
			"Describe the intent, not the tool.",
		}
	case !row.ShouldTrigger && row.Triggers > 0:
		return []string{
			"It overlaps another skill's work.",
			"Name the hand-off: where it stops.",
			"Check which skill did load.",
		}
	}
	return []string{
		"The yes/no line is not explicit.",
		"Say what makes this a yes.",
		"If it recurs, fix the row.",
	}
}

func (m *Model) quadSetRow(i, idW, trigW, resW, queryW int) string {
	f := m.flow
	e := f.rows[i]
	mark := "  "
	// Only the region with the keys draws the cursor, and the band goes with it: a row that is
	// marked in a pane nobody is driving reads as a second cursor, which is the thing this screen
	// had two of.
	// The cursor of a read-only table only stands where something can be learned: on a row the run
	// failed. A passing row keeps its verdict and loses the mark — a cursor there would say "this is
	// the failure I am looking at" about a row the pane beside it says nothing about.
	on := i == f.sel && f.focus == focusSet && f.fails(e.ID)
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
// revision is an edit and not a screen you navigate to. While a proposal is waiting it is that
// proposal instead: the paragraph as it would read, with what left and what arrived marked.
func (m *Model) quadWording(w, h int) []string {
	if m.flow.proposal != nil {
		return m.quadProposal(w, h)
	}
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
		style.Render(counter)+" ", w),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, w-4)))}
	for _, line := range strings.Split(strings.TrimRight(d.text.View(), "\n"), "\n") {
		lines = append(lines, "  "+line)
	}
	return fitRows(lines, h)
}

// quadIterations is the loop's own history: every wording, what it scored on the train set, what the
// validation set made of it, and the gap between the two. It is the only place the loop's numbers are
// visible beside the wording they are about, in either half of the work.
//
// The validation column is not optional here. With it gone, a tree whose every wording had been
// validated showed nothing at all on the screen — the rates were on disk, ranked, and drawn nowhere —
// so what gives way as the pane narrows is the gap and then the note, never the two rates.
//
// It takes no keys, so its name says so: no block, no accent, and no band on the newest row. A band
// here used to mark "the wording you are revising", and beside the set's own cursor it made two
// rows on one screen look selected.
func (m *Model) quadIterations(w, h int) []string {
	f := m.flow
	c, note := blockColsFor(w)
	head := "  " + dimStyle.Render(padCell("ITER", c.iter, true)) + " " +
		dimStyle.Render(padCell("TRAIN", c.train, true)) + " " +
		dimStyle.Render(padCell("VALID", c.valid, true))
	if c.showGap {
		head += " " + dimStyle.Render(padCell("GAP", c.gap, true))
	}
	head += "  " + dimStyle.Render("NOTE")
	// The head's right corner answers the question the table raises when every row carries the same
	// rate: why the recommendation is the oldest wording. The sentence behind it is in the ranking.
	right := fmt.Sprintf("%d of %d", len(f.iters), iterationsRequired)
	if rate, n, tied := tieOf(f); tied {
		right = fmt.Sprintf("%d tied at %d%%", n, rate)
	}
	lines := []string{"", regionHead(w, "ITERATIONS", right, false),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, w-4))), head}
	room := max(1, h-len(lines)-2)
	start := max(0, len(f.iters)-room)
	best := bestIteration(f)
	for i := start; i < len(f.iters); i++ {
		it := f.iters[i]
		lines = append(lines, m.iterBlockRow(it, c, note, best != nil && best.Cand.ID == it.Cand.ID))
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

// blockCols is the ITERATIONS block's budget: the two rates always, the gap when the pane can hold
// it, and the note with whatever is left.
type blockCols struct {
	iter, train, valid, gap int
	showGap                 bool
}

func blockColsFor(w int) (c blockCols, note int) {
	c = blockCols{iter: 4, train: 9, valid: 9}
	switch {
	case w >= 58:
		// "1/2 50% of 4" is twelve cells: a partial cell is worth the extra two, because it is the
		// difference between a rank and a rounding of one.
		c.train, c.valid, c.gap, c.showGap = 13, 13, 6, true
	case w >= 46:
		c.train, c.valid = 11, 11
	}
	used := 2 + c.iter + 1 + c.train + 1 + c.valid + 2
	if c.showGap {
		used += 1 + c.gap
	}
	return c, max(4, w-used)
}

// iterBlockRow is one wording in the block: the rates, the gap, and the note that says why it was
// written — with the recommendation tagged on the line's right edge, where the eye lands last.
func (m *Model) iterBlockRow(it iteration, c blockCols, note int, best bool) string {
	f := m.flow
	train := fmt.Sprintf("%d/%d %s", it.Train.Train.Passed, it.Train.Train.Judged,
		pct(it.Train.Train.Passed, it.Train.Train.Judged))
	if !it.Train.TrainFull && f.lab != nil {
		train += fmt.Sprintf(" of %d", f.lab.TrainRows)
	}
	valid := "—"
	if v := it.valid(); v != nil {
		valid = fmt.Sprintf("%d/%d %s", v.Passed, v.Judged, pct(v.Passed, v.Judged))
		if !f.complete(v) && f.lab != nil {
			valid += fmt.Sprintf(" of %d", f.lab.ValidationRows)
		}
	}
	row := "  " + colID.Render(padCell(fmt.Sprint(it.N), c.iter, true)) + " " +
		colText.Render(padCell(train, c.train, true)) + " " +
		colText.Render(padCell(valid, c.valid, true))
	if c.showGap {
		row += " " + colSplit.Render(padCell(gapOf(&it.Train.Train, it.valid()), c.gap, true))
	}
	mark := ""
	if best {
		mark = " " + tagRecommend.Render("Recommended")
	}
	text := oneLine(it.Cand.Change)
	if text == "" {
		text = "—"
	}
	room := max(4, note-lipgloss.Width(mark))
	return row + "  " + colText.Render(padCell(ellipsis(text, room), room, false)) + mark
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
	parent, text := 0, ""
	if pend := f.pending(); pend != nil {
		parent, text = pend.ID, pend.Text
	} else if it := f.latest(); it != nil {
		parent, text = it.Cand.ID, it.Cand.Text
	} else if n := len(f.lab.Candidates); n > 0 {
		c := f.lab.Candidates[n-1]
		parent, text = c.ID, c.Text
	}

	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetValue(text)
	if strings.TrimSpace(text) == "" {
		// A skill with no description opens on an empty field, and an empty field with no sentence
		// about itself is a blank pane. The placeholder is that sentence, and it is not text.
		ta.Placeholder = "type the description that makes the router pick this skill"
	}
	ta.SetWidth(max(20, m.width-6))
	ta.SetHeight(6)

	f.draft = &iterDraft{parent: parent, loaded: text, text: ta}
	f.applyFocus()
}

// draftChanged reports whether the wording differs from what the draft was seeded from.
func (f *flowState) draftChanged() bool {
	if f.draft == nil {
		return false
	}
	return strings.TrimSpace(f.draft.text.Value()) != strings.TrimSpace(f.draft.loaded)
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

// fails reports whether the run in hand failed this row. A row with no verdict — a candidate that has
// not been run yet — has not failed: nothing has been said about it.
func (f *flowState) fails(id int) bool {
	p := f.passOf(id)
	return p != nil && !*p
}

// failIdxs are the rows the workbench can stand on. The set is drawn whole, because it is what the run
// left and its passing rows are half of what says whether the wording works; but the cursor walks the
// failures only, since a passing row is nothing to work on and the pane beside it would have nothing
// to say about it.
func (f *flowState) failIdxs() []int {
	var out []int
	for i, e := range f.rows {
		if f.fails(e.ID) {
			out = append(out, i)
		}
	}
	return out
}

// snapWorkbench puts the cursor on a failure: the first at or after where it stands, else the last.
// With nothing failed it rests at the top and the set draws no cursor at all.
func (f *flowState) snapWorkbench() {
	idxs := f.failIdxs()
	if len(idxs) == 0 {
		f.sel = 0
		return
	}
	for _, i := range idxs {
		if i >= f.sel {
			f.sel = i
			return
		}
	}
	f.sel = idxs[len(idxs)-1]
}

// stepFail moves the cursor to the next (dir > 0) or previous failure, skipping the rows that passed,
// and stays put at either end.
func (f *flowState) stepFail(dir int) {
	if len(f.failIdxs()) == 0 {
		return
	}
	if dir > 0 {
		for i := f.sel + 1; i < len(f.rows); i++ {
			if f.fails(f.rows[i].ID) {
				f.sel = i
				return
			}
		}
		return
	}
	for i := f.sel - 1; i >= 0; i-- {
		if f.fails(f.rows[i].ID) {
			f.sel = i
			return
		}
	}
}

// typing reports whether the iteration screen's keystrokes belong to a text field. A proposal owns
// the keys without owning a caret: it is read, kept or dropped, never typed into.
func (m *Model) typing() bool {
	if m.mode != screenFlow || m.flow == nil || m.flow.sec != secIteration {
		return false
	}
	return m.flow.focus != focusSet && m.flow.proposal == nil
}

// ---------------------------------------------------------------- keys

// updateIter is the workbench's keys: walk the set, run, read the failures — and type the wording.
// The set is not written here. `n`, `e`, `d` and `space` belong to the screen that owns the set, and
// a second editor on the screen whose one editable thing is the description cost the loop its focus.
func (m *Model) updateIter(msg tea.KeyMsg) tea.Cmd {
	f := m.flow
	// A proposal owns the pane it arrived in: it is kept, asked again, or scrolled, and every other
	// key belongs to it rather than to the set behind it.
	if f.proposal != nil {
		return m.updateProposal(msg)
	}
	switch msg.String() {
	case "tab":
		return m.cycleFocus(1)
	case "shift+tab":
		return m.cycleFocus(-1)
	case "j", "down":
		f.stepFail(1)
	case "k", "up":
		f.stepFail(-1)
	case "g", "home":
		if idxs := f.failIdxs(); len(idxs) > 0 {
			f.sel = idxs[0]
		}
	case "G", "end":
		if idxs := f.failIdxs(); len(idxs) > 0 {
			f.sel = idxs[len(idxs)-1]
		}
	case "R":
		// Run again: the same wording, another measurement. No screen between the key and the spend.
		return m.startTrainingRun()
	case "+", "=":
		// The knobs that were the section pane's: how many times each query is run, and whether the
		// next run spends anything. They live on this screen because the run does.
		if f.lab != nil {
			n := min(9, f.lab.Runs+1)
			if err := m.store.SetTriggerRuns(m.skill, n); err != nil {
				m.setStatus(err.Error(), true)
			} else {
				m.reload()
				m.setStatus(fmt.Sprintf("runs per query = %d — written into %s/evals/trigger-evals.json", n, m.skill), false)
			}
		}
	case "-":
		if f.lab != nil {
			n := max(1, f.lab.Runs-1)
			if err := m.store.SetTriggerRuns(m.skill, n); err != nil {
				m.setStatus(err.Error(), true)
			} else {
				m.reload()
				m.setStatus(fmt.Sprintf("runs per query = %d", n), false)
			}
		}
	case "x":
		f.dry = !f.dry
		if f.dry {
			m.setStatus("dry run — R prints the commands and writes nothing", false)
		} else {
			m.setStatus("dry run off — R spends", false)
		}
	case "s":
		// The split is a toggle on this screen now, not a screen in front of it: the same set, the
		// other half. The cursor stays on the row it was on where the row still exists.
		if f.sec == secIteration {
			f.sec = secGeneralization
		} else {
			f.sec = secIteration
		}
		m.reload()
		m.setStatus("the "+setRegionWord(f.sec == secIteration)+" — the wording may only learn from the train set", false)
	case "v":
		// The validation half's run: every measured wording, measured again on the rows held back.
		// With nothing owed it opens the ranking, because the rates are what the key is for.
		return m.startGeneralization()
	case "a":
		// The pick, which the deleted ranking screen used to own: the best validation rate wins, and
		// the confirm is the same one question.
		if best := bestIteration(f); best != nil {
			m.askApply(*best)
		} else {
			m.setStatus("nothing ranked yet — v measures the wordings on the validation set", true)
		}
	case "h":
		// The guide's last move: the fresh queries, measured once, before the wording is believed.
		return m.openHoldout()
	case "f":
		// One wording in full: both rates, the gap, what the revision before it fixed and broke, and
		// every train row it failed. It is the same reading the ranking's rows abbreviate.
		if it := f.latest(); it != nil {
			return m.openIterationRecord(*it)
		}
	case "p":
		// The guide's step 3 with the model doing the writing: one call, the failures as its only
		// evidence, and the paragraph lands in the pane beside the failures it was written from.
		if f.focus == focusSet {
			return m.startDescribe()
		}
	case "enter":
		// The loop in one key: save the wording being written (if it changed), then measure it.
		return m.runOrSave()
	case "n":
		// The set is edited here: the block is the table, and a row is written in it. It used to be a
		// step screen of its own, which made the set you were looking at and the set you could write
		// two different readings of one file.
		if f.focus == focusSet {
			m.openForm(nil)
		}
	case "e":
		if f.focus == focusSet && len(f.rows) > 0 {
			e := f.rows[min(f.sel, len(f.rows)-1)]
			m.openForm(&e)
			return m.focusForm()
		}
	case " ":
		if f.focus == focusSet && len(f.rows) > 0 {
			return m.toggleLabel()
		}
	case "d":
		if f.focus == focusSet && len(f.rows) > 0 {
			m.askDeleteRow()
		}
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
	case "ctrl+d":
		// The discarding key, spelled out: a half-typed sentence is not lost by a keystroke that
		// also means "up".
		f.draft, f.discardArmed = nil, false
		m.setStatus("the draft is gone — the wording is SKILL.md's again", false)
		return nil
	case "tab":
		return m.cycleFocus(1)
	case "shift+tab":
		return m.cycleFocus(-1)
	case "ctrl+s":
		return m.saveDraft(false)
	}
	var cmd tea.Cmd
	if f.focus == focusWording {
		f.draft.text, cmd = f.draft.text.Update(msg)
	}
	return cmd
}

// cycleFocus walks the three panes. The set takes letters as commands, so a field that has the keys
// has to say so: the footer changes, and the note's label turns into the caret. A proposal has the
// keys without having a caret, so tab waits: there is nothing to type into until it is kept.
func (m *Model) cycleFocus(dir int) tea.Cmd {
	f := m.flow
	if f.proposal != nil {
		m.setStatus("a proposal is on the pane — y keeps it in the field first, or esc drops it", true)
		return nil
	}
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
	default:
		f.blur()
	}
}

func (f *flowState) blur() {
	if f.draft != nil {
		f.draft.text.Blur()
	}
}

// runOrSave is the enter key on the set: write the wording you have been editing, then measure it.
// A save that fails starts nothing — the run measures what is on disk, and a wording that was
// refused is not.
func (m *Model) runOrSave() tea.Cmd {
	f := m.flow
	if f.draftChanged() {
		return m.saveDraft(true)
	}
	return m.startTrainingRun()
}

// saveDraft stages the wording as the next draft. It does not touch SKILL.md —
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
			return m.startTrainingRun()
		}
		return nil
	}
	if _, err := runner.New(m.store).SaveDescriptionCandidate(m.skill, text); err != nil {
		m.setStatus(err.Error(), true)
		return nil
	}
	m.reload()
	// Seed again after the draft is staged: the field is clean, and the pane says which wording the
	// next run measures.
	f.draft = nil
	m.ensureDraft()
	m.setStatus(fmt.Sprintf("draft staged in %s-workspace — enter runs the train set against it", m.skill), false)
	if thenRun {
		return m.startTrainingRun()
	}
	return nil
}
