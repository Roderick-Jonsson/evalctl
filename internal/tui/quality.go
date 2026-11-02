package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// The output lane's two screens.
//
// The loop here is the guide's own: run every case with the skill and without it, grade the
// assertions, read what failed and what the human said, revise SKILL.md, and run the next
// iteration. That is one workbench and one ranking, not the trigger lane's sections — the output
// loop has no train/validation split, because every case is both what a revision may learn from and
// what judges it. What replaces the split is the comparison: with the skill against without it, and
// this iteration against the best one before it.
//
// The workbench shows the cases of one iteration with both arms' rates and the failed assertions
// beside them — the evidence a revision is written from. The ranking puts the iterations in rate
// order and names the best complete one; a partial grade never ranks.

// qualityState is the output lane's reading of the disk: the lab (one row per iteration), the cases
// of the iteration on screen, and where the two cursors stand.
type qualityState struct {
	lab   *runner.QualityLab
	cases []runner.QualityCase
	iter  int // the iteration the table reads; 0 = none on disk
	sel   int
	top   int
	// rank is the ranking screen, opened from the workbench with tab: the same skill's iterations,
	// judged against each other.
	rank  bool
	rsel  int
	rtop  int
	first bool
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

// openQualitySkill opens one skill's output loop at its newest iteration, or at the first one when
// nothing has run yet. The reading is the disk's: a loop left at iteration three reopens there.
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
	if n := len(m.quality.lab.Iterations); n > 0 {
		m.setStatus(fmt.Sprintf("%s · %d iteration(s) on disk — g grades the newest, R runs the next",
			name, n), false)
	} else {
		m.setStatus(fmt.Sprintf("%s · %s, nothing run yet — R runs iteration 1", name,
			plural(m.quality.lab.Cases, "case")), false)
	}
}

// reloadQuality re-reads the lab and the iteration's cases. It runs after every write and every
// finished run, so the table never shows a number from before the change.
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
	m.quality.lab = lab
	if len(lab.Iterations) == 0 {
		m.quality.cases, m.quality.iter = nil, 0
		return
	}
	// Keep the iteration the operator is looking at while it still exists; otherwise open the
	// newest, which is the one the loop is waiting on.
	found := false
	for _, it := range lab.Iterations {
		if it.N == m.quality.iter {
			found = true
		}
	}
	if !found {
		m.quality.iter = lab.Iterations[len(lab.Iterations)-1].N
	}
	cases, err := runner.New(m.store).QualityCases(m.skill, m.quality.iter)
	if err != nil {
		m.setStatus(err.Error(), true)
		return
	}
	m.quality.cases = cases
	m.quality.sel = min(m.quality.sel, max(0, len(cases)-1))
	if m.quality.lab.Best != 0 && m.quality.rsel == 0 && m.quality.first {
		m.quality.rsel = bestRow(m.quality.lab)
	}
	m.quality.first = false
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

// ---------------------------------------------------------------- the workbench

// The case column is a list of names and nothing else: the rates, the failed count and the
// iteration's date all live on the pane's side of the screen now, where the case itself is. These
// bound the column: wide enough for a slug, never so wide that it eats the record beside it.
const (
	qListMin = 16
	qListMax = 34
)

// caseListW is the width the case column asks for: the longest slug it has to hold, plus its own
// two-cell mark.
func (m *Model) caseListW() int {
	w := lipgloss.Width("CASES")
	for _, c := range m.quality.cases {
		w = max(w, lipgloss.Width(c.Slug))
	}
	return 2 + min(qListMax, max(qListMin, w+1))
}

func (m *Model) qualityBody(h int) []string {
	q := m.quality
	if q == nil || q.lab == nil {
		return fitRows([]string{"", "  " + dimStyle.Render("reading the workspace…")}, h)
	}
	// The strip is paid for before the columns: a field the bottom of the window cuts off is a field
	// the editor cannot reach, while the columns have scroll and trim to lose instead.
	stripH := 0
	if m.assertEdit != nil {
		stripH = min(max(8, h/2), 4+m.assertEdit.want)
	}
	bodyH := max(8, h-stripH)
	listW := m.caseListW()
	var body []string
	if paneW := min(100, m.width-listW-3); paneW >= 40 {
		body = m.splitBody(bodyH, listW, paneW, m.qualityList(bodyH), m.qualityPane(paneW, bodyH))
	} else {
		body = m.qualityStacked(bodyH)
	}
	if m.assertEdit != nil {
		body = append(body, m.assertEdit.body(m.width, stripH)...)
	}
	return fitRows(body, h)
}

// qualityList is the cases of the iteration on screen: their slugs, the cursor, and the iteration
// they belong to in the region head's corner. Nothing else — the evidence for a row is the pane, and
// a second reading of it in this column was two numbers where the screen only needs one place to
// look.
func (m *Model) qualityList(h int) []string {
	q := m.quality
	it := q.iteration()
	right := ""
	if it != nil {
		right = fmt.Sprintf("iteration %d", it.N)
	}
	w := m.caseListW()
	lines := []string{"", regionHead(w, "CASES", right, true),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, w-4)))}
	if it == nil {
		return lines
	}
	room := max(1, h-len(lines))
	q.top = windowStart(q.sel, q.top, room, len(q.cases))
	for i := q.top; i < len(q.cases) && i < q.top+room; i++ {
		c := q.cases[i]
		mark := "  "
		if i == q.sel {
			mark = cursorMark.Render(glyphs.Cursor)
		}
		line := mark + colText.Render(ellipsis(c.Slug, w-2))
		if i == q.sel {
			line = selected(line)
		}
		lines = append(lines, line)
	}
	return lines
}

func (m *Model) qualityStacked(h int) []string {
	lines := m.qualityList(h)
	if c := m.quality.caseAt(); c != nil {
		lines = append(lines, "")
		lines = append(lines, m.qualityBlock(*c)...)
	}
	return fitRows(lines, h)
}

// qualityBlock is the narrow screen's version of the pane: the same record — prompt, expected
// output, files, assertions — as labelled lines, so the case reads the same whichever shape the
// window gives the workbench.
func (m *Model) qualityBlock(c runner.QualityCase) []string {
	d := &docWriter{w: m.width, label: 15}
	d.lines = append(d.lines, "  "+titleStyle.Render(c.Slug)+"  "+
		dimStyle.Render(fmt.Sprintf("#%d · %s", c.ID, rateSummary(c))), "")
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

// qualityPane is the case as its own record writes it: `prompt`, `expected_output`, `files`,
// `assertions` — the case's keys, in the case's order — and then the reviewer's note from
// feedback.json. The assertions carry both arms' verdicts and the judge's evidence under the failed
// ones, because that list is what a revision is written against, and `e` edits it where it is shown.
//
// It is a reflection and not a summary: a pane that reorders the record, or drops the files the case
// stages, is a reading of the case rather than the case. The one thing on it that is not in the file
// is the pair of rates, said above the record because it is how the arms scored against it.
func (m *Model) qualityPane(w, h int) []string {
	q := m.quality
	c := q.caseAt()
	if c == nil {
		d := &docWriter{w: w}
		d.head("NO ITERATION", false)
		d.rule()
		d.para("Nothing has run yet. R runs iteration 1; the guide starts with two or three cases and writes the assertions after seeing what the first run produces.")
		return fitRows(d.lines, h)
	}
	// The action line is reserved before anything is measured against the window: the one line that
	// says what to press must not be the one a short window cuts.
	act := &docWriter{w: w}
	if it := q.iteration(); it != nil && !it.Complete {
		act.action("g", "grade this iteration · e the assertions · tab the ranking")
	} else {
		act.action("R", "run the next iteration · e the assertions · tab the ranking")
	}
	room := max(1, h-len(act.lines))

	d := &docWriter{w: w, label: 12}
	d.blank()
	d.lines = append(d.lines, regionHead(w, ellipsis(c.Slug, max(4, d.body()-2)),
		fmt.Sprintf("#%d", c.ID), false))
	d.rule()
	d.lead(rateSummary(*c))
	out := d.lines
	if len(out) >= room {
		return fitRows(out, h)
	}

	// The record, in the file's order. A block goes whole or not at all, and the record is truncated
	// at its bottom rather than with a hole in it: a pane that shows `prompt` and `assertions` while
	// the `files` between them are missing has reordered the case, which is the one thing this pane
	// exists not to do. Only the assertion list is cut, and it says so, because it is the one block
	// that can be longer than any window.
	block := func(build func(*docWriter)) bool {
		if len(out) >= room {
			return false
		}
		r := &docWriter{w: w, label: 12}
		build(r)
		if len(r.lines) == 0 {
			return true // an empty field is nothing to draw, not a hole in the record
		}
		if len(out)+len(r.lines) > room {
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
	// list, and an empty list is a fact about the case, not about the window.
	if whole && room-len(out) >= 3 {
		out = append(out, m.assertionList(d, *c, room-len(out))...)
		block(func(r *docWriter) {
			if c.Feedback != "" {
				r.card("feedback")
				r.para(oneLine(c.Feedback))
			}
		})
	}
	return fitRows(append(out, act.lines...), h)
}

// assertionList is the case's grading list: every declared assertion, the two arms' marks, and the
// judge's words under the ones that failed. The guide's patterns are read off this — passes in both
// arms is no signal, a pass here and a fail there is the skill's contribution, the reverse is a
// regression — which is why the failures alone were not enough to write a revision from.
//
// room is how many lines the pane can give it. Rows are dropped whole and counted: a row cut in half
// reads as a verdict with no assertion, which is the one lie this pane cannot tell.
func (m *Model) assertionList(d *docWriter, c runner.QualityCase, room int) []string {
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

// ---------------------------------------------------------------- the ranking

func (m *Model) qualityRankBody(h int) []string {
	q := m.quality
	if q == nil || q.lab == nil || len(q.lab.Iterations) == 0 {
		return fitRows([]string{"", "  " + dimStyle.Render("no iterations to rank yet")}, h)
	}
	const (
		iterW  = 6
		rateW  = 10
		deltaW = 7
		casesW = 6
	)
	lines := []string{"", regionHead(m.width, "ITERATIONS", "", true),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(m.width-4, iterW+rateW*2+deltaW+casesW+12)))),
		"  " + dimStyle.Render(padCell("ITER", iterW, true)) + " " +
			dimStyle.Render(padCell("WITH", rateW, true)) + " " +
			dimStyle.Render(padCell("WITHOUT", rateW, true)) + " " +
			dimStyle.Render(padCell("DELTA", deltaW, true)) + " " +
			dimStyle.Render(padCell("CASES", casesW, true)) + "  " +
			dimStyle.Render("WHEN"),
	}
	room := max(1, h-len(lines))
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
		case !it.SetComplete:
			state = "  " + dimStyle.Render("subset")
		case !it.Complete && (it.With.Graded() || it.Without.Graded()):
			state = "  " + dimStyle.Render("partial")
		case !it.Complete:
			state = "  " + dimStyle.Render("no verdicts")
		}
		line := mark + colSplit.Render(padCell(fmt.Sprintf("#%d", it.N), iterW, true)) + " " +
			qRankRate(it.With, rateW) + " " + qRankRate(it.Without, rateW) + " " +
			qRankDelta(it, deltaW) + " " +
			colText.Render(padCell(fmt.Sprint(it.Cases), casesW, true)) + "  " +
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
	return fitRows(lines, h)
}

func qRankRate(a runner.QualityArm, width int) string {
	if !a.Graded() {
		return dimStyle.Render(padCell("—", width, true))
	}
	return styleOn.Render(padCell(fmt.Sprintf("%.0f%% %d/%d", a.Rate*100, a.Passed, a.Judged), width, true))
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
	switch msg.String() {
	case "j", "down":
		q.sel = min(q.sel+1, max(0, len(q.cases)-1))
	case "k", "up":
		q.sel = max(0, q.sel-1)
	case "home":
		q.sel = 0
	case "G", "end":
		q.sel = max(0, len(q.cases)-1)
	case "tab":
		if q.lab == nil {
			return nil
		}
		q.rank = true
		q.rsel = bestRow(q.lab)
		m.mode = screenQRank
	case "g":
		return m.startGrading()
	case "R":
		return m.startQualityRun()
	case "f":
		m.openQualityFailures()
	case "e":
		return m.openAssertEditor()
	case "enter":
		m.openQualityReport()
	}
	return nil
}

func (m *Model) updateQRank(msg tea.KeyMsg) tea.Cmd {
	q := m.quality
	if q == nil || q.lab == nil {
		return nil
	}
	switch msg.String() {
	case "j", "down":
		q.rsel = min(q.rsel+1, max(0, len(q.lab.Iterations)-1))
	case "k", "up":
		q.rsel = max(0, q.rsel-1)
	case "g", "home":
		q.rsel = 0
	case "G", "end":
		q.rsel = max(0, len(q.lab.Iterations)-1)
	case "enter":
		if q.rsel >= 0 && q.rsel < len(q.lab.Iterations) {
			q.iter = q.lab.Iterations[q.rsel].N
			m.reloadQuality()
			q.rank = false
			m.mode = screenQuality
			m.setStatus(fmt.Sprintf("iteration %d is on the workbench", q.iter), false)
		}
	case "f":
		m.openQualityFailures()
	case "tab":
		q.rank = false
		m.mode = screenQuality
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
	if it != nil && it.Complete {
		m.setStatus(fmt.Sprintf("iteration %d is already graded — R runs the next one", q.iter), false)
		return nil
	}
	m.run = &runState{kind: runGrade, scope: fmt.Sprintf("grading run · iteration %d", q.iter),
		hint: "the judge reads each arm's outputs against the declared assertions"}
	m.run.live = newLiveRun(m.run.scope, false)
	m.mode = screenRunLive
	return m.startJudgeRun()
}

func (m *Model) startJudgeRun() tea.Cmd {
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
		if _, err := r.GradeIteration(ctx, skill, iteration, runner.GradeOptions{}); err != nil {
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
	next := 1
	if len(q.lab.Iterations) > 0 {
		next = q.lab.Iterations[len(q.lab.Iterations)-1].N + 1
	}
	m.run = &runState{kind: runQuality, scope: fmt.Sprintf("output run · iteration %d", next),
		hint: "both arms, every case: the with-skill arm against the baseline"}
	m.run.live = newLiveRun(m.run.scope, false)
	m.mode = screenRunLive
	return m.startQualityRunCmd()
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
	opts := runner.Options{Lane: store.LaneOutput, Skills: []string{m.skill},
		Arms: runner.AllArms, Isolate: true, Log: runner.ActivityFull}
	go func() {
		defer close(ch)
		if err := r.RunQuality(ctx, opts); err != nil {
			ch <- runner.Event{Kind: "error", Lane: store.LaneOutput, Err: err, Message: err.Error()}
		}
	}()
	return tea.Batch(waitForEvent(ch), tickEvery())
}

// openQualityFailures is the failures of the iteration on screen as one document: every failed
// assertion, both arms, with its evidence. It is the list a revision is written against.
func (m *Model) openQualityFailures() {
	q := m.quality
	if q == nil || q.iter == 0 {
		return
	}
	var lines []string
	for _, c := range q.cases {
		for _, f := range c.Failed {
			lines = append(lines, titleStyle.Render(c.Slug+" · "+f.Arm), "  "+f.Text, "")
			for _, line := range wrapPlain(f.Evidence, max(20, m.width-4)) {
				lines = append(lines, "  "+dimStyle.Render(line))
			}
			lines = append(lines, "")
		}
	}
	if len(lines) == 0 {
		lines = []string{dimStyle.Render("no failed assertion in this iteration")}
	}
	m.doc = &docState{scope: fmt.Sprintf("failures · iteration %d", q.iter),
		title: fmt.Sprintf("FAILURES · ITERATION %d", q.iter), lines: lines}
	m.backTo = m.mode
	m.mode = screenDoc
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

// qualityHints are the workbench's keys, worded as the thing each one does here.
func qualityHints(m *Model) []hint {
	q := m.quality
	if q == nil {
		return []hint{{"esc", "skills"}}
	}
	if m.assertEditActive() {
		return m.assertEdit.hints()
	}
	if q.rank {
		return []hint{{"j k", "iterations"}, {"enter", "that iteration"}, {"f", "failures"}, {"esc", "the loop"}}
	}
	hints := []hint{{"j k", "cases"}, {"e", "assertions"}, {"tab", "the ranking"}, {"f", "failures"}, {"enter", "the report"}}
	if it := q.iteration(); it != nil && !it.Complete {
		hints = append(hints, hint{"g", "grade"})
	}
	return append(hints, hint{"R", "run next"}, hint{"esc", "skills"})
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
	actDoc.action("enter", "open the loop at its newest iteration")
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
