package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"

	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// The output lane has three jobs, and they are three views on one menu.
//
//   - INITIALIZATION is the set the loop is measured on: the skill, the prompt, the expected output,
//     the fixtures a case stages, and the assertions it will be judged on. This is the only place
//     the declaration is edited, because a case is written before anything can be measured against
//     it, and it is the first thing to come back to when an assertion turns out to be unmeasurable.
//   - ITERATION works one iteration: the assertions that failed, the judge's evidence for each, the
//     arm's execution transcript, and the human's feedback. That is the guide's loop — a person
//     reads, the skill is revised, the next iteration measures it — so it is one screen and not a
//     document opened off another.
//   - TESTING QUALITY judges the trend: every iteration's two rates against each other and against
//     the last one, and which complete iteration is the best.
//
// The loop can also be CLOSED — no set, a case with no assertion, nothing run, or a run that nothing
// judged. That is not a fourth view; it is the state of the first one, so the initialization view says
// it in a band above the case list, with the one errand that opens the loop and the key that does
// it. It used to be a screen of its own, unnumbered, that the lane opened on and that could not be
// returned to once it was behind you: a state you cannot look at again is a state you have to
// remember instead.
//
// The three used to be two screens and a hidden toggle: the workbench carried declaration, work and
// rank at once, and the ranking was reachable only through `tab`. Then they were a rail — right, but
// you still did not choose one, the disk picked for you. A view you did not choose is a view you
// cannot find again; the menu is the one screen that says what the lane's three jobs are.
const (
	qSetup   = iota // declare: the cases, their records and their assertions
	qDojo           // work: one iteration's failures, transcripts and feedback
	qQuality        // judge: the iterations against each other
)

// qualitySections is the lane's three views in the order the loop walks them: the menu's rows, the
// rail's chips, and one name per screen — so a view is called the same thing in all three places.
var qualitySections = []struct {
	sec   int
	name  string
	blurb string
}{
	{qSetup, "initialization", "the set the loop is measured on"},
	{qDojo, "iteration", "one iteration, worked: failures, evidence, feedback"},
	{qQuality, "testing quality", "the trend: every iteration's two rates"},
}

// qualityState is the output lane's reading of the disk: the lab (one row per iteration), the cases
// of the iteration on screen, the failures of that iteration, and where the cursors stand.
type qualityState struct {
	lab   *runner.QualityLab
	cases []runner.QualityCase
	iter  int // the iteration every section reads; 0 = none on disk
	sec   int // qSetup, qDojo or qQuality

	// The case cursor, shared by the declaration and the dojo: one set, one walk.
	sel int
	top int

	// The dojo's assertion cursor, and which of the dojo's two columns has the keys. asel walks the
	// selected case's assertion table — passing rows included, because an assertion that passes in
	// both arms is a verdict a person may want to argue with — and tbl is whether j k move it or move
	// the case list beside it.
	asel int
	tbl  bool

	// The dojo's failures, flattened for its counts, and the tail of the selected case's arm, cached
	// under the path it came from. The rail itself is the case list: sel and top are shared with the
	// declaration, so walking the set in one section keeps the cursor in the other.
	fails   []runner.QualityFailure
	logPath string
	logTail []string

	// The quality section's table.
	rsel, rtop int

	// note is the human's note being typed, open as a strip under the dojo's two columns.
	feedback *feedbackState
	first    bool

	// vcursor is the menu's row: which of the three views the skill's screen is offering. It is on
	// the state and not on the model because it is a reading of this skill's disk — the row the
	// lane opens on is the one the loop is waiting in.
	vcursor int
}

func (q *qualityState) iteration() *runner.QualityIteration {
	if q == nil || q.lab == nil {
		return nil
	}
	for i := range q.lab.Iterations {
		if q.lab.Iterations[i].N == q.iter {
			return &q.lab.Iterations[i]
		}
	}
	return nil
}

func (q *qualityState) caseAt() *runner.QualityCase {
	if q == nil || q.sel < 0 || q.sel >= len(q.cases) {
		return nil
	}
	return &q.cases[q.sel]
}

// selectFirstFailed puts the case cursor on the first case that failed, if any: a dojo that opens on
// a case that passed is a screen answering "what went wrong" with "nothing here".
func (q *qualityState) selectFirstFailed() {
	if q == nil {
		return
	}
	for i, c := range q.cases {
		if len(c.Failed) > 0 {
			q.sel, q.top = i, 0
			q.asel = firstFailedAssertion(c)
			q.syncLog()
			return
		}
	}
	if q.sel >= len(q.cases) {
		q.sel, q.top = 0, 0
	}
	q.syncLog()
}

func (q *qualityState) caseOf(slug string) *runner.QualityCase {
	if q == nil {
		return nil
	}
	for i := range q.cases {
		if q.cases[i].Slug == slug {
			return &q.cases[i]
		}
	}
	return nil
}

// sectionName is where the path bar says you are inside the lane.
func (q *qualityState) sectionName() string {
	if q == nil {
		return qualitySections[qSetup].name
	}
	for _, s := range qualitySections {
		if s.sec == q.sec {
			return s.name
		}
	}
	return qualitySections[qSetup].name
}

// openQualitySkill opens one skill's output loop where the disk is asking for it: the menu, with the
// cursor on the view that is asking for work. The lane opens on the choice and not inside a section,
// because the three views are three jobs and which one you want is not something a directory name
// can decide for you — the cursor is the part the disk gets to choose.
func (m *Model) openQualitySkill(name string) {
	if m.store.Skill(name) == nil {
		m.setStatus("unknown skill "+name, true)
		return
	}
	m.skill = name
	m.lane = store.LaneOutput
	m.quality = &qualityState{}
	m.reload()
	m.mode = screenQualityMenu
	if m.quality.lab == nil {
		return
	}
	m.quality.vcursor = menuRowFor(m.quality.lab)
	if m.quality.lab.InitPending {
		if _, text := gateNext(m.quality.lab); text != "" {
			m.setStatus(name+" · initialization — "+text, false)
		}
		return
	}
	if n := len(m.quality.lab.Iterations); n > 0 {
		last := m.quality.lab.Iterations[n-1]
		switch {
		case len(last.Failed) > 0:
			m.setStatus(fmt.Sprintf("%s · iteration %d failed %s — read them, revise, R runs iteration %d",
				name, last.N, plural(len(last.Failed), "assertion"), last.N+1), false)
		case !last.Complete:
			m.setStatus(fmt.Sprintf("%s · iteration %d not fully graded — g grades it", name, last.N), false)
		default:
			m.setStatus(fmt.Sprintf("%s · iteration %d is clean — R runs the next one", name, last.N), false)
		}
		return
	}
	m.setStatus(fmt.Sprintf("%s · %s declared, nothing run yet — R runs iteration 1", name,
		plural(m.quality.lab.Cases, "case")), false)
}

// setQualitySection moves the rail and drops whatever was being typed: a field opened in one section
// is not a field of another, and a caret that survives a section change is a caret in the wrong pane.
func (m *Model) setQualitySection(sec int) {
	q := m.quality
	if q == nil {
		return
	}
	sec = min(max(qSetup, sec), qQuality)
	if sec == q.sec {
		return
	}
	q.sec = sec
	q.feedback, m.assertEdit, m.caseNew = nil, nil, nil
}

// reloadQuality re-reads the lab, the iteration's cases and its failures. It runs after every write
// and every finished run, so the table never shows a number from before the change.
func (m *Model) reloadQuality() {
	if m.skill == "" || m.lane != store.LaneOutput {
		return
	}
	lab, err := runner.New(m.store).QualityLab(m.skill)
	if err != nil {
		m.setStatus(err.Error(), true)
		return
	}
	if m.quality == nil {
		m.quality = &qualityState{}
	}
	q := m.quality
	q.lab = lab
	// Keep the iteration the operator is looking at while it still exists; otherwise open the
	// newest, which is the one the loop is waiting on.
	if len(lab.Iterations) > 0 {
		found := false
		for _, it := range lab.Iterations {
			if it.N == q.iter {
				found = true
			}
		}
		if !found {
			q.iter = lab.Iterations[len(lab.Iterations)-1].N
		}
	} else {
		q.iter = 0
	}
	// The declaration is read whether or not anything has run: evals.json is a file, and the screen
	// that writes the first assertions has to list the cases before iteration 1 exists.
	cases, err := runner.New(m.store).QualityCases(m.skill, q.iter)
	if err != nil {
		m.setStatus(err.Error(), true)
		return
	}
	q.cases = cases
	q.fails = failuresOf(cases)
	q.sel = min(q.sel, max(0, len(cases)-1))
	if q.lab.Best != 0 && q.rsel == 0 && q.first {
		q.rsel = bestRow(q.lab)
	}
	q.first = false
	q.syncLog()
}

// failuresOf is the iteration's failures in the skill's case order — the order the case list uses,
// so the rail and the cases are two readings of one set and not two sortings of it.
func failuresOf(cases []runner.QualityCase) []runner.QualityFailure {
	var out []runner.QualityFailure
	for _, c := range cases {
		out = append(out, c.Failed...)
	}
	return out
}

// syncLog keeps the dojo's transcript cache on the case under the cursor: a file read only when the
// selection moves, never on every frame.
func (q *qualityState) syncLog() {
	if q == nil {
		return
	}
	path := ""
	if c := q.caseAt(); c != nil {
		if it := q.iteration(); it != nil {
			path = filepath.Join(it.Path, c.Slug, dojoArm(*c), "activity.log")
		}
	}
	if path == q.logPath {
		return
	}
	q.logPath = path
	q.logTail = tailLines(path, 6)
}

// tailLines is the last n non-blank lines of a file, stripped of their colour and indented: the pane
// shows the end of the transcript, and `v` opens the whole one. A missing log is an empty tail and
// not an error — a run cancelled before it started wrote no lines to show.
func tailLines(path string, n int) []string {
	if path == "" {
		return nil
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var keep []string
	for _, line := range strings.Split(string(text), "\n") {
		line = strings.TrimRight(xansi.Strip(line), " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		keep = append(keep, line)
	}
	if len(keep) > n {
		keep = keep[len(keep)-n:]
	}
	return keep
}

// windowStart keeps the cursor inside the rows a screen has room for: the list scrolls, the frame
// does not grow.
func windowStart(sel, top, room, n int) int {
	if sel < top {
		top = sel
	}
	if sel >= top+room {
		top = sel - room + 1
	}
	return max(0, min(top, max(0, n-room)))
}

func bestRow(lab *runner.QualityLab) int {
	for i, it := range lab.Iterations {
		if it.N == lab.Best {
			return i
		}
	}
	return 0
}

// ---------------------------------------------------------------- the lane's body

func (m *Model) qualityBody(h int) []string {
	q := m.quality
	if q == nil || q.lab == nil {
		return fitRows([]string{"", "  " + dimStyle.Render("reading the workspace…")}, h)
	}
	switch q.sec {
	case qDojo:
		return m.dojoBody(h)
	case qQuality:
		return m.qualityTableBody(h)
	}
	return m.testCasesBody(h)
}

// ---------------------------------------------------------------- initialization: the set, and the loop

// testCasesBody is the first view: the set the loop is measured on, and — while the loop is still
// closed — the band that says why and what opens it. The band is part of this view and not a screen
// of its own, because "the loop is not open yet" is a fact about the set, and a screen you can only
// see once (the old entry) is a screen you cannot check the set against later.
func (m *Model) testCasesBody(h int) []string {
	band := m.loopBand(m.width)
	if band == nil {
		return m.setupBody(h)
	}
	// A band with no room under it is not a view: the case list is what this screen is about, so a
	// window too short for both spends its rows on the set and drops the band's own tail.
	rest := h - len(band)
	if rest < 6 {
		return fitRows(band, h)
	}
	return fitRows(append(band, m.setupBody(rest)...), h)
}

// loopBand is the loop's state as one band above the set: the four steps the loop needs before it
// exists, as chips in the order they have to be done, then the one errand it is asking for and the
// key that does it.
//
// It used to be a table of four rows with a STEP/WHAT/NOW/KEY header, and then a second table of the
// cases under it — two tables competing for one window, and both of them re-stating the set the
// screen was already going to draw. A chip row says the same four facts in one line, and it reads in
// the same register as the step rail on every other screen here.
func (m *Model) loopBand(w int) []string {
	lab := m.quality.lab
	if lab == nil || !lab.InitPending {
		return nil
	}
	done, current := 0, -1
	for i, c := range lab.Checks {
		if c.OK {
			done++
			continue
		}
		if current < 0 {
			current = i
		}
	}
	out := []string{"", regionHead(w, "INITIALIZATION",
		fmt.Sprintf("the loop is not open yet · %d of %d", done, len(lab.Checks)), true), laneRule(w)}

	var chips []string
	for i, c := range lab.Checks {
		mark, name := dimStyle.Render(glyphs.Pending), dimStyle.Render(c.Key)
		switch {
		case c.OK:
			// A done step keeps its tick: a count in the corner is a number, the ticks are a record.
			mark = styleOn.Render(glyphs.Done)
		case i == current:
			mark, name = stepHere.Render(glyphs.Here), titleStyle.Render(c.Key)
		}
		chips = append(chips, mark+" "+name)
	}
	out = append(out, "  "+strings.Join(chips, "  "))

	if key, text := gateNext(lab); key != "" {
		act := &docWriter{w: w}
		act.action(key, text)
		// The action's own leading blank is the gutter the columns below already get from their
		// region head: keeping both would spend a row of a short window on white space.
		out = append(out, act.lines[1:]...)
	}
	return out
}

// laneRule is the quiet rule under a region head inside the lane, at the width a head gets: it is
// the same mark the case list and the tables draw, and one rule per region is the whole grammar.
func laneRule(w int) string {
	return "  " + dimStyle.Render(strings.Repeat(glyphs.Rule, min(64, max(10, w-4))))
}

// gateNext is the one errand the band is asking for: the first step that is not done, with the key
// that does it and the sentence that says what doing it means. The steps are the guide's own order —
// the cases, the first run, the assertions, the grade — because the first run is what the assertions
// are written from: a band that asks for sentences before anything has been run is a band asking for
// guesses.
func gateNext(lab *runner.QualityLab) (key, text string) {
	for _, c := range lab.Checks {
		if c.OK {
			continue
		}
		switch c.Key {
		case "cases":
			return c.Fix, "write the cases — evals.json, two or three to start, S opens it"
		case "run":
			if lab.Unasserted > 0 {
				return c.Fix, fmt.Sprintf("run iteration 1 — %s, both arms · nothing is judged yet", plural(lab.Cases, "case"))
			}
			return c.Fix, fmt.Sprintf("run iteration 1 — %s, both arms", plural(lab.Cases, "case"))
		case "assertions":
			if len(lab.Iterations) > 0 {
				return c.Fix, "write the assertions — v reads what the arms wrote, e writes the sentences"
			}
			return c.Fix, "an assertion for every case: a grade is made of them, e writes them"
		case "grade":
			if c.Fix == "R" {
				return c.Fix, "run iteration 1 — it grades itself, and the loop opens on the grades"
			}
			return c.Fix, "grade the run — its arms carry not graded, so the loop has nothing to open on"
		}
	}
	return "", ""
}

// ---------------------------------------------------------------- setup: the declaration

// The case column is a list of names and one mark: no rates, no counts. What it adds to the slug is
// how much of the case is declared, because "which one is still a stub" is the question this screen
// exists to answer and no rate answers it.
const (
	qListMin = 16
	qListMax = 46
)

// caseListW is the width the case column asks for: the longest slug it has to hold, plus the two-cell
// cursor mark and the one-cell declaration mark in front of it.
func (m *Model) setupBody(h int) []string {
	listW := m.caseListW()
	if paneW := min(100, m.width-listW-3); paneW >= 40 {
		return m.splitBody(h, listW, paneW, m.caseList(h), m.casePane(paneW, h))
	}
	return m.setupStacked(h)
}

// caseMark is the case's state as one glyph, and it is a reading of the grades and not of the
// declaration once there are grades to read: a tick for a case that measures the skill, a cross for one
// the skill makes worse, a hollow circle for one that proves nothing, and the declaration's own mark
// (quiet) while the case has never been graded. It is the same ladder `signalCell` colours, so the glyph
// and the count beside it can never disagree.
func caseMark(c runner.QualityCase) string {
	n, total := signalCount(c)
	if total == 0 || signalOf(c) == signalNone {
		switch {
		case strings.TrimSpace(c.Prompt) == "":
			return dimStyle.Render(glyphs.Failed)
		case len(c.Rows) == 0:
			return dimStyle.Render(glyphs.Pending)
		}
		return dimStyle.Render("·")
	}
	switch {
	case caseHurts(c):
		return statusBad.Render(glyphs.Failed)
	case n == total:
		return styleOn.Render(glyphs.Done)
	}
	return badgeWarn.Render(glyphs.Pending)
}

// caseSignal is what one case's grades say about the skill — per case, not per assertion, and from the
// newest iteration that ran it. It exists because a set can be all green and measure nothing: a case the
// baseline passes every assertion of cannot tell a skill from no skill.
type caseSignal int

const (
	signalNone     caseSignal = iota // no grades in one or both arms: run and grade first
	signalMeasures                   // every assertion passes with the skill, and not every one without it
	signalNoSignal                   // the without-skill arm passes every assertion: the baseline already does it
	signalBothFail                   // some assertion fails in each arm
	signalHurts                      // the baseline passes it and the skill does not
)

// signalOf reads a case's rows. A nil grade is "not graded", never a pass, so a half-graded case is
// signalNone and stays that way until `g` finishes — the tag is a measurement, and an unfinished one
// would be the same lie the rates refuse to tell.
func signalOf(c runner.QualityCase) caseSignal {
	if len(c.Rows) == 0 {
		return signalNone
	}
	with, without, hurts := true, true, false
	for _, a := range c.Rows {
		if a.With == nil || a.Without == nil {
			return signalNone
		}
		if !*a.With {
			with = false
			// The baseline passed one the skill fails: that is worse than a set that proves nothing, and
			// it wins the roll-up so the case tag agrees with the red glyph and the red count beside it.
			if *a.Without {
				hurts = true
			}
		}
		if !*a.Without {
			without = false
		}
	}
	switch {
	case hurts:
		return signalHurts
	case without:
		return signalNoSignal
	case with:
		return signalMeasures
	}
	return signalBothFail
}

// tag is the cell a person acts on, so the bad states are written as the move they ask for: `make it
// fail` is the whole fix for an assertion the baseline passes — sharpen it until the arm without the
// skill fails it — and `fix or drop` and `fix the skill` say what to do about the other two. The good
// state is the one thing there is nothing to do about, so it is named for what it gives: `adds value`.
func (s caseSignal) tag() string {
	switch s {
	case signalMeasures:
		return "adds value"
	case signalNoSignal:
		return "make it fail"
	case signalBothFail:
		return "fix or drop"
	case signalHurts:
		return "fix the skill"
	}
	return "not graded"
}

// style is the tag's register: the pass colour for a case doing its job, the warning colour for a case
// that cannot, the failure colour for one the skill makes worse, and quiet for one not graded yet.
func (s caseSignal) style() lipgloss.Style {
	switch s {
	case signalMeasures:
		return styleOn
	case signalNoSignal:
		return badgeWarn
	case signalBothFail:
		return badgeWarn
	case signalHurts:
		return statusBad
	}
	return dimStyle
}

// line is the sentence behind the tag: what it means, and what to do about it. The tag says what the
// case cannot show; the sentence says where the fix is, because a mark with no next move is a scold.
func (s caseSignal) line() string {
	switch s {
	case signalMeasures:
		return "the skill is what makes it pass — every assertion passes with it, and not every one without it"
	case signalNoSignal:
		return "the baseline passes every assertion, so this case cannot show a difference: sharpen the assertion, or add a harder case"
	case signalBothFail:
		return "neither arm passes every assertion — the model cannot do it either: sharpen the assertion, or drop the case"
	case signalHurts:
		return "the baseline passes it and the skill does not — the skill is making this output worse"
	}
	return "not graded yet — R runs an iteration and the judge grades it"
}

func (m *Model) caseListW() int {
	w := lipgloss.Width("CASES")
	for _, c := range m.quality.cases {
		w = max(w, lipgloss.Width(c.Slug))
	}
	// The count column is paid for out of the cases' names, so the list is wider than the longest slug by
	// the width of the number beside it: a name cut to "dummy-1…" to make room for "1/2" has spent the one
	// thing the row was for.
	return min(qListMax, max(qListMin, w+4+signalCellW+1))
}

// signalCellW is the width of the list's count column: wide enough to be headed "SIGNAL" and to hold
// "9/9", so a case with more assertions than its neighbours does not shift the column.
const signalCellW = 6

// signalCount is how many of a case's assertions measure the skill — pass with it and fail without it —
// against how many it declares. It is the case list's version of the pane's reading: the pattern column
// under the cursor says it one assertion at a time, this says it for the whole case.
func signalCount(c runner.QualityCase) (n, total int) {
	for i := range c.Rows {
		total++
		if assertionPattern(c.Rows[i]) == signalMeasures {
			n++
		}
	}
	return n, total
}

// caseHurts is whether any assertion of the case passes without the skill and fails with it. That is worse
// than measuring nothing: the skill is taking a pass away, so the case's count is red and not merely
// unproven.
func caseHurts(c runner.QualityCase) bool {
	for i := range c.Rows {
		if assertionPattern(c.Rows[i]) == signalHurts {
			return true
		}
	}
	return false
}

// signalCell is the list's count and its colour, in the order the reader needs them: red if the case
// holds an assertion the skill makes worse, green when every assertion measures the skill, the warning
// colour for anything in between, and a quiet dash when the case has not been graded.
func signalCell(c runner.QualityCase) (string, lipgloss.Style) {
	n, total := signalCount(c)
	if total == 0 || signalOf(c) == signalNone {
		return "—", dimStyle
	}
	cell := fmt.Sprintf("%d/%d", n, total)
	switch {
	case caseHurts(c):
		return cell, statusBad
	case n == total:
		return cell, styleOn
	}
	return cell, badgeWarn
}

func (m *Model) caseList(h int) []string {
	q := m.quality
	w := m.caseListW()
	lines := []string{"", regionHead(w, "CASES", "", true),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, w-4))),
		// The column names are the iteration view's own shape: a case and a count, named. The count is
		// the assertions that measure the skill over the assertions declared.
		"    " + dimStyle.Render(padCell("CASE", max(4, w-4-signalCellW-1), false)) + " " +
			dimStyle.Render(padCell("SIGNAL", signalCellW, true))}
	// The list is the declared set, with or without a run behind it: an iteration that has not
	// happened is not a reason to hide the cases iteration 1 is written from.
	room := max(1, h-len(lines))
	q.top = windowStart(q.sel, q.top, room, len(q.cases))
	for i := q.top; i < len(q.cases) && i < q.top+room; i++ {
		c := q.cases[i]
		mark := "  "
		if i == q.sel {
			mark = cursorMark.Render(glyphs.Cursor)
		}
		// The count sits at the row's right end, where the iteration view keeps its `1/2`: full is green
		// — every assertion of this case is one the skill is what makes pass — and anything short of full
		// is the warning colour, because a case that does not measure the skill is the thing to look at.
		// An ungraded case carries a dash and no colour: a count nobody measured is not a number.
		cell, style := signalCell(c)
		nameW := max(4, w-4-signalCellW-1)
		line := mark + caseMark(c) + " " +
			colText.Render(padCell(ellipsis(c.Slug, nameW), nameW, false)) + " " +
			padCell(style.Render(cell), signalCellW, true)
		if i == q.sel {
			line = selected(line)
		}
		lines = append(lines, line)
	}
	return lines
}

func (m *Model) setupStacked(h int) []string {
	lines := m.caseList(h)
	// The narrow window has no pane for the fields to go in, so they follow the list: the same two
	// fields, under the set they are being added to.
	if m.caseNewActive() {
		lines = append(lines, "")
		return fitRows(append(lines, m.caseNew.pane(m.width, max(4, h-len(lines)))...), h)
	}
	if c := m.quality.caseAt(); c != nil {
		lines = append(lines, "")
		lines = append(lines, m.caseBlock(*c, max(4, h-len(lines)))...)
	}
	return fitRows(lines, h)
}

// caseBlock is the narrow screen's version of the pane: the same record — prompt, expected output,
// files, assertions — as labelled lines, so the case reads the same whichever shape the window gives
// the setup section.
func (m *Model) caseBlock(c runner.QualityCase, room int) []string {
	d := &docWriter{w: m.width, label: 15}
	sig := signalOf(c)
	d.lines = append(d.lines, "  "+titleStyle.Render(c.Slug)+"  "+
		dimStyle.Render(fmt.Sprintf("#%d", c.ID)))
	for i, line := range wrapPlain(sig.line(), max(10, m.width-4-lipgloss.Width(sig.tag()))) {
		if i == 0 {
			d.lines = append(d.lines, "  "+sig.style().Render(sig.tag())+"  "+dimStyle.Render(line))
			continue
		}
		d.lines = append(d.lines, "  "+strings.Repeat(" ", lipgloss.Width(sig.tag())+2)+dimStyle.Render(line))
	}
	d.lines = append(d.lines, "")
	d.fact("prompt", oneLine(c.Prompt))
	d.fact("expected_output", oneLine(c.ExpectedOutput))
	if len(c.Files) == 0 {
		d.fact("files", "none — the run root starts empty")
	} else {
		d.fact("files", strings.Join(c.Files, " · "))
	}
	if len(c.Rows) == 0 {
		d.fact("assertions", "none declared yet — e writes the first one")
	}
	// The assertions are the same table the wide pane draws, in the same columns: a narrow window
	// changes where the table sits, never how a verdict is spelled.
	if len(c.Rows) == 0 {
		d.fact("assertions", "none declared yet — e writes the first one")
		return d.lines
	}
	rest := max(4, room-len(d.lines))
	d.lines = append(d.lines, assertionTable(&docWriter{w: m.width}, c, rest, true)...)
	return d.lines
}

func (m *Model) casePane(w, h int) []string {
	q := m.quality
	// A case being written takes the pane the record is drawn in: the fields are where the record
	// goes, and the set they are about stays on the left as it does for the assertion list.
	if m.caseNewActive() {
		return m.caseNew.pane(w, h)
	}
	c := q.caseAt()
	if c == nil {
		d := &docWriter{w: w}
		d.head("NO CASE", false)
		d.rule()
		d.para("The set is empty. n makes a case and S opens evals.json: two or three are enough to start, and e writes the assertions a grade is made of.")
		return fitRows(d.lines, h)
	}
	// The human review is written where the case is: the output, the sentences and the grade stay on
	// the left while the note is typed, which is the same bargain the assertion list makes.
	if m.feedbackActive() && q.feedback.slug == c.Slug {
		return q.feedback.body(w, h)
	}
	// The assertion list takes the pane while it is being typed: the list is the thing on screen, and
	// it grows with the assertions instead of scrolling inside a field half the pane's height.
	if m.assertEditActive() && m.assertEdit.slug == c.Slug {
		return m.assertEdit.pane(w, h)
	}
	// The action line is reserved before anything is measured against the window: the one line that
	// says what to press must not be the one a short window cuts.
	act := &docWriter{w: w}
	key, text := m.setupAction(*c)
	act.action(key, text)
	room := max(1, h-len(act.lines))

	recordRoom := room

	d := &docWriter{w: w, label: 12}
	d.blank()
	d.lines = append(d.lines, regionHead(w, ellipsis(c.Slug, max(4, d.body()-2)),
		fmt.Sprintf("#%d", c.ID), false))
	out := d.lines

	// The pane is two named groups — what the case asks for, and what the grade said — and the one the
	// operator came for is drawn first: the grading once there is one, the declaration while the loop is
	// closed. Each carries a bold head and a rule under it, so the pane reads as two blocks instead of
	// one column of prose with a table somewhere in the middle.
	sig := signalOf(*c)
	grading := func(budget int) []string { return m.caseGradingBlock(w, *c, sig, budget) }
	declare := func(budget int) []string { return m.caseDeclarationBlock(w, *c, budget) }
	if sig != signalNone {
		// The declaration is measured first and reserved whole when it fits beside the grading: the
		// grading's footnote must not be the thing that pushes the case out of its own pane, and half a
		// record is worse than a pointer to the file it came from.
		need, gNeed := len(declare(400)), len(grading(400))
		reserve := 1 // the line that names the group that did not fit
		if need > 0 && recordRoom-len(out)-gNeed >= need {
			reserve = need
		}
		out = append(out, grading(recordRoom-len(out)-reserve)...)
		rest := recordRoom - len(out)
		if blk := declare(rest); len(blk) > 0 {
			out = append(out, blk...)
		} else if rest >= 1 {
			// The group that did not fit is named, not halved: a head with nothing under it reads as an
			// empty case, and `S` opens the file the fields came from.
			out = append(out, "  "+dimStyle.Render(ellipsis("CASE · the prompt, expected_output and files are in evals.json — S opens it", d.body()-2)))
		}
		return fitRows(append(out, act.lines...), h)
	}
	out = append(out, declare(recordRoom-len(out))...)
	if rest := recordRoom - len(out); rest >= 4 {
		out = append(out, grading(rest)...)
	}
	return fitRows(append(out, act.lines...), h)
}

// caseDeclarationBlock is the CASE group: what the case asks for, in the file's own order, as a table
// of two columns — the field's name in the file's spelling, bold, and its value beside it. The label
// column is measured from the names themselves, so `expected_output` (fifteen cells) does not push its
// value out of line with `prompt`: a fixed fifteen-cell label, as it was, is a column that fits the
// longest name and nothing else.
func (m *Model) caseDeclarationBlock(w int, c runner.QualityCase, budget int) []string {
	if budget < 4 {
		return nil
	}
	d := &docWriter{w: w, label: 14}
	d.blank()
	d.lines = append(d.lines, regionHead(w, "CASE", "", false))
	d.lines = append(d.lines, "  "+dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(d.body(), 48)))))
	d.lines = append(d.lines, m.caseFieldRows(w, c)...)
	// The declaration goes whole or not at all: a record that shows the prompt and hides the files it
	// stages is a summary of the case, which is the one thing this group is not.
	if len(d.lines) > budget {
		return nil
	}
	return d.lines
}

// caseFieldRows is the case's declaration as its three labelled rows — the file's own names, each value
// wrapped under its label. It is drawn inside the CASE group in the initialization view and bare in the
// iteration view, where the case's slug is already the pane's head: a review is a judgement of an output
// against its expected output, and the screen that asks for one has no business hiding either of them.
func (m *Model) caseFieldRows(w int, c runner.QualityCase) []string {
	d := &docWriter{w: w, label: 14}
	prompt := oneLine(c.Prompt)
	if strings.TrimSpace(c.Prompt) == "" {
		prompt = "none — a case with no prompt cannot be run"
	}
	expected := oneLine(c.ExpectedOutput)
	if strings.TrimSpace(c.ExpectedOutput) == "" {
		expected = "none — grading has nothing to check against"
	}
	files := "none — the run root starts empty"
	if len(c.Files) > 0 {
		files = strings.Join(c.Files, " · ")
	}
	fields := []struct{ name, value string }{
		{"prompt", prompt}, {"expected_output", expected}, {"files", files},
	}
	lw := 0
	for _, f := range fields {
		lw = max(lw, lipgloss.Width(f.name))
	}
	valueW := max(10, d.body()-lw-2)
	for _, f := range fields {
		lines := wrapPlain(f.value, valueW)
		d.lines = append(d.lines, "  "+regionLabel.Render(padCell(f.name, lw, false))+"  "+
			colText.Render(lines[0]))
		for _, extra := range lines[1:] {
			d.lines = append(d.lines, strings.Repeat(" ", lw+4)+colText.Render(extra))
		}
	}
	return d.lines
}

// caseGradingBlock is the GRADING group, in four registers and one order: the numbers, then what they
// mean, then the assertions they were read from, then the key to the colours. That is the order the
// questions are asked in — how did each arm do, what does that say about the skill, which assertion is
// it, what do I do about it — and each register is a block, so a short window loses the last one rather
// than cutting the middle of the first.
func (m *Model) caseGradingBlock(w int, c runner.QualityCase, sig caseSignal, budget int) []string {
	if budget < 6 {
		return nil
	}
	d := &docWriter{w: w, label: 14}
	d.blank()
	right := "not graded yet"
	switch {
	case !c.Ran && m.quality.iter > 0:
		// The case is not ungraded: this iteration never ran it, and the verdicts it has belong to an
		// older one. Saying "not graded yet" here is the pane blaming the grade for a run that did not
		// happen.
		right = fmt.Sprintf("not in iteration %d", m.quality.iter)
	case sig == signalNone:
	case c.FailedAssertions() > 0:
		right = fmt.Sprintf("%d of %d failed", c.FailedAssertions(), c.Assertions)
	default:
		right = "every assertion passes"
	}
	d.lines = append(d.lines, regionHead(w, "GRADING", right, false))
	d.lines = append(d.lines, "  "+dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(d.body(), 48)))))
	// The group's registers are separated by a blank line each, because they answer three different
	// questions and a solid block of them is a paragraph: what the grades mean, then the table they were
	// read from, then the footnote. The arms' own counts used to stand here and no longer do — every row
	// of the table below already says which arm passed and which failed, and a summary that repeats the
	// detail above it is a line spent on nothing.
	if budget-len(d.lines) < 5 {
		return cutLines(d.lines, budget)
	}
	d.lines = append(d.lines, "")
	line, tag, style := sig.line(), sig.tag(), sig.style()
	if !c.Ran && m.quality.iter > 0 {
		line = fmt.Sprintf("iteration %d did not run this case — r runs it here, and the iteration view walks back to the one that graded it", m.quality.iter)
		tag, style = "not run", dimStyle
	}
	wrap := wrapPlain(line, max(10, d.body()-lipgloss.Width(tag)-4))
	d.lines = append(d.lines, "  "+style.Bold(true).Render(tag)+"  "+dimStyle.Render(wrap[0]))
	for _, extra := range wrap[1:] {
		if len(d.lines)+4 > budget {
			break
		}
		d.lines = append(d.lines, strings.Repeat(" ", lipgloss.Width(tag)+4)+dimStyle.Render(extra))
	}
	d.lines = append(d.lines, "")
	d.lines = append(d.lines, assertionTable(&docWriter{w: w, label: 12}, c, budget-len(d.lines), false)...)
	return cutLines(d.lines, budget)
}

// cutLines truncates a block to n lines and never pads it: a block measured with fitRows fills the room
// it was given, so the group below it is always given nothing.
func cutLines(lines []string, n int) []string {
	if n <= 0 {
		return nil
	}
	if len(lines) > n {
		return lines[:n]
	}
	return lines
}

// The assertion table's column plan: the pattern first — the reading, which is what a person scanning
// the pane needs before the evidence — then the statement it is about, then the two verdicts it was read
// from. The verdicts are the detail, the pattern is the conclusion, and the same order holds in the
// iteration view's table so one screen never reads left-to-right one way and the other another.
const (
	patW     = 14 // "fix the skill", and room to breathe beside it
	armWithW = 10 // "WITH SKILL"
	armWoW   = 13 // "WITHOUT SKILL"
	gutter   = 2  // between columns: a table read as a table, not as a sentence with numbers
)

// assertionCols is the table's width arithmetic, in one place: the indent, the pattern cell, the
// statement it gives the rest to, and three single-space gutters — the house table's own spacing, the
// one the iterations table under the trend view uses.
func assertionCols(body int) (textW int, arms bool, total int) {
	textW, arms = max(12, body-2-patW-3*gutter-armWithW-armWoW), true
	total = 2 + patW + gutter + textW + gutter + armWithW + gutter + armWoW
	// Progressive disclosure, the narrow way round: the two verdicts are the detail behind the pattern,
	// so on a pane too narrow to hold the statement and both of them the verdicts go and the statement
	// stays whole. What is drawn first is what the row is read for.
	if textW < 18 {
		arms = false
		textW = max(12, body-2-patW-gutter)
		total = 2 + patW + gutter + textW
	}
	return textW, arms, total
}

// assertionTable is the case's grading list as a table: every declared assertion, one row, its pattern,
// its statement, and both arms' verdicts. The facts an assertion is judged against ride in its own
// sentence — the format's list is strings, and a second array of facts would have to be kept in step by
// position, which is the one join this tool already distrusts.
//
// The row is read left to right as conclusion → subject → evidence, and the evidence that needs words
// (the judge's reason for a failing arm) hangs under the statement it belongs to, indented into its
// column rather than starting at the pane's edge: a line that begins at the margin under a table reads
// as a new block, not as part of the row above it.
//
// room is how many lines the pane can give it. Rows are dropped whole and counted: a row cut in half
// reads as a verdict with no assertion, which is the one lie this pane cannot tell.
func assertionTable(d *docWriter, c runner.QualityCase, room int, ownHead bool) []string {
	textW, arms, total := assertionCols(d.body())
	// The header is bold and the rule under it is sized to the table: a header in the same weight as the
	// rows is a row, and a table whose top edge is the group's rule reads as prose with numbers in it.
	sep := strings.Repeat(" ", gutter)
	cols := "  " + regionLabel.Render(padCell("PATTERN", patW, false)) + sep +
		regionLabel.Render(padCell("ASSERTION", textW, false))
	if arms {
		cols += sep + regionLabel.Render(padCell("WITH SKILL", armWithW, true)) + sep +
			regionLabel.Render(padCell("WITHOUT SKILL", armWoW, true))
	}
	colsRule := "  " + dimStyle.Render(strings.Repeat(glyphs.Rule, min(total-2, d.body())))
	title := "  " + titleStyle.Render(fmt.Sprintf("ASSERTIONS · %d", c.Assertions))
	rule := "  " + dimStyle.Render(strings.Repeat(glyphs.Rule, min(total, d.body())))
	if len(c.Rows) == 0 {
		head := []string{"", title, rule}
		for _, line := range wrapPlain("none declared yet — e writes the first one, one per line", d.body()) {
			if len(head) < room {
				head = append(head, "  "+dimStyle.Render(line))
			}
		}
		return fitRows(head, room)
	}

	// The rows are measured first, then the room is divided: the table's furniture is spent from the
	// inside out (the blank, then the title, then the rule) and the header row is the last thing kept,
	// because a table that keeps its heading and loses the assertions has kept the wrong half.
	rows := make([][]string, len(c.Rows))
	natural := 0
	for i := range c.Rows {
		rows[i] = assertionRow(d, c.Rows[i], textW, arms)
		natural += len(rows[i])
	}
	// With its own head (the narrow layout) the furniture is spent from the inside out — the blank,
	// then the title, then the rule — and the column row is the last thing kept. Inside the verdict
	// group there is no title to draw: the group is the title.
	cands := [][]string{{"", title, rule, cols, colsRule}, {title, rule, cols, colsRule}, {rule, cols, colsRule}, {cols, colsRule}}
	if !ownHead {
		cands = [][]string{{cols, colsRule}}
	}
	var head []string
	for _, cand := range cands {
		if room >= len(cand)+natural {
			head = cand
			break
		}
	}
	if head == nil {
		head = []string{cols}
	}
	out := append([]string(nil), head...)

	// The legend (what the colours mean) and the "not shown" line are the two things a table may not
	// lose silently — but only the note is worth a row: without it a list stops and says nothing.
	available := room - len(out)
	// No footnote under the table: the pattern column names the reading on every row, and a legend that
	// repeats it in a sentence is the same fact a third time. What is left is the count of what the
	// window could not hold, which is not a footnote — it is the rest of the table.
	noteRoom := 0
	if natural > available && len(c.Rows) > 1 {
		noteRoom = 1
	}
	limit := len(out) + max(1, available-noteRoom)
	drawn := 0
	for i, row := range rows {
		if len(out)+len(row) > limit {
			// A list that stops without saying how much it left out is a list that lies by omission, so
			// the count gets the last line — even at the cost of the row above it, as long as a row is
			// still drawn under the header: the count replaces a row, it never replaces the table.
			if noteRoom > 0 {
				if len(out) >= room && drawn > 1 {
					out = out[:len(out)-1]
					drawn--
				}
				if len(out) < room {
					out = append(out, "  "+dimStyle.Render(fmt.Sprintf("… %s not shown · e opens the list",
						plural(len(c.Rows)-i, "assertion"))))
				}
			}
			break
		}
		out = append(out, row...)
		drawn++
	}
	return out
}

// assertionRow is one assertion as lines: the pattern, the statement, its two verdicts as columns, and
// the judge's words under each arm that failed it — all of them in the statement's column.
func assertionRow(d *docWriter, a runner.QualityAssertion, textW int, arms bool) []string {
	pat := assertionPattern(a)
	var out []string
	hang := "  " + strings.Repeat(" ", patW+gutter)
	sep := strings.Repeat(" ", gutter)
	cell := ""
	if arms {
		cell = sep + padCell(armCell(a.With, runner.ArmWith), armWithW, true) + sep +
			padCell(mustFailCell(a.Without), armWoW, true)
	}
	for i, line := range wrapPlain(strings.Join(strings.Fields(a.Text), " "), max(10, textW)) {
		if i == 0 {
			out = append(out, "  "+pat.style().Bold(true).Render(padCell(pat.tag(), patW, false))+
				strings.Repeat(" ", gutter)+colText.Render(padCell(line, textW, false))+cell)
			continue
		}
		out = append(out, hang+colText.Render(line))
	}
	for _, arm := range []string{runner.ArmWith, runner.ArmWithout} {
		evidence := a.Evidence[arm]
		if evidence == "" {
			continue
		}
		mark := "↳ " + armWord(arm) + " · "
		lead := len([]rune(mark))
		for i, line := range wrapPlain(mark+evidence, max(10, textW)) {
			if i == 0 {
				out = append(out, hang+dimStyle.Render(line))
				continue
			}
			out = append(out, hang+strings.Repeat(" ", lead)+dimStyle.Render(line))
		}
	}
	return out
}

// armCell is one arm's verdict as a column prints it, coloured by what the verdict *means*, not by the
// word in it. In the with-skill column a fail is bad; in the without-skill column a fail is the healthy
// verdict — it is the baseline failing that proves the skill is doing the work — and a pass there is the
// problem, because the model already does it and the assertion measures nothing. Colouring `pass` green
// in both columns is the mistake this table exists to prevent: it points the reader at the wrong row.
func armCell(v *bool, arm string) string {
	switch {
	case v == nil:
		return dimStyle.Render("—")
	case *v && arm == runner.ArmWithout:
		return badgeWarn.Render("pass")
	case *v:
		return styleOn.Render("pass")
	case arm == runner.ArmWithout:
		return styleOn.Render("fail")
	}
	return statusBad.Render("fail")
}

// mustFailCell is the without-skill column, and it asks one question: did the arm without the skill fail
// this assertion, as it must for the assertion to prove anything? `✓` means it did — the assertion
// discriminates — and `✗` means it passed, so the assertion is proving the model can do it alone and has
// to be sharpened. The words are gone from this column on purpose: "pass" in that column is a fact with
// the wrong valence, and a reader who has to invert a word will invert it late, after the colour has
// already told them the wrong thing.
func mustFailCell(v *bool) string {
	switch {
	case v == nil:
		return dimStyle.Render("—")
	case !*v:
		return dimStyle.Render("fail") + " " + styleOn.Render(glyphs.Done)
	}
	return dimStyle.Render("pass") + " " + badgeWarn.Render(glyphs.Failed)
}

// assertionPattern is one assertion's reading, in the guide's own four shapes: `adds value` (pass with
// the skill, fail without), `no signal` (the baseline passes it too), `both fail` (the model cannot do it
// either — the assertion is broken, the case too hard, or it checks the wrong thing), and `ungraded`.
// `hurts` is the fifth, which the guide leaves implied: the baseline passes it and the skill does not.
func assertionPattern(a runner.QualityAssertion) caseSignal {
	switch {
	case a.With == nil || a.Without == nil:
		return signalNone
	case !*a.With && *a.Without:
		return signalHurts
	case *a.Without:
		return signalNoSignal
	case *a.With:
		return signalMeasures
	}
	return signalBothFail
}

// armWord is the arm as a sentence says it: the pane's evidence line names which arm failed.
func armWord(arm string) string {
	if arm == runner.ArmWith {
		return "with"
	}
	return "without"
}

// verdictCell is one arm's verdict as a mark: passed, failed, or never judged. A verdict nobody gave
// is a dash and not a tick — a case edited after it was graded has assertions the judge has not read.
func verdictCell(v *bool) string {
	switch {
	case v == nil:
		return dimStyle.Render(glyphs.Rule)
	case *v:
		return styleOn.Render(glyphs.Done)
	}
	return statusBad.Render(glyphs.Failed)
}

func dimWord(s string) string {
	if len(s) >= 16 {
		return strings.ReplaceAll(s[:16], "T", " ")
	}
	return s
}

// ---------------------------------------------------------------- dojo: one iteration, worked

// dojoListW is the case rail's column: the mark, the slug and the count, and no wider — the pane
// beside it carries the assertions, the evidence and the note, and it needs the room.
func (m *Model) dojoListW() int {
	w := lipgloss.Width("CASE")
	for _, c := range m.quality.cases {
		w = max(w, lipgloss.Width(c.Slug))
	}
	return min(44, max(28, 2+2+w+2+9))
}

func (m *Model) dojoBody(h int) []string {
	q := m.quality
	// The strip is paid for before the columns: a field the bottom of the window cuts off is a field
	// the editor cannot reach, while the columns have scroll and trim to lose instead. The assertion
	// list and the new-case form open here for the same reason the review does — a sentence is written
	// while the verdicts it is written against are on screen, not on a screen that pushed them away.
	stripH := 0
	switch {
	case q.feedback != nil:
		stripH = min(max(6, h/3), 4+q.feedback.want)
	case m.caseNewActive():
		stripH = min(max(6, h/3), 4+m.caseNew.want)
	case m.assertEditActive():
		rows, _ := m.assertEdit.rows(m.width - 4)
		stripH = min(max(6, h/3), 4+len(rows))
	}
	bodyH := max(8, h-stripH)
	var body []string
	if it := q.iteration(); it == nil {
		d := &docWriter{w: min(100, m.width), label: 10}
		d.head("NOTHING TO WORK ON", false)
		d.rule()
		d.para("No iteration has run yet. R runs iteration 1 with both arms over every case; the outputs, the failures and the feedback all arrive with that run.")
		body = fitRows(d.lines, bodyH)
	} else if paneW := min(100, m.width-m.dojoListW()-3); paneW >= 40 {
		body = m.splitBody(bodyH, m.dojoListW(), paneW, m.dojoList(it, bodyH), m.dojoPane(paneW, bodyH))
	} else {
		body = m.dojoStacked(bodyH)
	}
	switch {
	case q.feedback != nil:
		body = append(body, q.feedback.body(m.width, stripH)...)
	case m.caseNewActive():
		body = append(body, m.caseNew.pane(m.width, stripH)...)
	case m.assertEditActive():
		body = append(body, m.assertEdit.pane(m.width, stripH)...)
	}
	return fitRows(body, h)
}

// firstFailedAssertion is where the assertion cursor opens on a case: the first row that failed in
// either arm, or the first row when nothing did. A table that opens on a passing row answers "what
// went wrong" with "nothing" — the same mistake the case list already made once.
func firstFailedAssertion(c runner.QualityCase) int {
	for i, a := range c.Rows {
		if a.With != nil && !*a.With {
			return i
		}
		if a.Without != nil && !*a.Without {
			return i
		}
	}
	return 0
}

// failedAssertions is the iteration's failed assertions across its cases, each counted once — the
// number the list's head prints beside the declared total (runner.FailedAssertions holds the rule).
func failedAssertions(cases []runner.QualityCase) int {
	n := 0
	for i := range cases {
		n += cases[i].FailedAssertions()
	}
	return n
}

// dojoList is the iteration's CASES, one row each, with how many of each case's assertions failed out
// of how many it declares. It used to list the failed assertions instead, which had two costs: a case
// with three failures appeared three times, and a case that passed every assertion did not appear at
// all — so the human note, which is written against a case and not against a verdict, had nowhere to
// be written. The assertion that passed can still be the assertion that is wrong.
func (m *Model) dojoList(it *runner.QualityIteration, h int) []string {
	q := m.quality
	w := m.dojoListW()
	failed, total := 0, 0
	for _, c := range q.cases {
		failed += c.FailedAssertions()
		total += c.Assertions
	}
	right := fmt.Sprintf("%d/%d failed", failed, total)
	const countW = 9
	slugW := max(8, w-2-1-1-2-countW)
	lines := []string{"", regionHead(w, "CASES", right, !q.tbl),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, w-4))),
		"    " + dimStyle.Render(padCell("CASE", slugW, false)) + "  " +
			dimStyle.Render(padCell("FAILED", countW, true))}
	room := max(1, h-len(lines))
	q.top = windowStart(q.sel, q.top, room, len(q.cases))
	for i := q.top; i < len(q.cases) && i < q.top+room; i++ {
		c := q.cases[i]
		mark := "  "
		if i == q.sel {
			mark = cursorMark.Render(glyphs.Cursor)
		}
		// failed of declared: the numerator is what went wrong, the denominator is the case's whole
		// list — never a pass rate, which is a division the case's own rows do not support.
		cell := fmt.Sprintf("%d/%d", c.FailedAssertions(), c.Assertions)
		glyph, count := styleOn.Render(glyphs.Done), styleOn.Render(padCell(cell, countW, true))
		if n := c.FailedAssertions(); n > 0 {
			glyph = statusBad.Render(glyphs.Failed)
			count = statusBad.Render(padCell(cell, countW, true))
		}
		if !c.Ran {
			// A case the iteration never reached has no failures and no passes: a green `0/3` here is
			// a tick for a run that did not happen.
			glyph, count = dimStyle.Render(glyphs.Pending), dimStyle.Render(padCell("—", countW, true))
		}
		line := mark + glyph + " " + colText.Render(padCell(ellipsis(c.Slug, slugW), slugW, false)) +
			"  " + count
		if i == q.sel {
			line = selected(line)
		}
		lines = append(lines, line)
	}
	return fitRows(lines, h)
}

// dojoStacked is the dojo without its second column: the cases, then the one under the cursor.
func (m *Model) dojoStacked(h int) []string {
	lines := m.dojoList(m.quality.iteration(), max(6, h/2))
	if c := m.quality.caseAt(); c != nil {
		lines = append(lines, "")
		lines = append(lines, m.dojoPane(m.width, max(4, h-len(lines)))...)
	}
	return fitRows(lines, h)
}

// dojoPane is one case worked: the verdicts, the artifact they were read against, and the human's note.
//
// Three blocks, in the order the guide asks for them — the table of verdicts, the output those verdicts
// are about, the review of that output — each under its own head and rule, so the pane is three things
// and not a column of prose. The judge's evidence hangs under the row it is about instead of in a block
// of its own: evidence belongs to an assertion, and an "evidence" block under a table is a sentence
// about a row the reader has to find again. The arm's transcript is not drawn here at all — `enter`
// opens it, because eight dim lines of tool calls at the bottom of the pane is a wall in front of the
// three things this screen exists for.
func (m *Model) dojoPane(w, h int) []string {
	q := m.quality
	c := q.caseAt()
	if c == nil {
		d := &docWriter{w: w, label: 10}
		d.head("NO CASE", false)
		d.rule()
		d.para("This iteration has no cases on disk.")
		return fitRows(d.lines, h)
	}
	// The case's own next move, the same ladder the initialization view walks: grade it, review it,
	// revise from it.
	act := &docWriter{w: w}
	key, text := m.dojoAction(*c)
	act.action(key, text)
	room := max(1, h-len(act.lines))

	right := fmt.Sprintf("#%d · %s", c.ID, plural(len(c.Rows), "assertion"))
	if n := c.FailedAssertions(); n > 0 {
		right = fmt.Sprintf("#%d · %d of %d failed", c.ID, n, c.Assertions)
	}
	out := []string{"", regionHead(w, ellipsis(c.Slug, max(4, w-lipgloss.Width(right)-2)), right, !q.tbl),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, min(64, max(10, w-4))))}

	// Four blocks and one order of sacrifice. The table and a few rows of the output are paid for
	// first, because the review is a judgement of that output against that table; then the record the
	// judgement is made of — prompt, expected_output, files, which the pane draws bare under the case's
	// own head; then the note itself. A window too short for all of it loses the note first, then the
	// record (replaced by the key that opens the file), and never the table or the output.
	const minTable, minOutput = 3, 3
	rest := room - len(out)

	review := m.dojoReview(w, *c)
	if q.feedback != nil && q.feedback.slug == c.Slug {
		// The note is in the strip below while it is being written: the pane does not draw it twice.
		review = nil
	}
	fields := m.caseFieldRows(w, *c)
	pointer := []string{"  " + dimStyle.Render(ellipsis("the prompt and expected_output are in evals.json — S opens it", max(10, w-2)))}
	// Two blocks give way, in this order: the record first, to the line that says where it is, then the
	// note. The table and a few rows of the output never do — they are what the note is written about.
	fits := func() bool { return minTable+minOutput+len(review)+len(fields)+1 <= rest }
	if !fits() {
		fields = pointer
	}
	if !fits() {
		review = nil
	}
	if !fits() {
		fields = nil
	}
	out = append(out, fields...)

	// The table, then the one line that says what its pattern of verdicts means.
	tableRoom := max(minTable, rest-len(fields)-len(review)-1-minOutput)
	out = append(out, m.dojoTable(w, *c, tableRoom)...)
	// The measure line is the first thing to pay for the output: a case read without the sentence that
	// says what its verdicts mean still has its verdicts, while a review with no output above it has
	// nothing to review.
	if line := m.dojoMeasureLine(*c); line != "" && len(out)+1+minOutput <= room-len(review) {
		out = append(out, "  "+dimStyle.Render(ellipsis(line, max(10, w-2))))
	}
	// A verdict belongs to the words it was written against. The rows above show a dash for a sentence
	// the judge has not read, and this is the line that says why it is a dash and not a failure.
	if n := c.Unjudged(); n > 0 && len(out)+1+minOutput <= room-len(review) {
		out = append(out, "  "+badgeWarn.Render(fmt.Sprintf("%s changed since the grade — g re-grades",
			pluralOf(n, "assertion", "assertions"))))
	}

	// The artifact the verdicts are about, with whatever rows the window has left for it.
	if b := m.outputBlock(w, *c, room-len(out)-len(review)); len(b) > 0 {
		out = append(out, b...)
	}
	out = append(out, review...)
	return fitRows(append(out, act.lines...), h)
}

// dojoAction is the case's next move in this view, one key at a time: the grade while the iteration
// owes verdicts, then the review, then the revision that reads both.
func (m *Model) dojoAction(c runner.QualityCase) (key, text string) {
	// The grade is owed by the case, not by the iteration. A probe iteration — one case of a five-case
	// set — is incomplete by count and still fully graded for the case it ran, and telling the operator
	// to grade it again is telling them to re-buy verdicts that are already on screen.
	if !c.Ran {
		// Nothing to grade and nothing to read: the case has no output in this iteration at all.
		return "r", "run it — this iteration did not"
	}
	owes := c.Assertions > 0 && (!c.Graded() || c.With.Judged == 0 || c.Without.Judged == 0)
	if owes {
		return "g", "grade it — the judge reads each arm against the assertions"
	}
	// The list can also change under a grade: a sentence edited after the judge read it has no verdict,
	// and an iteration that is complete by count still owes one for what the list says now.
	if c.Unjudged() > 0 {
		return "g", "grade again — the assertions changed since the judge read them"
	}
	reviewed := strings.TrimSpace(c.Feedback) != ""
	switch {
	case c.FailedAssertions() > 0 && !reviewed:
		return "f", "the review — what the assertions did not catch"
	case c.FailedAssertions() > 0:
		return "p", "a revision — the failures and the review are what it reads"
	case !reviewed:
		return "f", "the review — an output you were happy with leaves it empty, and that is a pass"
	}
	return "p", "a revision — or R runs the next iteration"
}

// dojoReview is the human's note on the case. It is per case and not per assertion: the guide's review
// is of the output, and an assertion is a sentence about the output, not the thing being reviewed. The
// block is the note and nothing else — how to write one is the `f` field's business, and a paragraph of
// instruction under a one-line note is the pane explaining itself instead of the case.
func (m *Model) dojoReview(w int, c runner.QualityCase) []string {
	rows := []string{}
	if strings.TrimSpace(c.Feedback) != "" {
		for i, line := range wrapPlain(oneLine(c.Feedback), max(10, w-4)) {
			if i == 3 {
				rows = append(rows, "  "+dimStyle.Render(ellipsis("… f reads the rest", max(10, w-4))))
				break
			}
			rows = append(rows, "  "+colText.Render(line))
		}
	} else {
		rows = append(rows, "  "+dimStyle.Render("none — f writes it, and an empty review is a case that passed"))
	}
	return append([]string{"", regionHead(w, "REVIEW", "one per case", false),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, min(64, max(10, w-4))))}, rows...)
}

// dojoTable is the case's assertions as a table: one row each, in the order the judge read them, with
// a verdict per arm — and, under the row the cursor is on, the judge's words for it.
//
// The verdicts are words, not glyphs, because two columns of marks read as punctuation and the words
// are what the guide's table says; a row that has no verdict says so rather than borrowing its
// neighbour's.
func (m *Model) dojoTable(w int, c runner.QualityCase, room int) []string {
	textW, arms, _ := assertionCols(w)
	textW -= 2 // the cursor mark takes two cells before the pattern column
	textW = max(12, textW)
	sep := strings.Repeat(" ", gutter)
	mark, label := "  ", regionLabel.Render(padCell("PATTERN", patW, false))
	if m.quality.tbl {
		mark, label = blockMark.Render(glyphs.Block)+" ", regionOn.Render(padCell("PATTERN", patW, false))
	}
	head := mark + label + sep + regionLabel.Render(padCell("ASSERTION", textW, false))
	if arms {
		head += sep + regionLabel.Render(padCell("WITH SKILL", armWithW, true)) + sep +
			regionLabel.Render(padCell("WITHOUT SKILL", armWoW, true))
	}
	lines := []string{head, "  " + dimStyle.Render(strings.Repeat(glyphs.Rule, min(patW+gutter+textW+gutter*2+armWithW+armWoW, w-2)))}
	hang := "  " + strings.Repeat(" ", patW+1)

	// Every assertion is drawn as a block — its row, wrapped, and the judge's words under it when it is
	// the one the cursor is on — and then the blocks are windowed so that the cursor's own block is on
	// screen. A table that always started at the first assertion put the row being read off the bottom
	// of a long case, which is a table you cannot walk.
	type block struct {
		rows     []string
		selected bool
	}
	var blocks []block
	for i := range c.Rows {
		a := c.Rows[i]
		marked := i == m.quality.asel
		wrap := wrapPlain(oneLine(a.Text), textW)
		if len(wrap) == 0 {
			wrap = []string{""}
		}
		var rows []string
		for j, text := range wrap {
			if j > 0 {
				rows = append(rows, hang+colText.Render(text))
				continue
			}
			rowMark := "  "
			if marked {
				rowMark = cursorMark.Render(glyphs.Cursor)
			}
			row := rowMark + patStyle(a).Render(padCell(assertionPattern(a).tag(), patW, false)) + sep +
				colText.Render(padCell(text, textW, false))
			if arms {
				row += sep + padCell(armCell(a.With, runner.ArmWith), armWithW, true) + sep +
					padCell(mustFailCell(a.Without), armWoW, true)
			}
			if marked {
				row = selected(row)
			}
			rows = append(rows, row)
		}
		if marked {
			rows = append(rows, m.rowEvidence(w, c, a)...)
		}
		blocks = append(blocks, block{rows: rows, selected: marked})
	}

	// The window: the cursor's block and as much of its neighbours as the room holds, and a line at each
	// end that says how much was left out.
	// The head and the rule are part of the room; what is left is rows. When they do not all fit, one
	// line of the rows' budget is set aside to say how much was left out, so the marker never pushes
	// the cursor's own row out of the window it exists to keep in view.
	budget := max(1, room-2)
	from, to := 0, len(blocks)
	total := 0
	for _, b := range blocks {
		total += len(b.rows)
	}
	if total > budget {
		at := 0
		for i, b := range blocks {
			if b.selected {
				at = i
			}
		}
		from, to = at, at+1
		spare := budget - 1 - len(blocks[at].rows)
		for up, down := at-1, at+1; spare > 0; {
			grew := false
			if up >= 0 && len(blocks[up].rows) <= spare {
				spare -= len(blocks[up].rows)
				from, up = up, up-1
				grew = true
			}
			if down < len(blocks) && len(blocks[down].rows) <= spare {
				spare -= len(blocks[down].rows)
				to, down = down+1, down+1
				grew = true
			}
			if !grew {
				break
			}
		}
		shown := 0
		for i := from; i < to; i++ {
			shown += len(blocks[i].rows)
		}
		if from > 0 && shown+1 <= budget {
			lines = append(lines, "  "+dimStyle.Render(fmt.Sprintf("… %s above — r opens the report",
				pluralOf(from, "assertion", "assertions"))))
		}
	}
	for i := from; i < to; i++ {
		lines = append(lines, blocks[i].rows...)
	}
	if to < len(blocks) {
		lines = append(lines, "  "+dimStyle.Render(fmt.Sprintf("… %s more — r opens the report",
			pluralOf(len(blocks)-to, "assertion", "assertions"))))
	}
	return lines
}

// patStyle is the pattern cell's style, kept in one place because the row draws it bold and the
// evidence under it does not.
func patStyle(a runner.QualityAssertion) lipgloss.Style {
	return assertionPattern(a).style().Bold(true)
}

// rowEvidence is the judge's words for one assertion, under the row it is about: one line per arm that
// failed it, the arm's name in the gutter. A row that failed in both arms shows both, because the two
// arms failed it for different reasons and that difference is what a revision is written from.
func (m *Model) rowEvidence(w int, c runner.QualityCase, a runner.QualityAssertion) []string {
	if a.With == nil && a.Without == nil {
		why := "no grade yet — g grades the iteration"
		if !c.Ran {
			why = "this iteration did not run the case"
		}
		return []string{"    " + dimStyle.Render(ellipsis(why, max(10, w-8)))}
	}
	var out []string
	for _, f := range c.Failed {
		if f.Text != a.Text {
			continue
		}
		text := oneLine(f.Evidence)
		if text == "" {
			text = "the judge wrote no evidence — a PASS without one is a FAIL, and this is a FAIL with nothing written down"
		}
		for i, line := range wrapPlain(text, max(10, w-12)) {
			prefix := "        "
			if i == 0 {
				prefix = "    " + dimStyle.Render(padCell(armWord(f.Arm), 7, false)+" · ")
			}
			out = append(out, prefix+dimStyle.Render(line))
			if len(out) == 3 {
				return out
			}
		}
	}
	if len(out) == 0 {
		// No failure was written down: either both arms passed it — the row's two verdicts are the whole
		// record, and saying that is not the same as saying nothing — or an arm failed and the judge
		// left no words behind, which is not a pass and must not be described as one.
		text := "both arms passed it — no failure to explain"
		if (a.With != nil && !*a.With) || (a.Without != nil && !*a.Without) {
			text = "the judge recorded no reason for this failure — the verdicts are the whole record"
		}
		out = append(out, "    "+dimStyle.Render(ellipsis(text, max(10, w-8))))
	}
	return out
}

// dojoMeasureLine is the case's own answer to "does this case measure the skill": how many of its
// assertions pass with the skill and fail without it. That is the loop's whole claim — the guide's
// wording — and it is readable per case, not only as a rate.
func (m *Model) dojoMeasureLine(c runner.QualityCase) string {
	n, total := 0, 0
	for _, a := range c.Rows {
		total++
		if a.With != nil && *a.With && a.Without != nil && !*a.Without {
			n++
		}
	}
	switch {
	case total == 0:
		return ""
	case n == total:
		return "every assertion passes with the skill and fails without it — this case measures the skill"
	case n == 0:
		return "no assertion passes with and fails without the skill — this case measures the model"
	}
	return fmt.Sprintf("%d of %d pass with the skill and fail without it — the rest measure the model", n, total)
}

// outputBlock is the arm's reply, capped: the artifact a review sentence is about. Four lines is enough
// to see what was produced; the whole reply and how it got there are `enter` (the transcript) and `r`
// (the report).
func (m *Model) outputBlock(w int, c runner.QualityCase, room int) []string {
	if room < 3 {
		return nil
	}
	arm := dojoArm(c)
	resp := c.With.Response
	if arm == runner.ArmWithout {
		resp = c.Without.Response
	}
	if strings.TrimSpace(resp) == "" {
		return nil
	}
	b := &docWriter{w: w, label: 11}
	b.card("output · " + armWord(arm) + " the skill")
	lines := wrapPlain(oneLine(resp), max(10, b.body()))
	capacity := min(len(lines), min(4, room-len(b.lines)))
	for _, line := range lines[:capacity] {
		b.lines = append(b.lines, "  "+colText.Render(line))
	}
	if capacity < len(lines) {
		b.lines = append(b.lines, "  "+dimStyle.Render(fmt.Sprintf("… %d more lines — v reads the outputs", len(lines)-capacity)))
	}
	return b.lines
}

// dojoArm is the arm a case is read through: the one that failed, or the with-skill arm when nothing
// did — the transcript of the arm that carries the skill is the one a revision is written from.
func dojoArm(c runner.QualityCase) string {
	if len(c.Failed) > 0 {
		return c.Failed[0].Arm
	}
	return runner.ArmWith
}

// caseID is the case's declared id, or its slug's stand-in when the case has left the set.
func caseID(q *qualityState, slug string) int {
	if c := q.caseOf(slug); c != nil {
		return c.ID
	}
	return 0
}

// ---------------------------------------------------------------- quality: the trend

func (m *Model) qualityTableBody(h int) []string {
	q := m.quality
	if q == nil || q.lab == nil || len(q.lab.Iterations) == 0 {
		return fitRows([]string{"", "  " + dimStyle.Render("no iterations to rank yet")}, h)
	}
	const (
		iterW   = 6
		rateW   = 7
		deltaW  = 7
		casesW  = 5
		judgedW = 8
	)
	lines := []string{"", regionHead(m.width, "ITERATIONS", "best = highest with-skill rate, complete only", true),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10,
			min(m.width-4, iterW+rateW*2+deltaW+casesW+judgedW+16)))),
		"  " + dimStyle.Render(padCell("ITER", iterW, true)) + " " +
			dimStyle.Render(padCell("WITH", rateW, true)) + " " +
			dimStyle.Render(padCell("WITHOUT", rateW, true)) + " " +
			dimStyle.Render(padCell("Δ w−w/o", deltaW, true)) + " " +
			dimStyle.Render(padCell("RAN", casesW, true)) + " " +
			dimStyle.Render(padCell("JUDGED", judgedW, true)) + "  " +
			dimStyle.Render("WHEN"),
	}
	room := max(1, h-len(lines)-3)
	q.rtop = windowStart(q.rsel, q.rtop, room, len(q.lab.Iterations))
	for i := q.rtop; i < len(q.lab.Iterations) && i < q.rtop+room; i++ {
		it := q.lab.Iterations[i]
		mark := "  "
		if i == q.rsel {
			mark = cursorMark.Render(glyphs.Cursor)
		}
		state := ""
		switch {
		case it.N == q.lab.Best:
			state = "  " + styleOn.Render("best")
		case !it.Complete && (it.With.Graded() || it.Without.Graded()):
			state = "  " + dimStyle.Render("partial")
		case !it.Complete:
			state = "  " + dimStyle.Render("not graded")
		}
		line := mark + colSplit.Render(padCell(fmt.Sprintf("#%d", it.N), iterW, true)) + " " +
			qRankRate(it.With, rateW) + " " + qRankRate(it.Without, rateW) + " " +
			qRankDelta(it, deltaW) + " " +
			colText.Render(padCell(fmt.Sprintf("%d/%d", it.Cases, max(it.Cases, q.lab.Cases)), casesW, true)) + " " +
			qRankJudged(it, q.lab.Assertions, judgedW) + "  " +
			dimStyle.Render(dimWord(it.Started)) + state
		if i == q.rsel {
			line = selected(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "")
	if q.lab.Best != 0 {
		lines = append(lines, "  "+dimStyle.Render("the best is the highest with-skill rate among iterations whose every case was graded — not the newest"))
	}
	if q.lab.Flat {
		lines = append(lines, "  "+dimStyle.Render("the last two complete iterations tie: the guide's stop signal"))
	}
	// The three column names that cannot carry their own basis on one head row. A delta between the
	// arms and a delta against the last iteration are different facts; RAN of the declared set is what
	// makes two rates comparable at all; and JUDGED is assertions judged, which is why a rate is a rate.
	lines = append(lines, "  "+dimStyle.Render(fmt.Sprintf(
		"Δ = with − without, this iteration · RAN = cases run of %d · JUDGED = assertions judged of %d",
		q.lab.Cases, q.lab.Assertions)))
	return fitRows(lines, h)
}

// qRankRate is one arm's rate as a rate: the percentage and nothing beside it. The judged count used
// to sit in the same cell ("89% 5/6"), which reads as arithmetic that does not hold — a rate that
// averages the cases is not the pooled assertions, and a cell that invites the division is a cell that
// lies about one of its two numbers.
func qRankRate(a runner.QualityArm, width int) string {
	if !a.Graded() {
		return dimStyle.Render(padCell("—", width, true))
	}
	return styleOn.Render(padCell(fmt.Sprintf("%.0f%%", a.Rate*100), width, true))
}

// qRankJudged is how much of the declared grading list the arms actually saw: the completeness fact,
// in its own column, where it cannot be mistaken for a pass count.
func qRankJudged(it runner.QualityIteration, declared, width int) string {
	if declared <= 0 || !it.With.Graded() {
		return dimStyle.Render(padCell("—", width, true))
	}
	text := fmt.Sprintf("%d/%d", it.With.Judged, declared)
	if it.With.Judged < declared {
		return statusBad.Render(padCell(text, width, true))
	}
	return colText.Render(padCell(text, width, true))
}

func qRankDelta(it runner.QualityIteration, width int) string {
	if !it.With.Graded() || !it.Without.Graded() {
		return dimStyle.Render(padCell("—", width, true))
	}
	text := fmt.Sprintf("%+.0f", it.Delta*100)
	if it.Delta < 0 {
		return statusBad.Render(padCell(text, width, true))
	}
	return styleOn.Render(padCell(text, width, true))
}

// ---------------------------------------------------------------- keys

func (m *Model) updateQuality(msg tea.KeyMsg) tea.Cmd {
	q := m.quality
	if q == nil {
		return nil
	}
	// A view is entered from the menu and left with esc; inside one, nothing switches views. The
	// keys here are the view's own, plus the two spends that belong to the loop rather than to the
	// screen it is standing on — a lane you can jump around inside is a lane where "where am I" is a
	// question you ask twice.
	switch msg.String() {
	case "g":
		return m.startGrading()
	case "R":
		return m.startQualityRun()
	}
	switch q.sec {
	case qDojo:
		return m.updateDojo(msg)
	case qQuality:
		return m.updateQualityTable(msg)
	}
	return m.updateSetup(msg)
}

func (m *Model) updateSetup(msg tea.KeyMsg) tea.Cmd {
	q := m.quality
	switch msg.String() {
	case "j", "down":
		q.sel = min(q.sel+1, max(0, len(q.cases)-1))
	case "k", "up":
		q.sel = max(0, q.sel-1)
	case "home":
		q.sel = 0
	case "G", "end":
		q.sel = max(0, len(q.cases)-1)
	case "e":
		return m.openAssertEditor()
	case "r":
		return m.startCaseRun()
	case "v":
		m.openCaseOutputs()
	case "f":
		return m.openFeedbackEditor()
	case "n":
		return m.openCaseForm()
	case "d", "delete":
		m.askDeleteCase()
	case "S":
		return m.editCaseFile()
	}
	return nil
}

// updateDojo walks one iteration: the case list and the assertions of the case under the cursor are
// two columns with one keyboard, and `tab` is which of them has it. The table is a second axis on
// purpose — j k on the case list answers "which case", j k in the table answers "which assertion",
// and one pair of keys for both would make one of the two questions unanswerable.
func (m *Model) updateDojo(msg tea.KeyMsg) tea.Cmd {
	q := m.quality
	rows := 0
	if c := q.caseAt(); c != nil {
		rows = len(c.Rows)
	}
	switch msg.String() {
	case "tab":
		if rows == 0 {
			return nil
		}
		q.tbl = !q.tbl
		if q.tbl {
			q.asel = firstFailedAssertion(*q.caseAt())
		}
	case "j", "down":
		if q.tbl {
			q.asel = min(q.asel+1, max(0, rows-1))
			return nil
		}
		q.sel = min(q.sel+1, max(0, len(q.cases)-1))
		q.asel = 0
		if c := q.caseAt(); c != nil {
			q.asel = firstFailedAssertion(*c)
		}
		q.syncLog()
	case "k", "up":
		if q.tbl {
			q.asel = max(0, q.asel-1)
			return nil
		}
		q.sel = max(0, q.sel-1)
		if c := q.caseAt(); c != nil {
			q.asel = firstFailedAssertion(*c)
		}
		q.syncLog()
	case "home":
		if q.tbl {
			q.asel = 0
			return nil
		}
		q.sel = 0
		if c := q.caseAt(); c != nil {
			q.asel = firstFailedAssertion(*c)
		}
		q.syncLog()
	case "G", "end":
		if q.tbl {
			q.asel = max(0, rows-1)
			return nil
		}
		q.sel = max(0, len(q.cases)-1)
		if c := q.caseAt(); c != nil {
			q.asel = firstFailedAssertion(*c)
		}
		q.syncLog()
	case "[", "left":
		m.stepIteration(-1)
	case "]", "right":
		m.stepIteration(1)
	case "f":
		return m.openFeedbackEditor()
	case "e":
		// The list itself, under the verdicts it is read against: an assertion that turns out to be
		// unmeasurable is edited where the evidence for that is, not on the screen that declared it.
		return m.openAssertEditor()
	case "r":
		// The case under the cursor, both arms and the judge: the same probe the initialization view
		// runs, because a revision is asked for from here and the case it changes has to be runnable
		// from here. `R` is the whole set, and it stays global to the lane.
		return m.startCaseRun()
	case "n":
		return m.openCaseForm()
	case "d", "delete":
		m.askDeleteCase()
	case "p":
		// The guide's step 1, asked of the agent that is allowed to write: the failures, the feedback
		// and the skill, in one call, landing as a proposal on its own screen.
		return m.startRevision()
	case "v":
		// v is what the arms wrote, in both views; the transcript of how they got there is `enter`.
		m.openCaseOutputs()
	case "enter":
		m.openTranscript()
	case "S":
		// The case file, for the fields the pane only reads: the prompt, the expected output, the
		// fixtures. It is the line the short window prints instead of them.
		return m.editCaseFile()
	case "E":
		return m.editSkillFile()
	}
	return nil
}

// stepIteration walks the iterations without leaving the dojo: the loop's ladder is one step of one
// iteration at a time, and reading the previous run's evidence beside this one's is how a revision
// gets checked against what it was fixing.
func (m *Model) stepIteration(delta int) {
	l := m.quality.lab
	if l == nil || len(l.Iterations) == 0 {
		return
	}
	at := 0
	for i, it := range l.Iterations {
		if it.N == m.quality.iter {
			at = i
		}
	}
	next := min(max(0, at+delta), len(l.Iterations)-1)
	if next == at {
		m.setStatus(fmt.Sprintf("iteration %d is the %s one on disk", m.quality.iter,
			map[bool]string{true: "newest", false: "oldest"}[delta > 0]), false)
		return
	}
	m.quality.iter, m.quality.sel, m.quality.top = l.Iterations[next].N, 0, 0
	m.reloadQuality()
	m.setStatus(fmt.Sprintf("iteration %d of %d in the iteration view", m.quality.iter, len(l.Iterations)), false)
}

func (m *Model) updateQualityTable(msg tea.KeyMsg) tea.Cmd {
	q := m.quality
	switch msg.String() {
	case "j", "down":
		q.rsel = min(q.rsel+1, max(0, len(q.lab.Iterations)-1))
	case "k", "up":
		q.rsel = max(0, q.rsel-1)
	case "home":
		q.rsel = 0
	case "G", "end":
		q.rsel = max(0, len(q.lab.Iterations)-1)
	case "enter":
		// The table's question is "what happened in that iteration", and the answer is its evidence,
		// not its own report: enter opens the dojo on it.
		if q.rsel >= 0 && q.rsel < len(q.lab.Iterations) {
			q.iter, q.sel, q.top = q.lab.Iterations[q.rsel].N, 0, 0
			m.reloadQuality()
			m.setQualitySection(qDojo)
			m.setStatus(fmt.Sprintf("iteration %d is in the iteration view", q.iter), false)
		}
	case "r":
		m.openQualityReport()
	case "g":
		if q.rsel >= 0 && q.rsel < len(q.lab.Iterations) {
			q.iter = q.lab.Iterations[q.rsel].N
		}
		return m.startGrading()
	}
	return nil
}

// startGrading grades the iteration on screen. It is the one spend the workbench asks for besides
// the run itself: one judge call per arm, and the verdicts land in the arm's own grading.json.
func (m *Model) startGrading() tea.Cmd {
	q := m.quality
	if q == nil || q.iter == 0 {
		m.setStatus("nothing to grade — no iteration on disk", true)
		return nil
	}
	it := q.iteration()
	owing := m.casesOwingVerdicts()
	if len(owing) == 0 {
		m.setStatus("every assertion on this iteration has a verdict — nothing to grade", false)
		return nil
	}
	// Any verdict at all means a judge pass has been here, and a second one replaces what it bought:
	// the reason is that the list those verdicts were read against has changed, and that is worth a
	// question. An iteration with not graded — the whole run made with --no-grade, an older build, or
	// the grade cancelled — has nothing to replace, so `g` finishes it without asking.
	if it != nil && (it.With.Graded() || it.Without.Graded()) {
		return m.askRegrade(it, owing)
	}
	return m.startGradeRun(false, owing)
}

// casesOwingVerdicts is the cases of the iteration the judge still has to read: a case whose
// assertions were rewritten after the grade, a case an arm was never judged for, and a case that never
// got a verdict at all. It is what makes a re-grade small — a sentence edited in one case is two judge
// calls, not two per case in the iteration — and a case the iteration never ran cannot owe anything,
// because there is no output to read.
func (m *Model) casesOwingVerdicts() []runner.QualityCase {
	q := m.quality
	if q == nil {
		return nil
	}
	var out []runner.QualityCase
	for _, c := range q.cases {
		if !c.Ran || c.Assertions == 0 {
			continue
		}
		if !c.Graded() || c.Unjudged() > 0 || c.With.Judged == 0 || c.Without.Judged == 0 {
			out = append(out, c)
		}
	}
	return out
}

// startGradeRun is one judge pass over the iteration on screen. force re-grades arms that already
// carry verdicts; without it the judge skips them and says so, which is what `g` on an ungraded
// iteration does.
func (m *Model) startGradeRun(force bool, cases []runner.QualityCase) tea.Cmd {
	q := m.quality
	if q == nil || len(cases) == 0 {
		return nil
	}
	scope := fmt.Sprintf("grading run · iteration %d", q.iter)
	if force {
		scope = fmt.Sprintf("re-grading · iteration %d", q.iter)
	}
	if len(cases) != len(q.cases) {
		scope += " · " + pluralOf(len(cases), "case", "cases")
	}
	ids := make([]int, 0, len(cases))
	for _, c := range cases {
		ids = append(ids, c.ID)
	}
	m.run = &runState{kind: runGrade, scope: scope,
		hint: "the judge reads each arm's outputs against the declared assertions"}
	m.run.live = newLiveRun(m.run.scope, false)
	m.seedOutputRun(cases, q.iter)
	m.mode = screenRunLive
	return m.startJudgeRun(force, ids)
}

// askRegrade is the one question `g` asks on a complete iteration. Re-grading spends one judge call
// per arm over verdicts that exist because the list moved under them — worth asking, and worth
// saying what it costs.
func (m *Model) askRegrade(it *runner.QualityIteration, owing []runner.QualityCase) tea.Cmd {
	calls := 2 * len(owing)
	// The count that matters is the cases, not the arms: a sentence edited in one case is two judge
	// calls, and the rest of the iteration is verdicts about words nobody touched.
	lines := []string{
		fmt.Sprintf("      %d of %s owe a verdict.", len(owing), pluralOf(it.Cases, "case", "cases")),
		fmt.Sprintf("      That is %d judge call(s), and the rates it produces replace the ones on screen.", calls),
		"",
		"      Do it when an assertion changed after the grade: the grades describe the old list,",
		"      and the iteration view draws an ungraded assertion as a dash.",
	}
	m.mode = screenAsk
	m.ask = &askState{
		scope: fmt.Sprintf("re-grade iteration %d", it.N),
		title: fmt.Sprintf("GRADE ITERATION %d AGAIN?", it.N),
		lines: lines,
		yes:   "re-grade it",
		back:  screenQuality,
		then:  func(m *Model) tea.Cmd { return m.startGradeRun(true, owing) },
	}
	return nil
}

func (m *Model) startJudgeRun(force bool, ids []int) tea.Cmd {
	if m.quality == nil {
		return nil
	}
	skill, iteration := m.skill, m.quality.iter
	ctx, cancel := context.WithCancel(context.Background())
	m.run.live.cancel = cancel
	ch := make(chan runner.Event, 32)
	m.runEvents = ch
	r := runner.New(m.store)
	r.Emit = func(e runner.Event) {
		select {
		case ch <- e:
		default:
		}
	}
	go func() {
		defer close(ch)
		if _, err := r.GradeIteration(ctx, skill, iteration,
			runner.GradeOptions{Force: force, CaseIDs: ids}); err != nil {
			ch <- runner.Event{Kind: "error", Lane: store.LaneOutput, Err: err, Message: err.Error()}
		}
	}()
	return tea.Batch(waitForEvent(ch), tickEvery())
}

// startQualityRun runs the next iteration: every case, both arms, the guide's rerun. It is the
// measurement of the set and it is always offered — including over a set that has no assertion yet,
// where the run is the first look: the outputs it writes are what the assertions are written from.
func (m *Model) startQualityRun() tea.Cmd {
	q := m.quality
	if q == nil || q.lab == nil {
		return nil
	}
	if q.lab.Cases == 0 {
		m.setStatus("no cases — write evals.json first", true)
		return nil
	}
	return m.startOutputRun(q.cases)
}

// startCaseRun is `r` in the initialization view: one case, both arms, then the judge.
//
// It is the probe an assertion is written against — a sentence cannot be sharpened until the case has
// been run, and the first run of a case exists to show what the agent writes. It writes an iteration
// of one case, so it never ranks: a rate over part of the set is not comparable with a rate over the
// set, and the band and the table both say which cases a run reached.
func (m *Model) startCaseRun() tea.Cmd {
	q := m.quality
	if q == nil {
		return nil
	}
	c := q.caseAt()
	if c == nil {
		m.setStatus("no case on screen — n makes the first one", true)
		return nil
	}
	if strings.TrimSpace(c.Prompt) == "" {
		m.setStatus(c.Slug+" has no prompt — S writes it; a case with no prompt cannot be run", true)
		return nil
	}
	return m.startOutputRun([]runner.QualityCase{*c})
}

// startOutputRun is the one place an output run starts: the cases it covers, the iteration it writes,
// both arms and the judge. `R` hands it the whole set and `r` hands it one case; everything after the
// choice — the live screen, the ids the runner filters on, the spend — is the same.
func (m *Model) startOutputRun(cases []runner.QualityCase) tea.Cmd {
	plan := m.outputRunPlan(cases)
	m.run = &runState{kind: runQuality, scope: plan.scope, hint: plan.hint}
	m.run.live = newLiveRun(plan.scope, false)
	m.seedOutputRun(plan.cases, plan.iter)
	m.mode = screenRunLive
	return m.startQualityRunCmd(plan)
}

// outputRunPlan is what a run will do, decided before anything spends: the iteration it writes into,
// the cases it covers, and the two lines the live screen opens with. It is a plain reading of the disk
// through the runner's own plan, so `r` and `R` differ by which cases they hand it and not by which
// iteration they believe in.
//
// An iteration is the set measured once. A case run on its own therefore joins the iteration being
// built instead of minting one of its own — otherwise three cases run one at a time made three
// one-case iterations and the set was never measured — and `R` over an iteration that half holds the
// set runs exactly the cases it is missing, which is the run that closes it.
type outputRunPlan struct {
	iter    int
	ids     []int
	cases   []runner.QualityCase // the cases this run covers
	replace bool                 // their directories are cleared first: they are measured again
	scope   string
	hint    string
}

func (m *Model) outputRunPlan(cases []runner.QualityCase) outputRunPlan {
	plan := outputRunPlan{cases: cases}
	target, err := runner.New(m.store).PlanIteration(m.skill, true)
	if err != nil || target.Iter <= 0 {
		target = runner.IterationPlan{Iter: 1}
		if n := len(m.quality.lab.Iterations); n > 0 {
			target = runner.IterationPlan{Iter: m.quality.lab.Iterations[n-1].N + 1}
		}
	}
	plan.iter = target.Iter
	open := map[string]bool{}
	for _, slug := range target.Open {
		open[slug] = true
	}
	if target.Join {
		var run []runner.QualityCase
		for _, c := range cases {
			if open[c.Slug] {
				run = append(run, c)
			}
		}
		if len(run) == 0 {
			// Everything asked for is already in the iteration: this measures it again, in place, and
			// leaves the cases nobody asked about on the runs they have.
			plan.replace = true
		} else {
			plan.cases = run
		}
	}
	for _, c := range plan.cases {
		plan.ids = append(plan.ids, c.ID)
	}

	plan.scope = fmt.Sprintf("output run · iteration %d", plan.iter)
	if len(plan.cases) == 1 {
		plan.scope += " · " + plan.cases[0].Slug
	}
	// What the run leaves behind: an iteration that holds every case is a measurement, and one that
	// does not is a look at part of the set. The hint says which before the spend, because a rate over
	// half a set is not comparable with a rate over the set.
	set, held := len(m.quality.cases), len(plan.cases)
	if target.Join {
		held = len(m.quality.cases) - len(target.Open) + len(plan.cases)
	}
	hint := ""
	switch {
	case plan.replace && len(plan.cases) == 1:
		hint = fmt.Sprintf("measured again in iteration %d — the other cases keep the runs they have", plan.iter)
	case plan.replace:
		hint = fmt.Sprintf("measured again in iteration %d — a case is replaced, never doubled", plan.iter)
	case held >= set:
		hint = fmt.Sprintf("both arms, %s, then the judge — the set is whole, so it ranks",
			pluralOf(held, "case", "cases"))
	case target.Join:
		hint = fmt.Sprintf("iteration %d holds %d of %d — this runs %d of the %d still open; no rate until the set is whole",
			plan.iter, held-len(plan.cases), set, len(plan.cases), len(target.Open))
	default:
		hint = fmt.Sprintf("both arms, this one case, then the judge — iteration %d starts with %d of %d, so no rate yet",
			plan.iter, held, set)
	}
	// A run over cases that declare no assertion has nothing to judge: it is the look the first
	// assertions are written from, and the screen says so before the spend rather than after it.
	if assertedIn(plan.cases) == 0 {
		hint = "both arms, no judge — the outputs are what the assertions are written from"
	} else if assertedIn(plan.cases) < len(plan.cases) {
		hint = fmt.Sprintf("both arms; %d of %d cases are judged — the rest have no assertion yet",
			assertedIn(plan.cases), len(plan.cases))
	}
	plan.hint = hint
	return plan
}

// assertedIn is how many of the cases being run declare at least one assertion: the count that decides
// whether the judge has anything to read.
func assertedIn(cases []runner.QualityCase) int {
	n := 0
	for _, c := range cases {
		if c.Assertions > 0 {
			n++
		}
	}
	return n
}

// seedOutputRun puts the whole set on the live screen before the first agent call: every case, both
// arms, waiting. A run whose table grows as it goes shows one row at the start and reads as a run of
// one case, which is exactly the question the operator is asking while it spends his money.
func (m *Model) seedOutputRun(cases []runner.QualityCase, iter int) {
	p := m.run.live
	p.iter = iter
	p.path = fmt.Sprintf("%s-workspace/iteration-%d/", m.skill, iter)
	for _, c := range cases {
		lc := p.caseAt(c.Slug)
		for _, arm := range runner.AllArms {
			lc.runs = append(lc.runs, liveArm{name: arm, status: "pending"})
		}
	}
}

func (m *Model) startQualityRunCmd(plan outputRunPlan) tea.Cmd {
	ch := make(chan runner.Event, 128)
	m.runEvents = ch
	ctx, cancel := context.WithCancel(context.Background())
	m.run.live.cancel = cancel
	r := runner.New(m.store)
	r.Emit = func(e runner.Event) {
		select {
		case ch <- e:
		case <-ctx.Done():
		}
	}
	// Grade is part of the run: a set of outputs nobody judged is not an iteration, and the second
	// key that used to judge it was a step with nothing to wait for in between. CaseIDs is what makes
	// `r` smaller than `R` — one case, the same two arms, the same judge.
	// Fill and Replace are the plan the screen showed: the run joins the iteration it named, and a case
	// it is measuring again replaces its own directory instead of sitting beside it.
	opts := runner.Options{Lane: store.LaneOutput, Skills: []string{m.skill}, CaseIDs: plan.ids,
		Arms: runner.AllArms, Isolate: true, Log: runner.ActivityFull, Grade: true,
		Fill: true, Replace: plan.replace}
	go func() {
		defer close(ch)
		if err := r.RunQuality(ctx, opts); err != nil {
			ch <- runner.Event{Kind: "error", Lane: store.LaneOutput, Err: err, Message: err.Error()}
		}
	}()
	return tea.Batch(waitForEvent(ch), tickEvery())
}

// openTranscript is `v` in the dojo: the selected arm's whole execution log, in the document reader.
// It is the run's own lines, not a summary of them — "what did the agent actually do" is a question a
// report cannot answer and a transcript can.
func (m *Model) openTranscript() {
	q := m.quality
	c := q.caseAt()
	if c == nil {
		m.setStatus("no case under the cursor — nothing to transcribe", true)
		return
	}
	if q.logPath == "" {
		m.setStatus("that arm has no activity log on disk", true)
		return
	}
	m.openLogDoc(q.logPath, c.Slug+" · "+armWord(dojoArm(*c)), screenQuality)
}

// openQualityReport opens the iteration's own report.md — the artifact the run wrote. It is markdown,
// and the reader renders it as markdown: the report's headings, tables and code blocks are how it
// makes its case, and the source of them is not what it says.
func (m *Model) openQualityReport() {
	q := m.quality
	if q == nil || q.iter == 0 {
		return
	}
	info := runner.RunInfo{}
	r := runner.New(m.store)
	infos := r.ListRunsFor(m.skill)
	for _, in := range infos {
		if in.Lane == store.LaneOutput && in.Iteration == q.iter {
			info = in
		}
	}
	if info.Path == "" {
		m.setStatus("that iteration has no report yet", true)
		return
	}
	text, err := runner.ReadReport(r.ReportPath(info))
	if err != nil {
		m.setStatus(err.Error(), true)
		return
	}
	m.doc = &docState{scope: "report", title: fmt.Sprintf("REPORT · ITERATION %d", q.iter),
		src: text}
	m.backTo = m.mode
	m.mode = screenDoc
}

// qualityHints are the lane's keys, worded as the thing each one does in the view you are in. A view
// has no key to another view: the way across is esc, and the menu names the three.
func qualityHints(m *Model) []hint {
	q := m.quality
	if q == nil {
		return []hint{{"esc", "skills"}}
	}
	if m.assertEditActive() {
		return m.assertEdit.hints()
	}
	if m.caseNewActive() {
		return m.caseNew.hints()
	}
	if q.feedback != nil {
		return q.feedback.hints()
	}
	// A key is offered only where it can do its job — R over a set with nothing to judge is the
	// spend the lane exists to prevent — and the way out names the level it goes to.
	trail := []hint{{"esc", "the three views"}}
	switch q.sec {
	case qDojo:
		// The band carries the case's next move and the two human keys, and not the six others this
		// view answers to: `[ ]` steps iterations, `E` opens SKILL.md and `r` the report, and a band of
		// eight hints is a legend rather than a hint.
		hints := []hint{{"j k", "cases"}, {"tab", "the assertions"}}
		if q.tbl {
			hints[0], hints[1] = hint{"j k", "assertions"}, hint{"tab", "the cases"}
		}
		if c := q.caseAt(); c != nil {
			key, _ := m.dojoAction(*c)
			hints = append(hints, hint{key, dojoWord(key)})
			if key != "f" {
				hints = append(hints, hint{"f", "review"})
			}
			if key != "p" {
				hints = append(hints, hint{"p", "revision"})
			}
			if c.Ran {
				hints = append(hints, hint{"v", "outputs"})
			}
			hints = append(hints, hint{"r", "run it"})
			hints = append(hints, hint{"R", "all"})
		}
		return append(hints, trail...)
	case qQuality:
		hints := []hint{{"j k", "iterations"}, {"enter", "its evidence"}, {"r", "report"},
			{"g", "grade"}}
		return append(append(hints, hint{"R", "run next"}), trail...)
	}
	hints := []hint{{"j k", "cases"}, {"n d", "new·del"}}
	// The band offers the key this case is waiting for and the ones that are always true: the ladder
	// in the pane is one press at a time, and a row of nine keys is a legend, not a hint.
	if c := q.caseAt(); c != nil {
		if k, _ := m.setupAction(*c); k != "" {
			hints = append(hints, hint{k, ladderWord(k)})
		}
		// `e` is always on offer, run or not: the assertions are a list a person writes, and nothing
		// about the disk makes the list illegal — the ladder says what to do next, not what is allowed.
		if k, _ := m.setupAction(*c); k != "e" {
			hints = append(hints, hint{"e", "assertions"})
		}
		if c.Ran {
			hints = append(hints, hint{"v", "outputs"})
		}
	}
	// R is the whole set in one spend, and it is offered whenever there is a set to run: a declared
	// set is measurable, and a key that appears only once every case is asserted is a key you look
	// for and cannot find.
	if q.lab != nil && q.lab.Cases > 0 {
		hints = append(hints, hint{"R", "all cases"})
	}
	hints = append(hints, hint{"S", "evals.json"})
	return append(hints, trail...)
}

// dojoWord is the iteration view's version of the band's one-word errand.
func dojoWord(key string) string {
	switch key {
	case "g":
		return "grade"
	case "f":
		return "review"
	case "p":
		return "revision"
	}
	return key
}

// ladderWord is the one word the band gives the errand the pane says in a sentence.
func ladderWord(key string) string {
	switch key {
	case "S":
		return "the prompt"
	case "r":
		return "run"
	case "e":
		return "assertions"
	case "g":
		return "grade"
	case "f":
		return "review"
	}
	return key
}

// qualityFactsLine is the row under the bar inside a view: the loop's numbers, and nothing else. The
// three view names used to stand on the left of this row as a rail — the screen's siblings with a dot
// on the one you are in — and that was the menu drawn a second time above a view whose path already
// ends in its own name, advertising a position with no key behind it. The numbers keep the row
// because no view counts the loop: the set counts its cases, the iteration its failures, the table its
// iterations, and none of them says "iteration #3 · 5 on disk · best #3".
func (m *Model) qualityFactsLine(w int) string {
	facts := m.qualityFacts()
	if facts == "" {
		return ""
	}
	return " " + statValue.Render(ellipsis(facts, max(8, w-1)))
}

// qualityFacts is that row's content: the loop's numbers, and which iteration the sections are
// reading. The sections count the set once above the columns; this row is where the whole loop is one
// glance — which is why it outlives the view names that used to share the row.
func (m *Model) qualityFacts() string {
	q := m.quality
	if q == nil || q.lab == nil {
		return ""
	}
	parts := []string{plural(q.lab.Cases, "case"), plural(q.lab.Assertions, "assertion")}
	if n := len(q.lab.Iterations); n > 0 {
		// The number on screen and how many there are, in that order: the ladder's position is a
		// detail, and the number is what a note, a report and a failure all refer to.
		parts = append(parts, fmt.Sprintf("iteration #%d · %d on disk", q.iter, n))
	}
	if q.lab.Best != 0 {
		parts = append(parts, fmt.Sprintf("best #%d", q.lab.Best))
	}
	return strings.Join(parts, " · ")
}

// ---------------------------------------------------------------- the chooser's output rows

// loadQualitySkills fills the skill table for the output lane: one row per skill with its cases,
// the last iteration's two rates, and how many iterations it has measured.
func (m *Model) loadQualitySkills() {
	m.skills.rows = nil
	bySkill := map[string][]store.Issue{}
	for _, issue := range m.store.Validate() {
		bySkill[issue.Skill] = append(bySkill[issue.Skill], issue)
	}
	r := runner.New(m.store)
	for _, name := range m.skillNames() {
		row := skillRow{name: name, issues: bySkill[name], qWith: "—", qWithout: "—"}
		lab, err := r.QualityLab(name)
		if err == nil {
			row.onDisk = true
			row.qCases = lab.Cases
			row.qAssert = lab.Assertions
			row.qIters = len(lab.Iterations)
			row.qBest = lab.Best
			row.qState = qualityRowState(lab)
			if n := len(lab.Iterations); n > 0 {
				last := lab.Iterations[n-1]
				row.qWith, row.qWithout = qRateText(last.With), qRateText(last.Without)
				row.qFail = len(last.Failed)
			}
		} else {
			row.qState = "cannot read"
		}
		m.skills.rows = append(m.skills.rows, row)
	}
	m.skills.cursor = min(m.skills.cursor, max(0, len(m.skills.rows)-1))
}

func qRateText(a runner.QualityArm) string {
	if !a.Graded() {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", a.Rate*100)
}

// qualityRowState is one phrase per skill: what the output loop would do next with it.
func qualityRowState(lab *runner.QualityLab) string {
	switch {
	case lab.Cases == 0:
		return "no cases — write evals.json"
	case len(lab.Iterations) == 0:
		return "cases ready — nothing run yet"
	case lab.Assertions == 0:
		return "add assertions after the first run"
	case lab.Best == 0:
		return "grade the last iteration"
	}
	last := lab.Iterations[len(lab.Iterations)-1]
	if !last.Complete {
		return fmt.Sprintf("iteration %d ungraded", last.N)
	}
	if len(last.Failed) > 0 {
		return fmt.Sprintf("%s failing — revise", plural(len(last.Failed), "assertion"))
	}
	if lab.Flat {
		return "flat — stop or sharpen"
	}
	return "clean · best #" + fmt.Sprint(lab.Best)
}

// skillQualityPane is the chooser's pane for the output lane: the cases, the loop's state, and
// what the last iteration failed — the same three facts the trigger pane gives, in this lane's
// terms.
func (m *Model) skillQualityPane(w, h int) []string {
	if len(m.skills.rows) == 0 {
		return nil
	}
	r := m.skills.rows[m.skills.cursor]
	d := &docWriter{w: w, label: 12}
	var blocks [][]string
	from := len(d.lines)
	block := func() {
		blocks = append(blocks, append([]string(nil), d.lines[from:]...))
		from = len(d.lines)
	}
	d.head(strings.ToUpper(r.name), false)
	d.rule()
	d.lead(r.qState)
	block()

	d.card("The set")
	d.fact("cases", plural(r.qCases, "case"))
	d.fact("assertions", plural(r.qAssert, "assertion"))
	d.fact("fixtures", r.name+"/evals/files/<slug>/")
	block()

	d.card("The loop")
	with, without := r.qWith, r.qWithout
	if with == "" {
		with, without = "—", "—"
	}
	d.fact("with skill", with)
	d.fact("without", without)
	d.fact("iterations", fmt.Sprint(r.qIters))
	best := "none complete"
	if r.qBest > 0 {
		best = "#" + fmt.Sprint(r.qBest)
	}
	d.fact("best", best)
	block()

	if r.qFail > 0 {
		d.card("Findings")
		d.fact("failing", plural(r.qFail, "assertion")+" in the last iteration")
		block()
	}
	if len(r.issues) > 0 {
		d.card("The tree")
		for i, issue := range r.issues {
			if i == 2 {
				d.fact("more", plural(len(r.issues)-2, "finding"))
				break
			}
			d.fact(severityWord(issue.Severity), issue.Msg)
		}
		block()
	}

	actDoc := &docWriter{w: w}
	actDoc.action("enter", "open the loop at the section its disk is asking for")
	room := h - len(actDoc.lines)
	var out []string
	for _, b := range blocks {
		if len(out)+len(b) > room {
			break
		}
		out = append(out, b...)
	}
	return fitRows(append(out, actDoc.lines...), h)
}

// skillQualityBlock is the narrow chooser's version of the output pane: the row under the cursor,
// said out loud under the table, where four skills already fill the window.
func (m *Model) skillQualityBlock() []string {
	r := m.skills.rows[m.skills.cursor]
	rule := func(n int) string { return dimStyle.Render(strings.Repeat(glyphs.Rule, max(4, n))) }
	lines := []string{
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, 2)) + " " + titleStyle.Render(r.name) + " " +
			rule(min(64, max(8, m.width-8-lipgloss.Width(r.name)))),
	}
	facts := strings.Join([]string{
		plural(r.qCases, "case"),
		plural(r.qAssert, "assertion"),
		fmt.Sprintf("%s with · %s without", r.qWith, r.qWithout),
		fmt.Sprintf("%s measured", plural(r.qIters, "iteration")),
	}, " · ")
	for _, line := range wrapPlain(facts, max(20, m.width-4)) {
		lines = append(lines, "  "+dimStyle.Render(line))
	}
	for _, line := range wrapPlain(r.qState, max(20, m.width-4)) {
		lines = append(lines, "  "+dimStyle.Render(line))
	}
	return lines
}
