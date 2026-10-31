package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The description is the one field the tool rewrites inside a file it does not otherwise own, so the
// writer is tested for what it must NOT do: touch anything but the description's own lines.
func TestSetSkillDescriptionTouchesOnlyTheDescription(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "alpha", "SKILL.md")
	body := "---\nname: alpha\ndescription: Build the thing.\nallowed-tools: bash\n---\n\n# alpha\n\nText that mentions description: inside the body and must survive.\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "trigger"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "trigger", "trigger-evals.json"), []byte(`{"queries":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.SkillDescription("alpha"); got != "Build the thing." {
		t.Fatalf("read = %q", got)
	}

	long := strings.TrimSpace(strings.Repeat("Use this skill when the outline has to become slides even if the user never says slides. ", 3))
	if err := s.SetSkillDescription("alpha", long); err != nil {
		t.Fatalf("write: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(after)
	if !strings.Contains(text, "allowed-tools: bash") || !strings.HasSuffix(text, "Text that mentions description: inside the body and must survive.\n") {
		t.Fatalf("the rewrite reached past the description:\n%s", text)
	}
	if !strings.Contains(text, "description: >\n") {
		t.Fatalf("a long description should be written as a folded block:\n%s", text)
	}
	// The router reads the folded value; the parser has to see exactly what was asked for.
	if got := s.SkillDescription("alpha"); got != FoldText(long) {
		t.Fatalf("read back = %q, want %q", got, FoldText(long))
	}
	// A backup of the previous bytes exists, like every other write.
	backups, err := os.ReadDir(filepath.Join(root, ".evalctl", "backups"))
	if err != nil || len(backups) == 0 {
		t.Fatalf("no backup written (err %v)", err)
	}
}

func TestSetSkillDescriptionRefusesWhatTheFormatRefuses(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "alpha", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: alpha\n---\n\n# alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "trigger"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "trigger", "trigger-evals.json"), []byte(`{"queries":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSkillDescription("alpha", strings.Repeat("x", DescriptionLimit+1)); err == nil {
		t.Fatal("an over-limit description was accepted")
	}
	if err := s.SetSkillDescription("alpha", "anything"); err == nil || !strings.Contains(err.Error(), "no `description:`") {
		t.Fatalf("a frontmatter with no description should be refused, got %v", err)
	}
}

// TestDescriptionSpanReadsEveryScalarStyle: the four ways a description is written in the wild —
// plain, quoted, literal block and folded block — all read as the text the router sees.
func TestDescriptionSpanReadsEveryScalarStyle(t *testing.T) {
	cases := map[string]string{
		"---\nname: a\ndescription: plain words here\n---\n":                        "plain words here",
		"---\nname: a\ndescription: \"quoted words\"\n---\n":                        "quoted words",
		"---\nname: a\ndescription: |\n  literal one\n  literal two\n---\n":         "literal one literal two",
		"---\nname: a\ndescription: >\n  folded one\n  folded two\n\n  para\n---\n": "folded one folded two para",
	}
	for body, want := range cases {
		got, _, _, ok := DescriptionSpan(body)
		if !ok || got != want {
			t.Fatalf("%q → %q (ok=%v), want %q", body, got, ok, want)
		}
	}
}

// TestLedgerRoundTripsUnknownKeys: the ledger is a file a person may extend, and a manager that eats
// a key it does not know silently loses work — the same rule the eval files live by.
func TestLedgerRoundTripsUnknownKeys(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "trigger", "descriptions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "trigger"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "trigger", "trigger-evals.json"), []byte(`{"queries":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	body := `{
  "skill": "alpha",
  "note_for_humans": "keep me",
  "candidates": [{"id": 1, "text": "Use this skill when.", "origin": "file", "future_field": 7}]
}
`
	if err := os.WriteFile(s.DescriptionPath("alpha"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ledger, ok, err := s.LoadDescriptionLedger("alpha")
	if err != nil || !ok {
		t.Fatalf("load: %v (ok=%v)", err, ok)
	}
	if err := s.SaveDescriptionLedger(ledger); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(s.DescriptionPath("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"note_for_humans", "future_field"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("a round trip dropped %q:\n%s", want, raw)
		}
	}
}
