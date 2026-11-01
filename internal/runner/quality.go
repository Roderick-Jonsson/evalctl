package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"evalctl/internal/store"
	"evalctl/internal/version"
)

// RunQuality is the output lane: every selected case through every selected arm, then the
// benchmark and the report.
//
// One workspace per skill. A run that selects several stages writes one iteration into each
// skill's own workspace (<skill>-workspace/iteration-N/), which is why the iteration numbers are
// per skill: iteration 3 of learning-path and iteration 3 of slide-builder are different runs, and
// neither can be mistaken for the other.
func (r *Runner) RunQuality(ctx context.Context, o Options) error {
	if err := o.Normalize(); err != nil {
		return err
	}
	skills, err := r.ResolveSkills(o.Skills)
	if err != nil {
		return err
	}
	cases, err := LoadCases(r.Store, skills, idSet(o.CaseIDs))
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
			if c.Promoted != nil {
				r.message("  promoted from %s", promotionRef(c.Promoted.Skill, c.Promoted.Run,
					c.Promoted.Case, c.Promoted.Arm))
			}
			r.message("  %s", oneLine(c.Prompt))
		}
		return nil
	}

	if !o.DryRun {
		// Both arms run the same contract; the with arm holds exactly the skill under test, the
		// baseline holds none, and neither keeps memory from a previous case.
		profiles := map[string]bool{}
		for _, arm := range o.Arms {
			profiles[ArmProfiles[arm]] = true
		}
		for _, name := range keysOf(profiles) {
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

	for _, group := range groupBySkill(cases, skills) {
		if err := r.runSkillIteration(ctx, group, o); err != nil {
			return err
		}
	}
	return nil
}

// runSkillIteration writes one skill's cases into that skill's next iteration.
func (r *Runner) runSkillIteration(ctx context.Context, cases []Case, o Options) error {
	skill := cases[0].Skill
	workspace := r.WorkspaceDir(skill)
	iteration := o.Iteration
	if iteration == 0 {
		iteration = r.NextIteration(workspace, "iteration")
	}
	iterDir := r.IterDir(workspace, iteration)
	if fileExists(iterDir) && !o.DryRun && !o.Resume && !o.Force {
		return fmt.Errorf("%s already exists — rerun with --resume, --force, or pick another --iteration",
			r.relative(iterDir))
	}
	if !o.Resume {
		for _, c := range cases {
			if o.Force {
				_ = os.RemoveAll(filepath.Join(iterDir, c.Slug))
			}
		}
	}

	profiles := map[string]bool{}
	for _, arm := range o.Arms {
		profiles[ArmProfiles[arm]] = true
	}
	start := fmt.Sprintf("%s iteration %d · %d case(s) × %d arm(s) · profiles %s · evalctl %s",
		skill, iteration, len(cases), len(o.Arms), strings.Join(keysOf(profiles), ", "), version.Resolve().Short())
	if o.DryRun {
		start += " · DRY RUN"
	}
	r.emit(Event{Kind: EventStart, Lane: LaneOutput, Case: skill, Message: start})

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
				runRoot := filepath.Join(armDir, "outputs")
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

	collected, err := r.CollectRuns(iterDir, skill)
	if err != nil {
		return err
	}
	if err := r.RefreshIteration(skill, iteration); err != nil {
		return err
	}

	_, contractSHA, err := r.Profiles.Contract()
	if err != nil {
		return err
	}
	meta := metaQuality{Iteration: iteration, Skill: skill, Started: startedAt,
		Model: orDefault(o.Model, "default"), Arms: o.Arms, BudgetS: int(o.Budget.Seconds()), Cases: []string{}}
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
	meta.Provenance.Tool = toolInfo()
	// A fixture's hash is what makes a promoted case auditable later: evals.json records the hash
	// at promotion time, meta.json records the one the run actually staged.
	fixtures, err := r.fixtureProvenance(cases)
	if err != nil {
		return err
	}
	meta.Provenance.Fixtures = fixtures
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
	msg := fmt.Sprintf("wrote %s/report.md and benchmark.json", r.relative(iterDir))
	if failures > 0 {
		msg += fmt.Sprintf(" — %d run(s) did not exit 0, check stderr.txt in those arm directories", failures)
	}
	r.emit(Event{Kind: EventDone, Lane: LaneOutput, Case: fmt.Sprintf("%s@iteration-%d", skill, iteration), Message: msg})
	if o.Grade {
		return r.gradeAfterRun(ctx, skill, iteration, o)
	}
	return nil
}

// gradeAfterRun is --grade: the same judge the `grade` command runs, on the iteration that was
// just written. A grading failure does not throw the run away — the runs are on disk and the note
// in grading.json says they are ungraded.
func (r *Runner) gradeAfterRun(ctx context.Context, skill string, iteration int, o Options) error {
	out, err := r.GradeIteration(ctx, skill, iteration, GradeOptions{Model: o.Model, Budget: DefaultGradeBudget})
	if err != nil {
		r.message("%s: run kept, grading failed: %v", skill, err)
		return nil
	}
	if out.Graded == 0 {
		r.message("%s: nothing to grade — no case in iteration %d declares assertions", skill, iteration)
	}
	return nil
}

// groupBySkill splits the selected cases into one group per stage, in pipeline order, so each
// group can be written into that skill's own workspace.
func groupBySkill(cases []Case, skills []string) [][]Case {
	by := map[string][]Case{}
	for _, c := range cases {
		by[c.Skill] = append(by[c.Skill], c)
	}
	var out [][]Case
	for _, skill := range skills {
		if group := by[skill]; len(group) > 0 {
			out = append(out, group)
		}
	}
	// A case whose skill is not in the resolved list would otherwise vanish from the run; keep it
	// rather than silently dropping work.
	for skill, group := range by {
		if !contains(skills, skill) {
			out = append(out, group)
		}
	}
	return out
}

// promotionRef names a run the way a reader addresses it: source-research@iteration-2/case/arm.
func promotionRef(skill, run, cse, arm string) string {
	name := run
	if skill != "" {
		name = skill + "@" + run
	}
	return fmt.Sprintf("%s/%s/%s", name, cse, arm)
}

// relative is a path as a reader addresses it: root-relative and slash-separated when it is inside
// the tree, absolute otherwise.
func (r *Runner) relative(path string) string {
	if rel, err := filepath.Rel(r.Root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}

type metaProfile struct {
	Name       string   `json:"name"`
	SoulSHA256 *string  `json:"soul_sha256"`
	Skills     []string `json:"skills"`
}

type metaQuality struct {
	Iteration  int      `json:"iteration"`
	Skill      string   `json:"skill"`
	Started    string   `json:"started"`
	Model      string   `json:"model"`
	Arms       []string `json:"arms"`
	BudgetS    int      `json:"budget_s"`
	Cases      []string `json:"cases"`
	Provenance struct {
		ContractSHA256  string                 `json:"contract_sha256"`
		Profiles        map[string]metaProfile `json:"profiles"`
		SkillTreeSHA256 map[string]string      `json:"skill_tree_sha256"`
		Fixtures        map[string]metaFixture `json:"fixtures,omitempty"`
		Tool            metaTool               `json:"tool"`
	} `json:"provenance"`
}

// metaTool records which build produced an iteration. A run graded by a different harness is not
// comparable with the last one, so the number belongs in the artifact, not just in the header.
type metaTool struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Dirty   bool   `json:"dirty"`
	BuiltAt string `json:"built_at,omitempty"`
}

func toolInfo() metaTool {
	info := version.Resolve()
	return metaTool{Version: info.Version, Commit: info.Commit, Dirty: info.Dirty, BuiltAt: info.BuiltAt}
}

// metaFixture is one case's fixture as the run staged it, plus its promotion ancestry.
type metaFixture struct {
	SHA256   string              `json:"sha256,omitempty"`
	Promoted *store.PromotedFrom `json:"promoted_from,omitempty"`
}

// fixtureProvenance records, per case slug, the fixture hash the run is about to stage and where
// that fixture came from. A prompt-only case with no ancestry is left out: there is nothing to
// audit, and an empty hash would read like a missing fixture.
func (r *Runner) fixtureProvenance(cases []Case) (map[string]metaFixture, error) {
	out := map[string]metaFixture{}
	for _, c := range cases {
		sha, err := store.FixtureSHA256(r.Store.FixtureDir(c.Skill, c.Slug))
		if err != nil {
			return nil, err
		}
		if sha == "" && c.Promoted == nil {
			continue
		}
		out[c.Slug] = metaFixture{SHA256: sha, Promoted: c.Promoted}
	}
	return out, nil
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
