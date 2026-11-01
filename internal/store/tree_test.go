package store

import (
	"path/filepath"
	"testing"
)

// The stamp is what makes a long-running screen notice the disk; if it does not move when the tree
// moves, the symptom is a skill that only appears after a restart, and no test of the store would
// catch it because the store itself was always right.

func stampTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "trigger", "trigger-evals.json"), `{"queries":[]}`)
	mustWrite(t, filepath.Join(root, "slide-builder", "evals", "evals.json"), `{"skill_name":"slide-builder"}`)
	mustWrite(t, filepath.Join(root, "slide-builder", "SKILL.md"), "---\nname: slide-builder\n---\n")
	return root
}

func stampOf(t *testing.T, root string) string {
	t.Helper()
	stamp, err := TreeStamp(root)
	if err != nil {
		t.Fatal(err)
	}
	return stamp
}

func TestTreeStampHoldsWhileNothingMoves(t *testing.T) {
	root := stampTree(t)
	first := stampOf(t, root)
	if second := stampOf(t, root); second != first {
		t.Fatalf("an unchanged tree changed its stamp: %s then %s", first, second)
	}
}

func TestTreeStampMovesWhenASkillAppears(t *testing.T) {
	root := stampTree(t)
	before := stampOf(t, root)
	mustWrite(t, filepath.Join(root, "dummy", "evals", "evals.json"), `{"skill_name":"dummy"}`)
	if after := stampOf(t, root); after == before {
		t.Fatal("a new skill did not move the stamp — the chooser would never show it")
	}
}

func TestTreeStampMovesWhenARowIsRewritten(t *testing.T) {
	root := stampTree(t)
	before := stampOf(t, root)
	// The same file, the same directory, different bytes: a size that differs is enough for the
	// stat walk, and a same-size rewrite still moves mtime.
	mustWrite(t, filepath.Join(root, "trigger", "trigger-evals.json"), `{"queries":[{"id":1}]}`)
	if after := stampOf(t, root); after == before {
		t.Fatal("a rewritten trigger set did not move the stamp")
	}
}

func TestTreeStampSeesANewIterationButNotItsCasePayload(t *testing.T) {
	root := stampTree(t)
	// The case payload of a run is the part nobody's screen shows, and it is huge: one iteration
	// of the real slide-builder run is 27k files.
	mustWrite(t, filepath.Join(root, "slide-builder-workspace", "iteration-1", "case-a", "with_skill", "out.md"), "x")
	payload := stampOf(t, root)
	mustWrite(t, filepath.Join(root, "slide-builder-workspace", "iteration-1", "case-a", "with_skill", "out2.md"), "y")
	if stampOf(t, root) != payload {
		t.Fatal("a case file under a workspace moved the stamp — the walk is not pruned there")
	}
	mustWrite(t, filepath.Join(root, "slide-builder-workspace", "iteration-2", "meta.json"), `{"started":"x"}`)
	if stampOf(t, root) == payload {
		t.Fatal("a new iteration did not move the stamp — a finished run would never be read back")
	}
}

func TestTreeStampIgnoresTheBackups(t *testing.T) {
	root := stampTree(t)
	before := stampOf(t, root)
	// Every write the tool makes leaves a copy under .evalctl/backups. Counting those would make
	// the app re-read the whole tree after its own saves, for a file no screen shows.
	mustWrite(t, filepath.Join(root, ".evalctl", "backups", "trigger-evals.json.1"), "old")
	if after := stampOf(t, root); after != before {
		t.Fatal("a backup moved the stamp")
	}
}

func TestTreeStampNeedsAReadableTree(t *testing.T) {
	if _, err := TreeStamp(filepath.Join(t.TempDir(), "gone")); err == nil {
		t.Fatal("a missing root stamped cleanly")
	}
}
