package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"evalctl/internal/version"
)

// CollectedRun is one case read back from an iteration directory.
type CollectedRun struct {
	Skill          string               `json:"skill"`
	ID             int                  `json:"id"`
	Slug           string               `json:"slug"`
	SkillTreeSHA   string               `json:"skill_tree_sha,omitempty"`
	Prompt         string               `json:"prompt"`
	ExpectedOutput string               `json:"expected_output"`
	Arms           map[string]ArmResult `json:"arms"`
}

// CollectRuns reads every finished case directory of an iteration back from disk. With skill
// set, only that skill's cases are collected — an iteration lives in one skill's workspace.
//
// Aggregating from disk (not from this invocation) lets a `--case 1,3 --resume` run accumulate
// into the same iteration without clobbering the earlier cases.
func (r *Runner) CollectRuns(iterDir, skill string) ([]CollectedRun, error) {
	all, err := LoadCases(r.Store, r.Store.SkillNames(), nil)
	if err != nil {
		return nil, err
	}
	known := map[string]Case{}
	for _, c := range all {
		if skill != "" && c.Skill != skill {
			continue
		}
		known[c.Slug] = c
	}
	entries, err := os.ReadDir(iterDir)
	if err != nil {
		return nil, err
	}
	var runs []CollectedRun
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		c, ok := known[e.Name()]
		if !ok {
			continue
		}
		record := CollectedRun{Skill: c.Skill, ID: c.ID, Slug: c.Slug, Prompt: c.Prompt,
			ExpectedOutput: c.ExpectedOutput, Arms: map[string]ArmResult{}}
		for _, arm := range AllArms {
			armDir := filepath.Join(iterDir, c.Slug, arm)
			if info, err := os.Stat(armDir); err != nil || !info.IsDir() {
				continue
			}
			record.Arms[arm] = reloadArm(armDir)
		}
		runs = append(runs, record)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].Slug < runs[j].Slug })
	return runs, nil
}

// Stat is a mean plus the values it came from — never a mean alone.
type Stat struct {
	Mean   int64   `json:"mean"`
	Values []int64 `json:"values"`
}

// ArmSummary is one arm's aggregate across the iteration.
type ArmSummary struct {
	Cases       int  `json:"cases"`
	FailedRuns  int  `json:"failed_runs"`
	DurationMS  Stat `json:"duration_ms"`
	TotalTokens Stat `json:"total_tokens"`
}

// Delta is with-skill minus without-skill, the number the comparison exists for.
type Delta struct {
	DurationMS  int64 `json:"duration_ms"`
	TotalTokens int64 `json:"total_tokens"`
}

// Benchmark is benchmark.json.
type Benchmark struct {
	Iteration  int                   `json:"iteration"`
	Generated  string                `json:"generated"`
	RunSummary map[string]ArmSummary `json:"run_summary"`
	Assertions string                `json:"assertions"`
	Delta      *Delta                `json:"delta,omitempty"`
}

// BenchmarkOf aggregates the arms of one iteration.
func BenchmarkOf(runs []CollectedRun, iteration int) Benchmark {
	bench := Benchmark{
		Iteration:  iteration,
		Generated:  timestamp(),
		RunSummary: map[string]ArmSummary{},
		Assertions: "none yet — pass_rate unavailable until assertions are added to evals.json",
	}
	for _, arm := range AllArms {
		var durations, tokens []int64
		failed := 0
		for _, run := range runs {
			data, ok := run.Arms[arm]
			if !ok || !timingPresent(data.Timing) {
				continue
			}
			durations = append(durations, data.Timing.DurationMS)
			if data.Timing.TotalTokens != nil {
				tokens = append(tokens, int64(*data.Timing.TotalTokens))
			} else {
				tokens = append(tokens, 0)
			}
			if data.Timing.ExitCode != nil && *data.Timing.ExitCode != 0 {
				failed++
			}
		}
		if len(durations) == 0 {
			continue
		}
		bench.RunSummary[arm] = ArmSummary{
			Cases:       len(durations),
			FailedRuns:  failed,
			DurationMS:  Stat{Mean: mean(durations), Values: durations},
			TotalTokens: Stat{Mean: mean(tokens), Values: tokens},
		}
	}
	if with, ok := bench.RunSummary[ArmWith]; ok {
		if without, ok := bench.RunSummary[ArmWithout]; ok {
			bench.Delta = &Delta{
				DurationMS:  with.DurationMS.Mean - without.DurationMS.Mean,
				TotalTokens: with.TotalTokens.Mean - without.TotalTokens.Mean,
			}
		}
	}
	return bench
}

// timingPresent mirrors the old Python runner's truthiness test on timing.json: an arm directory without one
// contributes nothing to the benchmark.
func timingPresent(t Timing) bool {
	return t.RunRoot != "" || t.DurationMS != 0 || t.ExitCode != nil || t.TotalTokens != nil
}

func mean(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	var sum int64
	for _, v := range values {
		sum += v
	}
	return sum / int64(len(values))
}

// Report renders report.md — the thing a human actually reads after an iteration.
func Report(runs []CollectedRun, bench Benchmark, skill string, iteration int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s · eval iteration %d\n\n", skill, iteration)
	fmt.Fprintf(&b, "Generated %s · %d case(s) · arms: with_skill / without_skill · evalctl %s\n\n",
		bench.Generated, len(runs), version.Resolve())
	b.WriteString("No assertions exist yet, so there is no pass rate. Read the expected output next to what " +
		"each arm actually produced, then write your verdict into `feedback.json`.\n\n")
	b.WriteString("## Timing\n\n")
	b.WriteString("| Arm | Cases | Failed runs | Mean duration (s) | Mean tokens |\n")
	b.WriteString("|---|---|---|---|---|\n")
	arms := make([]string, 0, len(bench.RunSummary))
	for arm := range bench.RunSummary {
		arms = append(arms, arm)
	}
	sort.Slice(arms, func(i, j int) bool {
		if (arms[i] == ArmWith) != (arms[j] == ArmWith) {
			return arms[i] == ArmWith
		}
		return arms[i] < arms[j]
	})
	for _, arm := range arms {
		s := bench.RunSummary[arm]
		fmt.Fprintf(&b, "| %s | %d | %d | %.1f | %s |\n", arm, s.Cases, s.FailedRuns,
			float64(s.DurationMS.Mean)/1000, thousands(s.TotalTokens.Mean))
	}
	if bench.Delta != nil {
		sign := ""
		if bench.Delta.DurationMS >= 0 {
			sign = "+"
		}
		fmt.Fprintf(&b, "\nDelta (with − without): %s%.1fs, %s tokens\n", sign,
			float64(bench.Delta.DurationMS)/1000, signedThousands(bench.Delta.TotalTokens))
	}

	for _, run := range runs {
		fmt.Fprintf(&b, "\n## %s · %s case %d\n\n", run.Slug, run.Skill, run.ID)
		fmt.Fprintf(&b, "**Prompt** — %s\n\n**Expected** — %s\n\n", oneLine(run.Prompt), run.ExpectedOutput)
		for _, arm := range AllArms {
			data, ok := run.Arms[arm]
			if !ok {
				continue
			}
			t := data.Timing
			cont := data.Checks.Containment
			guard := data.Checks.RootGuard
			loaded := data.Checks.SkillsLoaded
			verify := data.Checks.Verify

			exit := "?"
			if t.ExitCode != nil {
				exit = fmt.Sprintf("%d", *t.ExitCode)
			}
			tokens := "?"
			if t.TotalTokens != nil {
				tokens = fmt.Sprintf("%d", *t.TotalTokens)
			}
			errorSuffix := ""
			if t.Error != nil && *t.Error != "" {
				errorSuffix = ", error: " + *t.Error
			}
			fmt.Fprintf(&b, "### %s\n\n", arm)
			fmt.Fprintf(&b, "- run: exit %s, %.1fs, %s tokens%s\n", exit, float64(t.DurationMS)/1000, tokens, errorSuffix)
			fmt.Fprintf(&b, "- files changed: %d (new %d, modified %d, deleted %d)\n", data.Changes.Count,
				len(data.Changes.New), len(data.Changes.Modified), len(data.Changes.Deleted))
			if cont.OK {
				b.WriteString("- containment: ok — everything inside MC-*/ or _archive/\n")
			} else {
				fmt.Fprintf(&b, "- containment: FAILED — wrote outside the workspace: %s\n",
					strings.Join(cont.Outside, ", "))
			}
			if guard.OK || guard.Root == "" {
				b.WriteString("- real masterclass root: untouched\n")
			} else {
				touched := append(append(append([]string{}, guard.Added...), guard.Changed...), guard.Removed...)
				fmt.Fprintf(&b, "- real masterclass root: TOUCHED — %s\n", strings.Join(touched, ", "))
			}
			if len(loaded) == 0 {
				b.WriteString("- skills loaded: none\n")
			} else {
				fmt.Fprintf(&b, "- skills loaded: %s\n", strings.Join(loaded, ", "))
			}
			for _, entry := range verify {
				status := "fail"
				if entry.Report != nil && entry.Report["failed"] == float64(0) {
					status = "pass"
				} else if entry.Error != "" {
					status = "error"
				}
				fmt.Fprintf(&b, "- `verify %s`: %s\n", entry.Workspace, status)
			}
			if len(data.Changes.New) > 0 {
				fmt.Fprintf(&b, "- new files: %s%s\n", codeList(data.Changes.New, 12),
					ellipsis(len(data.Changes.New), 12))
			}
			if len(data.Changes.Modified) > 0 {
				paths := make([]string, 0, len(data.Changes.Modified))
				for p := range data.Changes.Modified {
					paths = append(paths, p)
				}
				sort.Strings(paths)
				quoted := make([]string, 0, len(paths))
				for _, p := range paths {
					quoted = append(quoted, "`"+p+"`")
				}
				if len(quoted) > 12 {
					quoted = quoted[:12]
				}
				fmt.Fprintf(&b, "- modified: %s\n", strings.Join(quoted, ", "))
			}
			if response := strings.TrimSpace(data.Response); response != "" {
				fmt.Fprintf(&b, "\nFinal response:\n\n> %s\n", indentQuote(truncateText(response, 1800)))
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func codeList(values map[string]int64, limit int) string {
	paths := make([]string, 0, len(values))
	for p := range values {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	if len(paths) > limit {
		paths = paths[:limit]
	}
	quoted := make([]string, 0, len(paths))
	for _, p := range paths {
		quoted = append(quoted, "`"+p+"`")
	}
	return strings.Join(quoted, ", ")
}

func ellipsis(n, limit int) string {
	if n > limit {
		return " …"
	}
	return ""
}

func indentQuote(s string) string {
	return strings.ReplaceAll(s, "\n", "\n> ")
}

// ---------------------------------------------------------------- trigger scoring

// Tally is a pass count over the rows that could be judged.
type Tally struct {
	Rows       int      `json:"rows"`
	Judged     int      `json:"judged"`
	Passed     int      `json:"passed"`
	Rate       *float64 `json:"rate"`
	Unmeasured int      `json:"unmeasured"`
}

// ScoreRow is one query's measured trigger rate.
type ScoreRow struct {
	ID            int        `json:"id"`
	Skill         string     `json:"skill"`
	Query         string     `json:"query"`
	Why           string     `json:"why"`
	ShouldTrigger bool       `json:"should_trigger"`
	Split         string     `json:"split"`
	Runs          int        `json:"runs"`
	ValidRuns     int        `json:"valid_runs"`
	Triggers      int        `json:"triggers"`
	LoadedSets    [][]string `json:"loaded_sets"`
	Others        []string   `json:"others"`
	EarlyStops    int        `json:"early_stops"`
	RootGuardOK   bool       `json:"root_guard_ok"`
	DurationMS    []int64    `json:"duration_ms"`
	TotalTokens   []*int     `json:"total_tokens"`
	Stderr        string     `json:"stderr"`
	Rate          *float64   `json:"rate"`
	Passed        *bool      `json:"passed"`
}

// SkillScore is the per-skill breakdown of one iteration.
type SkillScore struct {
	Positives     Tally `json:"positives"`
	Negatives     Tally `json:"negatives"`
	All           Tally `json:"all"`
	Loop          Tally `json:"loop"`              // train + validation only: the number the loop ranks on
	Holdout       Tally `json:"holdout,omitempty"` // the fresh set, measured on its own
	FalseTriggers []int `json:"false_triggers"`
	Missed        []int `json:"missed"`
}

// TriggerScore is the lane's verdict for an iteration.
type TriggerScore struct {
	Threshold   float64               `json:"threshold"`
	Runs        int                   `json:"runs"`
	Rows        []ScoreRow            `json:"rows"`
	Skills      map[string]SkillScore `json:"skills"`
	Splits      map[string]Tally      `json:"splits"`
	Overall     Tally                 `json:"overall"`
	Loop        Tally                 `json:"loop"`
	RootGuardOK bool                  `json:"root_guard_ok"`
}

func tally(pool []ScoreRow) Tally {
	out := Tally{Rows: len(pool)}
	for _, row := range pool {
		if row.Passed == nil {
			continue
		}
		out.Judged++
		if *row.Passed {
			out.Passed++
		}
	}
	out.Unmeasured = out.Rows - out.Judged
	if out.Judged > 0 {
		rate := float64(out.Passed) / float64(out.Judged)
		out.Rate = &rate
	}
	return out
}

// ScoreTrigger is the trigger rate per query, then the guide's pass rule: above the threshold on
// positives, below it on negatives.
func (r *Runner) ScoreTrigger(cases []TriggerCase, records map[int][]*TriggerRecord, runs int,
	threshold float64) TriggerScore {
	rows := make([]ScoreRow, 0, len(cases))
	for _, c := range cases {
		all := records[c.ID]
		var observed []*TriggerRecord
		attempted := 0
		for _, rec := range all {
			if rec == nil {
				continue
			}
			attempted++
			if rec.Evidence {
				observed = append(observed, rec)
			}
		}
		row := ScoreRow{
			ID: c.ID, Skill: c.Skill, Query: c.Query, Why: c.Why, ShouldTrigger: c.ShouldTrigger,
			Split: c.Split, Runs: attempted, ValidRuns: len(observed),
			RootGuardOK: true, LoadedSets: [][]string{}, Others: []string{},
			DurationMS: []int64{}, TotalTokens: []*int{},
		}
		for _, rec := range observed {
			if rec.TargetLoaded {
				row.Triggers++
			}
			loaded := rec.Loaded
			if loaded == nil {
				loaded = []string{}
			}
			row.LoadedSets = append(row.LoadedSets, loaded)
			for _, other := range rec.OtherLoaded {
				if !contains(row.Others, other) {
					row.Others = append(row.Others, other)
				}
			}
			if rec.EarlyStop != nil {
				row.EarlyStops++
			}
			if !rec.RootGuard.OK {
				row.RootGuardOK = false
			}
			row.DurationMS = append(row.DurationMS, rec.DurationMS)
			row.TotalTokens = append(row.TotalTokens, rec.TotalTokens)
			if row.Stderr == "" && rec.Stderr != "" {
				row.Stderr = rec.Stderr
			}
		}
		sort.Strings(row.Others)
		if row.Stderr == "" {
			// The launch that died is exactly the one with no evidence, so its stderr is read
			// from every attempt, not just the scored ones.
			for _, rec := range all {
				if rec != nil && rec.Stderr != "" {
					row.Stderr = rec.Stderr
					break
				}
			}
		}
		if len(observed) == 0 {
			row.Rate, row.Passed = nil, nil
		} else {
			rate := float64(row.Triggers) / float64(len(observed))
			row.Rate = &rate
			passed := rate < threshold
			if c.ShouldTrigger {
				passed = rate > threshold
			}
			row.Passed = &passed
		}
		rows = append(rows, row)
	}

	skills := map[string]SkillScore{}
	for _, skill := range r.Profiles.Skills {
		var positives, negatives, pool, holdout, loop []ScoreRow
		for _, row := range rows {
			if row.Skill != skill {
				continue
			}
			pool = append(pool, row)
			if row.Split == SplitHoldout {
				holdout = append(holdout, row)
			} else {
				loop = append(loop, row)
			}
			if row.ShouldTrigger {
				positives = append(positives, row)
			} else {
				negatives = append(negatives, row)
			}
		}
		if len(pool) == 0 {
			continue
		}
		entry := SkillScore{Positives: tally(positives), Negatives: tally(negatives), All: tally(pool),
			Loop: tally(loop), Holdout: tally(holdout),
			FalseTriggers: []int{}, Missed: []int{}}
		for _, row := range negatives {
			if row.Passed != nil && !*row.Passed {
				entry.FalseTriggers = append(entry.FalseTriggers, row.ID)
			}
		}
		for _, row := range positives {
			if row.Passed != nil && !*row.Passed {
				entry.Missed = append(entry.Missed, row.ID)
			}
		}
		skills[skill] = entry
	}

	splits := map[string]Tally{}
	for _, name := range append(append([]string{}, LoopSplits...), SplitHoldout) {
		var pool []ScoreRow
		for _, row := range rows {
			if row.Split == name {
				pool = append(pool, row)
			}
		}
		splits[name] = tally(pool)
	}
	var loopRows []ScoreRow
	for _, row := range rows {
		if row.Split != SplitHoldout {
			loopRows = append(loopRows, row)
		}
	}
	overall := tally(rows)
	guardOK := true
	for _, row := range rows {
		if !row.RootGuardOK {
			guardOK = false
		}
	}
	return TriggerScore{Threshold: threshold, Runs: runs, Rows: rows, Skills: skills,
		Splits: splits, Overall: overall, Loop: tally(loopRows), RootGuardOK: guardOK}
}

// CollectTriggerRuns reads the lane back from disk, so a resumed or filtered invocation still
// scores everything.
func (r *Runner) CollectTriggerRuns(cases []TriggerCase, iterDir string, runs int) map[int][]*TriggerRecord {
	out := map[int][]*TriggerRecord{}
	for _, c := range cases {
		caseDir := filepath.Join(iterDir, caseDirName(c))
		found := make([]*TriggerRecord, 0, runs)
		for n := 1; n <= runs; n++ {
			path := filepath.Join(caseDir, fmt.Sprintf("run-%d", n), "trigger.json")
			var record TriggerRecord
			if err := readJSONFile(path, &record); err == nil {
				copied := record
				found = append(found, &copied)
				continue
			}
			found = append(found, nil)
		}
		out[c.ID] = found
	}
	return out
}

func caseDirName(c TriggerCase) string {
	return fmt.Sprintf("q%02d-%s", c.ID, c.Skill)
}

// TriggerReport renders report.md for the trigger lane.
func TriggerReport(score TriggerScore, iteration int, lint []LintRow) string {
	var b strings.Builder
	pct := func(entry Tally) string {
		if entry.Rate == nil {
			return "—"
		}
		return fmt.Sprintf("%.0f%% (%d/%d)", *entry.Rate*100, entry.Passed, entry.Judged)
	}
	fmt.Fprintf(&b, "# Trigger eval %d\n\n", iteration)
	fmt.Fprintf(&b, "Generated %s · %d queries × %d runs · pass threshold %v · profile `%s` (all four skills installed) · evalctl %s\n\n",
		timestamp(), score.Overall.Rows, score.Runs, trimFloat(score.Threshold), profileNameTrigger, version.Resolve())
	fmt.Fprintf(&b, "**Pass rate %s** — train %s, validation %s. A query passes when its trigger rate is "+
		"above the threshold (should-trigger) or below it (should-not-trigger).\n\n",
		pct(score.Overall), pct(score.Splits["train"]), pct(score.Splits["validation"]))

	if score.Overall.Unmeasured > 0 {
		fmt.Fprintf(&b, "%d query(ies) had no usable run — a launch that dies before its first tool call is "+
			"not a failed trigger:\n\n", score.Overall.Unmeasured)
		for _, row := range score.Rows {
			if row.Passed != nil {
				continue
			}
			detail := "no stderr"
			if line := strings.TrimSpace(row.Stderr); line != "" {
				detail = strings.TrimSpace(strings.Split(line, "\n")[0])
			}
			fmt.Fprintf(&b, "- q%02d `%s`: %s\n", row.ID, row.Skill, detail)
		}
		b.WriteString("\n")
	}
	if !score.RootGuardOK {
		b.WriteString("**The production masterclass root was touched by a run.**\n\n")
	}

	b.WriteString("## By skill\n\n| Skill | Should trigger | Should not trigger | All |\n|---|---|---|---|\n")
	for _, skill := range sortedKeys(score.Skills) {
		data := score.Skills[skill]
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", skill, pct(data.Positives), pct(data.Negatives), pct(data.All))
	}

	b.WriteString("\n## Descriptions under test\n\n")
	fmt.Fprintf(&b, "The guide's principles as advisories — triggering is the verdict, this explains it. "+
		"Hard limit %d chars.\n\n", DescriptionLimit)
	b.WriteString("| Skill | Chars | ≤ limit | Imperative phrasing | Covers unstated contexts |\n|---|---|---|---|---|\n")
	for _, row := range lint {
		fmt.Fprintf(&b, "| `%s` | %d | %s | %s | %s |\n", row.Skill, row.Chars, yesNo(row.LimitOK),
			yesNo(row.Imperative), yesNo(row.CoversUnstated))
	}
	b.WriteString("\n```\n")
	for _, row := range lint {
		fmt.Fprintf(&b, "%s: %s\n", row.Skill, row.Description)
	}
	b.WriteString("```\n\n")

	var failures []ScoreRow
	for _, row := range score.Rows {
		if row.Passed != nil && !*row.Passed {
			failures = append(failures, row)
		}
	}
	b.WriteString("## Failures\n\n")
	if len(failures) == 0 {
		b.WriteString("None — every query landed on the right side of the threshold.\n\n")
	}
	for _, row := range failures {
		diagnosis := "Description too broad — the query shares this skill's vocabulary but needs something else."
		if row.ShouldTrigger {
			diagnosis = "Description too narrow — the query asks for what this skill does without naming it, " +
				"and the description doesn't cover that context."
		}
		others := ""
		if len(row.Others) > 0 {
			quoted := make([]string, 0, len(row.Others))
			for _, s := range row.Others {
				quoted = append(quoted, "`"+s+"`")
			}
			others = " Other skills opened: " + strings.Join(quoted, ", ") + "."
		}
		expected := "no load"
		if row.ShouldTrigger {
			expected = "a load"
		}
		fmt.Fprintf(&b, "- **q%02d `%s`** — trigger rate %d/%d; expected %s. %s%s\n",
			row.ID, row.Skill, row.Triggers, row.ValidRuns, expected, diagnosis, others)
		fmt.Fprintf(&b, "  - query: %s\n", row.Query)
		why := row.Why
		if why == "" {
			why = "—"
		}
		fmt.Fprintf(&b, "  - why it's in the set: %s\n", why)
	}
	b.WriteString("\n## Every query\n\n")
	b.WriteString("| # | Skill | Should | Split | Rate | Loaded (per run) | Verdict |\n|---|---|---|---|---|---|---|\n")
	for _, row := range score.Rows {
		rate := "—"
		if row.Rate != nil {
			rate = fmt.Sprintf("%d/%d", row.Triggers, row.ValidRuns)
		}
		loaded := "—"
		if len(row.LoadedSets) > 0 {
			sets := make([]string, 0, len(row.LoadedSets))
			for _, set := range row.LoadedSets {
				if len(set) == 0 {
					sets = append(sets, "none")
					continue
				}
				sets = append(sets, strings.Join(set, ","))
			}
			loaded = strings.Join(sets, " · ")
		}
		verdict := "—"
		if row.Passed != nil {
			verdict = "FAIL"
			if *row.Passed {
				verdict = "pass"
			}
		}
		should := "no"
		if row.ShouldTrigger {
			should = "yes"
		}
		fmt.Fprintf(&b, "| %d | `%s` | %s | %s | %s | %s | %s |\n",
			row.ID, row.Skill, should, row.Split, rate, loaded, verdict)
	}
	b.WriteString("\n## Next step\n\n")
	b.WriteString("Change the descriptions of the skills whose queries fail on the **train** split only, then " +
		"re-run the whole set. The validation pass rate is the number to compare between iterations — the " +
		"best description is not necessarily the last one. Do not tune against validation failures, and " +
		"don't paste query keywords into a description; find the context they represent.\n")
	return b.String()
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func sortedKeys(m map[string]SkillScore) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func trimFloat(f float64) string {
	return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.2f", f), "0"), ".")
}

// signedThousands is Python's ":+," — the sign always shows, because a delta of zero means
// something different from a missing delta.
func signedThousands(n int64) string {
	if n < 0 {
		return "-" + thousands(-n)
	}
	return "+" + thousands(n)
}

func thousands(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	out := strings.Join(parts, ",")
	if neg {
		return "-" + out
	}
	return out
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

func timestamp() string { return time.Now().Format("2006-01-02T15:04:05-0700") }

// ReadReport returns an iteration's report.md text.
func ReadReport(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
