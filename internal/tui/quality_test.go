package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// openView walks the lane the way a person does: the skill, then the view off the menu. A test that
// wants one of the three does not reach past the screen between them, because the menu is where the
// cursor lands and a section reached by a shortcut is not the section an operator reaches.
func openView(m *Model, name string, sec int) {
	m.openQualitySkill(name)
	press(m, fmt.Sprint(sec+1))
}

// qualityFixture builds the smallest tree the output lane can draw: one skill, two cases, and one
// iteration whose arms carry verdicts. Nothing here runs an agent — the screens read files, and the
// files are what a run would have written.
func qualityFixture(t *testing.T) (*Model, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HERMES_BIN", filepath.Join(root, "no-such-hermes"))
	t.Setenv("HERMES_ROOT", filepath.Join(root, "hermes"))
	t.Setenv("EVAL_MASTERCLASS_ROOT", filepath.Join(root, "masterclasses"))
	write(t, filepath.Join(root, "trigger", "trigger-evals.json"), triggerSet)
	write(t, filepath.Join(root, "dummy", "SKILL.md"),
		"---\nname: dummy\ndescription: Roll dice and report the faces that come up.\n---\n\nRoll them.\n")
	write(t, filepath.Join(root, "dummy", "evals", "evals.json"), `{
  "skill_name": "dummy",
  "evals": [
    {"id": 1, "slug": "dummy-1-one-die", "prompt": "Roll a die.", "expected_output": "One face.",
     "files": [], "assertions": ["The reply reports one face", "Nothing was written"]},
    {"id": 2, "slug": "dummy-2-three-dice", "prompt": "Roll three d20.", "expected_output": "Three faces and a total.",
     "files": [], "assertions": ["Three faces", "The total adds up"]}
  ]
}`)
	iter := filepath.Join(root, "dummy-workspace", "iteration-1")
	write(t, filepath.Join(iter, "meta.json"), `{"iteration":1,"skill":"dummy","started":"2026-11-01T12:00:00+0100","cases":["dummy-1-one-die","dummy-2-three-dice"]}`)
	writeGrading(t, iter, "dummy-1-one-die", "with_skill", []graded{{"The reply reports one face", true}, {"Nothing was written", true}})
	writeGrading(t, iter, "dummy-1-one-die", "without_skill", []graded{{"The reply reports one face", true}, {"Nothing was written", true}})
	writeGrading(t, iter, "dummy-2-three-dice", "with_skill", []graded{{"Three faces", true}, {"The total adds up", true}})
	writeGrading(t, iter, "dummy-2-three-dice", "without_skill", []graded{{"Three faces", true}, {"The total adds up", false}})
	write(t, filepath.Join(iter, "feedback.json"), `{"dummy-1-one-die":"","dummy-2-three-dice":"the total was one off"}`)

	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(s)
	m.width, m.height = 120, 34
	return m, root
}

type graded struct {
	text   string
	passed bool
}

func writeGrading(t *testing.T, iter, slug, arm string, results []graded) {
	t.Helper()
	type res struct {
		Text     string `json:"text"`
		Passed   bool   `json:"passed"`
		Evidence string `json:"evidence"`
	}
	body := map[string]any{"assertion_results": []res{}, "checks": map[string]any{}, "response": "roll"}
	var out []res
	passed := 0
	for _, r := range results {
		if r.passed {
			passed++
		}
		out = append(out, res{Text: r.text, Passed: r.passed, Evidence: "the reply said so"})
	}
	body["assertion_results"] = out
	body["summary"] = map[string]any{"passed": passed, "failed": len(out) - passed, "total": len(out),
		"pass_rate": float64(passed) / float64(len(out))}
	writeJSON(t, filepath.Join(iter, slug, arm, "grading.json"), body)
	write(t, filepath.Join(iter, slug, arm, "timing.json"), `{"duration_ms":1000,"total_tokens":100,"exit_code":0}`)
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, string(b))
}

// TestOutputLaneReadsTheLoop walks the lane the way it is drawn: the skill opens on its menu, the
// menu's three rows are the lane's three jobs, and a row opens the view it names — with the rail
// offering the same three once you are inside.
func TestOutputLaneReadsTheLoop(t *testing.T) {
	m, _ := qualityFixture(t)
	// The door, then output, then the skill.
	press(m, "j")
	press(m, "enter")
	if m.mode != screenSkill || m.lane != store.LaneOutput {
		t.Fatalf("mode %v lane %q, want the output skill list", m.mode, m.lane)
	}
	list := stripMarks(strings.Join(m.skillBody(m.bodyHeight()), "\n"))
	for _, want := range []string{"CASES", "WITH", "WITHOUT", "dummy", "100%", "75%"} {
		if !strings.Contains(list, want) {
			t.Fatalf("the output chooser is missing %q:\n%s", want, list)
		}
	}
	press(m, "enter")
	if m.mode != screenQualityMenu {
		t.Fatalf("mode = %v, want the skill's three views", m.mode)
	}
	// The menu offers the three names the rest of the lane uses, and the cursor is on the view the
	// disk is asking for: this iteration has a failure, so it is the iteration.
	menu := stripMarks(strings.Join(m.qualityMenuBody(m.bodyHeight()), "\n"))
	for _, want := range []string{"SECTIONS", "initialization", "iteration", "testing quality",
		"2 cases · 4 assertions", "1 assertion failed", "iteration #1"} {
		if !strings.Contains(menu, want) {
			t.Fatalf("the menu is missing %q:\n%s", want, menu)
		}
	}
	if m.quality.vcursor != qDojo {
		t.Fatalf("the menu opened on row %d, want the iteration — its newest run failed", m.quality.vcursor)
	}
	press(m, "enter")
	if m.mode != screenQuality || m.quality.sec != qDojo {
		t.Fatalf("mode %v section %d, want the iteration view", m.mode, m.quality.sec)
	}
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	for _, want := range []string{"FAILED", "without", "dummy-2-three-dice", "ASSERTION", "WITH",
		"WITHOUT SKILL", "The total adds up", "without · the reply said so", "REVIEW", "the total was one off"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the iteration view is missing %q:\n%s", want, body)
		}
	}
	// esc is the only way across: a view has no key to another view, so the menu is one level back
	// and the number is pressed there.
	press(m, "esc")
	if m.mode != screenQualityMenu {
		t.Fatalf("esc from the iteration = mode %v, want the menu", m.mode)
	}
	// 1: the declaration. The list is the cases and one declaration mark, and nothing else — the
	// rates and the verdicts belong to the views that read them.
	press(m, "1")
	if m.quality.sec != qSetup {
		t.Fatalf("1 did not open test cases: section %d", m.quality.sec)
	}
	setup := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	for _, want := range []string{"CASES", "dummy-1-one-die", "dummy-2-three-dice"} {
		if !strings.Contains(setup, want) {
			t.Fatalf("test cases is missing %q:\n%s", want, setup)
		}
	}
	// The iteration the views read is on the rail, once, for all three of them.
	if !strings.Contains(stripMarks(m.View()), "iteration #1") {
		t.Fatalf("the rail does not say which iteration is on screen:\n%s", stripMarks(m.View()))
	}
	for _, unwanted := range []string{"FAILED", "100%", "50%"} {
		if strings.Contains(setup, unwanted) {
			t.Fatalf("the case list still carries a rate or a failure count (%q):\n%s", unwanted, setup)
		}
	}
	// 3: the trend, with the best complete iteration tagged and the delta's basis named.
	press(m, "esc")
	press(m, "3")
	if m.quality.sec != qQuality {
		t.Fatalf("3 did not open testing quality: section %d", m.quality.sec)
	}
	rank := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	for _, want := range []string{"ITERATIONS", "#1", "75%", "best", "Δ w−w/o", "RAN"} {
		if !strings.Contains(rank, want) {
			t.Fatalf("testing quality is missing %q:\n%s", want, rank)
		}
	}
	// esc walks back one level at a time: the view to the menu, the menu to the skills.
	press(m, "esc")
	if m.mode != screenQualityMenu {
		t.Fatalf("esc from a view = mode %v, want the menu", m.mode)
	}
	press(m, "esc")
	if m.mode != screenSkill {
		t.Fatalf("esc from the menu = %v, want the skills", m.mode)
	}
}

// TestOutputLaneSaysWhatIsMissing: a skill with no cases is a closed loop, and the set's view says
// so in one band — the steps as chips, and the one errand that opens it — above the cases the screen
// is about.
func TestOutputLaneSaysWhatIsMissing(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HERMES_BIN", filepath.Join(root, "no-such-hermes"))
	t.Setenv("HERMES_ROOT", filepath.Join(root, "hermes"))
	t.Setenv("EVAL_MASTERCLASS_ROOT", filepath.Join(root, "masterclasses"))
	write(t, filepath.Join(root, "trigger", "trigger-evals.json"), triggerSet)
	write(t, filepath.Join(root, "bare", "SKILL.md"), "---\nname: bare\ndescription: A skill with no cases.\n---\n")
	write(t, filepath.Join(root, "bare", "evals", "evals.json"), `{"skill_name":"bare","evals":[]}`)
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(s)
	m.width, m.height = 120, 34
	m.openQualitySkill("bare")
	// Nothing declared means no loop: the menu's cursor is on the set, because that is the view the
	// disk is asking for.
	if m.quality.vcursor != qSetup {
		t.Fatalf("the menu opened on row %d, want the set", m.quality.vcursor)
	}
	press(m, "1")
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	// Three moves, not four: the grade is part of the run, so it is not a step before the first run.
	for _, want := range []string{"INITIALIZATION", "the loop is not open yet · 0 of 3", "cases", "assertions",
		"write the cases — evals.json", "S", "NO CASE"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the band is missing %q:\n%s", want, body)
		}
	}
	if m.quality.lab == nil || m.quality.lab.Next.Key != "set" {
		t.Fatalf("next = %+v, want the set", m.quality.lab)
	}
}

func TestQualityLabRanksOnlyCompleteIterations(t *testing.T) {
	root := t.TempDir()
	t.Setenv("EVAL_MASTERCLASS_ROOT", filepath.Join(root, "masterclasses"))
	write(t, filepath.Join(root, "trigger", "trigger-evals.json"), triggerSet)
	write(t, filepath.Join(root, "q", "SKILL.md"), "---\nname: q\ndescription: q.\n---\n")
	write(t, filepath.Join(root, "q", "evals", "evals.json"), `{
  "skill_name": "q",
  "evals": [{"id":1,"slug":"q-1","prompt":"p","expected_output":"e","files":[],"assertions":["a","b"]}]
}`)
	iter1 := filepath.Join(root, "q-workspace", "iteration-1")
	write(t, filepath.Join(iter1, "meta.json"), `{"iteration":1,"started":"2026-11-01T10:00:00+0100"}`)
	writeGrading(t, iter1, "q-1", "with_skill", []graded{{"a", true}, {"b", true}})
	writeGrading(t, iter1, "q-1", "without_skill", []graded{{"a", true}, {"b", false}})
	// iteration 2: only the with arm was graded — a partial rate must not outrank a complete one.
	iter2 := filepath.Join(root, "q-workspace", "iteration-2")
	write(t, filepath.Join(iter2, "meta.json"), `{"iteration":2,"started":"2026-11-01T11:00:00+0100"}`)
	writeGrading(t, iter2, "q-1", "with_skill", []graded{{"a", true}, {"b", true}})

	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	lab, err := runner.New(s).QualityLab("q")
	if err != nil {
		t.Fatal(err)
	}
	if lab.Best != 1 {
		t.Fatalf("best = %d, want the complete iteration 1", lab.Best)
	}
	if lab.Ungraded == 0 {
		t.Fatalf("iteration 2 owes an arm and the lab must count it")
	}
	if lab.Next.Key != "grade" {
		t.Fatalf("next = %+v, want grading the newest iteration", lab.Next)
	}
}

func TestQualityRunKindsAreNamed(t *testing.T) {
	if runQuality.String() != "output run" || runGrade.String() != "grading run" {
		t.Fatalf("run kinds lost their names: %q %q", runQuality, runGrade)
	}
}

// reportMD is what a run writes: markdown with headings, a table of the arms and a sentence with
// emphasis. Every construct here is one the source spells out and the reader must not.
const reportMD = `# dummy · eval iteration 1

## Grading

| Case | With | Without |
|---|---|---|
| dummy-1-one-die | PASS | PASS |

The **total** was one off, and ` + "`roll`" + ` is the tool it came from.
`

// TestTheIterationReportReadsAsMarkdown is the rule for every document that is markdown on disk: the
// reader renders it, so a report is read as headings, a table and emphasis rather than as the source
// that spells them. The report is drawn again on a resize, because a table wrapped for the window
// the reader used to have is a table cut in half.
func TestTheIterationReportReadsAsMarkdown(t *testing.T) {
	m, root := qualityFixture(t)
	write(t, filepath.Join(root, "dummy-workspace", "iteration-1", "report.md"), reportMD)
	openView(m, "dummy", qDojo)
	m.openQualityReport()
	if m.mode != screenDoc || m.doc == nil {
		t.Fatalf("mode = %v, want the report in the document reader", m.mode)
	}
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	for _, want := range []string{"dummy · eval iteration 1", "Grading", "dummy-1-one-die", "│", "total"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the report is missing %q — it is being shown as source:\n%s", want, body)
		}
	}
	for _, unwanted := range []string{"##", "**", "|---", "| Case |", "`roll`"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("the report still carries the markdown it was written in (%q):\n%s", unwanted, body)
		}
	}

	// A narrower window re-renders the same source at its own width: the table still reads as a
	// table, and it fits the frame it is drawn into.
	m.width, m.height = 60, 30
	narrow := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	if strings.Contains(narrow, "##") || !strings.Contains(narrow, "│") {
		t.Fatalf("a resize must re-render the report, not keep the old layout:\n%s", narrow)
	}
	for _, line := range strings.Split(narrow, "\n") {
		if w := len([]rune(line)); w > m.width {
			t.Fatalf("a rendered line is %d cells wide in a %d-cell frame: %q", w, m.width, line)
		}
	}
}

// readEvals is the case file as the store wrote it: the assertions are a field of the disk, and a
// test that reads the model instead of the file would pass on an edit nobody saved.
func readEvals(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "dummy", "evals", "evals.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestFeedbackIsPossibleOnACaseThatPassed: a note is written against the case, not against a verdict.
// An assertion can pass and the work still be wrong, and that judgement is the one thing the judge
// cannot make — so the case that passed has to be selectable and its note has to open.
func TestFeedbackIsPossibleOnACaseThatPassed(t *testing.T) {
	m, root := qualityFixture(t)
	openView(m, "dummy", qDojo)
	press(m, "home") // dummy-1-one-die passes every assertion in both arms
	if c := m.quality.caseAt(); c == nil || len(c.Failed) != 0 {
		t.Fatalf("the case under the cursor failed: %+v", c)
	}
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	// The case's assertions are in the table with both arms' verdicts, and the line under them says
	// what that pattern means: both arms pass, so this case measures the model and not the skill.
	for _, want := range []string{"ASSERTION", "REVIEW", "no assertion passes with and fails without the skill"} {
		if !strings.Contains(body, want) {
			t.Fatalf("a passing case has no feedback block: missing %q\n%s", want, body)
		}
	}
	press(m, "f")
	if m.quality.feedback == nil {
		t.Fatal("f must open feedback on a case whose assertions all passed")
	}
	if m.quality.feedback.slug != "dummy-1-one-die" {
		t.Fatalf("the note is on %q, want the case under the cursor", m.quality.feedback.slug)
	}
	m.quality.feedback.text = "the face is right, the tone is wrong"
	press(m, "ctrl+s")
	if m.quality.feedback != nil {
		t.Fatal("the strip must close after the write")
	}
	var notes map[string]string
	b, err := os.ReadFile(filepath.Join(root, "dummy-workspace", "iteration-1", "feedback.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &notes); err != nil {
		t.Fatal(err)
	}
	if notes["dummy-1-one-die"] != "the face is right, the tone is wrong" {
		t.Fatalf("the note on disk = %q (all: %s)", notes["dummy-1-one-die"], string(b))
	}
}

// The pane is the case's whole grading list, not a list of failures: both arms' marks on every
// assertion, and the judge's words only under the one that failed. The guide's patterns are read off
// this — an assertion both arms pass measures the model, not the procedure — so the passing
// assertions are evidence too.
func TestThePaneListsEveryAssertionWithBothVerdicts(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qSetup)
	press(m, "home") // the declaration opens on the first case
	first := stripMarks(strings.Join(m.casePane(80, 34), "\n"))
	for _, want := range []string{"GRADING", "WITH SKILL", "WITHOUT SKILL", "PATTERN", "make it fail",
		"The reply reports one", "Nothing was written",
		"prompt", "expected_output", "files", "none — the run root starts empty"} {
		if !strings.Contains(first, want) {
			t.Fatalf("the pane is missing %q:\n%s", want, first)
		}
	}
	// The case with a failure: both marks, both assertions (the passing one is not dropped), and the
	// evidence only under the one that failed.
	press(m, "j")
	second := stripMarks(strings.Join(m.casePane(80, 30), "\n"))
	for _, want := range []string{"Three faces", "The total adds up", "adds value", "without · the reply"} {
		if !strings.Contains(second, want) {
			t.Fatalf("the pane is missing %q:\n%s", want, second)
		}
	}
	if n := strings.Count(second, "the reply"); n != 1 {
		t.Fatalf("evidence appears %d times, want once — under the assertion that failed:\n%s", n, second)
	}
	// The record keeps the file's shape: prompt, expected_output, files, assertions — in that order,
	// whatever else the pane says around them.
	assertRecordOrder(t, second)
}

// assertRecordOrder pins the pane to the case's own record: the four keys evals.json writes, in the
// order it writes them. A reflection that reorders the fields is a summary of the case, and the
// whole point of this pane is that it is the case.
func assertRecordOrder(t *testing.T, pane string) {
	t.Helper()
	last := -1
	for _, key := range []string{"\n  prompt", "\n  expected_output", "\n  files"} {
		i := strings.Index(pane, key)
		if i < 0 {
			t.Fatalf("the pane does not show %q:\n%s", strings.TrimSpace(key), pane)
		}
		if i < last {
			t.Fatalf("the pane reorders the record at %q:\n%s", strings.TrimSpace(key), pane)
		}
		last = i
	}
}

// The files a case stages are part of the record the pane reflects: they are what the run root is
// given before the agent starts, so a revision can be about the fixture as much as about the skill.
func TestThePaneListsTheFilesTheCaseStages(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qDojo)
	c := m.quality.caseAt()
	c.Files = []string{"raw/page.md", "MC-dice/pages/01.md"}
	pane := stripMarks(strings.Join(m.casePane(70, 40), "\n"))
	assertRecordOrder(t, pane)
	for _, want := range []string{"raw/page.md", "MC-dice/pages/01.md"} {
		if !strings.Contains(pane, want) {
			t.Fatalf("the pane is missing the staged file %q:\n%s", want, pane)
		}
	}
	if strings.Contains(pane, "the run root starts empty") {
		t.Fatalf("a case with files must not be described as staging nothing:\n%s", pane)
	}
}

// An assertion written after the arm was graded has no verdict, and the pane says so with a dash: a
// tick borrowed from the neighbour is the one thing a grading list must never show.
func TestAnAssertionNobodyJudgedIsADash(t *testing.T) {
	m, root := qualityFixture(t)
	editAssertions(t, root, "dummy-2-three-dice", `["Three faces", "The total adds up", "Every face is a d20"]`)
	// A fresh store, the way a second window would open it: the edit is on disk, not in the model.
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	m = newModel(s)
	m.width, m.height = 120, 34
	openView(m, "dummy", qSetup)
	press(m, "j")
	pane := stripMarks(strings.Join(m.casePane(80, 30), "\n"))
	if !strings.Contains(pane, "Every face is a d20") {
		t.Fatalf("the new assertion is not on the pane:\n%s", pane)
	}
	row := ""
	for _, line := range strings.Split(pane, "\n") {
		if strings.Contains(line, "Every face is a d20") {
			row = line
			break
		}
	}
	if strings.Count(row, "\u2014") < 2 {
		t.Fatalf("an ungraded assertion must carry two dashes, not a mark: %q", row)
	}
}

// e opens the list under both columns, as one field: the case table and the pane stay on screen, the
// caret is in the field, and a keystroke is a character — q is a letter here, not a command.
func TestTheAssertionListIsEditedUnderThePane(t *testing.T) {
	m, root := qualityFixture(t)
	was := readEvals(t, root)
	openView(m, "dummy", qSetup)
	press(m, "j")
	press(m, "e")
	if !m.assertEditActive() {
		t.Fatal("e must open the case's assertion list")
	}
	view := stripMarks(m.View())
	for _, want := range []string{"CASES", "ASSERTIONS", "dummy/evals/evals.json",
		"Three faces", "The total adds up"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the strip is missing %q:\n%s", want, view)
		}
	}
	if strings.Index(view, "CASES") > strings.Index(view, "ASSERTIONS") {
		t.Fatalf("the strip must be drawn under the columns, not over them:\n%s", view)
	}
	// The list owns the keystrokes: q and j are characters, and nothing is written while typing. The
	// caret opens at the end of the last assertion, so the letters land there.
	last := m.assertEdit.cur
	before := m.assertEdit.lines[last]
	press(m, "q")
	press(m, "j")
	if m.assertEdit == nil || !strings.Contains(m.assertEdit.lines[last], before+"qj") {
		t.Fatalf("the list did not take the keystrokes: %+v", m.assertEdit.lines)
	}
	if m.status != "" {
		t.Fatalf("typing ran a command: status = %q", m.status)
	}
	if readEvals(t, root) != was {
		t.Fatal("typing wrote the file; only ctrl+s does")
	}
	// Enter is the next assertion, and the second bullet is drawn by the pane and not typed into the
	// text: the assertion above keeps its own sentence, and the new one starts empty.
	rows := len(m.assertEdit.lines)
	above := m.assertEdit.lines[last]
	press(m, "enter")
	if m.assertEdit == nil || len(m.assertEdit.lines) != rows+1 {
		t.Fatalf("enter must add an assertion to the list: %+v", m.assertEdit.lines)
	}
	if m.assertEdit.lines[last] != above {
		t.Fatalf("enter edited the assertion above it: %q", m.assertEdit.lines[last])
	}
	if m.assertEdit.cur != last+1 || m.assertEdit.lines[last+1] != "" {
		t.Fatalf("enter did not open an empty assertion under the caret: cur=%d lines=%v",
			m.assertEdit.cur, m.assertEdit.lines)
	}
	if strings.Contains(strings.Join(m.assertEdit.lines, "\n"), glyphs.Bullet) {
		t.Fatalf("the bullet is part of the text: %v", m.assertEdit.lines)
	}
	if readEvals(t, root) != was {
		t.Fatal("enter wrote the file")
	}
	// ctrl+s writes the list: one assertion per line, the blank line dropped.
	m.assertEdit.lines = []string{"Three faces", "", "The total adds up", "Every face is a d20", ""}
	press(m, "ctrl+s")
	if m.assertEdit != nil {
		t.Fatal("the strip must close after the write")
	}
	file := readEvals(t, root)
	for _, want := range []string{"Three faces", "The total adds up", "Every face is a d20"} {
		if !strings.Contains(file, want) {
			t.Fatalf("the file is missing %q:\n%s", want, file)
		}
	}
	if !strings.Contains(m.status, "old list") || !strings.Contains(m.status, "g re-grades") {
		t.Fatalf("the save must say the verdicts on screen are from the old list: status = %q", m.status)
	}
	// The pane re-reads the file: the new assertion is there, with no verdict.
	pane := stripMarks(strings.Join(m.casePane(60, 30), "\n"))
	if !strings.Contains(pane, "Every face is a d20") {
		t.Fatalf("the pane did not re-read the list:\n%s", pane)
	}
	if m.mode != screenQuality {
		t.Fatalf("the save stays on the workbench: mode = %v", m.mode)
	}
}

// esc hands the keys back and writes nothing: leaving the editor is not a way to save.
func TestEscLeavesTheAssertionListAlone(t *testing.T) {
	m, root := qualityFixture(t)
	was := readEvals(t, root)
	openView(m, "dummy", qSetup)
	press(m, "j")
	press(m, "e")
	m.assertEdit.lines = []string{"something the file must not keep"}
	press(m, "esc")
	if m.assertEdit != nil {
		t.Fatal("esc must close the strip")
	}
	if m.mode != screenQuality {
		t.Fatalf("esc in the strip stays on the workbench: mode = %v", m.mode)
	}
	if readEvals(t, root) != was {
		t.Fatalf("a discarded edit wrote the file:\n%s", readEvals(t, root))
	}
}

// Clearing the list is not an empty array: the case is written as one with no assertions yet, the
// shape the format gives a case whose list is still unknown.
func TestClearingTheListWritesACaseWithNoAssertionKey(t *testing.T) {
	m, root := qualityFixture(t)
	openView(m, "dummy", qSetup)
	press(m, "j")
	press(m, "e")
	m.assertEdit.lines = []string{"", "  ", ""}
	press(m, "ctrl+s")
	var file struct {
		Evals []map[string]any `json:"evals"`
	}
	if err := json.Unmarshal([]byte(readEvals(t, root)), &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Evals) != 2 {
		t.Fatalf("evals = %d, want both cases kept", len(file.Evals))
	}
	if _, ok := file.Evals[1]["assertions"]; ok {
		t.Fatalf("an empty list must drop the key, not write []:\n%s", readEvals(t, root))
	}
	if _, ok := file.Evals[0]["assertions"]; !ok {
		t.Fatalf("the other case's list was lost:\n%s", readEvals(t, root))
	}
	if !strings.Contains(m.status, "no assertions") {
		t.Fatalf("status = %q", m.status)
	}
}

// A short window keeps the field and takes the rows: the strip is reserved before the columns, so
// the editor cannot be the thing that falls off the bottom. The same rule the set screens follow.
func TestAShortWindowKeepsTheFieldOnScreen(t *testing.T) {
	m, _ := qualityFixture(t)
	m.width, m.height = 100, 20
	openView(m, "dummy", qSetup)
	press(m, "j")
	press(m, "e")
	view := stripMarks(m.View())
	if !strings.Contains(view, "ASSERTIONS") {
		t.Fatalf("the strip is gone in a short window:\n%s", view)
	}
	if !strings.Contains(view, "CASES") {
		t.Fatalf("the case list is gone in a short window:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if w := len([]rune(line)); w > m.width {
			t.Fatalf("a line is %d cells in a %d-cell frame: %q", w, m.width, line)
		}
	}
}

// editAssertions writes a case's list straight to the file, the way a hand edit would: the pane's
// reading of an edited list is the thing under test, not the editor.
func editAssertions(t *testing.T, root, slug, list string) {
	t.Helper()
	path := filepath.Join(root, "dummy", "evals", "evals.json")
	var file struct {
		SkillName string `json:"skill_name"`
		Evals     []struct {
			ID         int      `json:"id"`
			Slug       string   `json:"slug"`
			Prompt     string   `json:"prompt"`
			Expected   string   `json:"expected_output"`
			Files      []string `json:"files"`
			Assertions []string `json:"assertions"`
		} `json:"evals"`
	}
	if err := json.Unmarshal([]byte(readEvals(t, root)), &file); err != nil {
		t.Fatal(err)
	}
	for i := range file.Evals {
		if file.Evals[i].Slug != slug {
			continue
		}
		if err := json.Unmarshal([]byte(list), &file.Evals[i].Assertions); err != nil {
			t.Fatal(err)
		}
	}
	out, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------- the dojo

// The dojo is the loop's middle, and it has to carry the four things a revision is written from: the
// assertion that failed, the judge's words for it, the end of the arm's execution transcript, and the
// human's note. It is one screen because the note is written ABOUT the other three.
func TestTheDojoReadsTheFailuresWithTheEvidence(t *testing.T) {
	m, root := qualityFixture(t)
	write(t, filepath.Join(root, "dummy-workspace", "iteration-1", "dummy-2-three-dice",
		"without_skill", "activity.log"),
		"read the die\nrolled three times\n  3 17 9\nwrote nothing\n")
	// The disk is asking for the iteration: its newest run has a failure to read, so the menu's
	// cursor is on that view rather than on the set.
	m.openQualitySkill("dummy")
	if m.mode != screenQualityMenu || m.quality.vcursor != qDojo {
		t.Fatalf("lane opened at mode %v row %d, want the menu on the iteration", m.mode, m.quality.vcursor)
	}
	press(m, "2")
	if m.quality.sec != qDojo {
		t.Fatalf("the iteration view did not open: section %d", m.quality.sec)
	}
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	// The right column is the case's own arithmetic — failed of declared — and the head is the same
	// sum over the iteration: one of four assertions failed, and the row that owns it says 1/2.
	for _, want := range []string{"CASES", "1/4 failed", "0/2", "1/2", "#2 · 1 of 2 failed",
		"dummy-2-three-dice", "ASSERTION", "WITH SKILL", "WITHOUT SKILL",
		"The total adds up", "without · the reply said so", "the total was one off", "REVIEW"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the dojo is missing %q:\n%s", want, body)
		}
	}
	// The assertion that passed is a row of the table like the one that failed — the case's whole list
	// is what a revision is judged against, and the row's own two verdicts are its record.
	if !strings.Contains(body, "Three faces") || !strings.Contains(body, "1 of 2 pass with the skill") {
		t.Fatalf("the table did not show the assertion that passed and what it means:\n%s", body)
	}
	// But it is not evidence: the other case's passing assertion is not this case's failure.
	if strings.Contains(body, "Nothing was written") {
		t.Fatalf("the pane listed another case's assertion as this case's record:\n%s", body)
	}
}

// enter opens the selected arm's whole transcript, and esc comes back to the dojo — not to the run
// screen it was opened from, which has not existed for hours. `v` is the outputs, the same key as in
// the initialization view.
func TestTheTranscriptOpensAndComesBack(t *testing.T) {
	m, root := qualityFixture(t)
	write(t, filepath.Join(root, "dummy-workspace", "iteration-1", "dummy-2-three-dice",
		"without_skill", "activity.log"), "first line\nsecond line\n")
	openView(m, "dummy", qDojo)
	press(m, "enter")
	if m.mode != screenDoc || m.doc == nil {
		t.Fatalf("mode = %v, want the transcript in the document reader", m.mode)
	}
	if !strings.Contains(stripMarks(strings.Join(m.doc.lines, "\n")), "second line") {
		t.Fatalf("the transcript is not the arm's log: %+v", m.doc.title)
	}
	press(m, "esc")
	if m.mode != screenQuality {
		t.Fatalf("esc from the transcript = %v, want the dojo", m.mode)
	}
}

// The iteration ladder walks the disk without leaving the dojo: [ and ] are one iteration either way,
// and the ends say they are the ends instead of wrapping into each other.
func TestTheDojoStepsThroughIterations(t *testing.T) {
	m, root := qualityFixture(t)
	two := filepath.Join(root, "dummy-workspace", "iteration-2")
	write(t, filepath.Join(two, "meta.json"), `{"iteration":2,"started":"2026-11-01T13:00:00+0100"}`)
	writeGrading(t, two, "dummy-1-one-die", "with_skill", []graded{{"The reply reports one face", true}, {"Nothing was written", true}})
	writeGrading(t, two, "dummy-1-one-die", "without_skill", []graded{{"The reply reports one face", true}, {"Nothing was written", true}})
	writeGrading(t, two, "dummy-2-three-dice", "with_skill", []graded{{"Three faces", true}, {"The total adds up", true}})
	writeGrading(t, two, "dummy-2-three-dice", "without_skill", []graded{{"Three faces", true}, {"The total adds up", true}})
	openView(m, "dummy", qDojo)
	if m.quality.iter != 2 {
		t.Fatalf("the lane opened on iteration %d, want the newest", m.quality.iter)
	}
	press(m, "]")
	if !strings.Contains(m.status, "newest") {
		t.Fatalf("] past the newest must say so: status = %q", m.status)
	}
	press(m, "[")
	if m.quality.iter != 1 {
		t.Fatalf("iteration = %d, want 1 — the previous one", m.quality.iter)
	}
	// Iteration 1 is the one with the failure, so the cursor has something under it.
	if len(m.quality.fails) != 1 || m.quality.fails[0].Case != "dummy-2-three-dice" {
		t.Fatalf("failures = %+v, want iteration 1's one failure", m.quality.fails)
	}
	press(m, "[")
	if !strings.Contains(m.status, "oldest") {
		t.Fatalf("[ past the oldest must say so: status = %q", m.status)
	}
}

// ---------------------------------------------------------------- the note

// n opens the note of the case under the failure cursor and ctrl+s writes it into the iteration's
// feedback.json — the file the pane has always read and nothing has ever written. The write keeps the
// other case's note, backs the old file up, and the pane re-reads it.
func TestFeedbackIsWrittenIntoTheIteration(t *testing.T) {
	m, root := qualityFixture(t)
	openView(m, "dummy", qDojo)
	press(m, "f")
	if m.quality.feedback == nil {
		t.Fatal("f must open the feedback of the case under the cursor")
	}
	view := stripMarks(m.View())
	for _, want := range []string{"FEEDBACK · dummy-2-three-dice", "feedback.json"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the note strip is missing %q:\n%s", want, view)
		}
	}
	// The field owns the keystrokes: q and j are characters here, not commands.
	press(m, "q")
	press(m, "j")
	if !strings.Contains(m.quality.feedback.text, "qj") && !strings.Contains(m.quality.feedback.text, "the total was one off") {
		t.Fatalf("the field did not take the keystrokes: %+v", m.quality.feedback)
	}
	m.quality.feedback.text = "the total drifts when the faces repeat\ncheck the second roll"
	press(m, "ctrl+s")
	if m.quality.feedback != nil {
		t.Fatal("the strip must close after the write")
	}
	var notes map[string]string
	b, err := os.ReadFile(filepath.Join(root, "dummy-workspace", "iteration-1", "feedback.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &notes); err != nil {
		t.Fatal(err)
	}
	if notes["dummy-2-three-dice"] != "the total drifts when the faces repeat\ncheck the second roll" {
		t.Fatalf("the note on disk = %q", notes["dummy-2-three-dice"])
	}
	if _, ok := notes["dummy-1-one-die"]; !ok {
		t.Fatalf("the other case's key was dropped: %q", string(b))
	}
	backups, _ := filepath.Glob(filepath.Join(root, ".evalctl", "backups", "*feedback.json"))
	if len(backups) == 0 {
		t.Fatal("the note write left no backup of the old file")
	}
	// The pane is the file's again.
	pane := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	if !strings.Contains(pane, "check the second roll") {
		t.Fatalf("the pane did not re-read the note:\n%s", pane)
	}
}

// esc in the note writes nothing: leaving the field is not a way to save.
func TestEscLeavesTheFeedbackAlone(t *testing.T) {
	m, root := qualityFixture(t)
	was, _ := os.ReadFile(filepath.Join(root, "dummy-workspace", "iteration-1", "feedback.json"))
	openView(m, "dummy", qDojo)
	press(m, "f")
	m.quality.feedback.text = "something the file must not keep"
	press(m, "esc")
	if m.quality.feedback != nil {
		t.Fatal("esc must close the note strip")
	}
	now, _ := os.ReadFile(filepath.Join(root, "dummy-workspace", "iteration-1", "feedback.json"))
	if string(now) != string(was) {
		t.Fatalf("a discarded note wrote the file:\n%s", now)
	}
}

// ---------------------------------------------------------------- the facts row and the table

// A view carries no rail of its three siblings: the menu is the only place they are listed and the
// only place a key switches between them, and the bar's path already ends in the view's own name. The
// row under it is the loop's numbers, which no view counts for itself.
func TestAViewCarriesTheLoopNumbersAndNoRail(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qDojo)
	row := stripMarks(m.contextLine(m.width))
	// The rail's own marks: the here-dot on the current view and the two names beside it. "iteration"
	// alone is not the tell — the facts row legitimately says "iteration #1".
	for _, rail := range []string{glyphs.Here, "testing quality", glyphs.Rule + glyphs.Rule} {
		if strings.Contains(row, rail) {
			t.Fatalf("the view still carries its siblings (%q) in the facts row: %q", rail, row)
		}
	}
	// What the row is for: the loop in one glance, including the iteration the view is reading.
	for _, want := range []string{"2 cases", "4 assertions", "iteration #1"} {
		if !strings.Contains(row, want) {
			t.Fatalf("the facts row is missing %q: %q", want, row)
		}
	}
	// This screen's keys cannot switch views, so there is nothing to advertise beyond esc.
	keys := qualityHints(m)
	if len(keys) == 0 || keys[len(keys)-1].key != "esc" {
		t.Fatalf("the iteration view does not end its band with esc: %v", keys)
	}
	// Nothing on this screen changes the view: the three keys, tab and shift+tab are all inert.
	for _, key := range []string{"1", "2", "3", "tab", "shift+tab"} {
		press(m, key)
		if m.mode != screenQuality || m.quality.sec != qDojo {
			t.Fatalf("%q left the iteration view: mode %v section %d", key, m.mode, m.quality.sec)
		}
	}
	// esc is the door, and the menu is where the numbers do their job.
	press(m, "esc")
	if m.mode != screenQualityMenu {
		t.Fatalf("esc = mode %v, want the menu", m.mode)
	}
	press(m, "2")
	if m.mode != screenQuality || m.quality.sec != qDojo {
		t.Fatalf("2 on the menu = mode %v section %d, want the iteration", m.mode, m.quality.sec)
	}
}

// The table's enter is "what happened in that iteration", and the answer is its evidence, not its
// own report: enter lands on the dojo at that iteration, r opens the report.
func TestTheTableOpensTheDojoAndTheReport(t *testing.T) {
	m, root := qualityFixture(t)
	write(t, filepath.Join(root, "dummy-workspace", "iteration-1", "report.md"), reportMD)
	openView(m, "dummy", qQuality)
	press(m, "enter")
	if m.quality.sec != qDojo || m.quality.iter != 1 {
		t.Fatalf("enter = section %d iteration %d, want the iteration view on iteration 1",
			m.quality.sec, m.quality.iter)
	}
	press(m, "esc")
	press(m, "3")
	press(m, "r")
	if m.mode != screenDoc || m.doc == nil {
		t.Fatalf("r did not open the report: mode %v", m.mode)
	}
}

// A finished run lands on the dojo: the next move is to read what failed, and the evidence is there.
func TestAFinishedGradeLandsOnTheDojo(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qQuality)
	m.run = &runState{kind: runGrade}
	m.run.live = newLiveRun("grading run", false)
	m.run.live.done = true
	m.leaveRun()
	if m.mode != screenQuality || m.quality.sec != qDojo {
		t.Fatalf("after a grade: mode %v section %d, want the dojo", m.mode, m.quality.sec)
	}
}

// The editor is whatever $EDITOR says, split the way a shell splits it; vi is the last resort, not
// the first guess.
func TestTheEditorComesFromTheEnvironment(t *testing.T) {
	t.Setenv("EDITOR", "nvim -u NONE")
	t.Setenv("VISUAL", "nano")
	argv, err := editorCommand("/tmp/x.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(argv, " ") != "nvim -u NONE /tmp/x.md" {
		t.Fatalf("argv = %v", argv)
	}
	t.Setenv("EDITOR", "")
	argv, err = editorCommand("/tmp/x.md")
	if err != nil {
		t.Fatal(err)
	}
	if argv[0] != "nano" {
		t.Fatalf("argv = %v, want $VISUAL when $EDITOR is empty", argv)
	}
}

// Every section is a window: at no size may a line be wider than the frame, and at no size may the
// panes be missing — a section that only works at 132 columns is a section half the terminals cannot
// use. With the three-view rail gone the name has to survive on the bar's path, so that is what is
// read at every size.
func TestEverySectionFitsTheWindow(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qDojo)
	// The view's name lives on the bar's path now that the rail is gone: it is that path's last
	// segment, drawn at every width by the bar's own rules.
	if segs := m.pathSegments(); segs[len(segs)-1] != m.quality.sectionName() {
		t.Fatalf("the path ends at %q, want the view's own name", segs[len(segs)-1])
	}
	for _, size := range [][2]int{{132, 40}, {100, 24}, {80, 20}, {60, 18}} {
		m.width, m.height = size[0], size[1]
		for _, sec := range []int{qSetup, qDojo, qQuality} {
			m.setQualitySection(sec)
			view := stripMarks(m.View())
			// The row under the bar is the loop's numbers — the one reading no view counts for itself.
			if !strings.Contains(view, "cases ·") {
				t.Fatalf("%dx%d %s: the facts row is gone:\n%s", size[0], size[1],
					m.quality.sectionName(), view)
			}
			for _, line := range strings.Split(view, "\n") {
				if w := len([]rune(line)); w > size[0] {
					t.Fatalf("%dx%d %s: a line is %d cells: %q", size[0], size[1],
						m.quality.sectionName(), w, line)
				}
			}
		}
	}
	// And the two fields: the list is reachable in the pane, the feedback under the columns.
	m.width, m.height = 90, 22
	m.setQualitySection(qSetup)
	press(m, "e")
	if !strings.Contains(stripMarks(m.View()), "ASSERTIONS") {
		t.Fatalf("the assertion field is not in the pane at 90x22:\n%s", stripMarks(m.View()))
	}
	press(m, "esc")
	m.setQualitySection(qDojo)
	press(m, "f")
	if !strings.Contains(stripMarks(m.View()), "FEEDBACK ·") {
		t.Fatalf("the feedback field is not under the dojo at 90x22:\n%s", stripMarks(m.View()))
	}
}
