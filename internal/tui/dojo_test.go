package tui

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/runner"
)

// The dojo's right hand is the guide's table: one row per declared assertion, both arms' verdicts in
// columns, in the order the judge read them. It is not a list of failures — a case whose two
// assertions both pass and a case whose two assertions both fail look the same width here, which is
// the point: what the skill changes is the difference between the two columns, not the absence of ✗.
func TestTheDojoIsATableOfAssertions(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qDojo)
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	for _, want := range []string{"ASSERTION", "WITH SKILL", "WITHOUT SKILL", "Three faces", "The total adds up"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the table is missing %q:\n%s", want, body)
		}
	}
	// Declaration order, not failures first: the passing assertion is above the failing one because
	// that is the order the case declares them in and the order the judge read them.
	if strings.Index(body, "Three faces") > strings.Index(body, "The total adds up") {
		t.Fatalf("the rows are not in declaration order:\n%s", body)
	}
	// The one that failed only without the skill: the pattern the loop exists to find.
	if !strings.Contains(body, "1 of 2 pass with the skill and fail without it") {
		t.Fatalf("the table does not say what its pattern means:\n%s", body)
	}
}

// Every row is selectable, passing ones included, and tab is what moves the keyboard between the case
// list and the table — a second axis, because j k on the left answers "which case" and j k on the
// right answers "which assertion", and one pair of keys for both makes one question unanswerable.
func TestTheAssertionCursorWalksEveryRow(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qDojo)
	if m.quality.asel != 1 {
		t.Fatalf("the assertion cursor opened on row %d, want the failed one", m.quality.asel)
	}
	press(m, "tab")
	if !m.quality.tbl || m.mode != screenQuality || m.quality.sec != qDojo {
		t.Fatalf("tab left the view: tbl=%v mode=%v section=%d", m.quality.tbl, m.mode, m.quality.sec)
	}
	// Up onto the assertion both arms passed: its row is selectable and its evidence says why there is
	// none, which is a different sentence from "nothing here".
	press(m, "k")
	if m.quality.asel != 0 {
		t.Fatalf("k moved the assertion cursor to %d, want 0", m.quality.asel)
	}
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	if !strings.Contains(body, "both arms passed it") {
		t.Fatalf("the passing row has no record under it:\n%s", body)
	}
	if strings.Contains(body, "the reply said so") {
		t.Fatalf("the evidence of the failing row is still under a passing one:\n%s", body)
	}
	// g G are the ends of the list that has the keys, and k at the top stays put.
	press(m, "k")
	if m.quality.asel != 0 {
		t.Fatalf("k ran past the top: %d", m.quality.asel)
	}
	press(m, "G")
	if m.quality.asel != 1 {
		t.Fatalf("G = %d, want the last assertion", m.quality.asel)
	}
	// tab back: the case list has the keys again, and moving it re-seats the assertion cursor on the
	// first row that failed in the new case.
	press(m, "tab")
	if m.quality.tbl {
		t.Fatal("the second tab did not give the case list its keys back")
	}
	press(m, "k")
	if m.quality.sel != 0 {
		t.Fatalf("the case cursor is on %d, want the first case", m.quality.sel)
	}
	if m.quality.asel != 0 {
		t.Fatalf("the assertion cursor did not re-seat on the new case: %d", m.quality.asel)
	}
}

// The case says what its own pattern means, and the two halves of the guide's claim are different
// sentences: a case where every assertion passes with the skill and fails without it measures the
// skill; a case where nothing does measures the model.
func TestTheMeasureLineReadsTheCase(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qDojo)
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	if !strings.Contains(body, "1 of 2 pass with the skill and fail without it") {
		t.Fatalf("a half-measuring case is not read that way:\n%s", body)
	}
	press(m, "home")
	press(m, "tab")
	body = stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	if !strings.Contains(body, "no assertion passes with and fails without the skill") {
		t.Fatalf("a case both arms pass is not read that way:\n%s", body)
	}
}

// The feedback is the case's, and it opens from any row — a person arguing with a verdict that passed
// is writing the note this pane exists for. The row under the cursor is what they were looking at.
func TestFeedbackOpensFromAPassingRow(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qDojo)
	press(m, "tab")
	press(m, "k") // the assertion both arms passed
	press(m, "f")
	if m.quality.feedback == nil {
		t.Fatal("f on a passing row opened no feedback")
	}
	if m.quality.feedback.slug != "dummy-2-three-dice" {
		t.Fatalf("the note is on %q, want the case that owns the row", m.quality.feedback.slug)
	}
	if st := stripMarks(m.status); !strings.Contains(st, "the total was one off") && !strings.Contains(st, "feedback") {
		t.Fatalf("the status says nothing about what was opened: %q", st)
	}
}

// TestTheReviewIsPerCaseNotPerAssertion: the note belongs to the case, so the row the assertion cursor
// stands on changes nothing about it — not its text, not its head, not a "written from this assertion"
// line. Two rows, one note.
func TestTheReviewIsPerCaseNotPerAssertion(t *testing.T) {
	m, root := qualityFixture(t)
	write(t, filepath.Join(root, "dummy-workspace", "iteration-1", "dummy-2-three-dice",
		"without_skill", "activity.log"), "read the die\n")
	openView(m, "dummy", qDojo)
	press(m, "tab")
	press(m, "home") // the first row
	press(m, "f")
	if m.quality.feedback == nil {
		t.Fatal("f must open the case's review")
	}
	first := stripMarks(strings.Join(m.quality.feedback.body(80, 12), "\n"))
	if strings.Contains(first, "assertion") && strings.Contains(first, "of 2") {
		t.Fatalf("the review names an assertion as if it were written about one:\n%s", first)
	}
	if strings.Contains(first, "the reply said so") {
		t.Fatalf("the review quotes the selected assertion's evidence:\n%s", first)
	}
	press(m, "esc")

	// The other row, the same case, the same note.
	press(m, "k")
	press(m, "f")
	second := stripMarks(strings.Join(m.quality.feedback.body(80, 12), "\n"))
	if first != second {
		t.Fatalf("the review changed with the assertion cursor:\n%s\n---\n%s", first, second)
	}
}

// TestTheOutputsAreAVNotTheLog: v is what the arms wrote, in both views; the transcript of how they got
// there is enter. The pane does not draw the tail of the log — eight dim lines of tool calls under a
// table is the wall this view was cleaned of.
func TestTheOutputsAreAVNotTheLog(t *testing.T) {
	m, root := qualityFixture(t)
	write(t, filepath.Join(root, "dummy-workspace", "iteration-1", "dummy-2-three-dice",
		"without_skill", "activity.log"), "a log line that is not the output\n")
	openView(m, "dummy", qDojo)
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	if strings.Contains(body, "a log line that is not the output") {
		t.Fatalf("the pane drew the transcript tail:\n%s", body)
	}
	press(m, "v")
	if m.mode != screenDoc || m.doc == nil {
		t.Fatalf("v did not open the outputs: mode %v", m.mode)
	}
	doc := stripMarks(strings.Join(m.doc.lines, "\n"))
	if !strings.Contains(doc, "WITH SKILL") || !strings.Contains(doc, "WITHOUT SKILL") {
		t.Fatalf("v opened %q, want both arms' outputs", m.doc.title)
	}
	if strings.Contains(doc, "a log line that is not the output") {
		t.Fatal("v opened the log, not the outputs")
	}
	press(m, "esc")
	press(m, "enter")
	if msg := stripMarks(strings.Join(func() []string {
		if m.doc == nil {
			return nil
		}
		return m.doc.lines
	}(), "\n")); !strings.Contains(msg, "a log line that is not the output") {
		t.Fatalf("enter is not the transcript: %+v", m.doc)
	}
}

// TestTheIterationViewEditsTheSetItIsReading: e opens the case's assertions under the verdicts they
// are read against, n makes a case and d deletes one — the same three keys as the initialization view,
// because a sentence that turns out to be unmeasurable is rewritten where the evidence for that is.
func TestTheIterationViewEditsTheSetItIsReading(t *testing.T) {
	m, root := qualityFixture(t)
	openView(m, "dummy", qDojo)
	was := readEvals(t, root)

	press(m, "e")
	if !m.assertEditActive() {
		t.Fatal("e must open the case's assertions in the iteration view")
	}
	if len(m.assertEdit.lines) != 2 || m.assertEdit.lines[0] != "Three faces" {
		t.Fatalf("the list is not the case's own: %q", m.assertEdit.lines)
	}
	// The strip is under the columns: the verdicts stay on screen while the sentence is rewritten.
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	for _, want := range []string{"ASSERTIONS", "WITH SKILL", "Three faces"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the table is gone while the list is open: missing %q\n%s", want, body)
		}
	}
	if strings.Index(body, "PATTERN") > strings.Index(body, "ASSERTIONS") {
		t.Fatalf("the list is not drawn under the table:\n%s", body)
	}
	// Editing a sentence and writing it keeps the case's other assertions and replaces none with two.
	m.assertEdit.lines = []string{"Three faces, each 1 to 20", "The total adds up"}
	press(m, "ctrl+s")
	if m.assertEditActive() {
		t.Fatal("the list stays open after the write")
	}
	var file struct {
		Evals []struct {
			Slug       string   `json:"slug"`
			Assertions []string `json:"assertions"`
		} `json:"evals"`
	}
	if err := json.Unmarshal([]byte(readEvals(t, root)), &file); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range file.Evals {
		if c.Slug == "dummy-2-three-dice" {
			got = c.Assertions
		}
	}
	if len(got) != 2 || got[0] != "Three faces, each 1 to 20" || got[1] != "The total adds up" {
		t.Fatalf("the edit duplicated or dropped an assertion: %q", got)
	}
	if readEvals(t, root) == was {
		t.Fatal("nothing was written")
	}

	// n and d are the case's own, behind the same confirmation.
	press(m, "n")
	if !m.caseNewActive() {
		t.Fatal("n must make a case from the iteration view too")
	}
	press(m, "esc")
	press(m, "d")
	if m.mode != screenAsk {
		t.Fatalf("d did not ask before deleting: mode %v", m.mode)
	}
	press(m, "n")
	if m.mode != screenQuality {
		t.Fatalf("the cancel left the view: mode %v", m.mode)
	}
}

// TestAnEditedAssertionOwesAVerdict: a verdict belongs to the words it was written against. An
// assertion rewritten in the iteration view is not graded — the row is a dash, the pane says why, and
// the case's next move is the grade and not the revision. That is the guide's order: edit the
// assertions, re-grade them, and only then prompt for a revision.
func TestAnEditedAssertionOwesAVerdict(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qDojo)
	press(m, "e")
	m.assertEdit.lines = []string{"Three faces, each an integer from 1 to 20", "The total adds up"}
	press(m, "ctrl+s")
	if m.assertEditActive() {
		t.Fatal("the write did not close the list")
	}
	c := m.quality.caseAt()
	if c == nil {
		t.Fatal("the case left the view")
	}
	if n := c.Unjudged(); n != 1 {
		t.Fatalf("%d assertions owe a verdict, want the one that was rewritten: %+v", n, c.Rows)
	}
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	if !strings.Contains(body, "changed since the grade") {
		t.Fatalf("the pane does not say the grade is stale:\n%s", body)
	}
	if key, _ := m.dojoAction(*c); key != "g" {
		t.Fatalf("the case's next move is %q, want the grade", key)
	}
}

// TestTheIterationViewCarriesTheRecordTheReviewNeeds: a review is a judgement of an output against the
// expected output, so the iteration view prints the case's own record — prompt, expected_output, files —
// under the case's head, exactly as the initialization view does. A window too short for it prints the
// key that opens the file instead of half a record.
func TestTheIterationViewCarriesTheRecordTheReviewNeeds(t *testing.T) {
	m, _ := qualityFixture(t)
	m.width, m.height = 110, 34
	openView(m, "dummy", qDojo)
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	for _, want := range []string{"prompt", "Roll three d20.", "expected_output", "Three faces and a total.",
		"files", "none — the run root starts empty", "REVIEW", "output ·"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the pane is missing %q for the review:\n%s", want, body)
		}
	}
	// The output the review is about and the expected output it is judged against are both on screen —
	// that is the whole point of the block.
	if strings.Index(body, "expected_output") > strings.Index(body, "PATTERN") {
		t.Fatalf("the record is not above the verdicts:\n%s", body)
	}

	// A short window keeps the table and the output and pays for them with the record.
	m.width, m.height = 110, 18
	short := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	if !strings.Contains(short, "the prompt and expected_output are in evals.json — S opens it") {
		t.Fatalf("a short window did not fall back to the pointer:\n%s", short)
	}
	if !strings.Contains(short, "PATTERN") {
		t.Fatalf("a short window lost the table:\n%s", short)
	}
}

// TestTheIterationViewRunsTheCaseItIsWorkingOn: `r` is the probe in both views. A revision is asked for
// from the iteration view, and the case whose failure justifies it has to be runnable from there too —
// one case, both arms, judged, and an iteration that never ranks.
func TestTheIterationViewRunsTheCaseItIsWorkingOn(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qDojo)
	// The band offers both spends, so the key is not one you have to be told about.
	keys := strings.Join(hintKeys(m.hints()), " ")
	if !strings.Contains(keys, "r") || !strings.Contains(keys, "R") {
		t.Fatalf("the band does not offer the run keys: %q", keys)
	}
	press(m, "r")
	if m.mode != screenRunLive || m.run == nil {
		t.Fatalf("r in the iteration view did not start a run: mode %v, status %q", m.mode, stripMarks(m.status))
	}
	defer func() {
		if m.run != nil && m.run.live != nil && m.run.live.cancel != nil {
			m.run.live.cancel()
		}
	}()
	if n := len(m.run.live.cases); n != 1 {
		t.Fatalf("the probe holds %d cases, want the one under the cursor", n)
	}
	if got := m.run.live.cases[0].name; got != "dummy-2-three-dice" {
		t.Fatalf("the probe is on %q, want the case under the cursor", got)
	}
}

// TestAGradedCaseInAPartialIterationOffersTheReview: an iteration that ran part of the set is
// incomplete by count, and the case it did run is graded. The pane must not tell the operator to grade
// a case whose verdicts are already on screen — the grade is owed by the case, not by the iteration.
func TestAGradedCaseInAPartialIterationOffersTheReview(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qDojo)
	// The same case, in an iteration that is a subset of the declared set.
	for i := range m.quality.lab.Iterations {
		m.quality.lab.Iterations[i].SetComplete = false
		m.quality.lab.Iterations[i].Complete = false
	}
	c := *m.quality.caseAt()
	if !c.Graded() {
		t.Fatalf("the fixture's case has no verdicts: %+v", c)
	}
	// With a note on file the next move is the revision; without one it is the review. Neither is the
	// grade, because there is nothing left to grade.
	if key, _ := m.dojoAction(c); key != "p" {
		t.Fatalf("a graded, reviewed case offers %q, want the revision", key)
	}
	c.Feedback = ""
	if key, _ := m.dojoAction(c); key != "f" {
		t.Fatalf("a graded case with no review offers %q, want the review", key)
	}
}

// TestACaseTheIterationDidNotRunSaysSo: a probe iteration runs one case, and the others are not
// ungraded — they are absent from that run, with their verdicts belonging to an older one. The pane and
// the list say that instead of "not graded yet" and a green 0/N, which are the two ways this screen
// used to blame the grade for a run that did not happen.
func TestACaseTheIterationDidNotRunSaysSo(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qSetup)
	press(m, "home")
	// The iteration on screen is iteration 1; pretend it ran only the other case.
	c := m.quality.cases[0]
	c.Ran = false
	c.With, c.Without = runner.QualityArm{}, runner.QualityArm{}
	for i := range c.Rows {
		c.Rows[i].With, c.Rows[i].Without = nil, nil
	}
	m.quality.cases[0] = c

	pane := stripMarks(strings.Join(m.casePane(max(40, m.width-m.caseListW()-3), m.bodyHeight()), "\n"))
	if !strings.Contains(pane, "not in iteration 1") {
		t.Fatalf("the pane calls a case the iteration never ran \"ungraded\":\n%s", pane)
	}
	if !strings.Contains(pane, "did not run this case") {
		t.Fatalf("the pane does not say the iteration skipped it:\n%s", pane)
	}

	// The iteration view's list: a case it did not run is a dash, not a green 0/N. The section is moved
	// without re-reading the disk, because re-opening the view would undo the pretend iteration.
	m.setQualitySection(qDojo)
	list := stripMarks(strings.Join(m.dojoList(m.quality.iteration(), 12), "\n"))
	if strings.Contains(list, "0/2") {
		t.Fatalf("a case the iteration did not run is counted as passing:\n%s", list)
	}
	if !strings.Contains(list, "—") {
		t.Fatalf("the list does not mark the case as absent:\n%s", list)
	}
}

// TestTheTableKeepsTheRowTheCursorIsOnOnScreen: a case with more assertions than the pane holds used
// to draw its first rows and cut the rest, which on a four-assertion case meant the failing row — the
// one the cursor is put on — was the row the pane left out. The table windows around the cursor and
// says how much it left out at each end.
func TestTheTableKeepsTheRowTheCursorIsOnOnScreen(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qDojo)
	c := m.quality.cases[0]
	// Four assertions, and the failure on the last one.
	pass, fail := true, false
	c.Rows = append(c.Rows,
		runner.QualityAssertion{Text: "a fourth assertion about the reply", With: &pass, Without: &pass},
		runner.QualityAssertion{Text: "a fifth assertion, the one that fails", With: &pass, Without: &fail})
	m.quality.cases[0] = c
	m.quality.asel = len(c.Rows) - 1

	// Six rows of room: not enough for five rows and the evidence under the cursor's.
	table := stripMarks(strings.Join(m.dojoTable(max(60, m.width-m.caseListW()-3), c, 6), "\n"))
	if !strings.Contains(table, "the one that fails") {
		t.Fatalf("the row the cursor is on was cut off the table:\n%s", table)
	}
	if !strings.Contains(table, " above — r opens the report") {
		t.Fatalf("the table cut rows above the cursor without saying so:\n%s", table)
	}
	if strings.Contains(table, "assertions more") {
		t.Fatalf("the table says rows were cut below a cursor on the last row:\n%s", table)
	}
}
