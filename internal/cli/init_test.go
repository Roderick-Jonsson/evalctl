package cli

import (
	"os"
	"path/filepath"
	"testing"

	"evalctl/internal/stage"
	"evalctl/internal/store"
)

// The starter is only a front door if it opens: a tree written by `init` must load and validate
// clean, with no follow-up edits, or the first thing a new project sees is an error message it did
// not cause.
func TestTheStarterTreeValidatesClean(t *testing.T) {
	root := t.TempDir()
	for _, f := range starterTree(root, "meeting-notes", "notes") {
		path := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(f.Body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	s, err := store.Open(root)
	if err != nil {
		t.Fatalf("the starter tree does not open: %v", err)
	}
	if names := s.SkillNames(); len(names) != 1 || names[0] != "meeting-notes" {
		t.Fatalf("skills = %v, want [meeting-notes]", names)
	}
	issues := s.Validate()
	for _, i := range issues {
		if i.Severity == store.SeverityError {
			t.Errorf("%s: %s", i.String(), i.Msg)
		}
	}

	// The profile the tree names is what a run measures against, so init must not leave it out.
	rules := stage.Load(root)
	if rules.Profile != "notes" {
		t.Fatalf("evalctl.json profile = %q, want notes", rules.Profile)
	}

	// Four rows, both labels in both loop halves: two of each kind is the smallest set that can
	// measure a decision without a warning.
	q := s.Skill("meeting-notes").Trigger.Queries
	if len(q) != 4 {
		t.Fatalf("trigger rows = %d, want 4", len(q))
	}
	labels := map[string]map[bool]bool{}
	for _, row := range q {
		if labels[row.Split] == nil {
			labels[row.Split] = map[bool]bool{}
		}
		labels[row.Split][row.ShouldTrigger] = true
	}
	for _, split := range []string{store.SplitTrain, store.SplitValidation} {
		if len(labels[split]) != 2 {
			t.Fatalf("%s labels = %v, want both", split, labels[split])
		}
	}
}

// TestInitRefusesInsideAProfile: the tree is a project folder. Scaffolding inside a profile would
// give the same files a second home with the profile's contract around them, which is the layout
// this runner deliberately does not read.
func TestInitRefusesInsideAProfile(t *testing.T) {
	hermes := t.TempDir()
	profileDir := filepath.Join(hermes, "profiles", "notes")
	if err := os.MkdirAll(filepath.Join(profileDir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(profileDir, "SOUL.md"), []byte("# contract\n"), 0o644)
	os.WriteFile(filepath.Join(profileDir, "config.yaml"), []byte("model:\n  default: m\n"), 0o644)
	t.Setenv("HERMES_ROOT", hermes)

	if code := runInit([]string{"--dir", profileDir, "--skill", "notes-skill"}); code != 2 {
		t.Fatalf("init inside a profile exited %d, want 2", code)
	}
	if _, err := os.Stat(filepath.Join(profileDir, "notes-skill")); err == nil {
		t.Fatal("init wrote a tree into the profile")
	}
	// The same folder shape outside a profile is an ordinary tree and is accepted.
	elsewhere := t.TempDir()
	if code := runInit([]string{"--dir", elsewhere, "--skill", "notes-skill"}); code != 0 {
		t.Fatalf("init in a plain folder exited %d", code)
	}
}
