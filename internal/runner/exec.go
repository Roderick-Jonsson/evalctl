package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"evalctl/internal/profile"
)

// Timing is one arm's cost, written to timing.json.
type Timing struct {
	TotalTokens  *int    `json:"total_tokens"`
	InputTokens  *int    `json:"input_tokens"`
	OutputTokens *int    `json:"output_tokens"`
	DurationMS   int64   `json:"duration_ms"`
	ExitCode     *int    `json:"exit_code"`
	Error        *string `json:"error"`
	SessionID    *string `json:"session_id"`
	RunRoot      string  `json:"run_root"`
}

// Checks are the mechanical observations for one arm: never self-reported by the child.
type Checks struct {
	Containment  Containment   `json:"containment"`
	RootGuard    Guard         `json:"root_guard"`
	SkillsLoaded []string      `json:"skills_loaded"`
	Verify       []VerifyEntry `json:"verify"`
}

// VerifyEntry is one run of a skill's own `scripts/ verify` CLI.
type VerifyEntry struct {
	Workspace string         `json:"workspace"`
	ExitCode  *int           `json:"exit_code,omitempty"`
	Report    map[string]any `json:"report,omitempty"`
	Stdout    string         `json:"stdout,omitempty"`
	Stderr    string         `json:"stderr,omitempty"`
	Error     string         `json:"error,omitempty"`
}

// Grading is grading.json: the mechanical checks, the final response, and — once the judge has run
// — one verdict per assertion with the evidence it was decided on. The shape is the one
// agentskills.io's evaluating-skills page prints: assertion_results plus a summary.
type Grading struct {
	AssertionResults []AssertionResult `json:"assertion_results"`
	Summary          *GradeSummary     `json:"summary"`
	Note             string            `json:"note,omitempty"`
	// Judge says which call produced the verdicts. A rate whose judge is unknown is not a
	// measurement, and a re-grade by a different model is a different measurement.
	Judge    *JudgeInfo `json:"judge,omitempty"`
	Checks   Checks     `json:"checks"`
	Response string     `json:"response"`
}

// AssertionResult is one declared assertion, graded. Evidence quotes the output; a PASS without
// evidence is the judge's opinion, and the guide asks for the output's own words.
type AssertionResult struct {
	Text     string `json:"text"`
	Passed   bool   `json:"passed"`
	Evidence string `json:"evidence"`
}

// GradeSummary is the arithmetic the benchmark aggregates: passed/total, per arm.
type GradeSummary struct {
	Passed   int     `json:"passed"`
	Failed   int     `json:"failed"`
	Total    int     `json:"total"`
	PassRate float64 `json:"pass_rate"`
}

// JudgeInfo records the call that graded an arm. The profile has no skill installed and the prompt
// never names the arm, so the judge cannot know which side of the comparison it is reading.
type JudgeInfo struct {
	Profile string `json:"profile"`
	Model   string `json:"model,omitempty"`
	Started string `json:"started"`
}

const gradingNoteNoAssertions = "This case declares no assertions yet (agentskills.io: write them " +
	"after the first run shows what good looks like). The expected output is what a human reads."

const gradingNoteUngraded = "Assertions are declared but not graded yet — `evalctl grade --skill " +
	"<skill> --iteration <N>` runs the judge, or press g on the iteration screen."

// ArmResult is what one arm produced, as the report and the TUI read it.
type ArmResult struct {
	Timing   Timing  `json:"timing"`
	Changes  Changes `json:"changes"`
	Checks   Checks  `json:"checks"`
	Grading  Grading `json:"grading"`
	Response string  `json:"response"`
	ArmDir   string  `json:"arm_dir"`
}

// TriggerRecord is one trigger observation, written to run-N/trigger.json.
type TriggerRecord struct {
	ID            int      `json:"id"`
	Skill         string   `json:"skill"`
	Query         string   `json:"query"`
	ShouldTrigger bool     `json:"should_trigger"`
	Split         string   `json:"split"`
	Loaded        []string `json:"loaded"`
	TargetLoaded  bool     `json:"target_loaded"`
	OtherLoaded   []string `json:"other_loaded"`
	Evidence      bool     `json:"evidence"`
	EarlyStop     *string  `json:"early_stop"`
	ExitCode      *int     `json:"exit_code"`
	DurationMS    int64    `json:"duration_ms"`
	TotalTokens   *int     `json:"total_tokens"`
	Error         *string  `json:"error"`
	Stderr        string   `json:"stderr"`
	RootGuard     Guard    `json:"root_guard"`
	RunRoot       string   `json:"run_root"`
}

// buildArmCmd is the shell command a quality arm runs — printed verbatim by --dry-run.
func (r *Runner) buildArmCmd(promptFile, runRoot, arm string, o Options) []string {
	cmd := []string{r.Profiles.Bin, "-p", r.ArmProfile(arm), "chat"}
	if o.Model != "" {
		cmd = append(cmd, "-m", o.Model)
	}
	return append(cmd, "--oneshot", "--query-file", promptFile, "--format", "stream-json",
		"--yolo", "--in", runRoot)
}

// buildTriggerCmd is one trigger observation: the trigger profile, a turn cap, an empty root.
func (r *Runner) buildTriggerCmd(promptFile, runRoot string, o Options, maxTurns int) []string {
	cmd := []string{r.Profiles.Bin, "-p", r.Profiles.Name(profile.ArmTrigger), "chat"}
	if o.Model != "" {
		cmd = append(cmd, "-m", o.Model)
	}
	return append(cmd, "--oneshot", "--query-file", promptFile, "--format", "stream-json",
		"--yolo", "--in", runRoot, "--max-turns", fmt.Sprintf("%d", maxTurns))
}

// ---------------------------------------------------------------- quality arm

// RunArm runs one side of one case into armDir: stage, launch, fingerprint, write the three
// evidence files.
func (r *Runner) RunArm(ctx context.Context, c Case, arm, armDir string, o Options) (ArmResult, error) {
	stream := filepath.Join(armDir, "stream.jsonl")
	if o.Resume && !o.Force && FindResult(stream) != nil {
		r.message("  %s: already finished, skipping (rerun with --force)", arm)
		return reloadArm(armDir), nil
	}
	if err := os.MkdirAll(armDir, 0o755); err != nil {
		return ArmResult{}, err
	}

	// The arm dir holds the harness's own evidence — stream, stderr, timing, changes, grading — and
	// outputs/ holds the tree the run produced, so "what the run made" is exactly one directory.
	outputs := filepath.Join(armDir, "outputs")
	var base string
	runRoot := outputs
	if o.Isolate {
		dir, err := os.MkdirTemp("", "hermes-eval-")
		if err != nil {
			return ArmResult{}, err
		}
		base = dir
		defer os.RemoveAll(base)
		runRoot = filepath.Join(base, "work")
	}
	if err := os.MkdirAll(runRoot, 0o755); err != nil {
		return ArmResult{}, err
	}
	fixture := r.Store.FixtureDir(c.Skill, c.Slug)
	if err := Stage(fixture, c.Files, runRoot); err != nil {
		return ArmResult{}, fmt.Errorf("%s case %d: %w", c.Skill, c.ID, err)
	}
	before, err := Inventory(runRoot)
	if err != nil {
		return ArmResult{}, err
	}

	promptFile := filepath.Join(filepath.Dir(armDir), "prompt.txt")
	if base != "" {
		promptFile = filepath.Join(base, "prompt.txt")
	}
	if err := os.WriteFile(promptFile, []byte(c.Prompt+"\n"), 0o644); err != nil {
		return ArmResult{}, err
	}

	cmdArgs := r.buildArmCmd(promptFile, runRoot, arm, o)
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = runRoot
	cmd.Env = append(os.Environ(), "HERMES_EVAL_ARM="+arm)
	configureProcess(cmd)
	out, err := os.Create(stream)
	if err != nil {
		return ArmResult{}, err
	}
	defer out.Close()
	errFile, err := os.Create(filepath.Join(armDir, "stderr.txt"))
	if err != nil {
		return ArmResult{}, err
	}
	defer errFile.Close()
	cmd.Stdout, cmd.Stderr = out, errFile

	guardBefore := RootSnapshot(r.Profiles.Production)
	started := time.Now()

	runCtx, cancel := context.WithTimeout(ctx, o.Budget)
	defer cancel()
	var exitCode *int
	var runErr *string
	proc, err := startProcess(cmd)
	if err != nil {
		return ArmResult{}, err
	}
	// The child is writing; tail its stream so the run is watchable while it happens.
	act := r.startActivity(stream, filepath.Join(armDir, "activity.log"), c.Slug, arm, o.Log)
	ticker := time.NewTicker(activityPoll)
	defer ticker.Stop()
loop:
	for {
		select {
		case <-proc.done:
			code := proc.code
			exitCode = &code
			if proc.err != nil {
				msg := proc.err.Error()
				runErr = &msg
			}
			break loop
		case <-runCtx.Done():
			act.poll()
			proc.stop()
			code := proc.code
			exitCode = &code
			if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
				msg := fmt.Sprintf("timeout after %s", o.Budget)
				runErr = &msg
			} else {
				msg := "cancelled"
				runErr = &msg
			}
			break loop
		case <-ticker.C:
			act.poll()
		}
	}
	act.close()

	// What the launch actually produced, before anything writes it down: no stream, no score. A
	// credential failure is named as one instead of being recorded as a skill that did not work.
	events := IterEvents(stream)
	result := FindResult(stream)
	if prob := StreamProblem(events, result, readText(filepath.Join(armDir, "stderr.txt"))); prob != nil {
		return ArmResult{}, fmt.Errorf("%s case %d · %s: %w", c.Skill, c.ID, arm, prob)
	}

	guard := RootGuard(guardBefore, RootSnapshot(r.Profiles.Production), r.Profiles.Production)
	after, err := Inventory(runRoot)
	if err != nil {
		return ArmResult{}, err
	}
	if base != "" {
		if err := CopyTree(runRoot, outputs); err != nil {
			return ArmResult{}, err
		}
	}

	if result == nil {
		result = map[string]any{}
	}
	timing := Timing{
		TotalTokens:  anyInt(tokensOf(result)["total"]),
		InputTokens:  anyInt(tokensOf(result)["input"]),
		OutputTokens: anyInt(tokensOf(result)["output"]),
		DurationMS:   duration(result, started),
		ExitCode:     exitCode,
		Error:        firstNonEmpty(runErr, anyString(result["error"])),
		SessionID:    anyString(result["session_id"]),
		RunRoot:      runRoot,
	}
	if err := writeJSONFile(filepath.Join(armDir, "timing.json"), timing); err != nil {
		return ArmResult{}, err
	}
	changes := Diff(before, after)
	if err := writeJSONFile(filepath.Join(armDir, "changes.json"), changes); err != nil {
		return ArmResult{}, err
	}
	checks := Checks{
		Containment:  CheckContainment(before, after, r.Store.Rules),
		RootGuard:    guard,
		SkillsLoaded: LoadedFromEvents(events, r.Profiles.Skills),
		Verify:       r.verifyWorkspaces(c.Skill, armDir, o),
	}
	response := truncateText(anyStringValue(result["text"]), 4000)
	note := gradingNoteNoAssertions
	if len(c.Assertions) > 0 {
		note = gradingNoteUngraded
	}
	grading := Grading{AssertionResults: []AssertionResult{}, Note: note, Checks: checks, Response: response}
	if err := writeJSONFile(filepath.Join(armDir, "grading.json"), grading); err != nil {
		return ArmResult{}, err
	}

	arm2 := ArmResult{Timing: timing, Changes: changes, Checks: checks, Grading: grading, Response: response, ArmDir: armDir}
	r.emit(Event{Kind: EventArmDone, Arm: arm, Case: c.Slug, Message: armSummary(arm, arm2), Result: &arm2})
	return arm2, nil
}

func armSummary(arm string, res ArmResult) string {
	tokens := "?"
	if res.Timing.TotalTokens != nil {
		tokens = fmt.Sprintf("%d", *res.Timing.TotalTokens)
	}
	containment := "ok"
	if !res.Checks.Containment.OK {
		containment = "OUTSIDE WORKSPACE"
	}
	exit := "?"
	if res.Timing.ExitCode != nil {
		exit = fmt.Sprintf("%d", *res.Timing.ExitCode)
	}
	return fmt.Sprintf("%s: exit=%s %.1fs %s tokens, %d file changes, containment=%s",
		arm, exit, float64(res.Timing.DurationMS)/1000, tokens, res.Changes.Count, containment)
}

// reloadArm reads a finished arm back from disk, so --resume does not lose earlier evidence.
func reloadArm(armDir string) ArmResult {
	out := ArmResult{ArmDir: armDir}
	_ = readJSONFile(filepath.Join(armDir, "timing.json"), &out.Timing)
	_ = readJSONFile(filepath.Join(armDir, "changes.json"), &out.Changes)
	var grading Grading
	if err := readJSONFile(filepath.Join(armDir, "grading.json"), &grading); err == nil {
		out.Checks = grading.Checks
		out.Response = grading.Response
		out.Grading = grading
	}
	return out
}

// verifyWorkspaces runs the skill's own mechanical checks when it ships a scripts/ CLI. It reads
// the tree the run produced (armDir/outputs), not the harness's evidence next to it.
func (r *Runner) verifyWorkspaces(skill, armDir string, o Options) []VerifyEntry {
	if o.NoVerify || !fileExists(filepath.Join(r.Store.SkillDir(skill), "scripts", "go.mod")) {
		return []VerifyEntry{}
	}
	outputs := filepath.Join(armDir, "outputs")
	entries, err := os.ReadDir(outputs)
	if err != nil {
		return []VerifyEntry{}
	}
	out := []VerifyEntry{}
	for _, e := range entries {
		if !e.IsDir() || !r.Store.Rules.Carries(e.Name()) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
		cmd := exec.CommandContext(ctx, "go", "run", "-C", filepath.Join(r.Store.SkillDir(skill), "scripts"), ".",
			"verify", e.Name(), "--root", outputs)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		runErr := cmd.Run()
		cancel()
		entry := VerifyEntry{Workspace: e.Name()}
		if runErr != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				entry.Error = "verify timed out"
				out = append(out, entry)
				continue
			}
		}
		if code := cmd.ProcessState; code != nil {
			exit := code.ExitCode()
			entry.ExitCode = &exit
		}
		var report map[string]any
		if json.Unmarshal(stdout.Bytes(), &report) == nil {
			entry.Report = report
		} else {
			entry.Stdout = truncateText(stdout.String(), 1000)
			entry.Stderr = truncateText(stderr.String(), 1000)
		}
		out = append(out, entry)
	}
	return out
}

// ---------------------------------------------------------------- trigger observation

// The trigger arm's profile is derived from the tree's real profile (see Runner.ArmProfile), so the
// the Python it replaces.

// RunTriggerCase is one observation: the query in an empty run root, stopped as soon as the
// skill loads.
//
// Nothing is staged and the root is a throwaway temp dir, so a trigger run can never touch a
// workspace. The load itself is the outcome, which is why the process is killed the moment the
// skill is opened — everything after that is work the lane does not pay for.
func (r *Runner) RunTriggerCase(ctx context.Context, c TriggerCase, runDir string, o Options) (TriggerRecord, error) {
	recordFile := filepath.Join(runDir, "trigger.json")
	if o.Resume && !o.Force && fileExists(recordFile) {
		var record TriggerRecord
		if err := readJSONFile(recordFile, &record); err == nil {
			r.message("    already finished, skipping (rerun with --force)")
			return record, nil
		}
	}
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return TriggerRecord{}, err
	}
	base, err := os.MkdirTemp("", "hermes-trigger-")
	if err != nil {
		return TriggerRecord{}, err
	}
	defer os.RemoveAll(base)
	runRoot := filepath.Join(base, "work")
	if err := os.MkdirAll(runRoot, 0o755); err != nil {
		return TriggerRecord{}, err
	}
	promptFile := filepath.Join(base, "query.txt")
	if err := os.WriteFile(promptFile, []byte(c.Query+"\n"), 0o644); err != nil {
		return TriggerRecord{}, err
	}

	stream := filepath.Join(runDir, "stream.jsonl")
	started := time.Now()
	guardBefore := RootSnapshot(r.Profiles.Production)

	// What ends the observation early. A should-trigger query is decided by its own skill being
	// opened; a should-not-trigger query is decided as soon as *any* stage skill is opened,
	// because in a four-skill bundle the first skill opened is the routing decision — waiting
	// longer only buys the transcript of the wrong stage doing a workspace's worth of work.
	stopOn := r.Profiles.Skills
	if c.ShouldTrigger {
		stopOn = []string{c.Skill}
	}
	maxTurns := o.MaxTurns
	if !c.ShouldTrigger && maxTurns > NegativeTurns {
		maxTurns = NegativeTurns
	}

	cmdArgs := r.buildTriggerCmd(promptFile, runRoot, o, maxTurns)
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = runRoot
	cmd.Env = append(os.Environ(), "HERMES_EVAL_ARM=trigger")
	configureProcess(cmd)
	out, err := os.Create(stream)
	if err != nil {
		return TriggerRecord{}, err
	}
	defer out.Close()
	errFile, err := os.Create(filepath.Join(runDir, "stderr.txt"))
	if err != nil {
		return TriggerRecord{}, err
	}
	defer errFile.Close()
	cmd.Stdout, cmd.Stderr = out, errFile

	proc, err := startProcess(cmd)
	if err != nil {
		return TriggerRecord{}, err
	}
	// A trigger observation is a one-turn conversation whose only outcome is a load, so the same
	// tail that feeds the log decides the early stop: one read of the stream instead of two.
	act := r.startActivity(stream, filepath.Join(runDir, "activity.log"), caseDirName(c), "trigger", o.Log)
	var earlyStop, runErr *string
	var exitCode *int
	cancelled := false
	var loadedSoFar []string

loop:
	for {
		loadedSoFar = mergeLoaded(loadedSoFar, LoadedFromEvents(act.poll(), stopOn))
		if len(loadedSoFar) > 0 {
			msg := loadedSoFar[0] + " loaded"
			earlyStop = &msg
			break loop
		}
		if code, exited := proc.exitCode(); exited {
			exitCode = &code
			break loop
		}
		select {
		case <-ctx.Done():
			cancelled = true
		case <-time.After(activityPoll):
		}
		if cancelled {
			msg := "cancelled"
			runErr = &msg
			break loop
		}
		if time.Since(started) > o.Budget+30*time.Second {
			msg := fmt.Sprintf("no exit within %s — killed", o.Budget)
			runErr = &msg
			break loop
		}
	}
	act.poll()
	proc.stop()
	act.close()
	if exitCode == nil {
		code := proc.code
		exitCode = &code
	}

	guard := RootGuard(guardBefore, RootSnapshot(r.Profiles.Production), r.Profiles.Production)
	events := IterEvents(stream)
	loaded := LoadedFromEvents(events, r.Profiles.Skills)
	if loaded == nil {
		loaded = []string{}
	}
	result := FindResult(stream)
	if result == nil {
		result = map[string]any{}
	}
	others := []string{}
	for _, s := range loaded {
		if s != c.Skill {
			others = append(others, s)
		}
	}
	stderr := truncateText(readText(filepath.Join(runDir, "stderr.txt")), 600)
	problem := StreamProblem(events, result, stderr)
	record := TriggerRecord{
		ID:            c.ID,
		Skill:         c.Skill,
		Query:         c.Query,
		ShouldTrigger: c.ShouldTrigger,
		Split:         c.Split,
		Loaded:        loaded,
		TargetLoaded:  contains(loaded, c.Skill),
		OtherLoaded:   others,
		// A launch that died before doing anything (bad CLI flag, missing key) emits only the
		// init and result frames. Those runs must not be scored as "did not trigger".
		Evidence:    HasEvidence(events),
		EarlyStop:   earlyStop,
		ExitCode:    exitCode,
		DurationMS:  duration(result, started),
		TotalTokens: anyInt(tokensOf(result)["total"]),
		Error:       firstNonEmpty(runErr, anyString(result["error"]), errorText(problem)),
		Stderr:      stderr,
		RootGuard:   guard,
		RunRoot:     runRoot,
	}
	if err := writeJSONFile(recordFile, record); err != nil {
		return TriggerRecord{}, err
	}
	return record, nil
}

// process wraps one child with exactly one Wait: the trigger lane polls it while it runs, and a
// second Wait on the same exec.Cmd would report "Wait was already called" instead of the exit.
type process struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
	code int
}

func startProcess(cmd *exec.Cmd) (*process, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &process{cmd: cmd, done: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		p.code = cmd.ProcessState.ExitCode()
		close(p.done)
	}()
	return p, nil
}

// exitCode reports the exit status once the child is done; reading it is safe after done closes.
func (p *process) exitCode() (int, bool) {
	select {
	case <-p.done:
		return p.code, true
	default:
		return 0, false
	}
}

// stop terminates the child and everything it started. The platform decides how (a process group on
// Unix, taskkill /T on Windows, see proc_*.go): the tree, never just the leader.
func (p *process) stop() { terminate(p) }

// ---------------------------------------------------------------- helpers

func tokensOf(result map[string]any) map[string]any {
	if tokens, ok := result["tokens"].(map[string]any); ok {
		return tokens
	}
	return map[string]any{}
}

func duration(result map[string]any, started time.Time) int64 {
	if n := anyInt(result["duration_ms"]); n != nil {
		return int64(*n)
	}
	return int64(time.Since(started) / time.Millisecond)
}

func anyInt(v any) *int {
	switch n := v.(type) {
	case float64:
		i := int(n)
		return &i
	case int:
		i := n
		return &i
	case int64:
		i := int(n)
		return &i
	}
	return nil
}

func anyString(v any) *string {
	if s, ok := v.(string); ok && s != "" {
		return &s
	}
	return nil
}

func anyStringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// errorText is an error as the optional string the records carry, nil when there is none.
func errorText(err error) *string {
	if err == nil {
		return nil
	}
	msg := err.Error()
	return &msg
}

func firstNonEmpty(values ...*string) *string {
	for _, v := range values {
		if v != nil && *v != "" {
			return v
		}
	}
	return nil
}

func truncateText(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit])
}

func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// readText is a file's contents as a string, "" when it cannot be read: a missing stderr is the
// normal case for a launch that had nothing to complain about.
func readText(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func readJSONFile(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
