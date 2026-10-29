package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/store"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newTree builds a minimal but legal tree: one skill with two cases, one with one, and a trigger
// set carrying both labels in both splits.
func newTree(t *testing.T) (*store.Store, string) {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "evals", "trigger-evals.json"), `{
  "runs": 3,
  "threshold": 0.5,
  "queries": [
    {"id": 1, "skill": "slide-builder", "should_trigger": true, "split": "train", "query": "build me slides from this outline", "why": "core"},
    {"id": 2, "skill": "slide-builder", "should_trigger": false, "split": "train", "query": "research kubernetes operators for me"},
    {"id": 3, "skill": "slide-builder", "should_trigger": true, "split": "validation", "query": "turn PO-RevB into a deck"},
    {"id": 4, "skill": "slide-builder", "should_trigger": false, "split": "validation", "query": "what is a controller loop"}
  ]
}`)
	writeFile(t, filepath.Join(root, "slide-builder", "SKILL.md"),
		"---\nname: slide-builder\ndescription: >\n  Use this skill when an outline has to become\n  slides, even if the user never says slides.\n---\n\n# slide-builder\n")
	writeFile(t, filepath.Join(root, "slide-builder", "evals", "evals.json"), `{
  "skill_name": "slide-builder",
  "evals": [
    {"id": 1, "slug": "sb-1-slides", "prompt": "build slides", "expected_output": "SL.md", "files": ["PO-RevB.md"]},
    {"id": 2, "slug": "sb-2-no-outline", "prompt": "PS.md only", "expected_output": "points to learning-path", "files": []}
  ]
}`)
	writeFile(t, filepath.Join(root, "source-research", "evals", "evals.json"), `{
  "skill_name": "source-research",
  "evals": [
    {"id": 1, "slug": "src-1-new-topic", "prompt": "research this", "expected_output": "PS.md", "files": []}
  ]
}`)
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return s, root
}

func TestLoadCasesValidatesSlugAndUniqueness(t *testing.T) {
	_, root := newTree(t)
	cases, err := LoadCases(mustStore(t, root), []string{"slide-builder"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cases) != 2 || cases[0].Slug != "sb-1-slides" {
		t.Fatalf("got %+v", cases)
	}

	// A duplicate slug would collide in results/iteration-N/<slug>/, so it is refused.
	writeFile(t, filepath.Join(root, "source-research", "evals", "evals.json"), `{
  "skill_name": "source-research",
  "evals": [{"id": 1, "slug": "sb-1-slides", "prompt": "x", "expected_output": "", "files": []}]
}`)
	s2, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCases(s2, []string{"slide-builder", "source-research"}, nil); err == nil ||
		!strings.Contains(err.Error(), "duplicate slug") {
		t.Fatalf("expected duplicate slug error, got %v", err)
	}
}

func TestLoadCasesRefusesBadSlug(t *testing.T) {
	_, root := newTree(t)
	writeFile(t, filepath.Join(root, "source-research", "evals", "evals.json"), `{
  "skill_name": "source-research",
  "evals": [{"id": 1, "slug": "Not A Slug", "prompt": "x", "expected_output": "", "files": []}]
}`)
	if _, err := LoadCases(mustStore(t, root), []string{"source-research"}, nil); err == nil ||
		!strings.Contains(err.Error(), "single dir name") {
		t.Fatalf("expected slug error, got %v", err)
	}
}

func TestTriggerSplitNeedsBothLabels(t *testing.T) {
	_, root := newTree(t)
	writeFile(t, filepath.Join(root, "evals", "trigger-evals.json"), `{
  "queries": [
    {"id": 1, "skill": "slide-builder", "should_trigger": true, "split": "train", "query": "a"},
    {"id": 2, "skill": "slide-builder", "should_trigger": true, "split": "train", "query": "b"},
    {"id": 3, "skill": "slide-builder", "should_trigger": true, "split": "validation", "query": "c"},
    {"id": 4, "skill": "slide-builder", "should_trigger": false, "split": "validation", "query": "d"}
  ]
}`)
	s2, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(s2).LoadTriggerQueries([]string{"slide-builder"}, nil, "all"); err == nil ||
		!strings.Contains(err.Error(), "should-not-trigger") {
		t.Fatalf("expected a split-balance error, got %v", err)
	}
}

func TestTriggerSelectionAndConfig(t *testing.T) {
	s, _ := newTree(t)
	r := New(s)
	cfg := r.TriggerConfigOf()
	if cfg.Runs != 3 || cfg.Threshold != 0.5 {
		t.Fatalf("config = %+v", cfg)
	}
	got, err := r.LoadTriggerQueries([]string{"slide-builder"}, map[int]bool{1: true, 4: true}, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 4 {
		t.Fatalf("got %+v", got)
	}
	if _, err := r.LoadTriggerQueries([]string{"slide-builder"}, map[int]bool{99: true}, "all"); err == nil {
		t.Fatal("expected unknown id error")
	}
}

// TestScoreTriggerPassRule is the lane's contract: above the threshold on positives, below it on
// negatives, and a run with no evidence is unmeasured rather than a miss.
func TestScoreTriggerPassRule(t *testing.T) {
	s, _ := newTree(t)
	r := New(s)
	cases, err := r.LoadTriggerQueries([]string{"slide-builder"}, nil, "all")
	if err != nil {
		t.Fatal(err)
	}
	rec := func(id int, should, loaded, evidence bool) *TriggerRecord {
		return &TriggerRecord{ID: id, ShouldTrigger: should, TargetLoaded: loaded, Evidence: evidence,
			Loaded: []string{}, RootGuard: Guard{OK: true}}
	}
	records := map[int][]*TriggerRecord{
		1: {rec(1, true, true, true), rec(1, true, true, true), rec(1, true, false, true)},
		2: {rec(2, false, false, true), rec(2, false, true, true), rec(2, false, false, true)},
		3: {rec(3, true, false, true), rec(3, true, false, true)},
		4: {rec(4, false, false, true), rec(4, false, false, false)}, // second run: no evidence
	}
	score := r.ScoreTrigger(cases, records, 3, 0.5)

	byID := map[int]ScoreRow{}
	for _, row := range score.Rows {
		byID[row.ID] = row
	}
	if row := byID[1]; row.Passed == nil || !*row.Passed || row.Triggers != 2 || row.ValidRuns != 3 {
		t.Fatalf("q1 = %+v", row)
	}
	if row := byID[2]; row.Passed == nil || !*row.Passed || row.ValidRuns != 3 {
		t.Fatalf("q2 (negative, 1/3 loaded) should pass: %+v", row)
	}
	if row := byID[3]; row.Passed == nil || *row.Passed {
		t.Fatalf("q3 (positive, 0/2 loaded) should fail: %+v", row)
	}
	// The no-evidence run is excluded from the rate, so q4 is 0/1, not 0/2.
	if row := byID[4]; row.ValidRuns != 1 || row.Runs != 2 {
		t.Fatalf("q4 = %+v", row)
	}
	if score.Overall.Judged != 4 || score.Overall.Passed != 3 {
		t.Fatalf("overall = %+v", score.Overall)
	}
	if score.Splits["train"].Judged != 2 || score.Splits["validation"].Judged != 2 {
		t.Fatalf("splits = %+v", score.Splits)
	}
	if !score.RootGuardOK {
		t.Fatal("root guard should be ok")
	}
}

func TestLoadedFromEventsIsEvidenceOnly(t *testing.T) {
	stream := writeStream(t, []string{
		`{"type":"init"}`,
		`{"type":"text","text":"I'll use the slide-builder skill for this."}`,
		`{"type":"tool_use","name":"read_file","input":{"path":"notes.md"}}`,
		`{"type":"tool_use","name":"skill_view","input":{"name":"learning-path"}}`,
	})
	loaded := LoadedFromEvents(IterEvents(stream), []string{"source-research", "learning-path", "slide-builder"})
	if len(loaded) != 1 || loaded[0] != "learning-path" {
		t.Fatalf("prose counted as evidence or load missed: %v", loaded)
	}

	stream2 := writeStream(t, []string{
		`{"type":"tool_use","name":"read_file","input":{"path":"/p/skills/slide-builder/SKILL.md"}}`,
	})
	loaded = LoadedFromEvents(IterEvents(stream2), []string{"slide-builder"})
	if len(loaded) != 1 || loaded[0] != "slide-builder" {
		t.Fatalf("reading the installed SKILL.md is a load: %v", loaded)
	}

	dead := writeStream(t, []string{`{"type":"init"}`, `{"type":"result","error":"no api key"}`})
	if HasEvidence(IterEvents(dead)) {
		t.Fatal("a launch that died before its first tool call has no evidence")
	}
}

func mustStore(t *testing.T, root string) *store.Store {
	t.Helper()
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func writeStream(t *testing.T, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stream.jsonl")
	writeFile(t, path, strings.Join(lines, "\n")+"\n")
	return path
}

func TestStageRefusesEscapingFixtureEntry(t *testing.T) {
	root := t.TempDir()
	fixture := filepath.Join(root, "MC-x")
	writeFile(t, filepath.Join(fixture, "PO-RevB.md"), "x")
	writeFile(t, filepath.Join(root, "outside.md"), "secret")
	runRoot := filepath.Join(t.TempDir(), "work")

	if err := Stage(fixture, []string{"../outside.md"}, runRoot); err == nil {
		t.Fatal("expected an escape refusal")
	}
	if err := Stage(fixture, []string{"PO-RevB.md"}, runRoot); err != nil {
		t.Fatalf("legal stage failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runRoot, "PO-RevB.md")); err != nil {
		t.Fatalf("fixture was not staged: %v", err)
	}
}

func TestStagePromptOnlyLeavesRootEmpty(t *testing.T) {
	runRoot := filepath.Join(t.TempDir(), "work")
	if err := Stage(filepath.Join(t.TempDir(), "absent"), nil, runRoot); err != nil {
		t.Fatalf("a prompt-only case stages nothing and needs no dir: %v", err)
	}
	entries, err := os.ReadDir(runRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("run root should be empty: %v %d", err, len(entries))
	}
}

func TestContainmentFlagsWritesOutsideTheWorkspace(t *testing.T) {
	before := map[string]FileInfo{"MC-x/PS.md": {SHA256: "a", Bytes: 1}}
	after := map[string]FileInfo{
		"MC-x/PS.md":     {SHA256: "a", Bytes: 1},
		"MC-x/LP.md":     {SHA256: "b", Bytes: 2},
		"_archive/x.md":  {SHA256: "c", Bytes: 3},
		"notes/leak.txt": {SHA256: "d", Bytes: 4},
	}
	containment := CheckContainment(before, after)
	if containment.OK || len(containment.Outside) != 1 || containment.Outside[0] != "notes/leak.txt" {
		t.Fatalf("containment = %+v", containment)
	}
	changes := Diff(before, after)
	if changes.Count != 3 || len(changes.New) != 3 || len(changes.Deleted) != 0 {
		t.Fatalf("changes = %+v", changes)
	}
}

func TestBenchmarkAndReportCarryTheDelta(t *testing.T) {
	arm := func(ms int64, tokens int, exit int) ArmResult {
		return ArmResult{Timing: Timing{DurationMS: ms, TotalTokens: &tokens, ExitCode: &exit},
			Checks:  Checks{Containment: Containment{OK: true}, SkillsLoaded: []string{"slide-builder"}},
			Changes: Changes{New: map[string]int64{}, Modified: map[string]Modified{}, Deleted: []string{}}}
	}
	runs := []CollectedRun{
		{Skill: "slide-builder", ID: 1, Slug: "sb-1-slides", Prompt: "build slides", ExpectedOutput: "SL.md",
			Arms: map[string]ArmResult{ArmWith: arm(90_000, 40_000, 0), ArmWithout: arm(120_000, 30_000, 0)}},
	}
	bench := BenchmarkOf(runs, 4)
	if bench.RunSummary[ArmWith].DurationMS.Mean != 90_000 {
		t.Fatalf("bench = %+v", bench)
	}
	if bench.Delta == nil || bench.Delta.DurationMS != -30_000 || bench.Delta.TotalTokens != 10_000 {
		t.Fatalf("delta = %+v", bench.Delta)
	}
	report := Report(runs, bench, 4)
	for _, want := range []string{"# Eval iteration 4", "Delta (with − without): -30.0s, +10,000 tokens",
		"containment: ok", "skills loaded: slide-builder", "sb-1-slides"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func TestTriggerReportNamesUnmeasuredQueries(t *testing.T) {
	s, _ := newTree(t)
	r := New(s)
	cases, _ := r.LoadTriggerQueries([]string{"slide-builder"}, nil, "all")
	score := r.ScoreTrigger(cases, map[int][]*TriggerRecord{
		1: {{ID: 1, ShouldTrigger: true, Evidence: false, Stderr: "no api key", RootGuard: Guard{OK: true}}},
	}, 3, 0.5)
	report := TriggerReport(score, 2, r.LintDescriptions([]string{"slide-builder"}))
	if !strings.Contains(report, "no usable run") || !strings.Contains(report, "no api key") {
		t.Fatalf("report should name the unmeasured query:\n%s", report)
	}
	if !strings.Contains(report, "Use this skill when an outline has to become slides") {
		t.Fatalf("folded description missing:\n%s", report)
	}
}

func TestFrontmatterDescriptionFoldsBlockScalar(t *testing.T) {
	s, _ := newTree(t)
	got := New(s).FrontmatterDescription("slide-builder")
	want := "Use this skill when an outline has to become slides, even if the user never says slides."
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	lint := New(s).LintDescriptions([]string{"slide-builder"})
	if len(lint) != 1 || !lint[0].LimitOK || !lint[0].Imperative || !lint[0].CoversUnstated {
		t.Fatalf("lint = %+v", lint)
	}
}

// TestDryRunPlansWithoutWriting is the guard the whole port needs: planning a lane must produce
// the exact hermes command and must not create evals/results/.
func TestDryRunPlansWithoutWriting(t *testing.T) {
	s, root := newTree(t)
	r := New(s)
	var plan []string
	r.Emit = func(e Event) {
		if e.Kind == EventPlan {
			plan = append(plan, e.Message)
		}
	}
	err := r.RunQuality(context.Background(), Options{Lane: LaneOutput, Skills: []string{"slide-builder"},
		DryRun: true, Isolate: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 4 {
		t.Fatalf("want 4 arm plans (2 cases × 2 arms), got %d: %v", len(plan), plan)
	}
	for _, line := range plan {
		if !strings.Contains(line, "hermes -p eval-vcc-") || !strings.Contains(line, "--format stream-json") {
			t.Fatalf("plan line is not the real command: %q", line)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "evals", "results")); !os.IsNotExist(err) {
		t.Fatalf("a dry run wrote evals/results: %v", err)
	}

	plan = nil
	err = r.RunTrigger(context.Background(), Options{Lane: LaneTrigger, Skills: []string{"slide-builder"},
		DryRun: true, Runs: 2, Threshold: 0.5, MaxTurns: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 8 {
		t.Fatalf("want 4 queries × 2 runs, got %d", len(plan))
	}
	for _, line := range plan {
		if !strings.Contains(line, "-p eval-vcc-trigger") || !strings.Contains(line, "--max-turns") {
			t.Fatalf("trigger plan line is wrong: %q", line)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "evals", "results")); !os.IsNotExist(err) {
		t.Fatalf("a dry trigger run wrote evals/results: %v", err)
	}
}

func TestNextIterationAndListRuns(t *testing.T) {
	s, root := newTree(t)
	r := New(s)
	if got := r.NextIteration("iteration"); got != 1 {
		t.Fatalf("empty tree should start at 1, got %d", got)
	}
	writeFile(t, filepath.Join(root, "evals", "results", "iteration-2", "meta.json"),
		`{"iteration":2,"model":"m","cases":["sb-1-slides"]}`)
	writeFile(t, filepath.Join(root, "evals", "results", "iteration-2", "report.md"), "# x")
	writeFile(t, filepath.Join(root, "evals", "results", "trigger-1", "trigger.json"),
		`{"score":{"threshold":0.5,"runs":3,"overall":{"judged":4,"passed":3,"rate":0.75}}}`)
	if got := r.NextIteration("iteration"); got != 3 {
		t.Fatalf("next iteration should be 3, got %d", got)
	}
	infos := r.ListRuns()
	if len(infos) != 2 {
		t.Fatalf("list = %+v", infos)
	}
	var trigger *RunInfo
	for i := range infos {
		if infos[i].Lane == LaneTrigger {
			trigger = &infos[i]
		}
	}
	if trigger == nil || trigger.PassRate == nil || *trigger.PassRate != 0.75 {
		t.Fatalf("trigger run info = %+v", trigger)
	}
	info, err := r.ResolveRun("trigger-1", "")
	if err != nil || info.Lane != LaneTrigger {
		t.Fatalf("resolve: %v %+v", err, info)
	}
	if _, err := r.ResolveRun("9", ""); err == nil {
		t.Fatal("expected a miss for iteration-9")
	}
}

func TestArmsAreKnown(t *testing.T) {
	s, _ := newTree(t)
	r := New(s)
	if err := r.RunQuality(context.Background(), Options{Lane: LaneOutput, Arms: []string{"nope"},
		DryRun: true}); err == nil || !strings.Contains(err.Error(), "unknown arm") {
		t.Fatalf("expected unknown arm error, got %v", err)
	}
}

func TestJSONShapeOfTimingStaysStable(t *testing.T) {
	tokens := 12
	exit := 0
	b, err := json.Marshal(Timing{DurationMS: 5, TotalTokens: &tokens, ExitCode: &exit})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"total_tokens":12`, `"duration_ms":5`, `"exit_code":0`} {
		if !strings.Contains(string(b), key) {
			t.Fatalf("timing.json lost %s: %s", key, b)
		}
	}
}
