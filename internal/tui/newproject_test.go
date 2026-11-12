package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The first screen is the one screen that runs without a tree, so it is tested without one: the
// model's fields are the form's, and the write is the same call `evalctl init` makes.
func TestTheProjectScreenWritesAValidTree(t *testing.T) {
	root := t.TempDir()
	m := newProjectModel(root)
	m.skill = "meeting-notes"
	m.prof = ""

	if cmd := m.create(); cmd == nil {
		t.Fatalf("the write failed: %s", m.errMsg)
	}
	if m.created != root {
		t.Fatalf("created = %q, want %q", m.created, root)
	}
	for _, want := range []string{
		"evalctl.json",
		"meeting-notes/SKILL.md",
		"meeting-notes/evals/evals.json",
		"meeting-notes/evals/trigger-evals.json",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(want))); err != nil {
			t.Fatalf("%s was not written: %v", want, err)
		}
	}
}

// TestTheProjectScreenDeclaresWhatIsAlreadyHere is the case the screen exists for: a folder full of
// skills written for Hermes and never measured. It must add their eval sets and leave SKILL.md alone.
func TestTheProjectScreenDeclaresWhatIsAlreadyHere(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"notes", "recipes"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n---\nMINE\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	m := newProjectModel(root)
	if len(m.found) != 2 || len(m.choice) != 2 {
		t.Fatalf("detected %v, selected %v", m.found, m.choice)
	}
	m.prof = ""
	if cmd := m.create(); cmd == nil {
		t.Fatalf("the write failed: %s", m.errMsg)
	}
	for _, name := range []string{"notes", "recipes"} {
		if _, err := os.Stat(filepath.Join(root, name, "evals", "trigger-evals.json")); err != nil {
			t.Fatalf("%s got no trigger set: %v", name, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, "notes", "SKILL.md")); !strings.Contains(string(b), "MINE") {
		t.Fatal("the skill's own instructions were overwritten")
	}
}

// TestTheProjectScreenRefusesWhatInitRefuses: the same rules, answered before the write — a profile
// is not a folder for a tree, and a taken skill name is not a name.
func TestTheProjectScreenRefusesWhatInitRefuses(t *testing.T) {
	hermes := t.TempDir()
	profileDir := filepath.Join(hermes, "profiles", "notes")
	if err := os.MkdirAll(filepath.Join(profileDir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SOUL.md", "config.yaml"} {
		if err := os.WriteFile(filepath.Join(profileDir, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HERMES_ROOT", hermes)

	m := newProjectModel(profileDir)
	m.prof = ""
	if cmd := m.create(); cmd != nil {
		t.Fatal("the screen wrote a tree into a profile")
	}
	if !strings.Contains(m.errMsg, "Hermes profile") {
		t.Fatalf("error = %q", m.errMsg)
	}

	// A skill that is already there is the second refusal: the folder's skills are detected, so ask
	// to create a new one of a name that is taken.
	plain := t.TempDir()
	if err := os.MkdirAll(filepath.Join(plain, "notes-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plain, "notes-skill", "SKILL.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = newProjectModel(plain)
	m.found = nil
	m.choice = nil
	m.skill = "notes-skill"
	m.prof = ""
	if cmd := m.create(); cmd != nil {
		t.Fatal("the screen overwrote an existing skill")
	}
	if !strings.Contains(m.errMsg, "already exists") {
		t.Fatalf("error = %q", m.errMsg)
	}
}

// TestTheProjectScreenSuggestsTheFolderName: opening it in ~/work/meeting-notes should not make the
// operator type "meeting-notes" again — and the profile it suggests is the folder's own name.
func TestTheProjectScreenSuggestsTheFolderName(t *testing.T) {
	m := newProjectModel("/work/Meeting Notes")
	if m.skill != "meeting-notes" {
		t.Fatalf("suggested skill = %q", m.skill)
	}
	if m.prof != "meeting-notes" {
		t.Fatalf("suggested profile = %q", m.prof)
	}
}

// TestThePlanSaysWhatTheWriteWillDo: the line under the form is the promise, so it has to name the
// one case that creates a profile as well as writes files.
func TestThePlanSaysWhatTheWriteWillDo(t *testing.T) {
	m := newProjectModel(t.TempDir())
	m.prof = "brand-new-profile"
	if got := m.plan(); !strings.Contains(got, "creates profile brand-new-profile") {
		t.Fatalf("plan = %q", got)
	}
	m.prof = ""
	if got := m.plan(); !strings.Contains(got, "writes ") {
		t.Fatalf("plan without a profile = %q", got)
	}
}
