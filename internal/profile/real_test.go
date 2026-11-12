package profile

import (
	"os"
	"path/filepath"
	"testing"

	"evalctl/internal/stage"
)

// TestProfileAt: a directory belongs to a profile when it sits under profiles/, at any depth — the
// tool is usually started from ~/.hermes/profiles/<name>/skills/<skill>.
func TestProfileAt(t *testing.T) {
	hermes := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hermes, "profiles", "video-course-creator", "skills", "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		filepath.Join(hermes, "profiles", "video-course-creator"):                    "video-course-creator",
		filepath.Join(hermes, "profiles", "video-course-creator", "skills", "alpha"): "video-course-creator",
		filepath.Join(hermes, "profiles"):                                            "",
		filepath.Join(hermes, "profiles", "no-such-profile"):                         "",
		filepath.Join(hermes, "cache"):                                               "",
		filepath.Join(filepath.Dir(hermes), "elsewhere"):                             "",
	}
	for dir, want := range cases {
		if got := ProfileAt(dir, hermes); got != want {
			t.Fatalf("ProfileAt(%s) = %q, want %q", dir, got, want)
		}
	}
}

// TestRealProfilePrecedence: $EVALCTL_PROFILE, then the profile you are standing in, then the profile
// the root is, then the pipeline's own name. Compiling a profile name into the binary is the bug this
// order exists to avoid — `cd` into a profile must measure that profile.
func TestRealProfilePrecedence(t *testing.T) {
	hermes := t.TempDir()
	other := filepath.Join(hermes, "profiles", "other-profile", "skills", "alpha")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	t.Setenv("EVALCTL_PROFILE", "")
	if err := os.Chdir(other); err != nil {
		t.Fatal(err)
	}
	if got := RealProfile(hermes, t.TempDir(), stage.Rules{}); got != "other-profile" {
		t.Fatalf("standing in a profile read as %q", got)
	}

	t.Setenv("EVALCTL_PROFILE", "pinned-profile")
	if got := RealProfile(hermes, t.TempDir(), stage.Rules{}); got != "pinned-profile" {
		t.Fatalf("$EVALCTL_PROFILE did not win: %q", got)
	}

	// Not standing in one: the root decides when it is a profile, and the pipeline's own name
	// otherwise.
	t.Setenv("EVALCTL_PROFILE", "")
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if got := RealProfile(hermes, filepath.Dir(filepath.Dir(other)), stage.Rules{}); got != "other-profile" {
		t.Fatalf("the root's profile was ignored: %q", got)
	}
	if got := RealProfile(hermes, t.TempDir(), stage.Rules{}); got != DefaultReal {
		t.Fatalf("default = %q, want %q", got, DefaultReal)
	}
	// A tree started from outside its profile names it in evalctl.json.
	if got := RealProfile(hermes, t.TempDir(), stage.Rules{Profile: "declared-profile"}); got != "declared-profile" {
		t.Fatalf("the tree's declared profile was ignored: %q", got)
	}
}

// TestAttachedProfileIsMeasuredFromItsTree is the whole "work inside the profile" promise, end to
// end at the manager's level: the tree is the checkout, the contract is the profile's SOUL.md, and
// the arms get a byte copy of it.
func TestAttachedProfileIsMeasuredFromItsTree(t *testing.T) {
	tree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tree, "alpha", "evals"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(tree, "alpha", "evals", "evals.json"), "{}")
	writeFile(t, filepath.Join(tree, "alpha", "SKILL.md"), "---\nname: alpha\n---\n")

	m, _ := testManager(t)
	m.Root, m.SkillsRoot, m.Skills = tree, tree, []string{"alpha"}
	writeFile(t, filepath.Join(m.Home(m.Real), "SOUL.md"), "# the contract, edited here\n")

	if _, err := m.Render(m.Name(ArmWith), false); err != nil {
		t.Fatal(err)
	}
	arm, _ := os.ReadFile(m.Soul(m.Name(ArmWith)))
	if string(arm) != "# the contract, edited here\n" {
		t.Fatalf("the arm did not get the profile's SOUL.md verbatim: %q", arm)
	}
	// A tree that IS the profile's own skills dir must not sync a skill onto itself.
	m.Root = m.Home(m.Real)
	m.SkillsRoot = m.SkillsDir(m.Real)
	writeFile(t, filepath.Join(m.SkillsRoot, "alpha", "SKILL.md"), "---\nname: alpha\n---\n")
	line, err := m.SyncSkills(m.Real, []string{"alpha"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.SkillsRoot, "alpha", "SKILL.md")); err != nil {
		t.Fatalf("sync deleted the tree's own skill: %v (%s)", err, line)
	}
}

// TestSyncIntoTheRealProfileIsAdditive: the real profile carries Hermes's own bundle beside the stage
// skills, so syncing it must install what the tree declares and never evict what it did not.
func TestSyncIntoTheRealProfileIsAdditive(t *testing.T) {
	m, _ := testManager(t)
	writeFile(t, filepath.Join(m.Home(m.Real), "profile.yaml"), "description: real\n")
	writeFile(t, filepath.Join(m.SkillsDir(m.Real), "autonomous-ai-agents", "SKILL.md"), "---\nname: bundle\n---\n")
	if _, err := m.SyncSkills(m.Real, []string{"slide-builder"}, false); err != nil {
		t.Fatal(err)
	}
	names := m.InstalledSkills(m.Real)
	if !containsAll(names, []string{"slide-builder", "autonomous-ai-agents"}) {
		t.Fatalf("the real profile lost a skill: %v", names)
	}
	// A sandbox, by contrast, holds exactly the set.
	if _, err := m.SyncSkills(m.Name(ArmWith), []string{"slide-builder"}, false); err != nil {
		t.Fatal(err)
	}
	if got := m.InstalledSkills(m.Name(ArmWith)); len(got) != 1 {
		t.Fatalf("a sandbox kept more than its set: %v", got)
	}
}
