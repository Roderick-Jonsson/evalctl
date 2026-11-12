package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/stage"
	"evalctl/internal/store"
)

// writeSkill is a skill as somebody wrote it for Hermes: instructions, no eval sets.
func writeSkillFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n---\nMY INSTRUCTIONS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAdoptDeclaresExistingSkills: the common case is a folder whose skills already exist. Adopt adds
// what is missing — both sets and the fixture note — and never touches the SKILL.md it measured.
func TestAdoptDeclaresExistingSkills(t *testing.T) {
	root := t.TempDir()
	writeSkillFile(t, filepath.Join(root, "notes"), "notes")
	writeSkillFile(t, filepath.Join(root, "groups", "kitchen", "recipes"), "recipes")

	found := Detect(root)
	if len(found) != 2 || found[0].Name != "notes" || found[1].Name != "recipes" {
		t.Fatalf("detect = %v", found)
	}

	report, err := Adopt(root, nil, "notes-lab")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Skills) != 2 {
		t.Fatalf("adopted %v", report.Skills)
	}
	for _, dir := range []string{"notes", "groups/kitchen/recipes"} {
		for _, f := range []string{"evals/evals.json", "evals/trigger-evals.json", "evals/files/README.md"} {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir), filepath.FromSlash(f))); err != nil {
				t.Fatalf("%s/%s: %v", dir, f, err)
			}
		}
		if b, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(dir), "SKILL.md")); !strings.Contains(string(b), "MY INSTRUCTIONS") {
			t.Fatalf("%s/SKILL.md was rewritten", dir)
		}
	}

	// The tree is now loadable, and it names the profile it was adopted for.
	s, err := store.Open(root)
	if err != nil {
		t.Fatalf("the adopted tree does not open: %v", err)
	}
	if names := s.SkillNames(); len(names) != 2 {
		t.Fatalf("skills = %v", names)
	}
	if got := stage.Load(root).Profile; got != "notes-lab" {
		t.Fatalf("profile = %q", got)
	}

	// A second adopt is a no-op: nothing is replaced or re-added.
	again, err := Adopt(root, nil, "notes-lab")
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Wrote) > 0 {
		t.Fatalf("the second adopt rewrote %v", again.Wrote)
	}
}

// TestAdoptKeepsAnExistingConventionsFile: a tree that already declares its own conventions keeps
// them; only an undeclared profile is filled in.
func TestAdoptKeepsAnExistingConventionsFile(t *testing.T) {
	root := t.TempDir()
	writeSkillFile(t, filepath.Join(root, "notes"), "notes")
	body := "{\n  \"skill_order\": [\"notes\"],\n  \"containment_prefixes\": [\"NT-\"]\n}\n"
	if err := os.WriteFile(filepath.Join(root, stage.FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Adopt(root, []string{"notes"}, "lab"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, stage.FileName))
	if !strings.Contains(string(b), "containment_prefixes") {
		t.Fatalf("the conventions were replaced: %s", b)
	}
	if !strings.Contains(string(b), "\"profile\": \"lab\"") {
		t.Fatalf("the profile was not filled in: %s", b)
	}
}

// TestAdoptRefusesASkillThatIsNotThere: adopt does not invent skills. A name the folder does not hold
// is answered with the names it does.
func TestAdoptRefusesASkillThatIsNotThere(t *testing.T) {
	root := t.TempDir()
	writeSkillFile(t, filepath.Join(root, "notes"), "notes")
	if _, err := Adopt(root, []string{"ghost"}, ""); err == nil ||
		!strings.Contains(err.Error(), "no such skill in this folder (found: notes)") {
		t.Fatalf("adopt of a missing skill = %v", err)
	}
	// An empty folder says what to do instead.
	if _, err := Adopt(t.TempDir(), nil, ""); err == nil {
		t.Fatal("adopt of an empty folder succeeded")
	}
}

// TestAdoptingSeveralSkillsValidatesClean is the bug the first live adopt found: every starter case
// was slugged "example", so adopting two skills in one folder produced two cases with one slug — a
// tree that could not be measured until somebody renamed a case by hand.
func TestAdoptingSeveralSkillsValidatesClean(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"notes", "recipes", "plans"} {
		writeSkillFile(t, filepath.Join(root, name), name)
	}
	if _, err := Adopt(root, nil, ""); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range s.Validate() {
		if i.Severity == store.SeverityError {
			t.Errorf("%s: %s", i.String(), i.Msg)
		}
	}
}
