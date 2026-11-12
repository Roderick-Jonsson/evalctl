package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const alphaTriggerJSON = `{
  "note": "alpha's rows",
  "runs": 3,
  "threshold": 0.5,
  "queries": [
    {
      "id": 1,
      "skill": "alpha",
      "should_trigger": true,
      "split": "train",
      "why": "clear case",
      "query": "research the operator pattern for me and write the facts with links"
    },
    {
      "id": 2,
      "skill": "alpha",
      "should_trigger": false,
      "split": "train",
      "why": "near miss",
      "query": "just paste the three numbers here in chat, no files"
    },
    {
      "id": 3,
      "skill": "alpha",
      "should_trigger": true,
      "split": "validation",
      "why": "validation positive",
      "query": "turn my rough notes into a teaching order"
    },
    {
      "id": 4,
      "skill": "alpha",
      "should_trigger": false,
      "split": "validation",
      "why": "validation negative",
      "query": "render slide two as an image please"
    }
  ]
}
`

const betaTriggerJSON = `{
  "note": "beta's rows",
  "runs": 3,
  "threshold": 0.5,
  "queries": [
    {
      "id": 5,
      "skill": "beta",
      "should_trigger": true,
      "split": "train",
      "why": "clear case",
      "query": "make the deck's speaker notes say it out loud"
    },
    {
      "id": 6,
      "skill": "beta",
      "should_trigger": false,
      "split": "train",
      "why": "near miss",
      "query": "summarise this pdf into three bullets in chat"
    },
    {
      "id": 7,
      "skill": "beta",
      "should_trigger": true,
      "split": "validation",
      "why": "validation positive",
      "query": "cut a two-minute render from these slides"
    },
    {
      "id": 8,
      "skill": "beta",
      "should_trigger": false,
      "split": "validation",
      "why": "validation negative",
      "query": "find me sources with links for the reconciliation pattern"
    }
  ]
}
`

func writeTestTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "alpha", "evals", "trigger-evals.json"), alphaTriggerJSON)
	mustWrite(t, filepath.Join(root, "beta", "evals", "trigger-evals.json"), betaTriggerJSON)
	mustWrite(t, filepath.Join(root, "alpha", "evals", "evals.json"), `{
  "skill_name": "alpha",
  "evals": [
    {
      "id": 1,
      "slug": "a-1",
      "prompt": "do the alpha thing",
      "expected_output": "a thing",
      "files": ["in/one.md"]
    }
  ]
}
`)
	mustWrite(t, filepath.Join(root, "alpha", "evals", "files", "a-1", "in", "one.md"), "hello\n")
	mustWrite(t, filepath.Join(root, "beta", "evals", "evals.json"), `{
  "skill_name": "beta",
  "evals": [
    {
      "id": 1,
      "slug": "b-1",
      "prompt": "do the beta thing",
      "expected_output": "another thing",
      "files": []
    }
  ]
}
`)
	return root
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A manager that reformats a file it did not change is a manager nobody trusts: this pins the
// exact bytes, key order and all.
func TestSaveWithoutChangeIsByteIdentical(t *testing.T) {
	root := writeTestTree(t)
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha", "beta"} {
		if err := s.SaveTrigger(name); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]string{"alpha": alphaTriggerJSON, "beta": betaTriggerJSON}
	for name, expect := range want {
		got, err := os.ReadFile(filepath.Join(root, name, "evals", "trigger-evals.json"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != expect {
			t.Fatalf("%s: round trip changed the file:\n--- want ---\n%s\n--- got ---\n%s", name, expect, got)
		}
	}
}

// The agentskills.io format grows (assertions is the documented next field): unknown keys must
// survive an edit, not be silently dropped.
func TestUnknownKeysSurviveAnEdit(t *testing.T) {
	root := writeTestTree(t)
	path := filepath.Join(root, "alpha", "evals", "trigger-evals.json")
	withExtra := strings.Replace(alphaTriggerJSON, `"why": "clear case",`,
		`"why": "clear case",
      "notes": {"author": "rod", "reviewed": true},`, 1)
	mustWrite(t, path, withExtra)
	if err := os.WriteFile(filepath.Join(root, "beta", "evals", "evals.json"), []byte(`{
  "skill_name": "beta",
  "assertions_later": 1,
  "evals": [
    {"id": 1, "slug": "b-1", "prompt": "p", "expected_output": "e", "files": [], "tags": ["x"]}
  ]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	q := s.QueriesOf("alpha")[0]
	q.Query = "research the operator pattern and keep the links"
	if err := s.UpdateTrigger("alpha", 0, q); err != nil {
		t.Fatal(err)
	}
	sk := s.Skill("beta")
	sk.Output.Evals[0].Prompt = "p2"
	if _, err := s.UpdateOutput("beta", 0, sk.Output.Evals[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAll(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Queries []map[string]json.RawMessage `json:"queries"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	var notes map[string]any
	if err := json.Unmarshal(parsed.Queries[0]["notes"], &notes); err != nil {
		t.Fatalf("query-level extra key lost: %v (%s)", err, raw)
	}
	if notes["author"] != "rod" {
		t.Fatalf("extra key content changed: %v", notes)
	}
	rawBeta, err := os.ReadFile(filepath.Join(root, "beta", "evals", "evals.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawBeta), `"assertions_later": 1`) ||
		!strings.Contains(string(rawBeta), `"tags"`) {
		t.Fatalf("case-file extra keys lost:\n%s", rawBeta)
	}
}

func TestDeleteRowsKeepsFixtureUnlessAsked(t *testing.T) {
	root := writeTestTree(t)
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	refs := []RowRef{{Lane: LaneOutput, Skill: "alpha", Index: 0}}
	if _, err := s.DeleteRows(refs, false); err != nil {
		t.Fatal(err)
	}
	if !s.FixtureExists("alpha", "a-1") {
		t.Fatal("fixture dir was deleted without --with-fixture")
	}

	s2, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.DeleteRows([]RowRef{{Lane: LaneOutput, Skill: "alpha", Index: 0}}, true); err != nil {
		t.Fatal(err)
	}
	if s2.FixtureExists("alpha", "a-1") {
		t.Fatal("fixture dir survived --with-fixture")
	}
}

func TestSlugChangeMovesFixture(t *testing.T) {
	root := writeTestTree(t)
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Skill("alpha").Output.Evals[0]
	c.Slug = "a-renamed"
	if _, err := s.UpdateOutput("alpha", 0, c); err != nil {
		t.Fatal(err)
	}
	if s.FixtureExists("alpha", "a-1") {
		t.Fatal("old fixture dir still there")
	}
	if !isFile(filepath.Join(root, "alpha", "evals", "files", "a-renamed", "in", "one.md")) {
		t.Fatal("fixture did not move with the slug")
	}
}

func TestValidateCatchesRunPyFailures(t *testing.T) {
	root := writeTestTree(t)
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if issues := s.Validate(); len(issues) != 0 {
		t.Fatalf("clean tree reported %v", issues)
	}

	// Deleting the last negative of the train split is exactly what the runner refuses.
	if _, err := s.DeleteRows([]RowRef{{Lane: LaneTrigger, Skill: "alpha", Index: 1}}, false); err != nil {
		t.Fatal(err)
	}
	// A declared file that is not on disk cannot be staged.
	c := s.Skill("alpha").Output.Evals[0]
	c.Files = []string{"in/missing.md"}
	if _, err := s.UpdateOutput("alpha", 0, c); err != nil {
		t.Fatal(err)
	}

	issues := s.Validate()
	var splitErr, fileErr bool
	for _, i := range issues {
		if i.Severity == SeverityWarning && strings.Contains(i.Msg, "train split has no should-not-trigger") {
			// A one-sided split is measured, not refused — the warning is the whole rule.
			splitErr = true
			continue
		}
		if i.Severity != SeverityError {
			continue
		}
		if strings.Contains(i.Msg, "in/missing.md") {
			fileErr = true
		}
	}
	if !splitErr {
		t.Fatalf("missing split-balance error: %v", issues)
	}
	if !fileErr {
		t.Fatalf("missing fixture-file error: %v", issues)
	}
}

func TestDupeClustersFindNearTwins(t *testing.T) {
	root := writeTestTree(t)
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if groups := s.DupeGroups(LaneTrigger, DefaultDupeThreshold); len(groups) != 0 {
		t.Fatalf("distinct queries grouped: %v", groups)
	}
	twin := s.QueriesOf("alpha")[0]
	twin.ID = 9
	twin.Query = "research the operator pattern for me and write up the facts with links"
	if err := s.AddTrigger("alpha", twin); err != nil {
		t.Fatal(err)
	}
	groups := s.DupeGroups(LaneTrigger, DefaultDupeThreshold)
	if len(groups) != 1 || len(groups[0]) != 2 {
		t.Fatalf("expected one group of two, got %v", groups)
	}
}

func TestRenumberIsExplicitAndClosesGaps(t *testing.T) {
	root := writeTestTree(t)
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteRows([]RowRef{{Lane: LaneTrigger, Skill: "alpha", Index: 0}}, false); err != nil {
		t.Fatal(err)
	}
	if s.QueriesOf("alpha")[0].ID != 2 {
		t.Fatal("ids must not shift on delete")
	}
	s.RenumberTrigger("alpha")
	for i, q := range s.QueriesOf("alpha") {
		if q.ID != i+1 {
			t.Fatalf("renumber left id %d at position %d", q.ID, i)
		}
	}
}
