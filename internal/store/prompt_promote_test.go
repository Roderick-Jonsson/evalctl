package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// promptTree is the shared fixture: a trigger lane with four rows (alpha/beta, both labels) and
// one case per skill whose prompt a promotion can lift into that lane.
func promptTree(t *testing.T) *Store {
	t.Helper()
	s, err := Open(writeTestTree(t))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPromotePromptAddsATriggerRow(t *testing.T) {
	s := promptTree(t)
	res, err := s.PromotePrompt(PromotePromptRequest{Skill: "alpha", Index: 0, ShouldTrigger: true})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if res.ID != 5 {
		t.Errorf("id = %d, want the next free id in alpha's file, 5", res.ID)
	}
	if res.Skill != "alpha" || res.Split != "train" || !res.ShouldTrigger {
		t.Errorf("row = %s/%s/%v, want alpha/train/should-trigger", res.Skill, res.Split, res.ShouldTrigger)
	}
	if res.Query != "do the alpha thing" {
		t.Errorf("query = %q", res.Query)
	}
	if res.SourceSkill != "alpha" || res.SourceID != 1 || res.SourceSlug != "a-1" {
		t.Errorf("provenance = %+v", res)
	}
	if !strings.Contains(res.Why, "alpha case #1") {
		t.Errorf("why does not name the source case: %q", res.Why)
	}

	if len(s.QueriesOf("alpha")) != 5 {
		t.Fatalf("alpha queries = %d, want 5", len(s.QueriesOf("alpha")))
	}
	q := s.QueriesOf("alpha")[4]
	if q.ID != res.ID || q.Query != res.Query || q.Split != res.Split || !q.ShouldTrigger {
		t.Errorf("appended row = %+v", q)
	}
	prov, err := DecodePromotedQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	if prov == nil || prov.Skill != "alpha" || prov.Case != 1 || prov.Slug != "a-1" {
		t.Errorf("provenance = %+v", prov)
	}
}

// The row has to survive the file, not just memory: a promotion that evaporates on ctrl+s is
// worse than none, and the unknown key must come back intact.
func TestPromotePromptSurvivesTheRoundTrip(t *testing.T) {
	s := promptTree(t)
	if _, err := s.PromotePrompt(PromotePromptRequest{Skill: "alpha", Index: 0, ShouldTrigger: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTrigger("alpha"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.QueriesOf("alpha")) != 5 {
		t.Fatalf("alpha queries after reopen = %d, want 5", len(reopened.QueriesOf("alpha")))
	}
	q := reopened.QueriesOf("alpha")[4]
	prov, err := DecodePromotedQuery(q)
	if err != nil || prov == nil || prov.Slug != "a-1" {
		t.Fatalf("provenance did not survive: %+v, %v", prov, err)
	}
	if issues := reopened.Validate(); len(issues) != 0 {
		t.Errorf("a promoted row left the tree invalid: %v", issues)
	}
}

func TestPromotePromptRefusesADuplicateQuery(t *testing.T) {
	s := promptTree(t)
	if _, err := s.PromotePrompt(PromotePromptRequest{Skill: "alpha", Index: 0, ShouldTrigger: true}); err != nil {
		t.Fatal(err)
	}
	_, err := s.PromotePrompt(PromotePromptRequest{Skill: "alpha", Index: 0, ShouldTrigger: true})
	if err == nil || !strings.Contains(err.Error(), "already carries this prompt") {
		t.Fatalf("second promote = %v, want a duplicate refusal", err)
	}
	if len(s.QueriesOf("alpha")) != 5 {
		t.Errorf("alpha queries = %d, want the refused row not appended", len(s.QueriesOf("alpha")))
	}
}

// A case prompt is also a ready-made negative for a sibling: the user really asked for alpha, so
// beta must not load.
func TestPromotePromptAsASiblingNegative(t *testing.T) {
	s := promptTree(t)
	res, err := s.PromotePrompt(PromotePromptRequest{Skill: "alpha", Index: 0, Under: "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skill != "beta" || res.ShouldTrigger {
		t.Errorf("row = %s/%v, want beta/should-not-trigger", res.Skill, res.ShouldTrigger)
	}
	if !strings.Contains(res.Why, "must not select beta") {
		t.Errorf("why = %q", res.Why)
	}
	if res.SourceSkill != "alpha" {
		t.Errorf("provenance names %q as the source", res.SourceSkill)
	}
}

func TestPromotePromptOwnWhyAndSplit(t *testing.T) {
	s := promptTree(t)
	res, err := s.PromotePrompt(PromotePromptRequest{Skill: "beta", Index: 0, ShouldTrigger: true,
		Split: "validation", Why: "the beta case prompt, verbatim"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Split != "validation" || res.Why != "the beta case prompt, verbatim" {
		t.Errorf("split/why = %s / %q", res.Split, res.Why)
	}
}

func TestPromotePromptRefusals(t *testing.T) {
	s := promptTree(t)
	cases := []struct {
		name string
		req  PromotePromptRequest
		want string
	}{
		{"unknown skill", PromotePromptRequest{Skill: "gamma", Index: 0}, "unknown stage skill"},
		{"row out of range", PromotePromptRequest{Skill: "alpha", Index: 7}, "out of range"},
		{"unknown target", PromotePromptRequest{Skill: "alpha", Index: 0, Under: "gamma"}, "unknown stage skill"},
		{"bad split", PromotePromptRequest{Skill: "alpha", Index: 0, Split: "holdout"}, "split must be train or validation"},
	}
	for _, c := range cases {
		if _, err := s.PromotePrompt(c.req); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	if len(s.Queries()) != 8 {
		t.Errorf("a refused promotion appended a row: %d queries", len(s.Queries()))
	}
}

func TestPromotePromptNeedsATriggerLane(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "alpha", "evals", "evals.json"), `{
  "skill_name": "alpha",
  "evals": [{"id": 1, "slug": "a-1", "prompt": "do the alpha thing", "expected_output": "x", "files": []}]
}`)
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PromotePrompt(PromotePromptRequest{Skill: "alpha", Index: 0}); err == nil ||
		!strings.Contains(err.Error(), "trigger-evals.json is missing") {
		t.Fatalf("err = %v, want a refusal naming the missing file", err)
	}
}

func TestPromotePromptDryRunWritesNothing(t *testing.T) {
	s := promptTree(t)
	res, err := s.PromotePrompt(PromotePromptRequest{Skill: "alpha", Index: 0, ShouldTrigger: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != 5 || res.Query != "do the alpha thing" {
		t.Errorf("dry run did not describe the row: %+v", res)
	}
	if len(s.Queries()) != 8 {
		t.Errorf("dry run appended a row: %d queries", len(s.Queries()))
	}
}
