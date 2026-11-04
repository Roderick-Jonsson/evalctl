package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Step 1 of the guide's loop, done by an agent instead of by the operator's clipboard.
//
// The revision call is built like the judge's: one hermes one-shot, a prompt file written beside the
// iteration, the stream kept, and the answer parsed. It is deliberately the same shape, because the
// two are the same kind of thing — an agent reading an iteration's evidence and answering with text.
// What differs is the consequence: the judge's text lands in grading.json and changes no code, while
// the reviser's text is a proposal to overwrite SKILL.md. So nothing here writes the skill: `Revise`
// produces a Revision, and `ApplyRevision` is a separate call the operator triggers.
//
// What the reviser is given is exactly what the guide names — the failed assertions, the human
// feedback, and the current SKILL.md — plus the outputs those verdicts were about. What it is told
// not to do is as important: it cannot weaken an assertion, special-case a case, or touch the
// frontmatter, because all three make a set that passes and a skill that does not work.

// ReviseProfile is the sandbox the revision is written in: a profile of its own so the call cannot
// inherit the arms' skills (which would let it read the evals) or the judge's framing.
const ReviseProfile = "eval-vcc-revise"

// ReviseOptions is one revision call.
type ReviseOptions struct {
	Budget time.Duration // wall-clock per call
	Model  string
	DryRun bool
}

// Revision is one proposal to rewrite a skill, with the evidence it was written from.
type Revision struct {
	Skill     string `json:"skill"`
	Iteration int    `json:"iteration"`
	N         int    `json:"n"`
	Why       string `json:"why"`  // the reviser's own one-liner: what it changed and why
	Text      string `json:"text"` // the proposed SKILL.md, whole
	Path      string `json:"path"` // iteration-N/revision-N.json
	Prompt    string `json:"prompt_path"`
	Stream    string `json:"stream_path"`
	Started   string `json:"started"`
	Applied   bool   `json:"applied"`

	// Baseline is the SKILL.md the proposal was written against. It travels with the Revision so the
	// screen can diff without a second read, and so an apply can refuse when the file has moved on:
	// a proposal written against a text that no longer exists is a patch against the wrong skill.
	Baseline string `json:"baseline"`
}

// LatestQualityIteration is the newest iteration a skill has on disk, 0 when it has none: the
// evidence a revision with no number answers.
func (r *Runner) LatestQualityIteration(skill string) int {
	nums := iterationNumbers(r.WorkspaceDir(skill))
	if len(nums) == 0 {
		return 0
	}
	return nums[len(nums)-1]
}

// RevisionDir is where a skill's revisions are kept: beside the iteration they answer.
func (r *Runner) RevisionDir(skill string, iteration int) string {
	return r.IterDir(r.WorkspaceDir(skill), iteration)
}

// SkillText reads the skill's SKILL.md.
func (r *Runner) SkillText(skill string) (string, error) {
	sk := r.Store.Skill(skill)
	if sk == nil {
		return "", fmt.Errorf("unknown skill %q", skill)
	}
	b, err := os.ReadFile(filepath.Join(sk.Dir, "SKILL.md"))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// RevisionInputs is what the prompt is built from, kept so the screen can say what the reviser read.
type RevisionInputs struct {
	Failed    int
	Feedbacks int
	Passed    int
}

// BuildRevisionPrompt assembles the three things the guide names — the failed assertions with the
// judge's evidence, the human feedback, and SKILL.md — plus the outputs those verdicts were about and
// the rules a revision has to obey.
func (r *Runner) BuildRevisionPrompt(skill string, iteration int) (string, RevisionInputs, error) {
	sk := r.Store.Skill(skill)
	if sk == nil {
		return "", RevisionInputs{}, fmt.Errorf("unknown skill %q", skill)
	}
	body, err := r.SkillText(skill)
	if err != nil {
		return "", RevisionInputs{}, err
	}
	cases, err := r.QualityCases(skill, iteration)
	if err != nil {
		return "", RevisionInputs{}, err
	}
	if len(cases) == 0 {
		return "", RevisionInputs{}, fmt.Errorf("iteration %d has no cases on disk", iteration)
	}

	var in RevisionInputs
	var failing, passing, feedback strings.Builder
	for _, c := range cases {
		if len(c.Failed) == 0 {
			in.Passed++
			fmt.Fprintf(&passing, "- %s — every assertion passed in both arms\n", c.Slug)
			continue
		}
		in.Failed++
		fmt.Fprintf(&failing, "### %s\n\n", c.Slug)
		fmt.Fprintf(&failing, "The task: %s\n\n", strings.TrimSpace(c.Prompt))
		fmt.Fprintf(&failing, "What success looks like: %s\n\n", strings.TrimSpace(c.ExpectedOutput))
		for _, f := range c.Failed {
			fmt.Fprintf(&failing, "- FAILED (%s arm): %s\n  evidence: %s\n", armName(f.Arm), strings.TrimSpace(f.Text),
				orNone(strings.TrimSpace(f.Evidence)))
		}
		reply := c.With.Response
		if reply == "" {
			reply = c.Without.Response
		}
		if strings.TrimSpace(reply) != "" {
			fmt.Fprintf(&failing, "\nThe with-skill reply that was graded:\n---\n%s\n---\n\n", strings.TrimSpace(reply))
		}
	}
	for _, c := range cases {
		text := strings.TrimSpace(c.Feedback)
		if text == "" {
			continue // an empty feedback is a case that passed the review, not one to fix
		}
		in.Feedbacks++
		fmt.Fprintf(&feedback, "### %s\n%s\n\n", c.Slug, text)
	}

	var b strings.Builder
	b.WriteString("You are revising a skill from the evidence of one evaluated iteration.\n\n")
	b.WriteString("The operator ran the same test cases twice — once with the skill loaded and once without it — ")
	b.WriteString("and a judge read every output against the case's assertions. The failures, the operator's ")
	b.WriteString("feedback, and the skill's own text are below. Rewrite the skill so the next iteration does better.\n\n")

	b.WriteString("## The skill as it stands (SKILL.md)\n\n```markdown\n")
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n```\n\n")

	b.WriteString("## What failed, and what the judge saw\n\n")
	if in.Failed == 0 {
		b.WriteString("(nothing failed)\n\n")
	} else {
		b.WriteString(failing.String())
	}
	if passing.Len() > 0 {
		b.WriteString("## Cases that passed every assertion\n\n")
		b.WriteString(passing.String())
		b.WriteString("\nDo not change the skill to suit these — they already work.\n\n")
	}
	b.WriteString("## The operator's feedback\n\n")
	if in.Feedbacks == 0 {
		b.WriteString("(none — an empty feedback means the output looked fine to the operator)\n\n")
	} else {
		b.WriteString(feedback.String())
	}

	b.WriteString("## Rules\n\n")
	b.WriteString("- Fix the CAUSE in the skill's procedure or its pitfalls. Never special-case a test case, ")
	b.WriteString("never mention the eval, and never write a sentence whose only job is to satisfy one prompt.\n")
	b.WriteString("- Do not weaken, delete or rewrite an assertion. The assertions are the yardstick; the skill ")
	b.WriteString("is what is being measured. If an assertion looks wrong, say so in WHY and change nothing.\n")
	b.WriteString("- Keep the YAML frontmatter exactly as it is (`name` and `description` are the trigger lane's, ")
	b.WriteString("not yours). Keep the document's own shape: its headings, its numbered procedure, its voice.\n")
	b.WriteString("- Prefer the smallest change that would have produced a correct output for the cases that failed, ")
	b.WriteString("and keep what already passes passing.\n")
	b.WriteString("- If the evidence does not justify any change, return the file unchanged and say why in WHY.\n\n")
	b.WriteString("## Answer with exactly this, and nothing else\n\n")
	b.WriteString("=== WHY ===\n<one or two lines: what you changed, and which evidence it answers>\n")
	b.WriteString("=== SKILL.md ===\n<the whole new SKILL.md, frontmatter included>\n=== END ===\n")
	return b.String(), in, nil
}

// Revise runs one proposal. Nothing is written; the Revision is returned and kept on disk beside the
// iteration it answers, so the same proposal can be read again after a restart.
func (r *Runner) Revise(ctx context.Context, skill string, iteration int, o ReviseOptions) (Revision, error) {
	prompt, _, err := r.BuildRevisionPrompt(skill, iteration)
	if err != nil {
		return Revision{}, err
	}
	body, err := r.SkillText(skill)
	if err != nil {
		return Revision{}, err
	}
	dir := r.RevisionDir(skill, iteration)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return Revision{}, fmt.Errorf("iteration %d has no directory — R runs it first", iteration)
	}
	n := r.nextRevision(dir)
	rev := Revision{Skill: skill, Iteration: iteration, N: n, Baseline: body,
		Path:    filepath.Join(dir, fmt.Sprintf("revision-%d.json", n)),
		Prompt:  filepath.Join(dir, fmt.Sprintf("revision-%d-prompt.md", n)),
		Stream:  filepath.Join(dir, fmt.Sprintf("revision-%d-stream.jsonl", n)),
		Started: timestamp()}
	if err := os.WriteFile(rev.Prompt, []byte(prompt), 0o644); err != nil {
		return Revision{}, err
	}
	if o.DryRun {
		return rev, nil
	}
	cmdArgs := r.buildReviseCmd(rev.Prompt, r.Store.Skill(skill).Dir, o)
	callCtx, cancel := context.WithTimeout(ctx, o.Budget)
	defer cancel()
	out, err := os.Create(rev.Stream)
	if err != nil {
		return Revision{}, err
	}
	defer out.Close()
	errPath := filepath.Join(dir, fmt.Sprintf("revision-%d-stderr.txt", n))
	errFile, err := os.Create(errPath)
	if err != nil {
		return Revision{}, err
	}
	defer errFile.Close()
	cmd := exec.CommandContext(callCtx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = r.Store.Skill(skill).Dir
	cmd.Stdout, cmd.Stderr = out, errFile
	if err := cmd.Run(); err != nil {
		stderr, _ := os.ReadFile(errPath)
		return Revision{}, fmt.Errorf("the revision call failed: %v — %s", err, truncateText(oneLineBytes(stderr), 300))
	}
	result := FindResult(rev.Stream)
	if result == nil {
		return Revision{}, fmt.Errorf("the revision call produced no result frame — see %s", filepath.Base(rev.Stream))
	}
	why, text, err := parseRevisionReply(anyStringValue(result["text"]))
	if err != nil {
		return Revision{}, err
	}
	rev.Why, rev.Text = why, text
	if err := writeJSONFile(rev.Path, rev); err != nil {
		return Revision{}, err
	}
	return rev, nil
}

// buildReviseCmd is the revision call, printed verbatim by --dry-run. --in is the skill's own
// directory: the reviser may read the skill's scripts and references, and nothing of the workspace.
func (r *Runner) buildReviseCmd(promptFile, skillDir string, o ReviseOptions) []string {
	cmd := []string{r.Profiles.Bin, "-p", ReviseProfile, "chat"}
	if o.Model != "" {
		cmd = append(cmd, "-m", o.Model)
	}
	// --reasoning minimal: this is transcription, not deduction. A model that thinks through a whole
	// SKILL.md before writing it spends the output budget on the thinking and hands back half a file.
	return append(cmd, "--oneshot", "--query-file", promptFile, "--format", "stream-json",
		"--reasoning", "minimal", "--yolo", "--in", skillDir, "--max-turns", "6")
}

// nextRevision is max(existing)+1, so a proposal is never overwritten by the next one.
func (r *Runner) nextRevision(dir string) int {
	matches, _ := filepath.Glob(filepath.Join(dir, "revision-*.json"))
	n := 0
	for _, m := range matches {
		var probe struct {
			N int `json:"n"`
		}
		if readJSONFile(m, &probe) == nil && probe.N > n {
			n = probe.N
			continue
		}
		var num int
		if _, err := fmt.Sscanf(filepath.Base(m), "revision-%d.json", &num); err == nil && num > n {
			n = num
		}
	}
	return n + 1
}

// RevisionAt reads the newest proposal for an iteration, so a screen can reopen what was proposed
// after a restart without spending another call.
func (r *Runner) RevisionAt(skill string, iteration int) (*Revision, error) {
	dir := r.RevisionDir(skill, iteration)
	matches, _ := filepath.Glob(filepath.Join(dir, "revision-*.json"))
	if len(matches) == 0 {
		return nil, nil
	}
	newest, best := "", 0
	for _, m := range matches {
		var probe struct {
			N int `json:"n"`
		}
		if readJSONFile(m, &probe) == nil && probe.N >= best {
			best, newest = probe.N, m
		}
	}
	if newest == "" {
		return nil, nil
	}
	var rev Revision
	if err := readJSONFile(newest, &rev); err != nil {
		return nil, err
	}
	return &rev, nil
}

// ApplyRevision writes the proposal into SKILL.md, backing the old text up first, and marks the
// proposal applied. It refuses when the file has moved since the proposal was written: the diff the
// operator approved is a diff against the text that was on screen, and anything else is a blind write.
func (r *Runner) ApplyRevision(skill string, rev Revision) error {
	sk := r.Store.Skill(skill)
	if sk == nil {
		return fmt.Errorf("unknown skill %q", skill)
	}
	path := filepath.Join(sk.Dir, "SKILL.md")
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.TrimRight(string(current), "\n") != strings.TrimRight(rev.Baseline, "\n") {
		return fmt.Errorf("SKILL.md changed since this proposal was written — read it again before applying")
	}
	if err := r.Store.BackupFile(path); err != nil {
		return err
	}
	text := rev.Text
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return err
	}
	rev.Applied = true
	if rev.Path != "" {
		_ = writeJSONFile(rev.Path, rev)
	}
	return nil
}

// parseRevisionReply reads the proposal between its markers. A reply without them is an error rather
// than a guess: writing the wrong text into a skill is worse than spending another call.
func parseRevisionReply(text string) (why, body string, err error) {
	const whyMark, skillMark, endMark = "=== WHY ===", "=== SKILL.md ===", "=== END ==="
	s := strings.ReplaceAll(text, "\r\n", "\n")
	i := strings.Index(s, skillMark)
	if i == -1 {
		return "", "", fmt.Errorf("the revision came back without a SKILL.md block")
	}
	rest := s[i+len(skillMark):]
	j := strings.Index(rest, endMark)
	if j == -1 {
		// A reply that ends mid-sentence is a model that ran out of output budget, and half a skill is
		// worse than no proposal: the end marker is the only evidence that the file is whole.
		return "", "", fmt.Errorf("the revision was cut off before its === END === marker — the model ran out of output; try again, or revise SKILL.md by hand")
	}
	rest = rest[:j]
	body = strings.TrimSpace(rest)
	// A model that fences its block is still answering the question.
	if strings.HasPrefix(body, "```") {
		if nl := strings.Index(body, "\n"); nl != -1 {
			body = body[nl+1:]
		}
		body = strings.TrimSuffix(strings.TrimSpace(body), "```")
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "", "", fmt.Errorf("the revision's SKILL.md block was empty")
	}
	if k := strings.Index(s, whyMark); k != -1 && k < i {
		why = strings.TrimSpace(s[k+len(whyMark) : i])
	}
	return why, body, nil
}

// armName is the arm as the prompt names it: the reader knows the skill, the prompt does not need the
// harness's identifiers.
func armName(arm string) string {
	if arm == ArmWithout {
		return "without-skill"
	}
	return "with-skill"
}

func orNone(s string) string {
	if s == "" {
		return "(the judge wrote none)"
	}
	return s
}
