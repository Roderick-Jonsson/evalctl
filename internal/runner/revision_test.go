package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/store"
)

// revisionStore is a skill with one failing case, one passing case, and a feedback on the failure:
// the three inputs the prompt is built from.
func revisionStore(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "trigger", "trigger-evals.json"), `{"skill_name":"trigger","queries":[]}`)
	write(t, filepath.Join(root, "dummy", "SKILL.md"),
		"---\nname: dummy\ndescription: Roll dice.\n---\n\nRoll them and stop there.\n")
	write(t, filepath.Join(root, "dummy", "evals", "evals.json"), `{"skill_name":"dummy","evals":[
	  {"id":1,"slug":"d-1","prompt":"Roll a die.","expected_output":"One face.","files":[],"assertions":["a"]},
	  {"id":2,"slug":"d-2","prompt":"Roll three.","expected_output":"Three faces and the sum.","files":[],"assertions":["a"]}]}`)
	iter := filepath.Join(root, "dummy-workspace", "iteration-1")
	write(t, filepath.Join(iter, "meta.json"), `{"iteration":1,"started":"2026-11-01T10:00:00+0100"}`)
	qualityGraded(t, root, 1, "d-1", "with_skill", true)
	qualityGraded(t, root, 1, "d-1", "without_skill", true)
	qualityGraded(t, root, 1, "d-2", "with_skill", false)
	qualityGraded(t, root, 1, "d-2", "without_skill", false)
	write(t, filepath.Join(iter, "feedback.json"), `{"d-1":"","d-2":"the sum is stated, never shown"}`)
	return root
}

// TestRevisionPromptCarriesTheThreeInputs is step 1's contract with the agent: the failures with the
// judge's evidence, the human feedback, and the skill's own text — plus the rules that keep a
// revision from passing the set instead of fixing the skill.
func TestRevisionPromptCarriesTheThreeInputs(t *testing.T) {
	s, err := store.Open(revisionStore(t))
	if err != nil {
		t.Fatal(err)
	}
	prompt, in, err := New(s).BuildRevisionPrompt("dummy", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Roll them and stop there",          // the skill as it stands
		"d-2",                               // the case that failed
		"FAILED (with-skill arm): a",        // the assertion and the arm it failed in
		"evidence: saw it",                  // the judge's words
		"the sum is stated, never shown",    // the operator's feedback
		"Cases that passed every assertion", // what must not be changed
		"Do not weaken, delete or rewrite an assertion",
		"=== WHY ===", "=== SKILL.md ===", "=== END ===",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the prompt is missing %q:\n%s", want, prompt)
		}
	}
	if in.Failed != 1 || in.Feedbacks != 1 || in.Passed != 1 {
		t.Fatalf("inputs = %+v, want one failed, one feedback, one passing", in)
	}
	// An empty feedback is a case that passed the review, not one to fix: it must not become a
	// complaint in the prompt.
	if strings.Contains(prompt, "### d-1\n") && strings.Contains(prompt, "### d-1\n\n") {
		t.Fatalf("the passing case's empty feedback reached the prompt:\n%s", prompt)
	}
}

// TestParseRevisionReply: the markers are the contract. Half a file is a failure, not a proposal.
func TestParseRevisionReply(t *testing.T) {
	why, body, err := parseRevisionReply("=== WHY ===\nshow the sum\n=== SKILL.md ===\n---\nname: k\n---\nbody\n=== END ===")
	if err != nil {
		t.Fatalf("a complete reply failed to parse: %v", err)
	}
	if why != "show the sum" || !strings.HasPrefix(body, "---\nname: k") || strings.HasSuffix(body, "===") {
		t.Fatalf("parsed why=%q body=%q", why, body)
	}
	// A fenced block is still an answer.
	if _, fenced, err := parseRevisionReply("=== SKILL.md ===\n```markdown\n# k\n```\n=== END ==="); err != nil || fenced != "# k" {
		t.Fatalf("fenced reply = %q, %v", fenced, err)
	}
	// Cut off before END: the model ran out of output, and this is not a proposal.
	if _, _, err := parseRevisionReply("=== SKILL.md ===\n---\nname: k\n---\n1. Read"); err == nil {
		t.Fatal("a truncated answer must not parse as a proposal")
	}
	if _, _, err := parseRevisionReply("no markers here"); err == nil {
		t.Fatal("an answer with no block must not parse")
	}
}

// TestApplyRevisionWritesAndBacksUp: applying is the one path that overwrites a skill body, so it
// backs the old text up and refuses when the file moved under it.
func TestApplyRevisionWritesAndBacksUp(t *testing.T) {
	root := revisionStore(t)
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	r := New(s)
	before, err := r.SkillText("dummy")
	if err != nil {
		t.Fatal(err)
	}
	rev := Revision{Skill: "dummy", Iteration: 1, N: 1, Baseline: before,
		Text: before + "\nShow the addition.\n",
		Path: filepath.Join(root, "dummy-workspace", "iteration-1", "revision-1.json")}
	if err := r.ApplyRevision("dummy", rev); err != nil {
		t.Fatal(err)
	}
	after, err := r.SkillText("dummy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after, "Show the addition.") {
		t.Fatalf("SKILL.md did not take the proposal:\n%s", after)
	}
	backups, _ := filepath.Glob(filepath.Join(root, ".evalctl", "backups", "*SKILL.md"))
	if len(backups) == 0 {
		t.Fatal("applying a revision left no backup")
	}
	// The same proposal again is now against a text that no longer exists.
	if err := r.ApplyRevision("dummy", rev); err == nil {
		t.Fatal("a proposal must not apply over a SKILL.md that changed since it was written")
	}
}

// TestReviseDryRunWritesThePromptOnly: the prompt is the artifact worth reading, and a dry run must
// spend nothing and write no proposal.
func TestReviseDryRunWritesThePromptOnly(t *testing.T) {
	root := revisionStore(t)
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	rev, err := New(s).Revise(context.Background(), "dummy", 1, ReviseOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rev.Prompt); err != nil {
		t.Fatalf("the dry run wrote no prompt: %v", err)
	}
	if _, err := os.Stat(rev.Path); err == nil {
		t.Fatal("the dry run wrote a proposal")
	}
	if !strings.Contains(rev.Prompt, "revision-1-prompt.md") {
		t.Fatalf("prompt path = %q", rev.Prompt)
	}
}
