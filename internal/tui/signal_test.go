package tui

import (
	"strings"
	"testing"

	"evalctl/internal/runner"
)

// A set can be all green and measure nothing. The test cases view marks each case with what its own
// verdicts say about the skill: `measures` when every assertion passes with the skill and not every one
// without it, `no signal` when the baseline passes everything (that case can never show a difference),
// `both fail` when neither arm can, and `ungraded` while a verdict is missing.
func TestTheCaseListMarksWhatEachCaseMeasures(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qSetup)
	list := stripMarks(strings.Join(m.caseList(m.bodyHeight()), "\n"))
	// The list shows the case's count of measuring assertions, coloured: 1 of 2 here, and the case that
	// measures none says 0 of 2.
	if !strings.Contains(list, "1/2") || !strings.Contains(list, "0/2") {
		t.Fatalf("the case list does not mark what each case measures:\n%s", list)
	}
	// The names survive the tag: a case cut to "dummy-1…" to make room for a tag has spent the row.
	if !strings.Contains(list, "dummy-1-one-die") || !strings.Contains(list, "dummy-2-three-dice") {
		t.Fatalf("a case name was cut to make room for its tag:\n%s", list)
	}
	// And the pane says the same thing in a sentence: what the pattern means, and what to do about it.
	pane := stripMarks(strings.Join(m.casePane(70, m.bodyHeight()), "\n"))
	if !strings.Contains(pane, "make it fail") || !strings.Contains(pane, "the baseline passes every assertion") {
		t.Fatalf("the pane does not say what `no signal` means:\n%s", pane)
	}
	if !strings.Contains(pane, "sharpen the assertion") {
		t.Fatalf("the pane names no next move:\n%s", pane)
	}
}

// The tag is a measurement, so a half-graded case is not "make it fail" — it has not graded at all, and a
// missing verdict is never a pass.
func TestAnUngradedCaseIsNotANoSigNal(t *testing.T) {
	no := false
	yes := true
	c := runner.QualityCase{Rows: []runner.QualityAssertion{
		{Text: "one", With: &yes},
		{Text: "two", With: &yes, Without: &no},
	}}
	if got := signalOf(c).tag(); got != "not graded" {
		t.Fatalf("a case with a missing grade reads as %q", got)
	}
	c.Rows[0].Without = &yes
	c.Rows[1].Without = &yes
	if got := signalOf(c).tag(); got != "make it fail" {
		t.Fatalf("a case the baseline passes everywhere reads as %q, want make it fail", got)
	}
	c.Rows = []runner.QualityAssertion{{Text: "one", With: &no, Without: &no}}
	if got := signalOf(c).tag(); got != "fix or drop" {
		t.Fatalf("a case neither arm passes reads as %q, want fix or drop", got)
	}
	c.Rows = []runner.QualityAssertion{{Text: "one", With: &yes, Without: &no}}
	if got := signalOf(c).tag(); got != "adds value" {
		t.Fatalf("a case the skill is what passes reads as %q, want measures", got)
	}
}

// The columns are columns: `WITH` sits over the with-skill verdict and `WITHOUT` over the without-skill
// one, in the test cases pane and in the iteration table alike. A header that starts where the title
// ends is a caption with numbers after it, and the reader has to work out which number is which.
func TestTheVerdictColumnsLineUpUnderTheirHeads(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qSetup)
	press(m, "j") // the case whose second assertion fails without the skill
	lines := stripMarksSlice(m.casePane(70, m.bodyHeight()))
	head, weak, strong := "", "", ""
	for _, line := range lines {
		switch {
		case strings.Contains(line, "WITHOUT SKILL"):
			head = line
		case strings.Contains(line, "make it fail"):
			weak = line
		case strings.Contains(line, "adds value  "):
			strong = line
		}
	}
	if head == "" || weak == "" || strong == "" {
		t.Fatalf("no header or no rows to compare:\n%s", strings.Join(lines, "\n"))
	}
	// The cells are right-aligned, so it is the right edge that has to agree: a cell and its heading end
	// in the same column, whatever their widths.
	if a, b := cellAt(head, "WITH SKILL")+10, cellAt(strong, "pass")+4; a != b {
		t.Fatalf("WITH SKILL ends at %d, its cell at %d:\n%s\n%s", a, b, head, strong)
	}
	// The baseline column is a requirement, not a result: its cell is a glyph, and the heading ends
	// where the glyph does — a cross for the assertion that make it fail, a tick for the one that does.
	if a, b := cellAt(head, "WITHOUT SKILL")+13, cellAt(weak, "✗")+1; a != b {
		t.Fatalf("WITHOUT SKILL ends at %d, its cell at %d:\n%s\n%s", a, b, head, weak)
	}
	if a, b := cellAt(head, "WITHOUT SKILL")+13, cellAt(strong, "✓")+1; a != b {
		t.Fatalf("WITHOUT SKILL ends at %d, its cell at %d:\n%s\n%s", a, b, head, strong)
	}

	// The iteration view's table, the same reading.
	m.setQualitySection(qDojo)
	press(m, "j") // the case with a failure in it: its row is the one whose baseline passed
	lines = stripMarksSlice(m.qualityBody(m.bodyHeight()))
	head, row := "", ""
	for _, line := range lines {
		switch {
		case strings.Contains(line, "ASSERTION"):
			head = line
		case strings.Contains(line, "The total adds up"):
			row = line
		}
	}
	if head == "" || row == "" {
		t.Fatalf("the iteration table has no header or no row:\n%s", strings.Join(lines, "\n"))
	}
	if a, b := cellAt(head, "WITHOUT SKILL")+13, cellAt(row, "✓")+1; a != b {
		t.Fatalf("WITHOUT SKILL ends at %d, its cell at %d:\n%s\n%s", a, b, head, row)
	}
}

// cellAt is a cell position, not a byte offset: the table's own headings carry a `·`, and a byte index
// would be one cell off from the row it is compared with.
func cellAt(line, token string) int {
	i := strings.Index(line, token)
	if i < 0 {
		return -1
	}
	return len([]rune(line[:i]))
}

func stripMarksSlice(in []string) []string {
	out := make([]string, len(in))
	for i, line := range in {
		out[i] = stripMarks(line)
	}
	return out
}

// A table that loses its rows is not a table: on a short window the verdicts outlive the prose, keep a
// row, and keep the line that says what `pass!` means — the mark is spent on a row only if its meaning
// is still on screen.
func TestTheAssertionTableSurvivesAShortWindow(t *testing.T) {
	m, _ := qualityFixture(t)
	m.width, m.height = 100, 18
	openView(m, "dummy", qSetup)
	press(m, "j") // the case with a failure, and a passing assertion marked `pass!`
	pane := stripMarks(strings.Join(m.casePane(70, m.bodyHeight()), "\n"))
	for _, want := range []string{"WITH SKILL", "WITHOUT SKILL", "Three faces", "make it fail"} {
		if !strings.Contains(pane, want) {
			t.Fatalf("a short window lost %q from the table:\n%s", want, pane)
		}
	}
	// The fields go before the verdicts do: the record is one keypress away, the verdicts are the screen.
	if strings.Contains(pane, "expected_output") && !strings.Contains(pane, "assertions ·") {
		t.Fatalf("the pane kept the prose and lost the verdicts:\n%s", pane)
	}
}

// The colour follows the meaning, not the word. In the with-skill column a fail is bad and a pass is
// good; in the without-skill column it is the other way round, because the baseline failing is the whole
// evidence that the skill does anything. Colouring `pass` green in both columns points the reader at the
// row that is fine and hides the row that has to be rewritten.
func TestTheWithoutColumnColoursByHealth(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		cell *bool
		arm  string
		want string
		why  string
	}{
		{&yes, runner.ArmWith, styleOn.Render("pass"), "the skill passed it"},
		{&no, runner.ArmWith, statusBad.Render("fail"), "the skill failed it — that is the skill's problem"},
		{&no, runner.ArmWithout, styleOn.Render("fail"), "the baseline failed it — that is the assertion working"},
		{&yes, runner.ArmWithout, badgeWarn.Render("pass"), "the baseline passed it — that is the assertion to fix"},
		{nil, runner.ArmWithout, dimStyle.Render("—"), "no verdict is not a verdict"},
	} {
		if got := armCell(tc.cell, tc.arm); got != tc.want {
			t.Fatalf("%s: cell = %q, want %q", tc.why, got, tc.want)
		}
	}
}

// The three shapes the guide names, read off one assertion, plus the one it leaves implied.
func TestAnAssertionsPattern(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		with, without *bool
		want          string
	}{
		{&yes, &no, "adds value"},
		{&yes, &yes, "make it fail"},
		{&no, &no, "fix or drop"},
		{&no, &yes, "fix the skill"},
		{&yes, nil, "not graded"},
	}
	for _, c := range cases {
		if got := assertionPattern(runner.QualityAssertion{With: c.with, Without: c.without}).tag(); got != c.want {
			t.Fatalf("with=%v without=%v reads as %q, want %q", c.with, c.without, got, c.want)
		}
	}
}

// The count is coloured by how bad the case is, not by how complete it is. A case holding an assertion
// the skill makes *worse* — pass without it, fail with it — is red even though it is not full and even
// though it is "only" one row: that assertion is the loop saying the skill is a regression there, and the
// warning colour would put it in the same class as a case that merely make it fail.
func TestAHurtingCaseTurnsTheCountRed(t *testing.T) {
	yes, no := true, false
	measured := runner.QualityCase{Rows: []runner.QualityAssertion{
		{Text: "one", With: &yes, Without: &no},
		{Text: "two", With: &yes, Without: &no},
	}}
	if cell, style := signalCell(measured); cell != "2/2" || style.Render("x") != styleOn.Render("x") {
		t.Fatalf("a case that measures everything = %q, want 2/2 in the pass colour", cell)
	}
	mixed := runner.QualityCase{Rows: []runner.QualityAssertion{
		{Text: "one", With: &yes, Without: &no},
		{Text: "two", With: &yes, Without: &yes},
	}}
	if cell, style := signalCell(mixed); cell != "1/2" || style.Render("x") != badgeWarn.Render("x") {
		t.Fatalf("a case that measures half = %q, want 1/2 in the warning colour", cell)
	}
	hurting := runner.QualityCase{Rows: []runner.QualityAssertion{
		{Text: "one", With: &yes, Without: &no},
		{Text: "two", With: &no, Without: &yes},
	}}
	if cell, style := signalCell(hurting); cell != "1/2" || style.Render("x") != statusBad.Render("x") {
		t.Fatalf("a case with a hurting assertion = %q, want 1/2 in the failure colour", cell)
	}
}

// The glyph is the case's own verdict on itself, drawn from the same ladder as the count: a cross when the
// skill makes an assertion worse, a tick when every assertion measures it, a hollow circle when the case
// make it fail, and the declaration's quiet marks while there is nothing graded to read.
func TestTheCaseGlyphFollowsTheGrades(t *testing.T) {
	yes, no := true, false
	full := runner.QualityCase{Prompt: "p", Rows: []runner.QualityAssertion{
		{Text: "one", With: &yes, Without: &no},
	}}
	if got := caseMark(full); got != styleOn.Render(glyphs.Done) {
		t.Fatalf("a case that measures everything = %q, want a green tick", got)
	}
	hurting := runner.QualityCase{Prompt: "p", Rows: []runner.QualityAssertion{
		{Text: "one", With: &no, Without: &yes},
	}}
	if got := caseMark(hurting); got != statusBad.Render(glyphs.Failed) {
		t.Fatalf("a case that hurts = %q, want a red cross", got)
	}
	useless := runner.QualityCase{Prompt: "p", Rows: []runner.QualityAssertion{
		{Text: "one", With: &yes, Without: &yes},
	}}
	if got := caseMark(useless); got != badgeWarn.Render(glyphs.Pending) {
		t.Fatalf("a case that make it fail = %q, want the hollow circle", got)
	}
	// Nothing graded: the declaration's own state, in the quiet register.
	ungraded := runner.QualityCase{Prompt: "p", Rows: []runner.QualityAssertion{{Text: "one"}}}
	if got := caseMark(ungraded); got != dimStyle.Render("·") {
		t.Fatalf("an ungraded case = %q, want the quiet dot", got)
	}
	stub := runner.QualityCase{}
	if got := caseMark(stub); got != dimStyle.Render(glyphs.Failed) {
		t.Fatalf("a stub = %q, want a quiet cross", got)
	}
}

// The case-level roll-up is the worst thing in the case, not the average: one assertion the skill makes
// worse wins over the others, so the tag, the glyph and the count all say the same thing.
func TestAHurtingAssertionWinsTheCaseTag(t *testing.T) {
	yes, no := true, false
	c := runner.QualityCase{Rows: []runner.QualityAssertion{
		{Text: "one", With: &yes, Without: &yes},
		{Text: "two", With: &no, Without: &yes},
	}}
	if got := signalOf(c).tag(); got != "fix the skill" {
		t.Fatalf("a case holding a hurting assertion reads as %q, want fix the skill", got)
	}
	c.Rows[1] = runner.QualityAssertion{Text: "two", With: &yes, Without: &yes}
	c.Rows[0] = runner.QualityAssertion{Text: "one", With: &yes, Without: &yes}
	if got := signalOf(c).tag(); got != "make it fail" {
		t.Fatalf("a case whose baseline passes everything reads as %q, want make it fail", got)
	}
}
