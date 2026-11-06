package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"evalctl/internal/llm"
	"evalctl/internal/store"
)

// A measured fixture: the file's description adopted as candidate #1, one train failure and one
// train pass, and a validation row that the proposal must never be written from.
func describeFixture(t *testing.T) (*Runner, int, int) {
	t.Helper()
	r, root := labFixture(t)
	if _, err := r.AdoptFileDescription("slide-builder"); err != nil {
		t.Fatal(err)
	}
	text := r.Store.SkillDescription("slide-builder")
	row := func(id int, split string, should, hit bool, query string) ScoreRow {
		passed, rate := hit, 0.0
		if hit {
			rate = 1
		}
		fired := 0
		if hit {
			fired = 3
		}
		return ScoreRow{ID: id, Skill: "slide-builder", Split: split, ShouldTrigger: should, Query: query,
			Why: "core use", Runs: 3, ValidRuns: 3, Triggers: fired, Rate: &rate, Passed: &passed}
	}
	writeIteration(t, root, "slide-builder", text, 1, []ScoreRow{
		row(1, SplitTrain, true, true, "build me slides from this outline"),
		row(2, SplitTrain, true, false, "turn this outline into a deck for thursday"),
		row(3, SplitValidation, true, false, "turn PO-RevB into a deck"),
	})
	return r, 1, 1
}

func TestThePromptCarriesTheTrainFailuresAndNothingHeldBack(t *testing.T) {
	r, cand, _ := describeFixture(t)
	prompt, in, err := r.BuildDescriptionPrompt("slide-builder", cand)
	if err != nil {
		t.Fatal(err)
	}
	if in.Fails != 1 || in.Passes != 1 {
		t.Fatalf("inputs = %+v, want 1 failure and 1 pass", in)
	}
	if !strings.Contains(prompt, "turn this outline into a deck for thursday") {
		t.Error("the failing query is not in the prompt")
	}
	if !strings.Contains(prompt, "build me slides from this outline") {
		t.Error("the passing query is not in the prompt: a rewrite that loses it trades one failure for another")
	}
	// The validation row is the judgement of a revision and must never shape one.
	if strings.Contains(prompt, "turn PO-RevB into a deck") {
		t.Error("a validation query leaked into the prompt")
	}
	for _, want := range []string{"=== WHY ===", "=== DESCRIPTION ===", "=== END ===", "too narrow"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	if !strings.Contains(prompt, text(r, cand)) {
		t.Error("the wording as it stands is not quoted in the prompt")
	}
}

func text(r *Runner, cand int) string {
	lab, _ := r.DescriptionLab("slide-builder")
	if c := lab.Candidate(cand); c != nil {
		return c.Text
	}
	return ""
}

func TestParsingTheAnswerIsStrictAboutItsMarkers(t *testing.T) {
	good := "=== WHY ===\nnamed the deck context\n=== DESCRIPTION ===\nUse this skill when an outline, a deck or a PO revision has to become slides.\n=== END ===\n"
	why, body, err := parseDescriptionReply(good)
	if err != nil {
		t.Fatal(err)
	}
	if why != "named the deck context" {
		t.Errorf("why = %q", why)
	}
	if body != "Use this skill when an outline, a deck or a PO revision has to become slides." {
		t.Errorf("body = %q", body)
	}
	// A fenced block and a pair of quotes around the paragraph are wrappers, not text.
	_, body, err = parseDescriptionReply("=== DESCRIPTION ===\n```text\n\"A quoted paragraph.\"\n```\n=== END ===")
	if err != nil {
		t.Fatal(err)
	}
	if body != "A quoted paragraph." {
		t.Errorf("wrappers survived: %q", body)
	}
	if _, _, err := parseDescriptionReply("=== DESCRIPTION ===\nhalf a sentence"); err == nil {
		t.Error("a reply cut off before its end marker must fail: the text is not known to be whole")
	}
	if _, _, err := parseDescriptionReply("=== DESCRIPTION ===\n\n=== END ==="); err == nil {
		t.Error("an empty block must fail")
	}
	if _, _, err := parseDescriptionReply("here is a description: whatever"); err == nil {
		t.Error("a reply without the markers must fail rather than be guessed at")
	}
}

func TestAProposalIsWrittenBesideItsIterationAndReadBack(t *testing.T) {
	r, cand, iter := describeFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"=== WHY ===\nanswered q2\n=== DESCRIPTION ===\nUse this skill when an outline, deck or PO revision has to become slides, not for research.\n=== END ==="}}]}`))
	}))
	defer srv.Close()

	cfg := llm.Config{BaseURL: srv.URL, Model: "test-model", APIKey: "k", KeyFrom: "the environment"}
	prop, err := r.ReviseDescription(context.Background(), "slide-builder", cand,
		DescribeOptions{Budget: 10 * time.Second, Config: &cfg})
	if err != nil {
		t.Fatal(err)
	}
	if prop.Model != "test-model" || prop.Why != "answered q2" {
		t.Errorf("proposal = %+v", prop)
	}
	if !strings.Contains(prop.Text, "not for research") || prop.Chars != len([]rune(prop.Text)) {
		t.Errorf("text or char count wrong: %+v", prop)
	}
	dir := filepath.Join(r.Store.Root, "trigger-workspace", "trigger-1")
	for _, name := range []string{descriptionProposalFile, descriptionPromptFile, descriptionReplyFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was not kept: %v", name, err)
		}
	}
	back, err := r.DescriptionProposalAt("slide-builder", iter)
	if err != nil || back == nil {
		t.Fatalf("reading the proposal back: %v", err)
	}
	if back.Text != prop.Text || back.Baseline == "" {
		t.Errorf("the proposal read back is not the one written: %+v", back)
	}
}

func TestAProposalOverTheCeilingIsRefusedAndSaidSo(t *testing.T) {
	r, cand, _ := describeFixture(t)
	long := strings.Repeat("word ", store.DescriptionLimit)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"=== DESCRIPTION ===\n` + long + `\n=== END ==="}}]}`))
	}))
	defer srv.Close()

	cfg := llm.Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}
	prop, err := r.ReviseDescription(context.Background(), "slide-builder", cand,
		DescribeOptions{Budget: 5 * time.Second, Config: &cfg})
	// Refused, asked again once, refused again — and then shown with the reason, because a person
	// deciding on it is better served by seeing the flaw than by seeing nothing.
	if err != nil {
		t.Fatalf("a flawed proposal is still a proposal: %v", err)
	}
	if len(prop.Issues) == 0 || prop.Calls != 2 {
		t.Fatalf("want the ceiling named and two calls, got %v / %d", prop.Issues, prop.Calls)
	}
	if prop.Chars <= proposalCeiling {
		t.Fatalf("the fixture is not over the ceiling: %d", prop.Chars)
	}
}

func TestAProposalNeedsAMeasuredWording(t *testing.T) {
	r, root := labFixture(t)
	if _, err := r.AdoptFileDescription("slide-builder"); err != nil {
		t.Fatal(err)
	}
	// Candidate #2 is written but never run: there are no failures to learn from.
	if _, err := r.SaveDescriptionCandidate("slide-builder", "A wording nobody has measured.", "draft", 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.BuildDescriptionPrompt("slide-builder", 2); err == nil ||
		!strings.Contains(err.Error(), "train measurement") {
		t.Fatalf("want a refusal about the missing measurement, got %v", err)
	}
	// A measured wording that passes everything is also nothing to revise from.
	writeIteration(t, root, "slide-builder", r.Store.SkillDescription("slide-builder"), 1, []ScoreRow{
		{ID: 1, Skill: "slide-builder", Split: SplitTrain, ShouldTrigger: true, Runs: 3, ValidRuns: 3,
			Triggers: 3, Passed: boolPtr(true), Rate: floatPtr(1)},
	})
	if _, _, err := r.BuildDescriptionPrompt("slide-builder", 1); err == nil ||
		!strings.Contains(err.Error(), "nothing to revise from") && !strings.Contains(err.Error(), "passes every train query") {
		t.Fatalf("want a refusal about nothing failing, got %v", err)
	}
}

func TestDryRunSpendsNothing(t *testing.T) {
	r, cand, _ := describeFixture(t)
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		called = true
	}))
	defer srv.Close()
	cfg := llm.Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}
	prop, err := r.ReviseDescription(context.Background(), "slide-builder", cand, DescribeOptions{DryRun: true, Config: &cfg})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("a dry run made a call")
	}
	if prop.Text != prop.Baseline {
		t.Error("a dry run proposes nothing, so its text is the baseline")
	}
	if prop.Prompt == "" {
		t.Error("a dry run still says where the prompt would come from")
	}
	if _, err := os.Stat(prop.Prompt); err != nil {
		t.Errorf("the prompt was not written: %v", err)
	}
}

func boolPtr(b bool) *bool        { return &b }
func floatPtr(f float64) *float64 { return &f }

// The checks that turn a plausible paragraph into a usable one. Each of these was a real answer from
// the first version of this call, and each one would have been measured as a candidate as it stood.

func TestTheChecksCatchWhatTheFirstVersionReturned(t *testing.T) {
	baseline := "Roll dice and report the faces that come up."
	fails := []ScoreRow{{ID: 61, Skill: "dummy", Query: "Write a small script that rolls a die and save it as roll.py."}}

	long := "Roll dice and report the faces that come up. Use it when an actual throw is wanted right now, at a tabletop game or between people deciding something by chance, and the result is read straight off the dice. It only throws dice and tells you what landed: it is not for scripting or programming activity, where a dice roll is code to be written and saved to a file, and not for odds or probability questions, where the chance of an outcome is calculated rather than thrown."
	if issues := proposeIssues(long, baseline, fails); len(issues) == 0 {
		t.Error("a 400+ character paragraph must be refused; the ceiling is what stopped the first version's rambling")
	}
	if issues := proposeIssues(baseline, baseline, fails); len(issues) == 0 {
		t.Error("an unchanged wording must be refused: nothing would be measured")
	}
	if issues := proposeIssues("Roll dice and answer queries about them.", baseline, fails); len(issues) == 0 {
		t.Error("harness vocabulary in a description must be refused")
	}
	// The failure the guide is most explicit about: words lifted out of a failing request.
	if issues := proposeIssues("Roll dice, not for writing a script that saves roll.py.", baseline, fails); len(issues) == 0 {
		t.Error("a word lifted from the failing request must be refused")
	}
	// A tight, scoped rewrite passes everything.
	good := "Roll dice and report the faces that come up, not for writing code that throws dice or for odds questions."
	if issues := proposeIssues(good, baseline, fails); len(issues) != 0 {
		t.Fatalf("a good answer was refused: %v", issues)
	}
}

// One repair, not two, and the second answer is kept even when it still has a flaw: a person deciding
// on a paragraph is better served by seeing the flaw than by seeing nothing.
func TestARefusedAnswerIsAskedAgainOnce(t *testing.T) {
	r, cand, _ := describeFixture(t)
	answers := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		answers++
		text := "=== WHY ===\ntoo long\n=== DESCRIPTION ===\n" + strings.Repeat("word ", 120) + "\n=== END ==="
		if answers > 1 {
			text = "=== WHY ===\nnarrowed\n=== DESCRIPTION ===\nUse this skill when an outline has to become slides, not for research.\n=== END ==="
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":` + jsonString(text) + `}}]}`))
	}))
	defer srv.Close()

	cfg := llm.Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}
	prop, err := r.ReviseDescription(context.Background(), "slide-builder", cand, DescribeOptions{Config: &cfg, Budget: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if answers != 2 || prop.Calls != 2 {
		t.Fatalf("want one repair (2 calls), got %d answers, prop.Calls = %d", answers, prop.Calls)
	}
	if len(prop.Issues) != 0 {
		t.Errorf("the repaired answer should be clean, issues = %v", prop.Issues)
	}
	if !strings.Contains(prop.Text, "not for research") {
		t.Errorf("the repair was not the text kept: %q", prop.Text)
	}
	// Both answers are on disk: a proposal that failed twice is a prompt bug, and the raw text is how
	// it gets read.
	dir := filepath.Join(r.Store.Root, "trigger-workspace", "trigger-1")
	if _, err := os.Stat(filepath.Join(dir, "description-reply.txt")); err != nil {
		t.Errorf("the first answer was not kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "description-reply-repair.txt")); err != nil {
		t.Errorf("the repair's answer was not kept: %v", err)
	}
}

// A proposal that still fails its checks after the repair is shown with the reasons, not thrown away.
func TestATwiceRefusedAnswerIsShownWithItsReasons(t *testing.T) {
	r, cand, _ := describeFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		text := "=== DESCRIPTION ===\n" + strings.Repeat("word ", 120) + "\n=== END ==="
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":` + jsonString(text) + `}}]}`))
	}))
	defer srv.Close()

	cfg := llm.Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}
	prop, err := r.ReviseDescription(context.Background(), "slide-builder", cand, DescribeOptions{Config: &cfg, Budget: 5 * time.Second})
	if err != nil {
		t.Fatalf("a flawed proposal is still a proposal: %v", err)
	}
	if len(prop.Issues) == 0 || prop.Calls != 2 {
		t.Fatalf("want the issues kept and two calls, got %v / %d", prop.Issues, prop.Calls)
	}
}

// The prompt has to carry what the writer needs to name the right context: the skill's own body, a
// budget, and the ban on lifting a request's words.
func TestThePromptCarriesTheSkillsOwnBody(t *testing.T) {
	r, cand, _ := describeFixture(t)
	prompt, _, err := r.BuildDescriptionPrompt("slide-builder", cand)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"What the skill actually does", "# slide-builder", "80 to 250 characters",
		"Name the KIND of request, never a request", "=== DESCRIPTION ==="} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	// The frontmatter is not quoted back: the description under test lives there.
	if strings.Contains(prompt, "name: slide-builder") {
		t.Error("the prompt quotes the skill's frontmatter back at it")
	}
}

// jsonString is a JSON string literal for the fake provider's body: the answer is a paragraph with
// newlines in it, and pasting it in unescaped makes the fixture the bug.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
