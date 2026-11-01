package tui

import (
	"encoding/json"
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
		t.Fatalf("mode = %v, want the workbench", m.mode)
	}
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	for _, want := range []string{"CASES", "dummy-1-one-die", "dummy-2-three-dice", "100%", "50%"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the workbench is missing %q:\n%s", want, body)
		}
	}
	// The pane is the selected case's evidence: the failing arm and the human note.
	press(m, "j")
	pane := stripMarks(strings.Join(m.qualityPane(60, 30), "\n"))
	for _, want := range []string{"dummy-2-three-dice", "The total adds up", "the total was one off"} {
		if !strings.Contains(pane, want) {
			t.Fatalf("the pane is missing %q:\n%s", want, pane)
		}
	}
	// tab: the ranking, which marks the best complete iteration.
	press(m, "tab")
	if m.mode != screenQRank {
		t.Fatalf("tab did not open the ranking: mode %v", m.mode)
	}
	rank := stripMarks(strings.Join(m.qualityRankBody(m.bodyHeight()), "\n"))
	for _, want := range []string{"ITERATIONS", "#1", "75%", "best"} {
		if !strings.Contains(rank, want) {
			t.Fatalf("the ranking is missing %q:\n%s", want, rank)
		}
	}
	// esc walks back one level at a time.
	press(m, "esc")
	if m.mode != screenQuality {
		t.Fatalf("esc from the ranking = %v, want the loop", m.mode)
	}
	press(m, "esc")
	if m.mode != screenSkill {
		t.Fatalf("esc from the loop = %v, want the skills", m.mode)
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
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	if !strings.Contains(body, "no iteration on disk") {
		t.Fatalf("an empty loop should say it has nothing yet:\n%s", body)
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
