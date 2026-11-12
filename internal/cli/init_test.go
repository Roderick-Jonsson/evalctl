package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInitRefusesInsideAProfile: the tree is a project folder. Scaffolding inside a profile would
// give the same files a second home with the profile's contract around them, which is the layout
// this runner deliberately does not read. The starter itself is tested in internal/scaffold, where
// both surfaces get it from.
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
	if _, err := os.Stat(filepath.Join(elsewhere, "notes-skill", "evals", "trigger-evals.json")); err != nil {
		t.Fatalf("the tree was not written: %v", err)
	}
}
