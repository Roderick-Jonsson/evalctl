package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAProfileIsNotATree: the tree is a project folder. A Hermes profile is where the rules and the
// credentials come from, and its skills/ is Hermes's install directory — a set kept in there is
// measured under no tree of its own, so it is refused with the two things that fix it.
func TestAProfileIsNotATree(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "SOUL.md"), "# contract\n")
	mustWrite(t, filepath.Join(root, "config.yaml"), "model:\n  default: m\n")
	mustWrite(t, filepath.Join(root, "skills", "alpha", "SKILL.md"), "---\nname: alpha\n---\n")
	mustWrite(t, filepath.Join(root, "skills", "alpha", "evals", "evals.json"),
		`{"skill_name":"alpha","evals":[{"id":1,"slug":"a-1","prompt":"do it","expected_output":"x","files":[]}]}`)

	_, err := Open(root)
	if err == nil {
		t.Fatal("a profile with sets under skills/ opened as a tree")
	}
	for _, want := range []string{"not an eval tree", "profiles attach"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

// TestAProfileNamesItsTree: the profile's own skills are installs and carry no sets, so the tree
// lives in a project folder. One line in .evalctl-tree is what lets `evalctl` started inside the
// profile find it instead of walking up to an empty folder.
func TestAProfileNamesItsTree(t *testing.T) {
	tree := writeTestTree(t)
	profile := t.TempDir()
	mustWrite(t, filepath.Join(profile, "skills", "alpha", "SKILL.md"), "---\nname: alpha\n---\n")
	mustWrite(t, filepath.Join(profile, TreeMarker), tree+"\n")

	// From anywhere inside the profile, not just its top.
	s, err := Open(filepath.Join(profile, "skills", "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Root != tree {
		t.Fatalf("root = %s, want the tree the profile names (%s)", s.Root, tree)
	}
	if names := s.SkillNames(); len(names) != 2 {
		t.Fatalf("skills = %v", names)
	}

	// A marker that names nothing is not a tree, and the error says so rather than opening it.
	mustWrite(t, filepath.Join(profile, TreeMarker), filepath.Join(t.TempDir(), "gone")+"\n")
	if _, err := Open(profile); err == nil {
		t.Fatal("a dangling tree marker opened an empty tree")
	}
}

// TestACheckoutWithASkillsFolderIsStillACheckout: a project folder that happens to keep a `skills/`
// subfolder of prose is still the tree — grouping is a layout, not a second kind of root.
func TestACheckoutWithASkillsFolderIsStillACheckout(t *testing.T) {
	root := writeTestTree(t)
	mustWrite(t, filepath.Join(root, "skills", "notes", "README.md"), "prose, no set\n")

	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if s.SkillsDir != root {
		t.Fatalf("skills dir = %s", s.SkillsDir)
	}
	if names := s.SkillNames(); len(names) != 2 {
		t.Fatalf("skills = %v", names)
	}
}

// TestFindRootWalksUpFromASubdirectory keeps the ordinary path working: run from inside a skill.
func TestFindRootWalksUpFromASubdirectory(t *testing.T) {
	root := writeTestTree(t)
	got, err := FindRoot(filepath.Join(root, "alpha", "evals", "files", "a-1"))
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Fatalf("root = %s", got)
	}
}

func TestOpenReportsAMissingTree(t *testing.T) {
	empty := t.TempDir()
	if _, err := os.Stat(filepath.Join(empty, "skills")); err == nil {
		t.Fatal("fixture leaked")
	}
	if _, err := Open(empty); err == nil {
		t.Fatal("an empty dir opened as a tree")
	}
}
