package store

import (
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/stage"
)

// writeSkill is the smallest valid stage skill: a SKILL.md and one case.
func writeSkill(t *testing.T, dir, name string) {
	t.Helper()
	mustWrite(t, filepath.Join(dir, "SKILL.md"), "---\nname: "+name+"\n---\n")
	mustWrite(t, filepath.Join(dir, "evals", "evals.json"),
		`{"skill_name":"`+name+`","evals":[{"id":1,"slug":"`+name+`-1","prompt":"do it","expected_output":"x","files":[]}]}`)
}

// TestSkillsMayBeGroupedOrBundled: a skill is a directory with a set, wherever it sits. The flat
// layout is the common one, but grouping them by stage and shipping them inside a plugin are both
// ordinary, and a runner that only looks one folder deep calls those trees empty.
func TestSkillsMayBeGroupedOrBundled(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "flat"), "flat")
	writeSkill(t, filepath.Join(root, "groups", "kitchen", "recipe-notes"), "recipe-notes")
	writeSkill(t, filepath.Join(root, "plugins", "notes-bundle", "skills", "pantry"), "pantry")

	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(s.SkillNames(), " ")
	want := "flat pantry recipe-notes"
	if got != want {
		t.Fatalf("skills = %q, want %q", got, want)
	}
	if dir := s.SkillDir("recipe-notes"); dir != filepath.Join(root, "groups", "kitchen", "recipe-notes") {
		t.Fatalf("grouped skill dir = %s", dir)
	}
	if dir := s.SkillDir("pantry"); dir != filepath.Join(root, "plugins", "notes-bundle", "skills", "pantry") {
		t.Fatalf("bundled skill dir = %s", dir)
	}
}

// TestTwoSkillsWithOneNameIsAnError: the folder name is the skill's identity — a workspace, a
// profile install and a trigger row all key on it — so a second one is refused with both paths,
// not silently shadowed.
func TestTwoSkillsWithOneNameIsAnError(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "groups", "a", "notes"), "notes")
	writeSkill(t, filepath.Join(root, "groups", "b", "notes"), "notes")
	mustWrite(t, filepath.Join(root, stage.FileName), "{}")

	_, err := Open(root)
	if err == nil || !strings.Contains(err.Error(), "two skills are named") {
		t.Fatalf("duplicate skill names must be refused, got %v", err)
	}
}

// TestDeclaredTreeBeatsTheGroupFolder: with skills grouped in folders, the nearest directory that
// holds one is the group folder — so a tree that declares itself (its own evalctl.json) has to win,
// or its conventions would be read from the wrong level, or not at all.
func TestDeclaredTreeBeatsTheGroupFolder(t *testing.T) {
	shared := t.TempDir()
	project := filepath.Join(shared, "somebodys-project")
	writeSkill(t, filepath.Join(project, "groups", "x", "thing"), "thing")
	start := filepath.Join(project, "groups", "x", "thing")

	// Undeclared: the walk stops at the group folder and never climbs into the shared parent.
	got, err := FindRoot(start)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(project, "groups", "x") {
		t.Fatalf("root = %s, want the group folder", got)
	}
	if _, err := Open(start); err == nil {
		// Not fatal by itself, but the shared parent must never be the answer.
		if s, _ := Open(start); s != nil && s.Root == shared {
			t.Fatalf("the shared parent %s became the tree", shared)
		}
	}

	// Declared: the project's own evalctl.json is the tree, from the same directory.
	mustWrite(t, filepath.Join(project, stage.FileName), "{}")
	got, err = FindRoot(start)
	if err != nil {
		t.Fatal(err)
	}
	if got != project {
		t.Fatalf("root = %s, want the declared tree %s", got, project)
	}
}
