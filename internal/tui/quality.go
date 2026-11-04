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

// The output lane has three jobs, and they are three sections on one rail — after an entry that is
// not one of them.
//
//   - the ENTRY is not a section because it is not part of the loop: it is what has to be true
//     before a loop exists. A set of cases, an assertion on every one of them, a run, and a grade of
//     that run. The lane opens on it whenever the disk says the loop is still closed, and the rail,
//     the dojo and the ranking are unreachable from it — a section offered before there is anything
//     in it is a screen that answers "nothing measured yet" three times.
//   - SETUP declares the case: the skill it belongs to, the prompt, the expected output, the fixtures
//     it stages and the assertions it will be judged on. This is the only place the declaration is
//     edited, because a case is written before anything can be measured against it.
//   - DOJO works one iteration: the assertions that failed, the judge's evidence for each, the arm's
//     execution transcript, and the human note. That is the diagram's loop — a person reads, the skill
//     is revised, the next iteration measures it — so it is one screen and not a document opened off
//     another.
//   - QUALITY judges the trend: every iteration's two rates against each other and against the last
//     one, and which complete iteration is the best.
//
// They used to be two screens and a hidden toggle: the workbench carried declaration, work and rank
// at once, and the ranking was reachable only through `tab`. A section that is not on the rail is a
// section nobody finds, so all three are on the rail the way the trigger lane's sections are, and the
// ranking stops being a mode of the workbench.
const (
	qSetup   = iota // declare: the case's record and its assertion list
	qDojo           // work: one iteration's failures, transcripts and the note
	qQuality        // judge: the iterations against each other
)

var qualitySections = []struct {
	sec  int
	name string
}{
	{qSetup, "setup"},
	{qDojo, "dojo"},
	{qQuality, "quality"},
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

	// gate is the initialization checklist, and the lane opens on it while the loop is closed: no
	// set, a case with nothing to judge, nothing run, or a run with no verdict. The sections below
	// it are the loop, and a loop with no graded run has nothing to show but three ways of saying
	// "nothing measured yet".
	//
	// gateOff records that the operator asked for the declaration anyway. The gate is where the
	// disk says you are, not a lock on the files: the first run's assertions are written before the
	// first run, and that is the declaration's job.
	gate    bool
	gateOff bool
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
		return "setup"
	}
	if q.gate {
		// The bar names the job, not a section: there is no section yet, and saying "setup" over a
		// checklist would put the declaration's name on the entry's work.
		return "init"
	}
	for _, s := range qualitySections {
		if s.sec == q.sec {
			return s.name
		}
	}
	return "setup"
}

// openQualitySkill opens one skill's output loop at the section the disk is asking for: the dojo when
// the newest iteration has failures to read, setup otherwise. A loop left mid-iteration reopens where
// it stopped, which is the whole point of reading the disk instead of remembering the session.
func (m *Model) openQualitySkill(name string) {
	if m.store.Skill(name) == nil {
		m.setStatus("unknown skill "+name, true)
		return
	}
	m.skill = name
	m.lane = store.LaneOutput
	m.quality = &qualityState{}
	m.reload()
	m.mode = screenQuality
	if m.quality.lab == nil {
		return
	}
	if m.quality.gate {
		// The loop does not exist yet, so the lane opens on the checklist that builds it rather
		// than on a section of a loop with nothing in it.
		if _, text := gateNext(m.quality.lab); text != "" {
			m.setStatus(name+" · initialization — "+text, false)
		}
		return
	}
	if n := len(m.quality.lab.Iterations); n > 0 {
		last := m.quality.lab.Iterations[n-1]
		switch {
		case len(last.Failed) > 0:
			m.setQualitySection(qDojo)
			m.quality.selectFirstFailed()
			m.setStatus(fmt.Sprintf("%s · iteration %d failed %s — read them, revise, R runs iteration %d",
				name, last.N, plural(len(last.Failed), "assertion"), last.N+1), false)
		case !last.Complete:
			m.setQualitySection(qQuality)
			m.setStatus(fmt.Sprintf("%s · iteration %d owes verdicts — g grades it", name, last.N), false)
		default:
			m.setQualitySection(qQuality)
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
	q.feedback, m.assertEdit = nil, nil
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
	// The gate is the disk's verdict on whether a loop exists yet, not the session's memory of a
	// screen: it closes the moment the first verdict lands. gateOff only remembers that the
	// operator stepped past it into the declaration.
	wasGate := q.gate
	q.gate = lab.InitPending && !q.gateOff
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
	if wasGate && !q.gate {
		// The entry's last errand was just done, so the loop is open: it opens on the failures of
		// the run that opened it, which is what there now is to read.
		q.sec = qDojo
		q.feedback, m.assertEdit = nil, nil
		q.selectFirstFailed()
	}
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
	if q.gate {
		return m.gateBody(h)
	}
	switch q.sec {
	case qDojo:
		return m.dojoBody(h)
	case qQuality:
		return m.qualityTableBody(h)
	}
	return m.setupBody(h)
}

// ---------------------------------------------------------------- the entry: is there a loop yet

// gateBody is the entry, as two tables and one action line: the four steps the loop needs before it
// exists — numbered, in the order they have to be done, with the state each is in and the key that
// moves it — and the set those steps are about.
//
// It used to be a checklist of sentences. A screen whose lines are prose has no columns to scan and
// no bands to orient by, and this one is read by someone who has never seen the lane: a header row,
// a rule and a fixed label column are what make "which step, and what is it waiting for" one glance.
//
// It draws no rail. The sections are the loop, and offering three of them before the loop exists is
// three screens answering "nothing measured yet".
func (m *Model) gateBody(h int) []string {
	lab := m.quality.lab
	done := 0
	for _, c := range lab.Checks {
		if c.OK {
			done++
		}
	}
	// The action is reserved before either table is drawn: the one line that says what to press must
	// not be the line a short window cuts, and the set is the band that can afford to lose its tail.
	key, text := gateNext(lab)
	if key == "" {
		key, text = "enter", "the loop is open — the declaration is behind this screen"
	}
	act := &docWriter{w: m.width}
	act.action(key, text)

	out := m.gateSteps(done)
	if room := h - len(out) - len(act.lines); room > 0 {
		out = append(out, m.gateSet(room)...)
	}
	out = append(out, act.lines...)
	return fitRows(out, h)
}

// The two tables share one grid: a two-cell cursor column, a fixed label column, then the detail.
const (
	gateStepW = 4  // the step number
	gateWhatW = 12 // the step's name
	gateKeyW  = 3  // the key that does it, at the right edge
	gateCaseW = 22 // the longest slug the case table holds before it is cut
)

func gateRule(w int) string {
	return "  " + dimStyle.Render(strings.Repeat(glyphs.Rule, min(64, max(10, w-4))))
}

// gateSteps is the four steps as a table. The step the entry is asking for carries the here-mark and
// the bright register; the ones after it are quiet, and the ones before it are done — so the row the
// screen is about is the only row that reads as an instruction.
func (m *Model) gateSteps(done int) []string {
	lab := m.quality.lab
	w := m.width
	current := -1
	for i, c := range lab.Checks {
		if !c.OK {
			current = i
			break
		}
	}
	// The detail column is measured, not guessed: the four keys line up in one column just past the
	// longest detail, which keeps them near the sentence they act on instead of floating at the far
	// edge of a wide window.
	nowW := lipgloss.Width("NOW")
	for _, c := range lab.Checks {
		nowW = max(nowW, lipgloss.Width(c.Detail))
	}
	nowW = min(nowW, max(24, w-gateStepW-gateWhatW-gateKeyW-14))
	head := "    " + dimStyle.Render(padCell("STEP", gateStepW, true)) + "  " +
		dimStyle.Render(padCell("WHAT", gateWhatW, false)) + "  " +
		dimStyle.Render(padCell("NOW", nowW, false)) + "  " + dimStyle.Render("KEY")
	out := []string{"", regionHead(w, "INITIALIZATION", fmt.Sprintf("%d of %d", done, len(lab.Checks)), true),
		gateRule(w), head}
	for i, c := range lab.Checks {
		mark, name, detail, fix := "  ", dimStyle.Render(padCell(c.Key, gateWhatW, false)),
			dimStyle.Render(padCell(ellipsis(c.Detail, nowW), nowW, false)), dimStyle.Render(c.Fix)
		if c.OK {
			// A done step keeps its tick: "2 of 4" in the corner is a number, two ticks are a record.
			mark = styleOn.Render(glyphs.Done) + " "
		}
		if i == current {
			mark = stepHere.Render(glyphs.Here) + " "
			name = titleStyle.Render(padCell(c.Key, gateWhatW, false))
			detail = colText.Render(padCell(ellipsis(c.Detail, nowW), nowW, false))
			fix = titleStyle.Render(c.Fix)
		}
		if c.OK {
			fix = ""
		} else {
			fix = padCell(fix, gateKeyW, true)
		}
		out = append(out, mark+dimStyle.Render(padCell(fmt.Sprintf("%d", i+1), gateStepW, true))+
			"  "+name+"  "+detail+"  "+fix)
	}
	return out
}

// gateSet is the declaration the entry is about: the cases as a table, then the selected case's
// assertions as their own numbered band. "5 assertions" is a number nobody can check from this
// screen, and the entry is the last place to change the set before it costs a run.
//
// The list is walked with j and k, so the assertions under it are the ones of the case the cursor is
// on — a case at a time, because a set of six cases with three assertions each is not a block that
// fits, and a list cut mid-assertion reads as a declaration that ends there.
func (m *Model) gateSet(room int) []string {
	q := m.quality
	if len(q.cases) == 0 || room < 6 {
		return nil
	}
	w := m.width
	const assertW = 10
	head := "    " + dimStyle.Render(padCell("CASE", gateCaseW, false)) + "  " +
		dimStyle.Render(padCell("ASSERTIONS", assertW, true)) + "  " + dimStyle.Render("STATE")
	out := []string{"", regionHead(w, "THE SET", plural(len(q.cases), "case"), false), gateRule(w), head}
	// The case rows are spent first and the assertion band gets what is left: the list is the set, and
	// a band that would push the last case off the screen is the wrong band to keep.
	// The band is four lines (a blank, its head, its rule and its column names), and it is only worth
	// reserving when at least one case row survives the reservation. Measuring that against the rows
	// already drawn — and not against a constant — is what keeps a window one line taller from turning
	// the case table into "… 2 more cases".
	limit := room - 4
	if limit < len(out)+1 {
		limit = room
	}
	shown := 0
	for i, c := range q.cases {
		if len(out) >= limit {
			break
		}
		mark := "  "
		if i == q.sel {
			mark = cursorMark.Render(glyphs.Cursor)
		}
		line := mark + colText.Render(padCell(c.Slug, gateCaseW, false)) + "  " +
			colText.Render(padCell(fmt.Sprintf("%d", len(c.Rows)), assertW, true)) + "  " +
			caseState(c)
		if i == q.sel {
			line = selected(line)
		}
		out = append(out, line)
		shown++
	}
	if shown < len(q.cases) {
		out = append(out, "    "+dimStyle.Render(fmt.Sprintf("… %d more cases", len(q.cases)-shown)))
	}
	if c := q.caseAt(); c != nil {
		out = append(out, assertionLines(*c, w, room-len(out))...)
	}
	return out
}

// caseState is the case table's last column in words: a newcomer should not have to learn a glyph to
// find out that a case is still a stub.
func caseState(c runner.QualityCase) string {
	switch {
	case strings.TrimSpace(c.Prompt) == "":
		return statusBad.Render("empty")
	case len(c.Rows) == 0:
		return dimStyle.Render("prompt only")
	}
	return dimStyle.Render("declared")
}

// assertionLines is the selected case's list, in its own band under the set: numbered, because
// "assertion 2 failed" is a sentence the dojo will use, and the numbers are what it counts by. One
// line stays reserved for the count of what did not fit — a list that just stops reads as complete.
func assertionLines(c runner.QualityCase, w, room int) []string {
	const indent = "   "
	if room < 2 {
		return nil
	}
	out := []string{"", regionHead(w, "ASSERTIONS · "+strings.ToUpper(c.Slug),
		plural(len(c.Rows), "row"), false), gateRule(w)}
	if len(out)+1 > room {
		return out[:room]
	}
	if len(c.Rows) == 0 {
		return append(out, indent+dimStyle.Render("none declared — e writes the first one"))
	}
	hidden := 0
	for i, a := range c.Rows {
		block := wrapPlain(a.Text, max(10, w-10))
		if len(out)+len(block) > room-1 {
			hidden = len(c.Rows) - i
			break
		}
		for j, line := range block {
			if j == 0 {
				out = append(out, indent+dimStyle.Render(padCell(fmt.Sprintf("%d", i+1), 2, true))+
					"   "+colText.Render(line))
				continue
			}
			out = append(out, indent+"     "+colText.Render(line))
		}
	}
	if hidden > 0 {
		out = append(out, indent+dimStyle.Render(fmt.Sprintf("… %s more — enter for the record",
			plural(hidden, "assertion"))))
	}
	return out
}

// gateNext is the one errand the entry is asking for: the first step that is not done, with the key
// that does it and the sentence that says what doing it means.
func gateNext(lab *runner.QualityLab) (key, text string) {
	for _, c := range lab.Checks {
		if c.OK {
			continue
		}
		switch c.Key {
		case "cases":
			return c.Fix, "write the cases — evals.json, two or three to start, S opens it"
		case "assertions":
			return c.Fix, "an assertion for every case: a grade is made of them, e writes them"
		case "run":
			return c.Fix, fmt.Sprintf("run iteration 1 — %s, both arms", plural(lab.Cases, "case"))
		case "grade":
			if c.Fix == "R" {
				return c.Fix, "run iteration 1 — it grades itself, and the loop opens on the verdicts"
			}
			return c.Fix, "grade the run — its arms carry no verdicts, so the loop has nothing to open on"
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
	qListMax = 34
)

// caseListW is the width the case column asks for: the longest slug it has to hold, plus the two-cell
// cursor mark and the one-cell declaration mark in front of it.
func (m *Model) caseListW() int {
	w := lipgloss.Width("CASES")
	for _, c := range m.quality.cases {
		w = max(w, lipgloss.Width(c.Slug))
	}
	return min(qListMax, max(qListMin, w+4))
}

// declaredMark is a case's declaration at a glance: assertions and a prompt, a prompt alone, or a
// stub. It is a state mark of the declaration, so it uses the pass/tick family and not the accent.
func declaredMark(c runner.QualityCase) string {
	switch {
	case len(c.Rows) > 0 && strings.TrimSpace(c.Prompt) != "":
		return styleOn.Render(glyphs.Done)
	case strings.TrimSpace(c.Prompt) != "":
		return dimStyle.Render(glyphs.Pending)
	}
	return statusBad.Render(glyphs.Failed)
}

func (m *Model) setupBody(h int) []string {
	listW := m.caseListW()
	if paneW := min(100, m.width-listW-3); paneW >= 40 {
		return m.splitBody(h, listW, paneW, m.caseList(h), m.casePane(paneW, h))
	}
	return m.setupStacked(h)
}

func (m *Model) caseList(h int) []string {
	q := m.quality
	w := m.caseListW()
	lines := []string{"", regionHead(w, "CASES", "", true),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, w-4)))}
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
		line := mark + declaredMark(c) + " " + colText.Render(ellipsis(c.Slug, w-4))
		if i == q.sel {
			line = selected(line)
		}
		lines = append(lines, line)
	}
	return lines
}

func (m *Model) setupStacked(h int) []string {
	lines := m.caseList(h)
	if c := m.quality.caseAt(); c != nil {
		lines = append(lines, "")
		lines = append(lines, m.caseBlock(*c)...)
	}
	return fitRows(lines, h)
}

// caseBlock is the narrow screen's version of the pane: the same record — prompt, expected output,
// files, assertions — as labelled lines, so the case reads the same whichever shape the window gives
// the setup section.
func (m *Model) caseBlock(c runner.QualityCase) []string {
	d := &docWriter{w: m.width, label: 15}
	d.lines = append(d.lines, "  "+titleStyle.Render(c.Slug)+"  "+
		dimStyle.Render(fmt.Sprintf("#%d", c.ID)), "")
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
	// The marks and the statement, in the label column the fields above use; a long assertion wraps
	// under itself rather than being cut by the frame's last-line trim.
	const marksAt = 2 + 15 + 2 + 4
	for i, a := range c.Rows {
		label := ""
		if i == 0 {
			label = "assertions"
		}
		head := fmt.Sprintf("  %-15s  %s %s  ", label, verdictCell(a.With), verdictCell(a.Without))
		for j, line := range wrapPlain(a.Text, max(10, m.width-marksAt)) {
			if j == 0 {
				d.lines = append(d.lines, head+colText.Render(line))
				continue
			}
			d.lines = append(d.lines, strings.Repeat(" ", marksAt)+colText.Render(line))
		}
	}
	return d.lines
}

func rateSummary(c runner.QualityCase) string {
	with, without := "—", "—"
	if c.With.Graded() {
		with = fmt.Sprintf("%d/%d", c.With.Passed, c.With.Judged)
	}
	if c.Without.Graded() {
		without = fmt.Sprintf("%d/%d", c.Without.Passed, c.Without.Judged)
	}
	return fmt.Sprintf("with %s · without %s", with, without)
}

// casePane is the case as its own record writes it: `prompt`, `expected_output`, `files`,
// `assertions` — the case's keys, in the case's order. It is the setup section's pane, and the
// assertion list is edited where it is drawn: `e` turns the list into the field, in place, so the
// record above the list stays on screen while the sentence it is about is typed.
//
// It is a reflection and not a summary: a pane that reorders the record, or drops the files the case
// stages, is a reading of the case rather than the case.
func (m *Model) casePane(w, h int) []string {
	q := m.quality
	c := q.caseAt()
	if c == nil {
		d := &docWriter{w: w}
		d.head("NO CASE", false)
		d.rule()
		d.para("The set is empty. S opens evals.json: two or three cases are enough to start, and e writes the assertions a grade is made of.")
		return fitRows(d.lines, h)
	}
	// The action line is reserved before anything is measured against the window: the one line that
	// says what to press must not be the one a short window cuts.
	act := &docWriter{w: w}
	if it := q.iteration(); it != nil && !it.Complete {
		act.action("e", "the assertions · g grades this iteration · 2 the dojo")
	} else {
		act.action("e", "the assertions · 2 the dojo · R runs the next iteration")
	}
	room := max(1, h-len(act.lines))

	// The list being written is paid for before the record is drawn: a field the bottom of the window
	// cuts off is a field the editor cannot reach, while the record has whole blocks to lose.
	listEdit := m.assertEditActive() && m.assertEdit.slug == c.Slug
	editH := 0
	if listEdit {
		editH = min(max(4, room/2), 2+m.assertEdit.want)
	}
	recordRoom := max(1, room-editH)

	d := &docWriter{w: w, label: 12}
	d.blank()
	d.lines = append(d.lines, regionHead(w, ellipsis(c.Slug, max(4, d.body()-2)),
		fmt.Sprintf("#%d", c.ID), false))
	d.rule()
	d.lead(rateSummary(*c))
	out := d.lines
	if len(out) >= recordRoom {
		if listEdit {
			out = append(out, m.assertEdit.pane(w, editH)...)
		}
		return fitRows(append(out, act.lines...), h)
	}

	// The record, in the file's order. A block goes whole or not at all, and the record is truncated
	// at its bottom rather than with a hole in it: a pane that shows `prompt` and `assertions` while
	// the `files` between them are missing has reordered the case, which is the one thing this pane
	// exists not to do.
	block := func(build func(*docWriter)) bool {
		if len(out) >= recordRoom {
			return false
		}
		r := &docWriter{w: w, label: 12}
		build(r)
		if len(r.lines) == 0 {
			return true // an empty field is nothing to draw, not a hole in the record
		}
		if len(out)+len(r.lines) > recordRoom {
			return false
		}
		out = append(out, r.lines...)
		return true
	}
	whole := block(func(r *docWriter) {
		r.card("prompt")
		if strings.TrimSpace(c.Prompt) == "" {
			r.para("none — a case with no prompt cannot be run")
			return
		}
		r.source(c.Prompt)
	})
	whole = whole && block(func(r *docWriter) {
		r.card("expected_output")
		if strings.TrimSpace(c.ExpectedOutput) == "" {
			r.para("none — grading has nothing to check against")
			return
		}
		r.source(c.ExpectedOutput)
	})
	whole = whole && block(func(r *docWriter) {
		r.card("files")
		if len(c.Files) == 0 {
			r.para("none — the run root starts empty")
			return
		}
		for _, f := range c.Files {
			r.lines = append(r.lines, "  "+colText.Render(f))
		}
	})
	// The list needs room for its title and one row: a card with no rows under it reads as an empty
	// list, and an empty list is a fact about the case, not about the window. While the list is being
	// written, the field takes the list's place — same block, same position, the caret in it.
	switch {
	case listEdit:
		out = append(out, m.assertEdit.pane(w, editH)...)
	case whole && recordRoom-len(out) >= 3:
		out = append(out, assertionList(&docWriter{w: w, label: 12}, *c, recordRoom-len(out))...)
	}
	return fitRows(append(out, act.lines...), h)
}

// assertionList is the case's grading list: every declared assertion with the two arms' verdicts,
// and the judge's words under the ones that failed. The facts an assertion is judged against ride in
// its own sentence — the format's list is strings, and a second array of facts would have to be kept
// in step by position, which is the one join this tool already distrusts.
//
// room is how many lines the pane can give it. Rows are dropped whole and counted: a row cut in half
// reads as a verdict with no assertion, which is the one lie this pane cannot tell.
func assertionList(d *docWriter, c runner.QualityCase, room int) []string {
	title := fmt.Sprintf("assertions — %d", c.Assertions)
	if c.Assertions > 0 && (c.With.Graded() || c.Without.Graded()) {
		title += " · ✓ with · ✗ without"
	}
	out := []string{"", "  " + titleStyle.Render(title)}
	if len(out) > room {
		return out[:max(0, room)]
	}
	if len(c.Rows) == 0 {
		for _, line := range wrapPlain("none declared yet — e writes the first one, one per line", d.body()) {
			if len(out) >= room {
				break
			}
			out = append(out, "  "+dimStyle.Render(line))
		}
		return out
	}
	var lens []int
	note := func(hidden int) string {
		return "  " + dimStyle.Render(fmt.Sprintf("… %s not shown · e opens the list",
			plural(hidden, "assertion")))
	}
	for i, a := range c.Rows {
		row := assertionRow(d, a)
		if len(out)+len(row) > room {
			hidden := len(c.Rows) - i
			if len(out)+1 > room && len(lens) > 0 {
				// One row is worth less than the count of what is missing: drop the row that was
				// drawn and spend its lines on the note, so a list can never just stop.
				out = out[:len(out)-lens[len(lens)-1]]
				lens = lens[:len(lens)-1]
				hidden++
			}
			if len(out) < room {
				out = append(out, note(hidden))
			}
			return out
		}
		out = append(out, row...)
		lens = append(lens, len(row))
	}
	return out
}

// assertionRow is one assertion as lines: the two marks, the statement, and the judge's words for
// each arm that failed it.
func assertionRow(d *docWriter, a runner.QualityAssertion) []string {
	const marks = 7 // two mark cells and the gap, indents included
	var out []string
	head := "  " + verdictCell(a.With) + " " + verdictCell(a.Without) + "  "
	for i, line := range wrapPlain(a.Text, max(10, d.body()-marks)) {
		if i == 0 {
			out = append(out, head+colText.Render(line))
			continue
		}
		out = append(out, strings.Repeat(" ", marks)+colText.Render(line))
	}
	for _, arm := range []string{runner.ArmWith, runner.ArmWithout} {
		evidence := a.Evidence[arm]
		if evidence == "" {
			continue
		}
		for _, line := range wrapPlain(armWord(arm)+" · "+evidence, max(10, d.body()-marks)) {
			out = append(out, strings.Repeat(" ", marks)+dimStyle.Render(line))
		}
	}
	return out
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
	// the editor cannot reach, while the columns have scroll and trim to lose instead.
	stripH := 0
	if q.feedback != nil {
		stripH = min(max(8, h/2), 4+q.feedback.want)
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
	if q.feedback != nil {
		body = append(body, q.feedback.body(m.width, stripH)...)
	}
	return fitRows(body, h)
}

// dojoList is the iteration's CASES, one row each, with how much failed in each. It used to list the
// failed assertions instead, which had two costs: a case with three failures appeared three times,
// and a case that passed every assertion did not appear at all — so the human note, which is written
// against a case and not against a verdict, had nowhere to be written. The assertion that passed can
// still be the assertion that is wrong.
func (m *Model) dojoList(it *runner.QualityIteration, h int) []string {
	q := m.quality
	w := m.dojoListW()
	right := "passes"
	if n := len(q.fails); n > 0 {
		right = fmt.Sprintf("%d failed", n)
	}
	const countW = 9
	slugW := max(8, w-2-1-1-2-countW)
	lines := []string{"", regionHead(w, "CASES", right, true),
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
		glyph, count := styleOn.Render(glyphs.Done), styleOn.Render(padCell("passes", countW, true))
		if n := len(c.Failed); n > 0 {
			glyph = statusBad.Render(glyphs.Failed)
			count = statusBad.Render(padCell(fmt.Sprintf("%d failed", n), countW, true))
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

// dojoPane is one case worked: what failed in it, the judge's evidence for each, its transcript, and
// the note. The note is reserved before the failures are laid out — this pane exists so a person can
// say "the assertions passed and the work is still wrong", and a window that cut the note to show one
// more failure would be the screen refusing the one thing it is for.
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
	act := &docWriter{w: w}
	next := "R runs the next iteration"
	if it := q.iteration(); it != nil && !it.Complete {
		next = "g grades this iteration"
	}
	act.action("f", "the feedback · p a revision · v transcript · E SKILL.md · "+next)
	room := max(1, h-len(act.lines))

	d := &docWriter{w: w, label: 11}
	d.blank()
	right := fmt.Sprintf("#%d · %s", c.ID, plural(len(c.Rows), "assertion"))
	if n := len(c.Failed); n > 0 {
		right = fmt.Sprintf("#%d · %d failed", c.ID, n)
	}
	d.lines = append(d.lines, regionHead(w, ellipsis(c.Slug, max(4, d.body()-lipgloss.Width(right)-2)),
		right, false))
	d.rule()
	out := d.lines
	if len(out) >= room {
		return fitRows(append(out, act.lines...), h)
	}

	// The feedback block is built before the rest and given its room first: this pane exists so a
	// person can say what is wrong with an output the assertions could not see, and every other block
	// on it is the evidence that sentence is written from.
	fbBlock := &docWriter{w: w, label: 11}
	fbBlock.card("feedback · this case's review")
	if strings.TrimSpace(c.Feedback) != "" {
		fbBlock.source(c.Feedback)
	} else {
		fbBlock.para("none — an output you were happy with leaves this empty. That is a case that passed your review, not one you forgot.")
		fbBlock.para(`f writes it: name what is wrong with the output, not that it looks bad — "the total is right but the faces are out of order" is actionable, "bad" is not.`)
	}

	// What the feedback is about: the arm's own reply. The guide's first instruction is to review the
	// actual outputs beside the grades, and a pane that shows verdicts but not the output is asking
	// for a review of something nobody can see.
	out = append(out, m.outputBlock(w, *c, room-len(out)-len(fbBlock.lines))...)

	budget := room - len(out) - len(fbBlock.lines)
	failureBlock := func(f runner.QualityFailure) []string {
		r := &docWriter{w: w, label: 11}
		r.card("assertion · " + armWord(f.Arm))
		r.source(f.Text)
		r.card("evidence")
		if strings.TrimSpace(f.Evidence) == "" {
			r.para("the judge gave no evidence — a PASS without one is a FAIL, and this is a FAIL with nothing written down")
			return r.lines
		}
		r.source(f.Evidence)
		return r.lines
	}
	if len(c.Failed) == 0 {
		block := &docWriter{w: w, label: 11}
		block.card("every assertion passed")
		for _, a := range c.Rows {
			block.lines = append(block.lines, fmt.Sprintf("  %s %s  %s", verdictCell(a.With), verdictCell(a.Without),
				colText.Render(ellipsis(a.Text, max(10, w-12)))))
		}
		if len(block.lines) <= budget {
			out = append(out, block.lines...)
			budget -= len(block.lines)
		}
	}
	for i, f := range c.Failed {
		block := failureBlock(f)
		if len(block) > budget {
			out = append(out, "  "+dimStyle.Render(fmt.Sprintf("… %d more failed — the report has the rest",
				len(c.Failed)-i)))
			break
		}
		out = append(out, block...)
		budget -= len(block)
	}

	out = append(out, fbBlock.lines...)

	// The transcript of the arm this case is read through, with what is left of the window.
	if tail := q.logTail; len(tail) > 0 && len(out)+3 <= room {
		tr := &docWriter{w: w, label: 11}
		tr.card("transcript · " + armWord(dojoArm(*c)))
		for _, line := range tail {
			if len(out)+len(tr.lines)+1 >= room {
				break
			}
			tr.lines = append(tr.lines, "  "+dimStyle.Render(ellipsis(line, max(10, tr.body()))))
		}
		if len(tr.lines) > 2 {
			out = append(out, tr.lines...)
		}
	}
	return fitRows(append(out, act.lines...), h)
}

// outputBlock is the arm's reply, capped: the artifact a feedback sentence is about. Five lines is
// enough to see what was produced; what is longer is in the transcript (`v`) and the report.
func (m *Model) outputBlock(w int, c runner.QualityCase, room int) []string {
	if room < 3 {
		return nil
	}
	resp := ""
	if dojoArm(c) == runner.ArmWithout {
		resp = c.Without.Response
	} else {
		resp = c.With.Response
	}
	if strings.TrimSpace(resp) == "" {
		return nil
	}
	b := &docWriter{w: w, label: 11}
	b.card("output · " + armWord(dojoArm(c)))
	lines := wrapPlain(strings.Join(strings.Fields(resp), " "), max(10, b.body()*1))
	cap := min(len(lines), min(5, room-len(b.lines)))
	for _, line := range lines[:cap] {
		b.lines = append(b.lines, "  "+colText.Render(line))
	}
	if cap < len(lines) {
		b.lines = append(b.lines, "  "+dimStyle.Render(fmt.Sprintf("… %d more lines — v reads the whole log", len(lines)-cap)))
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
			state = "  " + dimStyle.Render("no verdicts")
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
	if q.gate {
		return m.updateGate(msg)
	}
	// The rail is the lane's first key of every section, so it is read before the section's own.
	switch msg.String() {
	case "1", "2", "3":
		m.setQualitySection(int(msg.String()[0] - '1'))
		return nil
	case "tab":
		m.setQualitySection((q.sec + 1) % len(qualitySections))
		return nil
	case "shift+tab":
		m.setQualitySection((q.sec + len(qualitySections) - 1) % len(qualitySections))
		return nil
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

// updateGate is the entry's keys: the one errand it is asking for, the declaration behind it, and
// the set itself — which is walkable, because the assertions are what the run will be judged on and
// this is the last screen before it costs one. `2` and `3` are answered with what the loop is still
// waiting on instead of a section, because there is no section yet to go to.
func (m *Model) updateGate(msg tea.KeyMsg) tea.Cmd {
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
	case "R":
		return m.startQualityRun()
	case "g":
		return m.startGrading()
	case "S":
		return m.editCaseFile()
	case "e":
		if q.caseAt() == nil {
			m.setStatus("no case to judge yet — S opens evals.json", true)
			return nil
		}
		q.gate, q.gateOff = false, true
		return m.openAssertEditor()
	case "enter", "1", "tab":
		q.gate, q.gateOff = false, true
		m.setStatus("the declaration — e writes a case's assertions, S opens evals.json", false)
	case "2", "3":
		if _, text := gateNext(q.lab); text != "" {
			m.setStatus("the loop is not open yet — "+text, true)
		}
	}
	return nil
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
	case "S":
		return m.editCaseFile()
	case "enter":
		// The dojo reads the same cursor the declaration does: enter on a case means "work this one".
		m.setQualitySection(qDojo)
	}
	return nil
}

func (m *Model) updateDojo(msg tea.KeyMsg) tea.Cmd {
	q := m.quality
	switch msg.String() {
	case "j", "down":
		q.sel = min(q.sel+1, max(0, len(q.cases)-1))
		q.syncLog()
	case "k", "up":
		q.sel = max(0, q.sel-1)
		q.syncLog()
	case "home":
		q.sel = 0
		q.syncLog()
	case "G", "end":
		q.sel = max(0, len(q.cases)-1)
		q.syncLog()
	case "[", "left":
		m.stepIteration(-1)
	case "]", "right":
		m.stepIteration(1)
	case "f":
		return m.openFeedbackEditor()
	case "p":
		// The guide's step 1, asked of the agent that is allowed to write: the failures, the feedback
		// and the skill, in one call, landing as a proposal on its own screen.
		return m.startRevision()
	case "v", "enter":
		m.openTranscript()
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
	m.setStatus(fmt.Sprintf("iteration %d of %d on the dojo", m.quality.iter, len(l.Iterations)), false)
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
			m.setStatus(fmt.Sprintf("iteration %d is on the dojo", q.iter), false)
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
	// Any verdict at all means a judge pass has been here, and a second one replaces what it bought:
	// the reason is that the list those verdicts were read against has changed, and that is worth a
	// question. An iteration with no verdicts — the whole run made with --no-grade, an older build, or
	// the grade cancelled — has nothing to replace, so `g` finishes it without asking.
	if it != nil && (it.With.Graded() || it.Without.Graded()) {
		return m.askRegrade(it)
	}
	return m.startGradeRun(false)
}

// startGradeRun is one judge pass over the iteration on screen. force re-grades arms that already
// carry verdicts; without it the judge skips them and says so, which is what `g` on an ungraded
// iteration does.
func (m *Model) startGradeRun(force bool) tea.Cmd {
	q := m.quality
	if q == nil {
		return nil
	}
	scope := fmt.Sprintf("grading run · iteration %d", q.iter)
	if force {
		scope = fmt.Sprintf("re-grading · iteration %d", q.iter)
	}
	m.run = &runState{kind: runGrade, scope: scope,
		hint: "the judge reads each arm's outputs against the declared assertions"}
	m.run.live = newLiveRun(m.run.scope, false)
	m.seedOutputRun(q.cases, q.iter)
	m.mode = screenRunLive
	return m.startJudgeRun(force)
}

// askRegrade is the one question `g` asks on a complete iteration. Re-grading spends one judge call
// per arm over verdicts that exist because the list moved under them — worth asking, and worth
// saying what it costs.
func (m *Model) askRegrade(it *runner.QualityIteration) tea.Cmd {
	arms := 2 * it.Cases
	m.mode = screenAsk
	m.ask = &askState{
		scope: fmt.Sprintf("re-grade iteration %d", it.N),
		title: fmt.Sprintf("GRADE ITERATION %d AGAIN?", it.N),
		lines: []string{
			"      Every arm in this iteration already carries verdicts.",
			fmt.Sprintf("      That is %d judge call(s), and the rates it produces replace the ones on screen.", arms),
			"",
			"      Do it when an assertion changed after the grade: the verdicts describe the old list,",
			"      and the dojo draws a verdict nobody gave as a dash.",
		},
		yes:  "re-grade it",
		back: screenQuality,
		then: func(m *Model) tea.Cmd { return m.startGradeRun(true) },
	}
	return nil
}

func (m *Model) startJudgeRun(force bool) tea.Cmd {
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
		if _, err := r.GradeIteration(ctx, skill, iteration, runner.GradeOptions{Force: force}); err != nil {
			ch <- runner.Event{Kind: "error", Lane: store.LaneOutput, Err: err, Message: err.Error()}
		}
	}()
	return tea.Batch(waitForEvent(ch), tickEvery())
}

// startQualityRun runs the next iteration: every case, both arms, the guide's rerun.
func (m *Model) startQualityRun() tea.Cmd {
	q := m.quality
	if q == nil || q.lab == nil {
		return nil
	}
	if q.lab.Cases == 0 {
		m.setStatus("no cases — write evals.json first", true)
		return nil
	}
	// A case with nothing to judge cannot enter a set: its grading carries no verdict, so the run
	// would spend both arms on an output nobody can score. The entry's first two steps are the
	// declaration, and this is the second of them.
	if q.lab.Unasserted > 0 {
		m.setStatus(fmt.Sprintf("%d of %s carry no assertion — e writes them; a run nothing can judge is not an iteration",
			q.lab.Unasserted, plural(q.lab.Cases, "case")), true)
		return nil
	}
	next := 1
	if len(q.lab.Iterations) > 0 {
		next = q.lab.Iterations[len(q.lab.Iterations)-1].N + 1
	}
	m.run = &runState{kind: runQuality, scope: fmt.Sprintf("output run · iteration %d", next),
		hint: "both arms, every case, then the judge — one spend, two passes"}
	m.run.live = newLiveRun(m.run.scope, false)
	m.seedOutputRun(q.cases, next)
	m.mode = screenRunLive
	return m.startQualityRunCmd()
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

func (m *Model) startQualityRunCmd() tea.Cmd {
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
	// key that used to judge it was a step with nothing to wait for in between.
	opts := runner.Options{Lane: store.LaneOutput, Skills: []string{m.skill},
		Arms: runner.AllArms, Isolate: true, Log: runner.ActivityFull, Grade: true}
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

// qualityHints are the lane's keys, worded as the thing each one does in the section you are in.
func qualityHints(m *Model) []hint {
	q := m.quality
	if q == nil {
		return []hint{{"esc", "skills"}}
	}
	if m.assertEditActive() {
		return m.assertEdit.hints()
	}
	if q.feedback != nil {
		return q.feedback.hints()
	}
	if q.gate {
		// The entry's keys: the set it is about, the errand it is asking for, the file it is asked
		// of, and the way back. A key is offered only where it can do its job — R over a set with
		// nothing to judge is the spend this screen exists to prevent.
		hints := []hint{}
		if len(q.cases) > 0 {
			hints = append(hints, hint{"j k", "the set"}, hint{"e", "assertions"})
		}
		hints = append(hints, hint{"enter", "the declaration"}, hint{"S", "evals.json"})
		if q.lab.Cases > 0 && q.lab.Unasserted == 0 {
			next := "run iteration 1"
			if n := len(q.lab.Iterations); n > 0 {
				next = fmt.Sprintf("run iteration %d", q.lab.Iterations[n-1].N+1)
			}
			hints = append(hints, hint{"R", next})
		}
		if len(q.lab.Iterations) > 0 {
			hints = append(hints, hint{"g", "grade · re-grade"})
		}
		return append(hints, hint{"esc", "skills"})
	}
	rail := []hint{{"1 2 3", "setup dojo quality"}}
	switch q.sec {
	case qDojo:
		hints := []hint{{"j k", "cases"}, {"[ ]", "iteration"}, {"v", "transcript"},
			{"f", "feedback"}, {"p", "a revision"}, {"E", "SKILL.md"}}
		if it := q.iteration(); it != nil && !it.Complete {
			hints = append(hints, hint{"g", "grade"})
		}
		return append(append(hints, hint{"R", "run next"}), rail...)
	case qQuality:
		hints := []hint{{"j k", "iterations"}, {"enter", "the dojo on it"}, {"r", "report"},
			{"g", "grade"}}
		return append(append(hints, hint{"R", "run next"}), rail...)
	}
	hints := []hint{{"j k", "cases"}, {"e", "assertions"}, {"S", "evals.json"},
		{"enter", "the dojo"}}
	if it := q.iteration(); it != nil && !it.Complete {
		hints = append(hints, hint{"g", "grade"})
	}
	return append(append(hints, hint{"R", "run next"}), rail...)
}

// qualityRail is the lane's three sections on the head row, in the same register as the trigger
// lane's step rail: a filled dot for the section you are in, the others quiet.
func (m *Model) qualityRail(w int) string {
	q := m.quality
	if q == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(" ")
	for i, s := range qualitySections {
		if i > 0 {
			b.WriteString(dimStyle.Render(" " + strings.Repeat(glyphs.Rule, 3) + " "))
		}
		label := fmt.Sprintf("%d %s", i+1, s.name)
		if q.sec == s.sec {
			b.WriteString(stepHere.Render(glyphs.Here + " " + label))
			continue
		}
		b.WriteString(dimStyle.Render(label))
	}
	facts := m.qualityFacts()
	if facts != "" && lipgloss.Width(b.String())+lipgloss.Width(facts)+4 <= w {
		return endsLine(b.String(), statValue.Render(facts)+" ", w)
	}
	return ellipsis(b.String(), w)
}

// qualityFacts is the rail row's right half: the loop's numbers, and which iteration the sections are
// reading. The sections count the set once above the columns; this row is where the whole loop is one
// glance.
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
