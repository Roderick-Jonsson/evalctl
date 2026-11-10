package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"evalctl/internal/profile"
)

// Grading is the step the evaluating-skills guide puts between a run and a benchmark: every
// assertion is read against what the arm actually produced and recorded PASS or FAIL with evidence.
//
// The judge is an agent, not this program: a separate Hermes call in the baseline profile — no
// skill installed, so the wording under test cannot bias the reading — whose cwd is the arm's own
// directory, so it can open outputs/ and quote it. The prompt never names the arm: a judge that
// knows which side it is reading is the bias blind comparison exists to remove.
//
// The numbers are the judge's verdicts and nothing else. If a call fails, the arm stays ungraded
// and the benchmark says so; a guessed verdict would be worse than a missing one.

// DefaultGradeBudget is the wall clock for one judge call. Grading reads a finished tree, so it is
// much shorter than a run — but it is still a model call with tools.
const DefaultGradeBudget = 180 * time.Second

// GradeProfile is the sandbox the judge runs in: no skills installed, no pinned working directory,
// memories wiped like every eval sandbox. It reads like the baseline arm because it is the same
// clean room; the grading rules arrive in the prompt, never in a persona.
const GradeProfile = profile.Without

// GradeOptions is one grading invocation.
type GradeOptions struct {
	Model   string
	CaseIDs []int
	Force   bool // re-grade arms that already carry verdicts
	DryRun  bool // print the judge commands and write nothing
	Budget  time.Duration
	// Keep leaves the judge's sandbox behind for inspection, like Options.Keep.
	Keep bool
	// Nested says the caller already owns the sandbox: a `run --grade` provisions both arms for
	// the whole operation and removes them after the judge pass, so the judge must not tear down
	// what its caller is still using.
	Nested bool
}

// GradeRun is what one invocation did, for the CLI line and the --json form.
type GradeRun struct {
	Skill     string `json:"skill"`
	Iteration int    `json:"iteration"`
	Cases     int    `json:"cases"`
	Graded    int    `json:"graded"` // arms judged by this invocation
	Skipped   int    `json:"skipped"`
	Passed    int    `json:"passed"`
	Judged    int    `json:"judged"`
	Path      string `json:"path"`
}

// GradeIteration grades the arms of one output iteration. iteration 0 means the newest one on
// disk; the cases are the skill's declared cases that this iteration actually ran.
func (r *Runner) GradeIteration(ctx context.Context, skill string, iteration int, o GradeOptions) (GradeRun, error) {
	if o.Budget <= 0 {
		o.Budget = DefaultGradeBudget
	}
	workspace := r.WorkspaceDir(skill)
	if iteration == 0 {
		iteration = r.latestIteration(workspace)
		if iteration == 0 {
			return GradeRun{}, fmt.Errorf("no iteration under %s — run one first", r.relative(workspace))
		}
	}
	iterDir := r.IterDir(workspace, iteration)
	if info, err := os.Stat(iterDir); err != nil || !info.IsDir() {
		return GradeRun{}, fmt.Errorf("%s does not exist", r.relative(iterDir))
	}
	cases, err := LoadCases(r.Store, []string{skill}, idSet(o.CaseIDs))
	if err != nil {
		return GradeRun{}, err
	}
	out := GradeRun{Skill: skill, Iteration: iteration, Path: iterDir}
	// The judge runs in a clean room of its own: created for this pass when it is not there, so a
	// grade run days after the run it reads costs a create, not a failed call.
	if !o.DryRun && !o.Nested {
		if err := r.provisionSandboxes([]string{GradeProfile}); err != nil {
			return out, err
		}
		defer r.releaseSandboxes([]string{GradeProfile}, o.Keep)
	}
	for _, c := range cases {
		caseDir := filepath.Join(iterDir, c.Slug)
		if info, err := os.Stat(caseDir); err != nil || !info.IsDir() {
			continue
		}
		out.Cases++
		if len(c.Assertions) == 0 {
			out.Skipped++
			r.message("%s: no assertions declared — nothing to grade", c.Slug)
			continue
		}
		for _, arm := range AllArms {
			armDir := filepath.Join(caseDir, arm)
			if info, err := os.Stat(armDir); err != nil || !info.IsDir() {
				continue
			}
			var existing Grading
			_ = readJSONFile(filepath.Join(armDir, "grading.json"), &existing)
			if existing.Summary != nil && !o.Force {
				out.Skipped++
				continue
			}
			cmdArgs := r.buildJudgeCmd(filepath.Join(caseDir, "judge-prompt.txt"), armDir, o)
			if o.DryRun {
				r.emit(Event{Kind: EventPlan, Lane: LaneOutput, Case: c.Slug, Arm: arm,
					Message: "  " + strings.Join(cmdArgs, " ")})
				continue
			}
			if err := ctx.Err(); err != nil {
				return out, err
			}
			r.emit(Event{Kind: EventCase, Lane: LaneOutput, Case: c.Slug,
				Message: fmt.Sprintf("%s  ·  %s case %d", c.Slug, c.Skill, c.ID)})
			r.emit(Event{Kind: EventArmStart, Lane: LaneOutput, Case: c.Slug, Arm: arm})
			grading, err := r.judgeArm(ctx, c, armDir, cmdArgs, o)
			if err != nil {
				return out, fmt.Errorf("%s %s: %w", c.Slug, arm, err)
			}
			out.Graded++
			out.Passed += grading.Summary.Passed
			out.Judged += grading.Summary.Total
			msg := fmt.Sprintf("%s: %d/%d assertions pass", arm, grading.Summary.Passed, grading.Summary.Total)
			r.emit(Event{Kind: EventArmDone, Lane: LaneOutput, Case: c.Slug, Arm: arm, Message: msg})
			r.message("%s · %s", c.Slug, msg)
		}
	}
	if o.DryRun {
		r.emit(Event{Kind: EventDone, Lane: LaneOutput, Message: "Dry run: nothing was written."})
		return out, nil
	}
	if err := r.RefreshIteration(skill, iteration); err != nil {
		return out, err
	}
	msg := fmt.Sprintf("graded %d arm(s) · %d/%d assertions pass · wrote %s/benchmark.json",
		out.Graded, out.Passed, out.Judged, r.relative(iterDir))
	if out.Graded == 0 && out.Skipped > 0 {
		msg = fmt.Sprintf("already graded — %d arm(s) skipped (--force re-grades) · refreshed %s/benchmark.json",
			out.Skipped, r.relative(iterDir))
	}
	r.emit(Event{Kind: EventDone, Lane: LaneOutput,
		Case: fmt.Sprintf("%s@iteration-%d", skill, iteration), Message: msg})
	return out, nil
}

// latestIteration is max(existing)+0 for one workspace: the iteration a grading call with no number
// means.
func (r *Runner) latestIteration(workspace string) int {
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return 0
	}
	best := 0
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "iteration-") {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "iteration-")); err == nil && n > best {
			best = n
		}
	}
	return best
}

// buildJudgeCmd is the grading call, printed verbatim by --dry-run. --in is the arm's directory:
// outputs/ is what the judge reads, and the harness's own evidence sits beside it.
func (r *Runner) buildJudgeCmd(promptFile, armDir string, o GradeOptions) []string {
	cmd := []string{r.Profiles.Bin, "-p", GradeProfile, "chat"}
	if o.Model != "" {
		cmd = append(cmd, "-m", o.Model)
	}
	return append(cmd, "--oneshot", "--query-file", promptFile, "--format", "stream-json",
		"--yolo", "--in", armDir, "--max-turns", "8")
}

// judgePrompt is the whole of what the judge knows: the ask, the case's expected output, the
// assertions, and the reply the arm gave. It never says which arm this is.
func judgePrompt(c Case, reply string) string {
	var b strings.Builder
	b.WriteString("You are grading one completed eval run against a list of assertions.\n\n")
	b.WriteString("The run's produced files are under `outputs/` in this directory. Read them as needed. ")
	b.WriteString("The agent's final reply is quoted below.\n\n")
	b.WriteString("Task given to the agent:\n")
	b.WriteString(strings.TrimSpace(c.Prompt) + "\n\n")
	b.WriteString("Expected output (what success looks like, in words):\n")
	b.WriteString(strings.TrimSpace(c.ExpectedOutput) + "\n\n")
	b.WriteString("Assertions — grade every one of them:\n")
	for i, a := range c.Assertions {
		fmt.Fprintf(&b, "%d. %s\n", i+1, a)
	}
	b.WriteString("\nThe agent's final reply:\n")
	if strings.TrimSpace(reply) == "" {
		b.WriteString("(empty)\n")
	} else {
		b.WriteString("---\n" + strings.TrimSpace(reply) + "\n---\n")
	}
	b.WriteString("\nRules:\n")
	b.WriteString("- PASS requires concrete evidence quoted from outputs/ or the reply. ")
	b.WriteString("A heading or label without the substance behind it is a FAIL.\n")
	b.WriteString("- If you cannot verify an assertion from what the run produced, it is a FAIL, and the evidence says what is missing.\n")
	b.WriteString("- Do not give the benefit of the doubt, and do not rewrite the assertion: grade the one written.\n")
	b.WriteString("- Quote or reference the output in the evidence; \"looks good\" is not evidence.\n\n")
	b.WriteString("Answer with ONE JSON object and nothing else:\n")
	b.WriteString(`{"assertion_results":[{"text":"<the assertion, verbatim>","passed":true,"evidence":"<what you saw>"}],`)
	b.WriteString(`"summary":{"passed":0,"failed":0,"total":0,"pass_rate":0}}` + "\n")
	return b.String()
}

// judgeArm runs one judge call and writes the arm's grading.json. The mechanical checks and the
// reply are kept: grading adds verdicts, it does not replace the evidence the run already wrote.
func (r *Runner) judgeArm(ctx context.Context, c Case, armDir string, cmdArgs []string, o GradeOptions) (Grading, error) {
	var existing Grading
	_ = readJSONFile(filepath.Join(armDir, "grading.json"), &existing)
	prompt := judgePrompt(c, existing.Response)
	if err := os.WriteFile(filepath.Join(filepath.Dir(armDir), "judge-prompt.txt"), []byte(prompt), 0o644); err != nil {
		return Grading{}, err
	}
	if err := os.MkdirAll(armDir, 0o755); err != nil {
		return Grading{}, err
	}
	streamPath := filepath.Join(armDir, "judge-stream.jsonl")
	out, err := os.Create(streamPath)
	if err != nil {
		return Grading{}, err
	}
	defer out.Close()
	errFile, err := os.Create(filepath.Join(armDir, "judge-stderr.txt"))
	if err != nil {
		return Grading{}, err
	}
	defer errFile.Close()

	callCtx, cancel := context.WithTimeout(ctx, o.Budget)
	defer cancel()
	cmd := exec.CommandContext(callCtx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = armDir
	cmd.Stdout, cmd.Stderr = out, errFile
	started := timestamp()
	if err := cmd.Run(); err != nil {
		stderr, _ := os.ReadFile(filepath.Join(armDir, "judge-stderr.txt"))
		return Grading{}, fmt.Errorf("judge failed: %v — %s", err, truncateText(oneLineBytes(stderr), 300))
	}

	result := FindResult(streamPath)
	if result == nil {
		return Grading{}, fmt.Errorf("judge produced no result frame — see %s", filepath.Base(streamPath))
	}
	text := anyStringValue(result["text"])
	results, err := parseJudgeReply(text, c.Assertions)
	if err != nil {
		return Grading{}, err
	}
	summary := summarize(results)
	grading := Grading{
		AssertionResults: results,
		Summary:          &summary,
		Checks:           existing.Checks,
		Response:         existing.Response,
		Judge:            &JudgeInfo{Profile: GradeProfile, Model: o.Model, Started: started},
	}
	if err := writeJSONFile(filepath.Join(armDir, "grading.json"), grading); err != nil {
		return Grading{}, err
	}
	return grading, nil
}

// parseJudgeReply turns the judge's answer into exactly one verdict per declared assertion, in the
// declared order. A missing verdict is a FAIL with the reason, never a pass by default; a judge that
// answered with prose around its JSON is still read.
func parseJudgeReply(text string, assertions []string) ([]AssertionResult, error) {
	body := extractJSONObject(text)
	if body == "" {
		return nil, fmt.Errorf("judge answered without a JSON object")
	}
	var reply struct {
		AssertionResults []AssertionResult `json:"assertion_results"`
	}
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		return nil, fmt.Errorf("judge JSON did not parse: %w", err)
	}
	byText := map[string]AssertionResult{}
	for _, res := range reply.AssertionResults {
		byText[foldAssertion(res.Text)] = res
	}
	out := make([]AssertionResult, 0, len(assertions))
	used := map[int]bool{}
	for i, want := range assertions {
		if res, ok := byText[foldAssertion(want)]; ok {
			out = append(out, finishResult(want, res))
			continue
		}
		if i < len(reply.AssertionResults) && !used[i] && len(reply.AssertionResults) == len(assertions) {
			used[i] = true
			out = append(out, finishResult(want, reply.AssertionResults[i]))
			continue
		}
		out = append(out, AssertionResult{Text: want, Passed: false,
			Evidence: "the judge returned no verdict for this assertion — graded FAIL rather than assumed"})
	}
	return out, nil
}

// finishResult enforces the guide's first grading principle: a PASS has to say what it saw. A pass
// with no evidence is the benefit of the doubt, and the guide asks for none.
func finishResult(text string, res AssertionResult) AssertionResult {
	evidence := strings.TrimSpace(res.Evidence)
	if evidence == "" {
		if res.Passed {
			return AssertionResult{Text: text, Passed: false,
				Evidence: "the judge passed it without evidence — graded FAIL (a pass must quote what it saw)"}
		}
		evidence = "no evidence given"
	}
	return AssertionResult{Text: text, Passed: res.Passed, Evidence: truncateText(evidence, 600)}
}

func foldAssertion(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// summarize recounts from the verdicts; the judge's own arithmetic is never trusted for a rate.
func summarize(results []AssertionResult) GradeSummary {
	s := GradeSummary{Total: len(results)}
	for _, res := range results {
		if res.Passed {
			s.Passed++
		} else {
			s.Failed++
		}
	}
	if s.Total > 0 {
		s.PassRate = float64(s.Passed) / float64(s.Total)
	}
	return s
}

// extractJSONObject returns the first COMPLETE JSON object inside s, with any prose before it and
// anything after it removed. It scans for the matching brace rather than slicing from the first '{'
// to the last '}': a judge that answers its object and then appends a stray `,"summary":{…}` is one
// object followed by junk, and slicing to the last brace glues the two into text that cannot parse —
// which aborts the whole iteration, since one unparsable arm fails the batch.
func extractJSONObject(s string) string {
	start := strings.Index(s, "{")
	if start == -1 {
		return ""
	}
	depth, inString, escaped := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

func oneLineBytes(b []byte) string {
	return strings.Join(strings.Fields(string(b)), " ")
}

// RefreshIteration re-aggregates one iteration from its arm directories: benchmark.json (timing and
// pass rates), report.md (the human read), and feedback.json's case keys (the review scaffold). It
// is called after a run and after grading, so the three files always describe the same arms.
func (r *Runner) RefreshIteration(skill string, iteration int) error {
	iterDir := r.IterDir(r.WorkspaceDir(skill), iteration)
	runs, err := r.CollectRuns(iterDir, skill)
	if err != nil {
		return err
	}
	bench := BenchmarkOf(runs, iteration)
	if err := writeJSONFile(filepath.Join(iterDir, "benchmark.json"), bench); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(iterDir, "report.md"), []byte(Report(runs, bench, skill, iteration)), 0o644); err != nil {
		return err
	}
	feedbackPath := filepath.Join(iterDir, "feedback.json")
	feedback := map[string]string{}
	_ = readJSONFile(feedbackPath, &feedback)
	for _, run := range runs {
		if _, ok := feedback[run.Slug]; !ok {
			feedback[run.Slug] = ""
		}
	}
	return writeJSONFile(feedbackPath, feedback)
}
