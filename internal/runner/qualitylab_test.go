package runner

import (
	"fmt"
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
	// Three steps, in the order they have to be done, and only the first is satisfied. The grade is not
	// one of them: the run grades itself, so there is no step to take before there is a run.
	want := []struct {
		key    string
		ok     bool
		detail string
	}{
		{"cases", true, ""},
		{"assertions", false, "1 of 2 cases carry none"},
		{"run", false, "nothing run yet"},
	}
	if len(lab.Checks) != len(want) {
		t.Fatalf("checks = %+v, want three steps", lab.Checks)
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
	if lab.Next.Key != "assertions" {
		t.Fatalf("next = %+v, want the assertions before the run", lab.Next)
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
