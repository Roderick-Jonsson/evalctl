package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RunQuality is the output lane: every selected case through every selected arm, then the
// benchmark and the report.
func (r *Runner) RunQuality(ctx context.Context, o Options) error {
	if err := o.Normalize(); err != nil {
		return err
	}
	skills, err := r.ResolveSkills(o.Skills)
	if err != nil {
		return err
	}
	only := idSet(o.CaseIDs)
	cases, err := LoadCases(r.Store, skills, only)
	if err != nil {
		return err
	}
	if len(cases) == 0 {
		return errors.New("no cases selected")
	}
	if o.List {
		for _, c := range cases {
			staged := "none (prompt only)"
			if len(c.Files) > 0 {
				staged = fmt.Sprintf("%d file(s)", len(c.Files))
			}
			r.message("%s  ·  %s case %d  ·  stages %s", c.Slug, c.Skill, c.ID, staged)
			r.message("  %s", oneLine(c.Prompt))
		}
		return nil
	}

	iteration := o.Iteration
	if iteration == 0 {
		iteration = r.NextIteration("iteration")
	}
	iterDir := r.IterDir("iteration", iteration)
	if fileExists(iterDir) && !o.DryRun && !o.Resume && !o.Force {
		return fmt.Errorf("evals/results/iteration-%d already exists — rerun with --resume, "+
			"--force, or pick another --iteration", iteration)
	}
	if !o.Resume {
		for _, c := range cases {
			caseDir := filepath.Join(iterDir, c.Slug)
			if o.Force {
				_ = os.RemoveAll(caseDir)
			}
		}
	}

	profiles := map[string]bool{}
	for _, arm := range o.Arms {
		profiles[ArmProfiles[arm]] = true
	}
	profileList := keysOf(profiles)

	start := fmt.Sprintf("iteration %d · %d case(s) × %d arm(s) · profiles %s",
		iteration, len(cases), len(o.Arms), strings.Join(profileList, ", "))
	if o.DryRun {
		start += " · DRY RUN"
	}
	r.emit(Event{Kind: EventStart, Lane: LaneOutput, Message: start})

	if !o.DryRun {
		// Both arms run the same contract; the with arm holds exactly the skill under test, the
		// baseline holds none, and neither keeps memory from a previous case.
		for _, name := range profileList {
			if line, err := r.Profiles.WriteEnv(name, false); err != nil {
				return err
			} else if line != "" {
				r.message("%s", line)
			}
			if line, err := r.Profiles.Render(name, false); err != nil {
				return err
			} else {
				r.message("%s", line)
			}
			if line, err := r.Profiles.Scrub(name, false); err != nil {
				return err
			} else {
				r.message("%s", line)
			}
		}
	}

	treeSHA := map[string]string{}
	startedAt := timestamp()
	for _, c := range cases {
		r.emit(Event{Kind: EventCase, Lane: LaneOutput, Case: c.Slug,
			Message: fmt.Sprintf("%s  ·  %s case %d", c.Slug, c.Skill, c.ID)})
		if sha, err := r.Profiles.TreeHash(c.Skill); err == nil {
			treeSHA[c.Slug] = sha
		}
		if !o.DryRun && contains(o.Arms, ArmWith) {
			line, err := r.Profiles.SyncSkills(ArmProfiles[ArmWith], []string{c.Skill}, false)
			if err != nil {
				return err
			}
			r.message("%s", line)
		}
		caseDir := filepath.Join(iterDir, c.Slug)
		if !o.DryRun {
			if err := os.MkdirAll(caseDir, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(caseDir, "prompt.txt"), []byte(c.Prompt+"\n"), 0o644); err != nil {
				return err
			}
		}
		for _, arm := range o.Arms {
			armDir := filepath.Join(caseDir, arm)
			if o.DryRun {
				runRoot := armDir
				if o.Isolate {
					runRoot = "<tmp>/work"
				}
				cmd := r.buildArmCmd(filepath.Join(caseDir, "prompt.txt"), runRoot, arm, o)
				r.emit(Event{Kind: EventPlan, Lane: LaneOutput, Case: c.Slug, Arm: arm,
					Message: "  " + strings.Join(cmd, " ")})
				continue
			}
			r.emit(Event{Kind: EventArmStart, Lane: LaneOutput, Case: c.Slug, Arm: arm})
			if _, err := r.RunArm(ctx, c, arm, armDir, o); err != nil {
				return fmt.Errorf("%s %s: %w", c.Slug, arm, err)
			}
		}
	}

	if o.DryRun {
		r.emit(Event{Kind: EventDone, Lane: LaneOutput, Message: "Dry run: nothing was written. Drop --dry-run to execute."})
		return nil
	}

	collected, err := r.CollectRuns(iterDir)
	if err != nil {
		return err
	}
	bench := BenchmarkOf(collected, iteration)
	if err := writeJSONFile(filepath.Join(iterDir, "benchmark.json"), bench); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(iterDir, "report.md"), []byte(Report(collected, bench, iteration)), 0o644); err != nil {
		return err
	}

	feedbackPath := filepath.Join(iterDir, "feedback.json")
	feedback := map[string]string{}
	_ = readJSONFile(feedbackPath, &feedback)
	for _, run := range collected {
		if _, ok := feedback[run.Slug]; !ok {
			feedback[run.Slug] = ""
		}
	}
	if err := writeJSONFile(feedbackPath, feedback); err != nil {
		return err
	}

	_, contractSHA, err := r.Profiles.Contract()
	if err != nil {
		return err
	}
	meta := metaQuality{Iteration: iteration, Started: startedAt, Model: orDefault(o.Model, "default"),
		Arms: o.Arms, BudgetS: int(o.Budget.Seconds()), Cases: []string{}}
	for _, run := range collected {
		meta.Cases = append(meta.Cases, run.Slug)
	}
	meta.Provenance.ContractSHA256 = contractSHA
	meta.Provenance.Profiles = map[string]metaProfile{}
	for _, arm := range o.Arms {
		name := ArmProfiles[arm]
		meta.Provenance.Profiles[arm] = metaProfile{Name: name, SoulSHA256: r.Profiles.SoulSHA(name),
			Skills: r.Profiles.InstalledSkills(name)}
	}
	meta.Provenance.SkillTreeSHA256 = map[string]string{}
	for slug, sha := range treeSHA {
		meta.Provenance.SkillTreeSHA256[slug] = sha
	}
	if err := writeJSONFile(filepath.Join(iterDir, "meta.json"), meta); err != nil {
		return err
	}

	failures := 0
	for _, run := range collected {
		for _, arm := range o.Arms {
			if data, ok := run.Arms[arm]; ok && data.Timing.ExitCode != nil && *data.Timing.ExitCode != 0 {
				failures++
			}
		}
	}
	msg := fmt.Sprintf("wrote evals/results/iteration-%d/report.md and benchmark.json", iteration)
	if failures > 0 {
		msg += fmt.Sprintf(" — %d run(s) did not exit 0, check stderr.txt in those arm directories", failures)
	}
	r.emit(Event{Kind: EventDone, Lane: LaneOutput, Case: fmt.Sprintf("iteration-%d", iteration), Message: msg})
	return nil
}

type metaProfile struct {
	Name       string   `json:"name"`
	SoulSHA256 *string  `json:"soul_sha256"`
	Skills     []string `json:"skills"`
}

type metaQuality struct {
	Iteration  int      `json:"iteration"`
	Started    string   `json:"started"`
	Model      string   `json:"model"`
	Arms       []string `json:"arms"`
	BudgetS    int      `json:"budget_s"`
	Cases      []string `json:"cases"`
	Provenance struct {
		ContractSHA256  string                 `json:"contract_sha256"`
		Profiles        map[string]metaProfile `json:"profiles"`
		SkillTreeSHA256 map[string]string      `json:"skill_tree_sha256"`
	} `json:"provenance"`
}

func idSet(ids []int) map[int]bool {
	if len(ids) == 0 {
		return nil
	}
	out := make(map[int]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
