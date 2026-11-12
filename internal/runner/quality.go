package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

	// A cancelled context stops before the gate: provisioning nothing is the point.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.Preflight(o); err != nil {
		return err
	}

	if !o.DryRun {
		// Both arms run the same contract; the with arm holds exactly the skill under test, the
		// baseline holds none, and neither keeps memory from a previous case. The sandboxes are
		// created here, when they are missing, and removed when the run that used them ends.
		arms := r.armSandboxes(o.Arms)
		if err := r.provisionSandboxes(arms); err != nil {
			return err
		}
		defer r.releaseSandboxes(arms, o.Keep)
	}

	for _, group := range groupBySkill(cases, skills) {
		if err := r.runSkillIteration(ctx, group, o); err != nil {
			return err
		}
	}
	return nil
}

// IterationPlan is which iteration a run writes into, and what it would close.
//
// An iteration is the set measured once. A case run on its own therefore belongs to the iteration
// being built — writing it into a fresh number every time left a workspace of one-case iterations that
// could never add up to a set, and running three cases one at a time then never produced an iteration
// of three. A run joins the newest iteration while that iteration is still open (it does not hold every
// case the set declares) and the skill has not moved since; a complete iteration, or one measured
// against a different skill, starts the next number.
type IterationPlan struct {
	Iter int
	Join bool     // the iteration is already on disk
	Open []string // declared cases it does not hold yet
}

func (r *Runner) PlanIteration(skill string, fill bool) (IterationPlan, error) {
	next := r.NextIteration(r.WorkspaceDir(skill), "iteration")
	if !fill {
		return IterationPlan{Iter: next}, nil
	}
	last := next - 1
	if last <= 0 {
		return IterationPlan{Iter: next}, nil
	}
	dir := r.IterDir(r.WorkspaceDir(skill), last)
	meta := metaQuality{}
	if err := readJSONFile(filepath.Join(dir, "meta.json"), &meta); err != nil || len(meta.Cases) == 0 {
		// An iteration with no record of its own is not one a run can be added to: what it holds is
		// whatever a half-finished run left behind.
		return IterationPlan{Iter: next}, nil
	}
	have := map[string]bool{}
	for _, slug := range meta.Cases {
		have[slug] = true
	}
	declared, err := LoadCases(r.Store, []string{skill}, nil)
	if err != nil {
		return IterationPlan{Iter: next}, nil
	}
	var open []string
	for _, c := range declared {
		if !have[c.Slug] {
			open = append(open, c.Slug)
		}
	}
	if len(open) == 0 {
		return IterationPlan{Iter: next}, nil
	}
	return IterationPlan{Iter: last, Join: true, Open: open}, nil
}

// runSkillIteration writes one skill's cases into that skill's next iteration.
func (r *Runner) runSkillIteration(ctx context.Context, cases []Case, o Options) error {
	skill := cases[0].Skill
	workspace := r.WorkspaceDir(skill)
	iteration := o.Iteration
	if iteration == 0 {
		plan, err := r.PlanIteration(skill, o.Fill)
		if err != nil {
			return err
		}
		iteration = plan.Iter
	}
	iterDir := r.IterDir(workspace, iteration)
	if fileExists(iterDir) && !o.DryRun && !o.Resume && !o.Force {
		return fmt.Errorf("%s already exists — rerun with --resume, --force, or pick another --iteration",
			r.relative(iterDir))
	}
	for _, c := range cases {
		if o.Force || o.Replace {
			// RemoveAll on a case the iteration does not hold is a no-op, so Replace is safe on a case
			// being added as well as on one being measured again.
			_ = os.RemoveAll(filepath.Join(iterDir, c.Slug))
		}
	}

	profiles := map[string]bool{}
	for _, arm := range o.Arms {
		profiles[r.ArmProfile(arm)] = true
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
		if !o.DryRun && contains(o.Arms, ArmWith) {
			line, err := r.Profiles.SyncSkills(r.ArmProfile(ArmWith), []string{c.Skill}, false)
			if err != nil {
				return err
			}
			r.message("%s", line)
		}
		// The hash is taken after the sync: it is the tree the arms are about to run, and a hash taken
		// before it recorded the copy the sync was about to replace.
		if sha, err := r.Profiles.TreeHash(c.Skill); err == nil {
			treeSHA[c.Slug] = sha
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
		// The plan says what the run would also spend: the judge is part of it, and a dry run that
		// stopped at the arms would price the iteration wrong. A set with no assertion spends nothing
		// on the judge — it is the look the first assertions are written from — and the plan says so.
		if o.Grade && assertedCases(cases) > 0 {
			r.message("%s: then the judge — one call per case and arm that declares an assertion, "+
				"on iteration %d", skill, iteration)
		} else {
			r.message("%s: no assertion is declared — nothing is judged, and the outputs are the point", skill)
		}
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
	// meta.json is the iteration's own record of what it holds. A run that joined an iteration adds its
	// cases to that record rather than replacing it, or the next join would read the iteration as
	// missing the cases this run did not touch and mint a number for a set that was already whole.
	var prev metaQuality
	joined := readJSONFile(filepath.Join(iterDir, "meta.json"), &prev) == nil && prev.Iteration == iteration
	meta := metaQuality{Iteration: iteration, Skill: skill, Started: startedAt,
		Model: orDefault(o.Model, "default"), Arms: o.Arms, BudgetS: int(o.Budget.Seconds()), Cases: []string{}}
	for _, run := range collected {
		meta.Cases = append(meta.Cases, run.Slug)
	}
	meta.Provenance.ContractSHA256 = contractSHA
	meta.Provenance.Profiles = map[string]metaProfile{}
	for _, arm := range o.Arms {
		name := r.ArmProfile(arm)
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
	if joined {
		meta.Cases = appendUnique(prev.Cases, meta.Cases)
		for slug, sha := range prev.Provenance.SkillTreeSHA256 {
			if _, ok := meta.Provenance.SkillTreeSHA256[slug]; !ok {
				meta.Provenance.SkillTreeSHA256[slug] = sha
			}
		}
		for slug, f := range prev.Provenance.Fixtures {
			if _, ok := meta.Provenance.Fixtures[slug]; !ok {
				meta.Provenance.Fixtures[slug] = f
			}
		}
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
	// The judge pass is announced before it starts: a run that grades is one spend and two passes, and
	// the screen has to be able to say which one it is waiting on — otherwise the same arm appears to
	// run twice and the second pass reads as a hang.
	r.emit(Event{Kind: EventPhase, Lane: LaneOutput, Phase: "grade",
		Message: fmt.Sprintf("judging iteration %d — one judge call per arm", iteration)})
	out, err := r.GradeIteration(ctx, skill, iteration, GradeOptions{Model: o.Model, Budget: DefaultGradeBudget,
		// The run owns both arms: they were provisioned above and are removed by the run's own
		// teardown, after this pass. A judge that removed its clean room here would delete the
		// baseline arm the next case of this same iteration may still have to run in.
		Nested: true, Keep: o.Keep})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			// The operator's x during the judge pass stops the pass and keeps the arms: the iteration is
			// on disk with outputs and no verdicts, and `g` finishes it without re-running a case.
			r.emit(Event{Kind: EventPhase, Lane: LaneOutput, Phase: "grade-off",
				Message: fmt.Sprintf("grading cancelled — iteration %d is on disk without verdicts; g grades it", iteration)})
			return nil
		}
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

// toolInfo is the build that measured this run. built_at is the build's own clock and only that: an
// unstamped `go build` carries the commit's time in BuiltAt, and recording it as the build time is
// how a stale binary would look fresh in the artifact — the same rule the TUI's corner follows.
func toolInfo() metaTool {
	info := version.Resolve()
	built := ""
	if t, ok := info.Built(); ok {
		built = t.UTC().Format(time.RFC3339)
	}
	return metaTool{Version: info.Version, Commit: info.Commit, Dirty: info.Dirty, BuiltAt: built}
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

// assertedCases is how many of the cases being run declare at least one assertion: the count that
// decides whether the judge has anything to read.
func assertedCases(cases []Case) int {
	n := 0
	for _, c := range cases {
		if len(c.Assertions) > 0 {
			n++
		}
	}
	return n
}

// appendUnique is a ∪ b, keeping the order of a and then what of b is new: an iteration's case list
// reads in the order the cases were measured.
func appendUnique(a, b []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, s := range list {
			if s == "" || seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
