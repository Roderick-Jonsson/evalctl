package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/profile"
	"evalctl/internal/store"
)

// genericTree is a tree that is not this repo: one skill, a case and a trigger row, and no
// evalctl.json at all. It is the fixture for "evalctl knows nothing about the project".
func genericTree(t *testing.T) *store.Store {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "writer", "SKILL.md"), "---\nname: writer\ndescription: Write a short brief.\n---\n")
	write(t, filepath.Join(root, "writer", "evals", "evals.json"),
		`{"skill_name":"writer","evals":[{"id":1,"slug":"w-1","prompt":"write a brief","expected_output":"a brief","files":[],"assertions":["under 200 words"]}]}`)
	write(t, filepath.Join(root, "writer", "evals", "trigger-evals.json"),
		`{"runs":3,"threshold":0.5,"queries":[
		  {"id":1,"skill":"writer","query":"write me a brief","should_trigger":true,"split":"train","why":"core"},
		  {"id":2,"skill":"writer","query":"write me a brief","should_trigger":false,"split":"validation","why":"negative"}]}`)
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	hermeticProfiles(t)
	writeContract(t)
	return s
}

// TestANeutralTreeRunsWithoutProjectVocabulary: nothing in the run is named after this repo. The
// sandboxes are derived from the profile being measured, the contract is that profile's SOUL.md,
// and no containment prefix is invented.
func TestANeutralTreeRunsWithoutProjectVocabulary(t *testing.T) {
	s := genericTree(t)
	if s.Rules.Declared() {
		t.Fatalf("a tree with no evalctl.json declared rules: %+v", s.Rules)
	}

	var plan []string
	r := New(s)
	r.Emit = func(e Event) {
		if e.Kind == EventPlan {
			plan = append(plan, e.Message)
		}
	}
	if err := r.RunQuality(context.Background(), Options{Lane: LaneOutput, Skills: []string{"writer"},
		DryRun: true, Isolate: true}); err != nil {
		t.Fatal(err)
	}
	if len(plan) != 2 {
		t.Fatalf("want two arm plans, got %v", plan)
	}
	with := r.Profiles.Name(profile.ArmWith)
	for _, line := range plan {
		if !strings.Contains(line, "-p "+with) {
			t.Fatalf("the plan does not use the derived sandbox %q: %q", with, line)
		}
	}
	if with != "eval-"+testProfile+"-with" {
		t.Fatalf("sandbox name = %q, want it derived from %q", with, testProfile)
	}

	// Nothing is outside the run root when the tree declares no layout.
	box := CheckContainment(map[string]FileInfo{}, map[string]FileInfo{
		"brief.md":    {SHA256: "a"},
		"notes/x.txt": {SHA256: "b"},
	}, s.Rules)
	if !box.OK {
		t.Fatalf("a neutral tree flagged %v as outside", box.Outside)
	}
}

// TestTheTreeNamesItsProfile: a tree started from outside its profile still says which one it is
// measured in, so the contract and the sandboxes are not the default profile's.
func TestTheTreeNamesItsProfile(t *testing.T) {
	s := genericTree(t)
	t.Setenv("EVALCTL_PROFILE", "") // nothing pinned: the tree decides
	write(t, filepath.Join(s.Root, "evalctl.json"), `{"profile":"writer-profile"}`)
	reopened, err := store.Open(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	r := New(reopened)
	if r.Profiles.Real != "writer-profile" {
		t.Fatalf("real profile = %q", r.Profiles.Real)
	}
	if got := r.Profiles.Name(profile.ArmWith); got != "eval-writer-profile-with" {
		t.Fatalf("sandbox = %q", got)
	}
	if _, err := os.Stat(r.Profiles.Soul("writer-profile")); err == nil {
		t.Fatal("fixture leak: the writer profile should not exist")
	}
	if r.Profiles.Production != "" {
		t.Fatalf("an unpinned profile invented a production root: %q", r.Profiles.Production)
	}
}
