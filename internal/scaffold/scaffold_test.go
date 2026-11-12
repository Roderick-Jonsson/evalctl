package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/stage"
	"evalctl/internal/store"
)

// The starter is only a front door if it opens: a tree written by it must load and validate clean,
// with no follow-up edits, or the first thing a new project sees is an error it did not cause.
func TestTheStarterTreeValidatesClean(t *testing.T) {
	root := t.TempDir()
	if _, _, err := Write(root, Tree("meeting-notes", "notes"), false); err != nil {
		t.Fatal(err)
	}

	s, err := store.Open(root)
	if err != nil {
		t.Fatalf("the starter tree does not open: %v", err)
	}
	if names := s.SkillNames(); len(names) != 1 || names[0] != "meeting-notes" {
		t.Fatalf("skills = %v, want [meeting-notes]", names)
	}
	for _, i := range s.Validate() {
		if i.Severity == store.SeverityError {
			t.Errorf("%s: %s", i.String(), i.Msg)
		}
	}

	// The profile the tree names is what a run measures against, so the starter must not leave it out.
	if rules := stage.Load(root); rules.Profile != "notes" {
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

// TestKeptFilesAreNotOverwritten: a starter run over an existing project adds what is missing and
// keeps what is there, unless force says otherwise — the CLI's `--force` is that flag, and the TUI
// never sets it.
func TestKeptFilesAreNotOverwritten(t *testing.T) {
	root := t.TempDir()
	write, kept, err := Write(root, Tree("a", ""), false)
	if err != nil || len(kept) != 0 || len(write) != 5 {
		t.Fatalf("first write = %d written, %d kept, %v", len(write), len(kept), err)
	}
	path := filepath.Join(root, "a", "SKILL.md")
	if err := os.WriteFile(path, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write, kept, err = Write(root, Tree("a", ""), false)
	if err != nil || len(write) != 0 || len(kept) != 5 {
		t.Fatalf("second write = %d written, %d kept, %v", len(write), len(kept), err)
	}
	if b, _ := os.ReadFile(path); string(b) != "mine\n" {
		t.Fatal("the existing file was overwritten")
	}
	if _, _, err := Write(root, Tree("a", ""), true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) == "mine\n" {
		t.Fatal("--force did not overwrite")
	}
}

func TestSkillNameRules(t *testing.T) {
	for _, bad := range []string{"", "Meeting Notes", "Cooking_Notes", "evals", "notes-workspace"} {
		if err := CheckSkillName(bad); err == nil {
			t.Errorf("%q was accepted as a skill name", bad)
		}
	}
	for _, good := range []string{"notes", "meeting-notes", "d8"} {
		if err := CheckSkillName(good); err != nil {
			t.Errorf("%q was refused: %v", good, err)
		}
	}
	if got := SuggestName("/work/Meeting Notes"); got != "meeting-notes" {
		t.Fatalf("suggested name = %q", got)
	}
	if got := Summary("notes"); !strings.Contains(got, "notes/evals/trigger-evals.json") {
		t.Fatalf("summary = %q", got)
	}
}
