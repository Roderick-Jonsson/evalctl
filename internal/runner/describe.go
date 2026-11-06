package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"evalctl/internal/llm"
	"evalctl/internal/store"
)

// Step 3 of the guide's loop — "revise the description" — with a model doing the writing.
//
// The description lane's revision was the operator's own clipboard work: the failure pane said which
// way the wording was wrong, and the sentence that fixed it was typed by hand. This is the same move
// done by a caller that reads exactly what the guide allows a revision to learn from — the train
// failures and their verdicts, never the validation set — and answers with one paragraph.
//
// It is a direct provider call and not a `hermes` one-shot, unlike the judge and the SKILL.md reviser
// next door. Those two have to read a skill tree and an eval file, so they run in a sandbox profile;
// this call is handed every byte it is allowed to see, in the prompt, and answers with 500 characters.
// An agent loop would add a sandbox, a skill tree and a tool budget to a task that has none of those
// problems — and it would put the description lane's cost behind the harness's model, which the
// operator cannot point at a different model per lane.
//
// Nothing here writes the ledger or SKILL.md. A proposal is a proposal: `ReviseDescription` returns it
// and keeps it beside the iteration it answers, and accepting it is the screen's `y`, which puts the
// text in the editable field and touches no file.

const (
	// DescriptionBudget is one call's wall clock. The answer is a paragraph; three minutes is a
	// stalled connection, not a hard model.
	DescriptionBudget = 3 * time.Minute

	descriptionProposalFile = "description-proposal.json"
	descriptionPromptFile   = "description-prompt.md"
	descriptionReplyFile    = "description-reply.txt"
)

// DescribeOptions is one proposal call.
type DescribeOptions struct {
	Budget time.Duration
	Model  string // overrides the configured model for this call
	DryRun bool   // build the prompt and resolve the provider; spend nothing
	// Config is the resolved provider, for a caller that has already read the environment (the TUI
	// reads it once, to name the model and the key's source on the run screen). Nil means "resolve
	// it from the environment".
	Config *llm.Config
}

// DescribeInputs is what the call was built from, so the screen can say what it read and what it
// cost, instead of "asking the model".
type DescribeInputs struct {
	Candidate int
	Iteration int
	Chars     int
	Fails     int
	Passes    int
	Model     string
	KeyFrom   string
}

// DescriptionProposal is one rewrite, with the text it was written against.
//
// Baseline travels with it for the same reason the SKILL.md revision carries its own: the diff is
// against a specific wording, and a proposal whose baseline is not the text on screen would show a
// change that is not the change it is proposing.
type DescriptionProposal struct {
	Skill     string `json:"skill"`
	Candidate int    `json:"candidate"`
	Iteration int    `json:"iteration"` // the trigger iteration whose failures it answers
	Baseline  string `json:"baseline"`
	Text      string `json:"text"`
	Why       string `json:"why"`
	Chars     int    `json:"chars"`
	Model     string `json:"model"`
	KeyFrom   string `json:"key_from"`
	// Issues are the checks the paragraph failed after its one repair. A proposal with issues is still
	// shown — a person deciding on it is better served by seeing the flaw than by seeing nothing — and
	// the screen prints them under the diff.
	Issues  []string `json:"issues,omitempty"`
	Calls   int      `json:"calls,omitempty"`
	Started string   `json:"started"`
	Path    string   `json:"path,omitempty"`
	Prompt  string   `json:"prompt_path,omitempty"`
}

// measuredCandidate is the ledger candidate and the train measurement its failures are about. Both
// halves are needed: the candidate carries the wording the baseline is, and the measurement is the
// only place the failures exist.
func (r *Runner) measuredCandidate(skill string, candidate int) (*LabCandidate, *LabMeasure, error) {
	lab, err := r.DescriptionLab(skill)
	if err != nil {
		return nil, nil, err
	}
	c := lab.Candidate(candidate)
	if c == nil {
		return nil, nil, fmt.Errorf("#%d is not in %s's ledger", candidate, skill)
	}
	m := c.MeasureOn(store.SplitTrain)
	if m == nil {
		return nil, nil, fmt.Errorf("#%d has no train measurement yet — run the train set first; a proposal is written from failures that were measured", candidate)
	}
	return c, m, nil
}

// BuildDescriptionPrompt assembles what the caller is given: the skill's own body, the wording as it
// stands, the requests it failed with their verdicts, the requests it already routes, and the rules
// that keep a rewrite from becoming a list of remembered tests.
func (r *Runner) BuildDescriptionPrompt(skill string, candidate int) (string, DescribeInputs, error) {
	c, m, err := r.measuredCandidate(skill, candidate)
	if err != nil {
		return "", DescribeInputs{}, err
	}
	prompt, in, err := descriptionPrompt(c, m, r.skillSummary(skill))
	if err != nil {
		return "", DescribeInputs{}, err
	}
	in.Model = llm.DefaultModel
	return prompt, in, nil
}

// skillSummary is the top of the skill's own body: enough for the writer to know what the skill does
// and when it is for, without handing it a whole procedure to summarise.
//
// The description is the routing label for the body, so a writer that has never read the body can only
// rearrange the old label — which is how the first version of this call invented capabilities ("it
// performs the roll itself, in the conversation") and named contexts the skill does not serve. Only
// the opening is taken: the procedure, the output format and the pitfalls are not what a description
// is about, and they are the parts most likely to be copied verbatim into it.
func (r *Runner) skillSummary(skill string) string {
	b, err := os.ReadFile(filepath.Join(r.Store.Root, skill, "SKILL.md"))
	if err != nil {
		return ""
	}
	body := string(b)
	// Off with the frontmatter: the description under test lives there, and repeating it would tell the
	// writer what it is writing.
	if strings.HasPrefix(body, "---") {
		if i := strings.Index(body[3:], "\n---"); i >= 0 {
			body = body[3+i+len("\n---"):]
		}
	}
	var keep []string
	size := 0
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		// The parts that describe what the skill is for stop at the parts that describe how it works.
		if strings.HasPrefix(trimmed, "## ") && size > 0 &&
			!strings.Contains(strings.ToLower(trimmed), "when to use") {
			break
		}
		if size += len(line) + 1; size > summaryLimit {
			break
		}
		keep = append(keep, line)
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

// summaryLimit is how much of the body is quoted: a screen's worth, which is what a `## When to Use`
// section and a goal line come to.
const summaryLimit = 1400

// proposalCeiling is the hard limit on a proposal. The format allows 1024; a routing label that long is
// a list of jobs, and a list of jobs is a description that matches on noise. The prompt asks for 80 to
// 250 and this rejects anything past 400, once.
const proposalCeiling = 400

// proposeIssues are the reasons a returned paragraph is not a usable candidate. They are also the
// retry's instructions: a call that fails a check is asked again with the sentences below, which
// makes the cheap checks part of the prompt rather than a comment on the answer.
func proposeIssues(text, baseline string, fails []ScoreRow) []string {
	var out []string
	n := len([]rune(text))
	switch {
	case n == 0:
		out = append(out, "the answer was empty")
	case n > proposalCeiling:
		out = append(out, fmt.Sprintf("it is %d characters; a description has to stay under %d, and 80 to 250 is the useful range", n, proposalCeiling))
	}
	if store.HashText(text) == store.HashText(baseline) {
		out = append(out, "it is the wording as it stands, so nothing would be measured; the failures do justify a change")
	}
	for _, word := range []string{"query", "queries", "eval", "evals", "evaluation", "assertion", "iteration", "router", "trigger"} {
		if containsWord(text, word) {
			out = append(out, fmt.Sprintf("it uses the word %q, which belongs to the harness and not to the skill's user", word))
			break
		}
	}
	if lifted := LiftedWords(text, baseline, fails); len(lifted) > 0 {
		out = append(out, fmt.Sprintf("it lifts %s from the failing requests (%s); name the kind of request instead of its words",
			pluralWords(len(lifted), "word"), strings.Join(lifted, ", ")))
	}
	return out
}

// containsWord is a whole-word search, case-insensitive: "triggered" in a description is the skill's
// own vocabulary, "trigger" as a noun is the harness talking.
func containsWord(text, word string) bool {
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if w == word {
			return true
		}
	}
	return false
}

func pluralWords(n int, noun string) string {
	if n == 1 {
		return "a " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// repairPrompt asks the same question again with the rejected answer and the reasons it was rejected.
// It is cheaper than a longer prompt and more reliable than another rule: a model that has already
// produced a paragraph can repair it, and a model that is asked to obey one more rule usually will not.
func repairPrompt(prompt, rejected string, issues []string) string {
	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString("\n## Your previous answer was not usable\n\n```text\n")
	b.WriteString(strings.TrimSpace(rejected))
	b.WriteString("\n```\n\nIt was refused for these reasons:\n\n")
	for _, issue := range issues {
		fmt.Fprintf(&b, "- %s\n", issue)
	}
	b.WriteString("\nAnswer again, fixing exactly those and keeping everything that was already right. Same markers, same shape.\n")
	return b.String()
}

// descriptionPrompt is the prompt itself, from a candidate and the measurement of it.
//
// It carries four things, and each one answered a failure of the first version. The skill's own body,
// because a writer that has never seen what the skill does can only rearrange the description it was
// given — it invents capabilities, or names a context the skill does not serve. A hard length budget,
// because "the ceiling is 1024" reads as "use it": a 52-character description came back at 321. The
// ban on lifting a failing query's words, with the reason, because "never quote a query" is an
// instruction a model will read past — it wrote "settling a bet, at a tabletop session" straight out
// of two queries, which is a description memorising its own test set. And a worked example of the
// smallest change, because the default behaviour is a rewrite.
func descriptionPrompt(c *LabCandidate, m *LabMeasure, body string) (string, DescribeInputs, error) {
	fails := c.FailedTrain()
	if len(fails) == 0 {
		return "", DescribeInputs{}, fmt.Errorf("#%d passes every train query — there is no failure to revise from", c.ID)
	}
	in := DescribeInputs{Candidate: c.ID, Iteration: m.Iteration, Chars: len([]rune(c.Text)), Fails: len(fails)}

	var fail strings.Builder
	for _, row := range fails {
		fmt.Fprintf(&fail, "### q%d · %s\n", row.ID, failureShape(row))
		fmt.Fprintf(&fail, "the request, as a person typed it: %q\n", oneLineText(row.Query))
		if why := strings.TrimSpace(row.Why); why != "" {
			fmt.Fprintf(&fail, "why it is in the set: %s\n", oneLineText(why))
		}
		fmt.Fprintf(&fail, "what happened: fired on %d of %d runs\n", row.Triggers, row.Runs)
		if loaded := loadedInRun(row); loaded != "" {
			fmt.Fprintf(&fail, "what loaded instead: %s\n", loaded)
		}
		fail.WriteString("\n")
	}

	var pass strings.Builder
	for _, row := range m.Rows {
		if row.Split != SplitTrain || row.Passed == nil || !*row.Passed {
			continue
		}
		in.Passes++
		fmt.Fprintf(&pass, "- %q\n", oneLineText(row.Query))
	}

	var b strings.Builder
	b.WriteString("You rewrite one field: the `description:` in the frontmatter of a skill's SKILL.md.\n\n")
	b.WriteString("An agent reads that single paragraph — and nothing else from this file — to decide whether to load the skill, ")
	b.WriteString("so it carries the whole routing decision. A train set of requests judged the wording below: the ones it got ")
	b.WriteString("wrong are the only evidence you may learn from, and the ones it got right must keep working.\n\n")

	if strings.TrimSpace(body) != "" {
		b.WriteString("## What the skill actually does (from its own body)\n\n```text\n")
		b.WriteString(body)
		b.WriteString("\n```\n\n")
	}

	fmt.Fprintf(&b, "## The description as it stands (%d characters)\n\n```text\n%s\n```\n\n",
		in.Chars, strings.TrimSpace(c.Text))

	fmt.Fprintf(&b, "## The %s the train set got wrong\n\n", plural(in.Fails, "request"))
	b.WriteString(fail.String())

	verb := "routes"
	if in.Passes != 1 {
		verb = "route"
	}
	fmt.Fprintf(&b, "## The %s it already %s correctly\n\nThese must keep working; a rewrite that breaks one has traded a failure for a failure.\n\n",
		plural(in.Passes, "request"), verb)
	b.WriteString(pass.String())
	b.WriteString("\n")
	if note := strings.TrimSpace(c.Note); note != "" {
		fmt.Fprintf(&b, "## Why this wording exists\nThe person tuning it wrote: %s\n\n", oneLineText(note))
	}

	b.WriteString("## The shape of a good answer\n\n")
	b.WriteString("The smallest change that answers every failure. Keep what already works, and let one clause do the work:\n\n")
	b.WriteString("  before  Search the web and summarise what you find.\n")
	b.WriteString("  after   Search the web and summarise what you find — for facts, news or sources someone cannot\n")
	b.WriteString("          produce from memory, not for writing a poem or an essay.\n\n")
	b.WriteString("The opening is untouched; one clause names the context that was missing, one names the boundary that was too\n")
	b.WriteString("wide; no word of any failing request appears in it.\n\n")

	b.WriteString("## Rules\n\n")
	b.WriteString("- Change only what a failure names. If a request for the skill's own work did not reach it, add the ")
	b.WriteString("context that was missing; if a request for other work fired, name the boundary. Neither is a reason to rewrite ")
	b.WriteString("the sentence that already works.\n")
	b.WriteString("- One or two sentences of plain prose, 80 to 250 characters. Never more than 400: a description that lists ")
	b.WriteString("jobs is one the router matches on noise.\n")
	b.WriteString("- Name the KIND of request, never a request. If a failing request says \"settling who drives\", write \"to settle ")
	b.WriteString("something between people\", not \"settling who drives\". A noun or phrase lifted from a failing request is the ")
	b.WriteString("one way a description memorises its test set instead of learning it.\n")
	b.WriteString("- At most one \"not for …\" clause, and only where a too-broad failure needs it. A second negation is a second ")
	b.WriteString("request remembered.\n")
	b.WriteString("- Never mention: this evaluation, tests, requests, queries, iterations, the router, \"the skill\", or SKILL.md.\n")
	b.WriteString("- Do not copy sentences out of the body above. It is there so you know what the skill does, not so the ")
	b.WriteString("description can summarise it.\n")
	b.WriteString("- Keep the voice, the person and the register of the description as it stands, and fix a typo if one is ")
	b.WriteString("in there.\n")
	b.WriteString("- If the failures do not justify a change, return the text unchanged — a rewrite that changes nothing is a ")
	b.WriteString("worse answer than an honest no.\n")
	b.WriteString("- No quotation marks around the answer, no markdown, no list, no trailing explanation inside it.\n\n")

	b.WriteString("## Answer with exactly this, and nothing else\n\n")
	b.WriteString("=== WHY ===\n<one line: narrowed, broadened or both — which words changed, and which failure that answers>\n")
	b.WriteString("=== DESCRIPTION ===\n<the whole new description: one paragraph, 80 to 250 characters>\n=== END ===\n")
	return b.String(), in, nil
}

// failureShape names the failure the way the guide does, because the fix differs: a missed
// should-trigger query asks for a broader description, a fired should-not-trigger query for a
// narrower one, and an unstable row for an explicit yes/no sentence.
func failureShape(row ScoreRow) string {
	switch {
	case row.ShouldTrigger && row.Triggers == 0:
		return "a query for this skill's own work, which the description never reached (too narrow)"
	case !row.ShouldTrigger && row.Triggers > 0:
		return "a query that is not this skill's work, which the description claimed anyway (too broad)"
	case row.ShouldTrigger:
		return fmt.Sprintf("a query for this skill's work, which triggered only sometimes (%d of %d runs)", row.Triggers, row.Runs)
	}
	return fmt.Sprintf("a query that is not this skill's work, which triggered anyway in %d of %d runs", row.Triggers, row.Runs)
}

// loadedInRun is what the router reached for instead: the other skills that loaded, or nothing at
// all when the description did not reach anything. It is the evidence that says "the boundary with
// that skill is wrong" more often than any assertion does — and the row's own skill is not "another
// skill", so it never appears: "dummy loaded, not dummy" is a sentence that reads as a bug.
func loadedInRun(row ScoreRow) string {
	var other []string
	for _, set := range row.LoadedSets {
		for _, name := range set {
			if name != "" && name != row.Skill && !containsString(other, name) {
				other = append(other, name)
			}
		}
	}
	for _, name := range row.Others {
		if name != "" && name != row.Skill && !containsString(other, name) {
			other = append(other, name)
		}
	}
	return strings.Join(other, ", ")
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func oneLineText(s string) string {
	line := strings.Join(strings.Fields(s), " ")
	if r := []rune(line); len(r) > 160 {
		return string(r[:159]) + "…"
	}
	return line
}

// ReviseDescription runs one proposal. The prompt, the raw reply and the parsed proposal are written
// beside the trigger iteration the failures came from, so the call can be read back after a restart
// and never costs a second time just to look at it.
func (r *Runner) ReviseDescription(ctx context.Context, skill string, candidate int, o DescribeOptions) (DescriptionProposal, error) {
	c, m, err := r.measuredCandidate(skill, candidate)
	if err != nil {
		return DescriptionProposal{}, err
	}
	prompt, in, err := descriptionPrompt(c, m, r.skillSummary(skill))
	if err != nil {
		return DescriptionProposal{}, err
	}
	cfg, err := describeProvider(o)
	if err != nil {
		return DescriptionProposal{}, err
	}
	in.Model, in.KeyFrom = cfg.Model, cfg.KeyFrom

	dir := r.descriptionDir(skill, m.Iteration)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return DescriptionProposal{}, err
	}
	prop := DescriptionProposal{
		Skill: skill, Candidate: c.ID, Iteration: m.Iteration, Baseline: c.Text,
		Model: cfg.Model, KeyFrom: cfg.KeyFrom, Started: timestamp(),
		Path:   filepath.Join(dir, descriptionProposalFile),
		Prompt: filepath.Join(dir, descriptionPromptFile),
	}
	if err := os.WriteFile(prop.Prompt, []byte(prompt), 0o644); err != nil {
		return DescriptionProposal{}, err
	}
	if o.DryRun {
		prop.Text = c.Text
		return prop, nil
	}
	budget := o.Budget
	if budget <= 0 {
		budget = DescriptionBudget
	}
	why, text, issues, calls, err := r.propose(ctx, cfg, dir, prompt, c, budget)
	if err != nil {
		return DescriptionProposal{}, err
	}
	prop.Why, prop.Text, prop.Chars = why, text, len([]rune(text))
	prop.Issues, prop.Calls = issues, calls
	if err := writeJSONFile(prop.Path, prop); err != nil {
		return DescriptionProposal{}, err
	}
	return prop, nil
}

// propose is the call and its one repair, in one place so the counting cannot disagree with what was
// spent: a paragraph that passes the checks costs one call, and one that does not is asked again with
// the reasons. The second answer is used even when it still has a flaw — it is a draft on a screen, and
// a person deciding on it is better served by seeing the flaw than by seeing nothing.
func (r *Runner) propose(ctx context.Context, cfg llm.Config, dir, prompt string, c *LabCandidate, budget time.Duration) (why, text string, issues []string, calls int, err error) {
	fails := c.FailedTrain()
	ask := prompt
	for calls < 2 {
		calls++
		reply, err := cfg.Complete(ctx, describeSystem, ask, budget)
		if err != nil {
			return "", "", nil, calls, err
		}
		// The raw answer is kept: a proposal that failed to parse is a prompt bug, and the only way to
		// read it is the text the model actually sent. The repair's answer goes beside the first.
		name := descriptionReplyFile
		if calls > 1 {
			name = strings.TrimSuffix(descriptionReplyFile, ".txt") + "-repair.txt"
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(reply), 0o644); err != nil {
			return "", "", nil, calls, err
		}
		why, text, err = parseDescriptionReply(reply)
		if err != nil {
			return "", "", nil, calls, err
		}
		text = store.FoldText(text)
		if text == "" {
			return "", "", nil, calls, fmt.Errorf("the proposal came back empty — see %s", name)
		}
		issues = proposeIssues(text, c.Text, fails)
		if len(issues) == 0 {
			return why, text, nil, calls, nil
		}
		ask = repairPrompt(prompt, text, issues)
	}
	return why, text, issues, calls, nil
}

// describeSystem is the frame, separate from the material: the rules that never change live here and
// the iteration's evidence lives in the user turn, so a cached prompt on the provider's side keeps
// its prefix.
const describeSystem = "You write the description field of skills in the agentskills.io format: the one paragraph an " +
	"agent reads before it decides whether the skill is relevant. You are precise about scope, you always " +
	"answer inside the markers you are given, and you never pad a description with jobs the skill does not do."

// DescriptionProposalAt reads the newest proposal for one trigger iteration, so a screen can reopen
// what was proposed without spending another call.
func (r *Runner) DescriptionProposalAt(skill string, iteration int) (*DescriptionProposal, error) {
	path := filepath.Join(r.descriptionDir(skill, iteration), descriptionProposalFile)
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	var prop DescriptionProposal
	if err := readJSONFile(path, &prop); err != nil {
		return nil, err
	}
	return &prop, nil
}

// descriptionDir is where a proposal lives: the trigger iteration whose failures it answers, because
// that is the evidence it was written from. With no iteration on disk there is nothing to answer, so
// the workspace root is the fallback rather than a failure — the caller has already refused that case.
func (r *Runner) descriptionDir(skill string, iteration int) string {
	if iteration > 0 {
		dir := r.TriggerIterDir(iteration)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return r.TriggerWorkspace()
}

// describeProvider resolves the provider once, so the run screen can name the model and where the key
// came from before the call is made.
func describeProvider(o DescribeOptions) (llm.Config, error) {
	var cfg llm.Config
	if o.Config != nil {
		cfg = *o.Config
	} else {
		c, err := llm.FromEnv()
		if err != nil {
			return llm.Config{}, err
		}
		cfg = c
	}
	if o.Model != "" {
		cfg.Model = o.Model
	}
	if cfg.Model == "" {
		cfg.Model = llm.DefaultModel
	}
	return cfg, nil
}

// parseDescriptionReply reads the paragraph between its markers. A reply without them is an error and
// not a guess: the text this returns becomes a candidate that a run is spent on, and a whole reply
// pasted into the ledger reads as a description the operator never saw.
func parseDescriptionReply(text string) (why, body string, err error) {
	const whyMark, descMark, endMark = "=== WHY ===", "=== DESCRIPTION ===", "=== END ==="
	s := strings.ReplaceAll(text, "\r\n", "\n")
	i := strings.Index(s, descMark)
	if i == -1 {
		return "", "", fmt.Errorf("the answer has no %s block", strings.TrimSpace(descMark))
	}
	rest := s[i+len(descMark):]
	j := strings.Index(rest, endMark)
	if j == -1 {
		return "", "", fmt.Errorf("the answer was cut off before %s — the model ran out of output; try again", strings.TrimSpace(endMark))
	}
	body = strings.TrimSpace(rest[:j])
	// A model that fences its paragraph is still answering the question.
	if strings.HasPrefix(body, "```") {
		if nl := strings.Index(body, "\n"); nl != -1 {
			body = strings.TrimSpace(body[nl+1:])
		}
		body = strings.TrimSuffix(body, "```")
	}
	body = strings.TrimSpace(body)
	// Straight quotes around the whole paragraph are a wrapper, not part of the description: a
	// description that begins and ends with a quote mark is one the router reads differently.
	body = strings.Trim(body, "\"“”")
	body = strings.TrimSpace(body)
	if body == "" {
		return "", "", fmt.Errorf("the %s block was empty", strings.TrimSpace(descMark))
	}
	if k := strings.Index(s, whyMark); k != -1 && k < i {
		why = strings.TrimSpace(s[k+len(whyMark) : i])
	}
	return why, body, nil
}
