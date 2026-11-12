package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"evalctl/internal/profile"
	"evalctl/internal/version"
)

// RunTrigger is the trigger lane: one skill's selected queries N times against the trigger profile,
// then the rate, the pass rule and the report. Nothing is staged, so a trigger run never touches a
// case fixture — but its results land in that skill's workspace, beside its output iterations.
func (r *Runner) RunTrigger(ctx context.Context, o Options) error {
	if o.Budget <= 0 {
		o.Budget = DefaultTriggerBudget
	}
	if o.MaxTurns <= 0 {
		o.MaxTurns = DefaultTriggerTurns
	}
	if o.Split == "" {
		o.Split = "all"
	}
	if o.Split != "all" && !KnownSplit(o.Split) {
		return fmt.Errorf("--split must be all, train, validation or holdout")
	}
	skills, err := r.ResolveSkills(o.Skills)
	if err != nil {
		return err
	}
	// A trigger iteration belongs to one skill's workspace, so the lane is scoped like the case
	// lanes: measuring several skills at once is several iterations, one per skill.
	if len(skills) != 1 {
		return fmt.Errorf("a trigger iteration measures one skill's set — pass --skill (%s)",
			strings.Join(skills, ", "))
	}
	skill := skills[0]
	cfg := r.TriggerConfigOf(skill)
	if o.Runs <= 0 {
		o.Runs = cfg.Runs
	}
	if o.Threshold <= 0 {
		o.Threshold = cfg.Threshold
	}
	only := idSet(o.QueryIDs)
	cases, err := r.LoadTriggerQueries(skills, only, o.Split)
	if err != nil {
		return err
	}
	if len(cases) == 0 {
		return errors.New("no trigger queries selected")
	}
	if o.List {
		for _, c := range cases {
			label := "no trigger"
			if c.ShouldTrigger {
				label = "trigger"
			}
			r.message("q%02d · %-15s · %-10s · %10s", c.ID, c.Skill, c.Split, label)
			r.message("  %s", c.Query)
		}
		r.message("\n%d queries · %d run(s) each · threshold %v", len(cases), o.Runs, trimFloat(o.Threshold))
		return nil
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.Preflight(o); err != nil {
		return err
	}

	iteration := o.Iteration
	if iteration == 0 {
		iteration = r.NextIteration(r.TriggerWorkspace(skill), "trigger")
	}

	// A batch nobody is waiting for provisions nothing: a cancelled context is checked before the
	// profile is written and before the first query is launched.
	if err := ctx.Err(); err != nil {
		return err
	}

	iterDir := r.TriggerIterDir(skill, iteration)
	if fileExists(iterDir) && !o.DryRun && !o.Resume && !o.Force {
		return fmt.Errorf("%s already exists — rerun with --resume, --force, or pick another --iteration",
			r.relative(iterDir))
	}

	start := fmt.Sprintf("trigger eval %s/trigger-%d · %d quer(y/ies) × %d run(s) · threshold %v · profile %s · evalctl %s",
		skill, iteration, len(cases), o.Runs, trimFloat(o.Threshold), r.Profiles.Name(profile.ArmTrigger), version.Resolve().Short())
	if o.DryRun {
		start += " · DRY RUN"
	}
	r.emit(Event{Kind: EventStart, Lane: LaneTrigger, Message: start})

	if !o.DryRun {
		target := r.Profiles.Name(profile.ArmTrigger)
		if err := r.provisionSandboxes([]string{target}); err != nil {
			return err
		}
		defer r.releaseSandboxes([]string{target}, o.Keep)
		line, err := r.Profiles.SyncSkills(target, r.Profiles.Skills, false)
		if err != nil {
			return err
		}
		r.message("%s", line)
	}

	startedAt := timestamp()
	for _, c := range cases {
		// A cancelled batch stops before it starts its next query. The context is the operator's
		// cancel and the run's budget at once, so once it is done every later launch would die
		// inside its first millisecond and still write a scored record — a batch that cancels
		// itself through to the end and reports itself finished. Aborting here leaves the iteration
		// without meta.json, which is how the lab reads "this run measured nothing".
		if err := ctx.Err(); err != nil {
			r.markAborted(iterDir, err)
			return err
		}
		caseDir := filepath.Join(iterDir, caseDirName(c))
		r.emit(Event{Kind: EventCase, Lane: LaneTrigger, Case: caseDirName(c),
			Message: fmt.Sprintf("q%02d · %s · should_trigger=%v · %s", c.ID, c.Skill, c.ShouldTrigger, c.Split)})
		r.message("  %s", clip(c.Query, 118))
		if !o.DryRun {
			if err := os.MkdirAll(caseDir, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(caseDir, "query.txt"), []byte(c.Query+"\n"), 0o644); err != nil {
				return err
			}
		}
		for n := 1; n <= o.Runs; n++ {
			if err := ctx.Err(); err != nil {
				r.markAborted(iterDir, err)
				return err
			}
			runDir := filepath.Join(caseDir, fmt.Sprintf("run-%d", n))
			if o.DryRun {
				turns := o.MaxTurns
				if !c.ShouldTrigger && turns > NegativeTurns {
					turns = NegativeTurns
				}
				cmd := r.buildTriggerCmd(filepath.Join(caseDir, "query.txt"), "<tmp>/work", o, turns)
				r.emit(Event{Kind: EventPlan, Lane: LaneTrigger, Case: caseDirName(c), Arm: fmt.Sprintf("run-%d", n),
					Message: "  " + strings.Join(cmd, " ")})
				continue
			}
			r.emit(Event{Kind: EventTriggerRun, Lane: LaneTrigger, Case: caseDirName(c),
				Arm: fmt.Sprintf("run-%d", n)})
			record, err := r.RunTriggerCase(ctx, c, runDir, o)
			if err != nil {
				return fmt.Errorf("q%02d run %d: %w", c.ID, n, err)
			}
			mark := "not loaded"
			if record.TargetLoaded {
				mark = "loaded"
			}
			extra := ""
			if record.EarlyStop != nil {
				extra += " (early stop)"
			}
			if len(record.OtherLoaded) > 0 {
				extra += ", also: " + strings.Join(record.OtherLoaded, ", ")
			}
			if !record.Evidence {
				extra += ", no evidence"
			}
			if record.Error != nil && *record.Error != "" {
				extra += " — " + clip(*record.Error, 200)
			}
			r.message("  run %d: %s%s, %.1fs", n, mark, extra, float64(record.DurationMS)/1000)
		}
	}

	if o.DryRun {
		r.emit(Event{Kind: EventDone, Lane: LaneTrigger,
			Message: "Dry run: nothing was written. Drop --dry-run to execute."})
		return nil
	}

	// A cancel that lands during the last run gets here too: the record is on disk for the reader,
	// but a stopped batch is not a scored one, and scoring it would put a rate over half the set
	// into the lab's ranking.
	if err := ctx.Err(); err != nil {
		r.markAborted(iterDir, err)
		return err
	}

	// Score the whole iteration, not just this invocation's slice: a filtered or resumed run
	// accumulates into one iteration, so the report must cover every query that has data on disk.
	allCases, err := r.LoadTriggerQueries(skills, nil, "all")
	if err != nil {
		return err
	}
	records := r.CollectTriggerRuns(allCases, iterDir, o.Runs)
	// Every record on disk but none with evidence is the failure this lane used to report as a rate
	// of zero: the launches happened, nothing the harness can read came back. Refuse to score it.
	evidence := 0
	for _, list := range records {
		for _, rec := range list {
			if rec != nil && rec.Evidence {
				evidence++
			}
		}
	}
	if evidence == 0 {
		err := fmt.Errorf("no run produced evidence — every launch came back with no tool call and no result. "+
			"Run one by hand: hermes -p %s chat --oneshot --query-file … --format stream-json",
			r.Profiles.Name(profile.ArmTrigger))
		r.markAborted(iterDir, err)
		return err
	}
	scored := make([]TriggerCase, 0, len(allCases))
	for _, c := range allCases {
		for _, rec := range records[c.ID] {
			if rec != nil {
				scored = append(scored, c)
				break
			}
		}
	}
	score := r.ScoreTrigger(scored, records, o.Runs, o.Threshold)
	score.Profile = r.Profiles.Name(profile.ArmTrigger)
	lint := r.LintDescriptions(sortedSkills(scored))

	triggerJSON := struct {
		Score   TriggerScore                `json:"score"`
		Records map[string][]*TriggerRecord `json:"records"`
	}{Score: score, Records: map[string][]*TriggerRecord{}}
	for id, list := range records {
		triggerJSON.Records[fmt.Sprintf("%d", id)] = list
	}
	if err := writeJSONFile(filepath.Join(iterDir, "trigger.json"), triggerJSON); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(iterDir, "report.md"), []byte(TriggerReport(score, iteration, lint)), 0o644); err != nil {
		return err
	}

	_, contractSHA, err := r.Profiles.Contract()
	if err != nil {
		return err
	}
	triggerFileSHA := ""
	if b, err := os.ReadFile(r.Store.TriggerPathFor(skill)); err == nil {
		triggerFileSHA = sha256Hex(b)
	}
	descriptions := map[string]struct {
		Chars       int    `json:"chars"`
		Description string `json:"description"`
	}{}
	for _, row := range lint {
		descriptions[row.Skill] = struct {
			Chars       int    `json:"chars"`
			Description string `json:"description"`
		}{row.Chars, row.Description}
	}
	treeSHA := map[string]string{}
	for _, skill := range r.Profiles.Skills {
		if sha, err := r.Profiles.TreeHash(skill); err == nil {
			treeSHA[skill] = sha
		}
	}
	meta := metaTrigger{Iteration: iteration, Lane: LaneTrigger, Skill: skill, Started: startedAt,
		Model: orDefault(o.Model, "default"), Runs: o.Runs, Threshold: o.Threshold, Split: o.Split,
		BudgetS: int(o.Budget.Seconds()), MaxTurns: o.MaxTurns, Queries: []string{}}
	for _, c := range cases {
		meta.Queries = append(meta.Queries, caseDirName(c))
	}
	meta.Provenance.ContractSHA256 = contractSHA
	meta.Provenance.TriggerEvalsSHA256 = triggerFileSHA
	triggerName := r.Profiles.Name(profile.ArmTrigger)
	meta.Provenance.Profile.Name = triggerName
	meta.Provenance.Profile.SoulSHA256 = r.Profiles.SoulSHA(triggerName)
	meta.Provenance.Profile.Skills = r.Profiles.InstalledSkills(triggerName)
	meta.Provenance.Descriptions = descriptions
	meta.Provenance.SkillTreeSHA256 = treeSHA
	meta.Provenance.Tool = toolInfo()
	if err := writeJSONFile(filepath.Join(iterDir, "meta.json"), meta); err != nil {
		return err
	}

	msg := fmt.Sprintf("%s · overall %s · train %s · validation %s · wrote %s/report.md",
		skill, pctText(score.Overall), pctText(score.Splits["train"]), pctText(score.Splits["validation"]),
		r.relative(iterDir))
	r.emit(Event{Kind: EventScored, Lane: LaneTrigger, Case: fmt.Sprintf("trigger-%d", iteration), Message: msg})
	r.emit(Event{Kind: EventDone, Lane: LaneTrigger, Case: fmt.Sprintf("trigger-%d", iteration), Message: msg})
	return nil
}

type metaTrigger struct {
	Iteration  int      `json:"iteration"`
	Skill      string   `json:"skill"`
	Lane       string   `json:"lane"`
	Started    string   `json:"started"`
	Model      string   `json:"model"`
	Runs       int      `json:"runs"`
	Threshold  float64  `json:"threshold"`
	Split      string   `json:"split"`
	BudgetS    int      `json:"budget_s"`
	MaxTurns   int      `json:"max_turns"`
	Queries    []string `json:"queries"`
	Provenance struct {
		ContractSHA256     string `json:"contract_sha256"`
		TriggerEvalsSHA256 string `json:"trigger_evals_sha256"`
		Profile            struct {
			Name       string   `json:"name"`
			SoulSHA256 *string  `json:"soul_sha256"`
			Skills     []string `json:"skills"`
		} `json:"profile"`
		Descriptions map[string]struct {
			Chars       int    `json:"chars"`
			Description string `json:"description"`
		} `json:"descriptions"`
		SkillTreeSHA256 map[string]string `json:"skill_tree_sha256"`
		Tool            metaTool          `json:"tool"`
	} `json:"provenance"`
}

func pctText(t Tally) string {
	if t.Rate == nil {
		return "—"
	}
	return fmt.Sprintf("%.0f%% (%d/%d)", *t.Rate*100, t.Passed, t.Judged)
}

func sortedSkills(cases []TriggerCase) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range cases {
		if !seen[c.Skill] {
			seen[c.Skill] = true
			out = append(out, c.Skill)
		}
	}
	sort.Strings(out)
	return out
}

func clip(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}

// markAborted says on disk why an iteration dir stopped short, so the leftover is a record instead
// of junk: no meta.json (nothing was scored, and the lab reads meta.json to decide that), and a
// file that answers "what is this directory" without the operator having to guess.
func (r *Runner) markAborted(iterDir string, err error) {
	// fileExists is a file check, and this is a directory: stat it directly.
	if info, statErr := os.Stat(iterDir); statErr != nil || !info.IsDir() {
		return
	}
	_ = writeJSONFile(filepath.Join(iterDir, "aborted.json"), map[string]any{
		"aborted": true,
		"reason":  err.Error(),
		"at":      timestamp(),
		"note":    "the batch stopped before it scored: this iteration measured nothing and the lab skips it",
	})
}
