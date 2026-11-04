package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/runner"
	"evalctl/internal/store"
)

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
	if m.mode != screenQuality {
		t.Fatalf("mode = %v, want the output lane", m.mode)
	}
	// The lane opens where the disk is asking: this iteration has a failure, so it opens on the dojo.
	if m.quality.sec != qDojo {
		t.Fatalf("section = %d, want the dojo — the newest iteration has a failure to read", m.quality.sec)
	}
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	for _, want := range []string{"FAILED", "without", "dummy-2-three-dice", "assertion",
		"The total adds up", "the reply said so", "the total was one off"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the dojo is missing %q:\n%s", want, body)
		}
	}
	// 1: the declaration. The list is the cases and one declaration mark, and nothing else — the
	// rates and the verdicts belong to the sections that read them.
	press(m, "1")
	if m.quality.sec != qSetup {
		t.Fatalf("1 did not open setup: section %d", m.quality.sec)
	}
	setup := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	for _, want := range []string{"CASES", "dummy-1-one-die", "dummy-2-three-dice"} {
		if !strings.Contains(setup, want) {
			t.Fatalf("setup is missing %q:\n%s", want, setup)
		}
	}
	// The iteration the sections read is on the rail, once, for all three of them.
	if !strings.Contains(stripMarks(m.View()), "iteration #1") {
		t.Fatalf("the rail does not say which iteration is on screen:\n%s", stripMarks(m.View()))
	}
	for _, unwanted := range []string{"WITHOUT", "FAILED", "100%", "50%"} {
		if strings.Contains(setup, unwanted) {
			t.Fatalf("the case list still carries %q:\n%s", unwanted, setup)
		}
	}
	// 3: the trend, with the best complete iteration tagged and the delta's basis named.
	press(m, "3")
	if m.quality.sec != qQuality {
		t.Fatalf("3 did not open the quality section: section %d", m.quality.sec)
	}
	rank := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	for _, want := range []string{"ITERATIONS", "#1", "75%", "best", "Δ w−w/o", "RAN"} {
		if !strings.Contains(rank, want) {
			t.Fatalf("the quality section is missing %q:\n%s", want, rank)
		}
	}
	// esc walks back one level at a time: a section, then the skills.
	press(m, "esc")
	if m.mode != screenQuality || m.quality.sec != qSetup {
		t.Fatalf("esc from quality = mode %v section %d, want setup", m.mode, m.quality.sec)
	}
	press(m, "esc")
	if m.mode != screenSkill {
		t.Fatalf("esc from setup = %v, want the skills", m.mode)
	}
}

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
	// Nothing declared means no loop, so the lane opens on the entry rather than on a section of a
	// loop that does not exist.
	if !m.quality.gate {
		t.Fatalf("the lane opened on section %d, want the initialization checklist", m.quality.sec)
	}
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	for _, want := range []string{"INITIALIZATION", "none declared", "cases", "assertions", "grade"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the checklist is missing %q:\n%s", want, body)
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
	m.openQualitySkill("dummy")
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
	m.openQualitySkill("dummy")
	press(m, "home") // dummy-1-one-die passes every assertion in both arms
	if c := m.quality.caseAt(); c == nil || len(c.Failed) != 0 {
		t.Fatalf("the case under the cursor failed: %+v", c)
	}
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	for _, want := range []string{"every assertion passed", "assertions", "feedback"} {
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
	m.openQualitySkill("dummy")
	press(m, "1")    // the record pane is setup's; the lane opens on the dojo
	press(m, "home") // the dojo opens on the case that failed, the declaration on the first
	first := stripMarks(strings.Join(m.casePane(60, 34), "\n"))
	for _, want := range []string{"assertions — 2", "✓ with", "✗ without",
		"The reply reports one face", "Nothing was written", "✓ ✓",
		"prompt", "expected_output", "files", "none — the run root starts empty"} {
		if !strings.Contains(first, want) {
			t.Fatalf("the pane is missing %q:\n%s", want, first)
		}
	}
	// The case with a failure: both marks, both assertions (the passing one is not dropped), and the
	// evidence only under the one that failed.
	press(m, "j")
	second := stripMarks(strings.Join(m.casePane(60, 30), "\n"))
	for _, want := range []string{"Three faces", "The total adds up", "✓ ✓", "✓ ✗", "without · the reply said so"} {
		if !strings.Contains(second, want) {
			t.Fatalf("the pane is missing %q:\n%s", want, second)
		}
	}
	if n := strings.Count(second, "the reply said so"); n != 1 {
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
	for _, key := range []string{"\n  prompt", "\n  expected_output", "\n  files", "\n  assertions"} {
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
	m.openQualitySkill("dummy")
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
	m.openQualitySkill("dummy")
	press(m, "1") // the case list is setup's; the lane opens on the dojo
	press(m, "j")
	pane := stripMarks(strings.Join(m.casePane(60, 30), "\n"))
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
	if !strings.Contains(row, "─ ─") {
		t.Fatalf("an ungraded assertion must carry two dashes, not a mark: %q", row)
	}
}

// e opens the list under both columns, as one field: the case table and the pane stay on screen, the
// caret is in the field, and a keystroke is a character — q is a letter here, not a command.
func TestTheAssertionListIsEditedUnderThePane(t *testing.T) {
	m, root := qualityFixture(t)
	was := readEvals(t, root)
	m.openQualitySkill("dummy")
	press(m, "1") // the case list is setup's; the lane opens on the dojo
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
	// The field owns the keystrokes: q and j are characters, and nothing is written while typing.
	press(m, "q")
	press(m, "j")
	if m.assertEdit == nil || !strings.Contains(m.assertEdit.text, "qj") {
		t.Fatalf("the list did not take the keystrokes: %+v", m.assertEdit)
	}
	if m.status != "" {
		t.Fatalf("typing ran a command: status = %q", m.status)
	}
	if readEvals(t, root) != was {
		t.Fatal("typing wrote the file; only ctrl+s does")
	}
	// Enter is a line in this field, not a submit: the list is lines, and a form that submitted on
	// the first enter would save half an edit.
	rows := strings.Count(m.assertEdit.text, "\n")
	press(m, "enter")
	if m.assertEdit == nil || strings.Count(m.assertEdit.text, "\n") != rows+1 {
		t.Fatalf("enter must add a line to the list, not submit it: %+v", m.assertEdit)
	}
	if readEvals(t, root) != was {
		t.Fatal("enter wrote the file")
	}
	// ctrl+s writes the list: one assertion per line, the blank line dropped.
	m.assertEdit.text = "Three faces\n\nThe total adds up\nEvery face is a d20\n"
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
	m.openQualitySkill("dummy")
	press(m, "1") // the case list is setup's; the lane opens on the dojo
	press(m, "j")
	press(m, "e")
	m.assertEdit.text = "something the file must not keep"
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
	m.openQualitySkill("dummy")
	press(m, "1") // the case list is setup's; the lane opens on the dojo
	press(m, "j")
	press(m, "e")
	m.assertEdit.text = "\n  \n"
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
	m.openQualitySkill("dummy")
	press(m, "1") // the case list is setup's; the lane opens on the dojo
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
	m.openQualitySkill("dummy")
	if m.quality.sec != qDojo {
		t.Fatalf("the lane opened on section %d, want the dojo", m.quality.sec)
	}
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	for _, want := range []string{"CASES", "1 failed", "dummy-2-three-dice", "assertion · without",
		"The total adds up", "the reply said so", "the total was one off", "wrote nothing", "feedback"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the dojo is missing %q:\n%s", want, body)
		}
	}
	// The passing assertions are counted, not listed: the rail is the failures.
	if strings.Contains(body, "Nothing was written") {
		t.Fatalf("the failures rail listed an assertion both arms passed:\n%s", body)
	}
}

// v opens the selected arm's whole transcript, and esc comes back to the dojo — not to the run screen
// it was opened from, which has not existed for hours.
func TestTheTranscriptOpensAndComesBack(t *testing.T) {
	m, root := qualityFixture(t)
	write(t, filepath.Join(root, "dummy-workspace", "iteration-1", "dummy-2-three-dice",
		"without_skill", "activity.log"), "first line\nsecond line\n")
	m.openQualitySkill("dummy")
	press(m, "v")
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
	m.openQualitySkill("dummy")
	if m.quality.iter != 2 {
		t.Fatalf("the lane opened on iteration %d, want the newest", m.quality.iter)
	}
	press(m, "2") // iteration 2 is clean, so the lane opened on the table; the ladder is the dojo's
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
	m.openQualitySkill("dummy")
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
	m.openQualitySkill("dummy")
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

// ---------------------------------------------------------------- the rail and the table

// The three sections are on the rail, and tab walks it: 1 2 3 in order, then round again. A section
// reachable only by a key nobody can see is the mistake this rail replaces.
func TestTheRailWalksTheThreeSections(t *testing.T) {
	m, _ := qualityFixture(t)
	m.openQualitySkill("dummy")
	if !strings.Contains(stripMarks(m.View()), "1 setup") {
		t.Fatalf("the rail is not on the head row:\n%s", stripMarks(m.View()))
	}
	want := []int{qQuality, qSetup, qDojo}
	for _, sec := range want {
		press(m, "tab")
		if m.quality.sec != sec {
			t.Fatalf("tab = section %d, want %d", m.quality.sec, sec)
		}
	}
	press(m, "shift+tab")
	if m.quality.sec != qSetup {
		t.Fatalf("shift+tab = section %d, want the section before the dojo", m.quality.sec)
	}
}

// The table's enter is "what happened in that iteration", and the answer is its evidence, not its
// own report: enter lands on the dojo at that iteration, r opens the report.
func TestTheTableOpensTheDojoAndTheReport(t *testing.T) {
	m, root := qualityFixture(t)
	write(t, filepath.Join(root, "dummy-workspace", "iteration-1", "report.md"), reportMD)
	m.openQualitySkill("dummy")
	press(m, "3")
	press(m, "enter")
	if m.quality.sec != qDojo || m.quality.iter != 1 {
		t.Fatalf("enter = section %d iteration %d, want the dojo on iteration 1",
			m.quality.sec, m.quality.iter)
	}
	press(m, "3")
	press(m, "r")
	if m.mode != screenDoc || m.doc == nil {
		t.Fatalf("r did not open the report: mode %v", m.mode)
	}
}

// A finished run lands on the dojo: the next move is to read what failed, and the evidence is there.
func TestAFinishedGradeLandsOnTheDojo(t *testing.T) {
	m, _ := qualityFixture(t)
	m.openQualitySkill("dummy")
	press(m, "3")
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
// rail or the panes be missing — a section that only works at 132 columns is a section half the
// terminals cannot use.
func TestEverySectionFitsTheWindow(t *testing.T) {
	m, _ := qualityFixture(t)
	m.openQualitySkill("dummy")
	for _, size := range [][2]int{{132, 40}, {100, 24}, {80, 20}, {60, 18}} {
		m.width, m.height = size[0], size[1]
		for _, sec := range []int{qSetup, qDojo, qQuality} {
			m.setQualitySection(sec)
			view := stripMarks(m.View())
			if !strings.Contains(view, "1 setup") {
				t.Fatalf("%dx%d %s: the rail is gone:\n%s", size[0], size[1],
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
