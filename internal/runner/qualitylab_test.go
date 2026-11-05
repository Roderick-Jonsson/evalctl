package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/store"
)

// qualityStore is the smallest tree the output lane reads: one skill, and the set the test declares.
func qualityStore(t *testing.T, cases string) *store.Store {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "trigger", "trigger-evals.json"), `{"skill_name":"trigger","queries":[]}`)
	write(t, filepath.Join(root, "dummy", "SKILL.md"), "---\nname: dummy\ndescription: Roll dice.\n---\n")
	write(t, filepath.Join(root, "dummy", "evals", "evals.json"), cases)
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// qualityGraded writes one arm's grading.json, which is the only thing that makes a rate exist.
func qualityGraded(t *testing.T, root string, iteration int, slug, arm string, passed bool) {
	t.Helper()
	dir := filepath.Join(root, "dummy-workspace", fmt.Sprintf("iteration-%d", iteration), slug, arm)
	write(t, filepath.Join(dir, "grading.json"), fmt.Sprintf(`{
  "assertion_results": [{"text": "a", "passed": %t, "evidence": "saw it"}],
  "checks": {},
  "summary": {"passed": %s, "failed": %s, "total": 1, "pass_rate": %s}
}`, passed, countText(passed), countText(!passed), rateText(passed)))
	write(t, filepath.Join(dir, "response.md"), "the reply")
}

func countText(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func rateText(b bool) string {
	if b {
		return "1.0"
	}
	return "0.0"
}

// TestQualityLabGateIsClosedWithoutAnAssertion is the entry's first rule: a set whose cases carry no
// assertion cannot be measured, so the loop does not exist and the next move is the assertions — not
// the run it used to ask for.
func TestQualityLabGateIsClosedWithoutAnAssertion(t *testing.T) {
	s := qualityStore(t, `{"skill_name":"dummy","evals":[
	  {"id":1,"slug":"d1","prompt":"p","expected_output":"e","files":[],"assertions":[]},
	  {"id":2,"slug":"d2","prompt":"p","expected_output":"e","files":[],"assertions":["a"]}]}`)
	lab, err := New(s).QualityLab("dummy")
	if err != nil {
		t.Fatal(err)
	}
	if !lab.InitPending {
		t.Fatalf("init_pending = false with one case unasserted: %+v", lab)
	}
	if lab.Unasserted != 1 || lab.Cases != 2 {
		t.Fatalf("unasserted = %d of %d cases, want 1 of 2", lab.Unasserted, lab.Cases)
	}
	// Four steps, and the first two are what the set needs: the cases, then the first run — the run
	// that shows what the agent writes, which is what the assertions are written from. The grade is
	// not one of them until a run exists: the run grades itself.
	want := []struct {
		key    string
		ok     bool
		detail string
	}{
		{"cases", true, ""},
		{"run", false, "nothing run yet"},
		{"assertions", false, "1 of 2 cases carry none"},
	}
	if len(lab.Checks) != len(want) {
		t.Fatalf("checks = %+v, want three steps before the first run", lab.Checks)
	}
	for i, w := range want {
		c := lab.Checks[i]
		if c.Key != w.key || c.OK != w.ok {
			t.Fatalf("check %d = %+v, want key %q ok %v", i, c, w.key, w.ok)
		}
		if w.detail != "" && !strings.HasPrefix(c.Detail, w.detail) {
			t.Fatalf("check %q detail = %q, want it to start with %q", c.Key, c.Detail, w.detail)
		}
		if !c.OK && c.Fix == "" {
			t.Fatalf("check %q is owed and names no key", c.Key)
		}
	}
	if lab.Next.Key != "run" {
		t.Fatalf("next = %+v, want the run before the assertions", lab.Next)
	}
}

// TestQualityLabGateOpensOnAVerdict is the entry's last rule: the run alone does not open the loop.
// A directory of outputs nobody judged is not an iteration, so the next move is the grade.
func TestQualityLabGateOpensOnAVerdict(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "trigger", "trigger-evals.json"), `{"skill_name":"trigger","queries":[]}`)
	write(t, filepath.Join(root, "dummy", "SKILL.md"), "---\nname: dummy\ndescription: Roll dice.\n---\n")
	write(t, filepath.Join(root, "dummy", "evals", "evals.json"), `{"skill_name":"dummy","evals":[
	  {"id":1,"slug":"d1","prompt":"p","expected_output":"e","files":[],"assertions":["a"]}]}`)
	iter := filepath.Join(root, "dummy-workspace", "iteration-1")
	write(t, filepath.Join(iter, "meta.json"), `{"iteration":1,"started":"2026-11-01T10:00:00+0100"}`)
	write(t, filepath.Join(iter, "d1", "with_skill", "response.md"), "the reply")
	write(t, filepath.Join(iter, "d1", "without_skill", "response.md"), "the reply")
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	lab, err := New(s).QualityLab("dummy")
	if err != nil {
		t.Fatal(err)
	}
	if !lab.InitPending {
		t.Fatalf("a run with no verdicts opened the loop: %+v", lab)
	}
	if lab.Checks[3].Key != "grade" || lab.Checks[3].OK {
		t.Fatalf("grade check = %+v, want it owed on an ungraded run", lab.Checks[3])
	}
	if lab.Next.Key != "grade" {
		t.Fatalf("next = %+v, want the grade", lab.Next)
	}

	// The judge's verdicts are what open it.
	qualityGraded(t, root, 1, "d1", "with_skill", true)
	qualityGraded(t, root, 1, "d1", "without_skill", false)
	lab, err = New(s).QualityLab("dummy")
	if err != nil {
		t.Fatal(err)
	}
	if lab.InitPending {
		t.Fatalf("init_pending = true after a graded run: %+v", lab)
	}
	for _, c := range lab.Checks {
		if !c.OK {
			t.Fatalf("check %q still owed after the first grade: %+v", c.Key, c)
		}
	}
}

// TestQualityCasesListsWhatNoRunReached is the declaration's own requirement: setup has to list the
// cases of a set iteration 1 has not run, because that is where iteration 1's assertions are written.
func TestQualityCasesListsWhatNoRunReached(t *testing.T) {
	s := qualityStore(t, `{"skill_name":"dummy","evals":[
	  {"id":1,"slug":"d1","prompt":"one","expected_output":"e","files":[],"assertions":["a"]},
	  {"id":2,"slug":"d2","prompt":"two","expected_output":"e","files":[],"assertions":["b"]}]}`)
	cases, err := New(s).QualityCases("dummy", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 {
		t.Fatalf("cases = %d, want the two the set declares", len(cases))
	}
	for _, c := range cases {
		if c.Slug == "" || c.Prompt == "" {
			t.Fatalf("case %+v lost its declaration", c)
		}
		if c.With.Graded() || c.Without.Graded() {
			t.Fatalf("case %q reports a verdict nothing gave: %+v", c.Slug, c)
		}
		if len(c.Rows) != c.Assertions {
			t.Fatalf("case %q declares %d assertions and draws %d", c.Slug, c.Assertions, len(c.Rows))
		}
	}
}

// TestAFillJoinsTheOpenIteration: an iteration is the set measured once. A case run on its own joins
// the iteration being built — otherwise three cases run one at a time make three one-case iterations
// and the set is never measured — and a run that joins adds its cases to the iteration's own record
// instead of replacing it, or the next join reads the iteration as missing what it already holds.
func TestAFillJoinsTheOpenIteration(t *testing.T) {
	s := qualityStore(t, `{"skill_name":"dummy","evals":[
	  {"id":1,"slug":"d1","prompt":"p","expected_output":"e","files":[],"assertions":["a"]},
	  {"id":2,"slug":"d2","prompt":"p","expected_output":"e","files":[],"assertions":["a"]},
	  {"id":3,"slug":"d3","prompt":"p","expected_output":"e","files":[],"assertions":["a"]}]}`)
	r := New(s)
	iter := filepath.Join(s.Root, "dummy-workspace", "iteration-1")
	write(t, filepath.Join(iter, "meta.json"),
		`{"iteration":1,"skill":"dummy","started":"2026-11-01T12:00:00+0100","cases":["d1"]}`)
	write(t, filepath.Join(iter, "d1", "with_skill", "response.md"), "one face")

	plan, err := r.PlanIteration("dummy", true)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Join || plan.Iter != 1 {
		t.Fatalf("an iteration holding 1 of 3 cases is not joined: %+v", plan)
	}
	if strings.Join(plan.Open, ",") != "d2,d3" {
		t.Fatalf("the open cases are %v, want the two the iteration does not hold", plan.Open)
	}

	// Off by request: a run that does not fill mints the next number, which is what the shell wants.
	next, err := r.PlanIteration("dummy", false)
	if err != nil {
		t.Fatal(err)
	}
	if next.Iter != 2 || next.Join {
		t.Fatalf("without fill the plan is %+v, want iteration 2", next)
	}

	// A whole iteration is not reopened for the case that runs next.
	write(t, filepath.Join(iter, "meta.json"),
		`{"iteration":1,"skill":"dummy","started":"2026-11-01T12:00:00+0100","cases":["d1","d2","d3"]}`)
	if plan, err = r.PlanIteration("dummy", true); err != nil {
		t.Fatal(err)
	}
	if plan.Join || plan.Iter != 2 {
		t.Fatalf("a complete iteration is reopened: %+v", plan)
	}
}

// TestAFillTakesTheCaseTheSetIsMissing: filling is a continuation of one measurement, so the case the
// iteration does not hold is the case that closes it — the number is not advanced for it.
func TestAFillTakesTheCaseTheSetIsMissing(t *testing.T) {
	s := qualityStore(t, `{"skill_name":"dummy","evals":[
	  {"id":1,"slug":"d1","prompt":"p","expected_output":"e","files":[],"assertions":["a"]},
	  {"id":2,"slug":"d2","prompt":"p","expected_output":"e","files":[],"assertions":["a"]}]}`)
	iter := filepath.Join(s.Root, "dummy-workspace", "iteration-1")
	write(t, filepath.Join(iter, "meta.json"), `{"iteration":1,"skill":"dummy","cases":["d1"]}`)
	plan, err := New(s).PlanIteration("dummy", true)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Join || plan.Iter != 1 || strings.Join(plan.Open, ",") != "d2" {
		t.Fatalf("the case the iteration is missing did not close it: %+v", plan)
	}
}

// TestTwoCasesRunOneAtATimeMakeOneIteration is the loop as a person actually walks it: `r` on one
// case, then `r` on the next. Both runs belong to the same iteration — the set measured once — and the
// second run finds the first one's case still there instead of starting iteration 2 beside it.
func TestTwoCasesRunOneAtATimeMakeOneIteration(t *testing.T) {
	s := qualityStore(t, `{"skill_name":"dummy","evals":[
	  {"id":1,"slug":"d1","prompt":"Roll one.","expected_output":"a face","files":[],"assertions":["a"]},
	  {"id":2,"slug":"d2","prompt":"Roll two.","expected_output":"two faces","files":[],"assertions":["a"]}]}`)
	bin := filepath.Join(t.TempDir(), "fake-hermes")
	script := "#!/bin/sh\nprintf '{\"type\":\"result\",\"text\":\"ok\",\"duration_ms\":5,\"tokens\":{\"total\":10}}\\n'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMES_BIN", bin)
	write(t, filepath.Join(s.Root, "contract.md"), "# contract\n")

	r := New(s)
	for _, id := range []int{1, 2} {
		err := r.RunQuality(context.Background(), Options{Lane: store.LaneOutput, Skills: []string{"dummy"},
			CaseIDs: []int{id}, Arms: AllArms, Isolate: true, NoVerify: true, Fill: true,
			Budget: DefaultBudget, Log: ActivityFull})
		if err != nil {
			t.Fatalf("case %d: %v", id, err)
		}
	}

	workspace := filepath.Join(s.Root, "dummy-workspace")
	entries, _ := os.ReadDir(workspace)
	var iters []string
	for _, e := range entries {
		if e.IsDir() {
			iters = append(iters, e.Name())
		}
	}
	if len(iters) != 1 || iters[0] != "iteration-1" {
		t.Fatalf("two cases run one at a time made %v, want one iteration-1", iters)
	}
	for _, slug := range []string{"d1", "d2"} {
		if _, err := os.Stat(filepath.Join(workspace, "iteration-1", slug, ArmWith)); err != nil {
			t.Fatalf("%s is missing from iteration-1: %v", slug, err)
		}
	}
	meta := metaQuality{}
	if err := readJSONFile(filepath.Join(workspace, "iteration-1", "meta.json"), &meta); err != nil {
		t.Fatal(err)
	}
	if strings.Join(meta.Cases, ",") != "d1,d2" {
		t.Fatalf("the iteration's record says %v — the join rewrote it instead of adding to it", meta.Cases)
	}
	lab, err := r.QualityLab("dummy")
	if err != nil {
		t.Fatal(err)
	}
	if len(lab.Iterations) != 1 || !lab.Iterations[0].SetComplete {
		t.Fatalf("the lab does not read one whole iteration: %+v", lab.Iterations)
	}
}
