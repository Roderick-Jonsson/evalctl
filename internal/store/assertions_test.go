package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// assertions survive the round trip in the position the format puts them, and a case without any
// keeps its byte shape — the field is written only when it says something.
func TestAssertionsRoundTrip(t *testing.T) {
	root := t.TempDir()
	at := func(p string) string { return filepath.Join(root, p) }
	mustWrite(t, at("alpha/evals/trigger-evals.json"), `{"queries":[{"id":1,"skill":"alpha","should_trigger":true,"split":"train","query":"research it"}]}`)
	mustWrite(t, at("alpha/evals/evals.json"), `{
  "skill_name": "alpha",
  "evals": [
    {"id": 1, "slug": "a-1", "prompt": "p", "expected_output": "e", "files": [],
     "assertions": ["The output is valid JSON", "The chart shows March, July and November"]},
    {"id": 2, "slug": "a-2", "prompt": "p2", "expected_output": "e2", "files": []}
  ]
}`)
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	first := s.Skill("alpha").Output.Evals[0]
	if len(first.Assertions) != 2 || first.Assertions[1] != "The chart shows March, July and November" {
		t.Fatalf("assertions = %v", first.Assertions)
	}
	if len(s.Skill("alpha").Output.Evals[1].Assertions) != 0 {
		t.Fatal("a case without assertions must stay without them")
	}
	before, err := json.Marshal(s.Skill("alpha").Output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), `"assertions":["The output is valid JSON"`) {
		t.Fatalf("assertions are not written in the case:\n%s", before)
	}
	if strings.Count(string(before), `"assertions"`) != 1 {
		t.Fatalf("an empty assertion list must not be written:\n%s", before)
	}

	// Saving twice writes the same bytes: the assertions are stable in the file, not just in
	// memory, so a save cannot shuffle a grading list.
	if err := s.SaveSkill("alpha"); err != nil {
		t.Fatal(err)
	}
	firstSave := mustRead(t, at("alpha/evals/evals.json"))
	if err := s.SaveSkill("alpha"); err != nil {
		t.Fatal(err)
	}
	if second := mustRead(t, at("alpha/evals/evals.json")); second != firstSave {
		t.Fatalf("re-save changed the file:\n%s\n---\n%s", firstSave, second)
	}
}

// An empty assertion can never be graded, so it is an error rather than a silently stored blank.
func TestEmptyAssertionIsRejected(t *testing.T) {
	root := t.TempDir()
	at := func(p string) string { return filepath.Join(root, p) }
	mustWrite(t, at("trigger/trigger-evals.json"), `{"queries":[{"id":1,"skill":"alpha","should_trigger":true,"split":"train","query":"research it"}]}`)
	mustWrite(t, at("alpha/evals/evals.json"), `{
  "skill_name": "alpha",
  "evals": [{"id": 1, "slug": "a-1", "prompt": "p", "expected_output": "e", "files": [],
             "assertions": ["fine", "   "]}]
}`)
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, i := range s.Validate() {
		if i.Severity == SeverityError && strings.Contains(i.Msg, "assertion 2 is empty") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a blank assertion was not reported: %v", s.Validate())
	}
}

// The skill summary is the view both faces start from, so its counts have to be the ones a
// reader would count by hand: one line per skill, its own rows only.
func TestSummariesCountPerSkill(t *testing.T) {
	root := t.TempDir()
	at := func(p string) string { return filepath.Join(root, p) }
	mustWrite(t, at("alpha/evals/trigger-evals.json"), `{"queries":[
      {"id":1,"skill":"alpha","should_trigger":true,"split":"train","query":"a1"},
      {"id":2,"skill":"alpha","should_trigger":false,"split":"train","query":"a2"},
      {"id":3,"skill":"alpha","should_trigger":true,"split":"validation","query":"a3"},
      {"id":4,"skill":"alpha","should_trigger":false,"split":"validation","query":"a4"}]}`)
	mustWrite(t, at("beta/evals/trigger-evals.json"), `{"queries":[
      {"id":5,"skill":"beta","should_trigger":true,"split":"train","query":"b1"},
      {"id":6,"skill":"beta","should_trigger":false,"split":"train","query":"b2"},
      {"id":7,"skill":"beta","should_trigger":true,"split":"validation","query":"b3"},
      {"id":8,"skill":"beta","should_trigger":false,"split":"validation","query":"b4"}]}`)
	mustWrite(t, at("alpha/evals/evals.json"), `{"skill_name":"alpha","evals":[
      {"id":1,"slug":"a-1","prompt":"p","expected_output":"e","files":[],"assertions":["one","two"]},
      {"id":2,"slug":"a-2","prompt":"p","expected_output":"e","files":[]}]}`)
	mustWrite(t, at("alpha/evals/files/a-1/in.md"), "x\n")
	mustWrite(t, at("beta/evals/evals.json"), `{"skill_name":"beta","evals":[]}`)
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	sums := s.Summaries()
	if len(sums) != 2 {
		t.Fatalf("summaries = %+v, want two skills", sums)
	}
	a, b := sums[0], sums[1]
	if a.Skill != "alpha" || a.Cases != 2 || a.Graded != 1 || a.Assertions != 2 ||
		a.Fixtures != 1 || a.Files != 1 || a.Queries != 4 || a.Should != 2 || a.ShouldNot != 2 {
		t.Fatalf("alpha summary = %+v", a)
	}
	if b.Skill != "beta" || b.Cases != 0 || b.Queries != 4 || b.Health() != "1 warning" {
		t.Fatalf("beta summary = %+v", b)
	}
}

// A row that names another skill is a contradiction, not a bundle fact: the file a row sits in IS
// the skill under test, and a row that says otherwise is reported instead of honoured.
func TestRowNamingAnotherSkillIsAnError(t *testing.T) {
	root := t.TempDir()
	at := func(p string) string { return filepath.Join(root, p) }
	mustWrite(t, at("alpha/evals/trigger-evals.json"), `{"queries":[
      {"id":1,"skill":"alpha","should_trigger":true,"split":"train","query":"a1"},
      {"id":2,"skill":"gone","should_trigger":false,"split":"train","query":"g1"}]}`)
	mustWrite(t, at("alpha/evals/evals.json"), `{"skill_name":"alpha","evals":[]}`)
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, i := range s.Validate() {
		if i.Severity == SeverityError && strings.Contains(i.Msg, `row names skill "gone"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("a row naming a foreign skill was not reported: %v", s.Validate())
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
