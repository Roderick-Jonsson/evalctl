// Package runner launches the stage-skill eval loop — the Go port of the Python loop, and now the
// only implementation of it.
//
// Two lanes, one driver:
//
//	output (default)  every case in <skill>/evals/evals.json runs twice — with the skill and
//	                  without it — in two dedicated profiles (eval-vcc-with / eval-vcc-without)
//	                  into fresh run roots, then timing, mechanical checks and a report are
//	                  written into the skill's own workspace (<skill>-workspace/iteration-N/).
//	trigger           every query in trigger/trigger-evals.json runs N times against
//	                  eval-vcc-trigger (all four skills installed) in an empty run root, and the
//	                  lane scores whether the right skill's SKILL.md got loaded — the
//	                  agentskills.io description-optimization loop, into trigger-workspace/.
//
// One workspace per skill, the layout agentskills.io's "Evaluating skill output quality" lays out:
// the cases live in <skill>/evals/ and the results sit beside the skill directory, so two stages'
// iterations can never be confused for one another.
//
// The skill reaches the with-skill arm the way it reaches a real user: installed in the profile
// and loaded by the agent on its own. The baseline arm holds zero skills, so the delta isolates
// the skill instead of measuring two prompts.
package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"evalctl/internal/profile"
	"evalctl/internal/store"
)

const (
	LaneOutput  = "output"
	LaneTrigger = "trigger"
)

// Arms are the two sides of the comparison.
const (
	ArmWith    = "with_skill"
	ArmWithout = "without_skill"
)

// ArmProfiles maps an arm to the sandbox it runs in.
var ArmProfiles = map[string]string{ArmWith: profile.With, ArmWithout: profile.Without}

// AllArms is the default arm set, in run order.
var AllArms = []string{ArmWith, ArmWithout}

const (
	DefaultBudget           = 1200 * time.Second
	DefaultTriggerRuns      = 3
	DefaultTriggerThreshold = 0.5
	DefaultTriggerBudget    = 240 * time.Second
	DefaultTriggerTurns     = 8
	NegativeTurns           = 3
	DescriptionLimit        = 1024
)

// Options is one invocation: which rows, which arms, how much time, and whether to write.
type Options struct {
	Lane      string
	Skills    []string
	CaseIDs   []int
	QueryIDs  []int
	Split     string
	Arms      []string
	Model     string
	Budget    time.Duration
	Resume    bool
	Force     bool
	NoVerify  bool
	Isolate   bool
	DryRun    bool
	List      bool
	Iteration int
	Runs      int
	Threshold float64
	MaxTurns  int
	// Log is how much of the agent's own stream reaches activity.log and the progress view.
	Log ActivityLevel
}

// Normalize fills in the lane defaults: a trigger run gets the trigger lane's shorter budget.
func (o *Options) Normalize() error {
	if o.Lane == "" {
		o.Lane = LaneOutput
	}
	if o.Lane != LaneOutput && o.Lane != LaneTrigger {
		return fmt.Errorf("unknown lane %q (want output or trigger)", o.Lane)
	}
	if o.Split == "" {
		o.Split = "all"
	}
	if o.Split != "all" && o.Split != "train" && o.Split != "validation" {
		return fmt.Errorf("--split must be all, train or validation")
	}
	if len(o.Arms) == 0 {
		o.Arms = append([]string(nil), AllArms...)
	}
	for _, arm := range o.Arms {
		if _, ok := ArmProfiles[arm]; !ok {
			return fmt.Errorf("unknown arm %q (want %s)", arm, strings.Join(AllArms, ", "))
		}
	}
	if o.Budget <= 0 {
		if o.Lane == LaneTrigger {
			o.Budget = DefaultTriggerBudget
		} else {
			o.Budget = DefaultBudget
		}
	}
	if o.MaxTurns <= 0 {
		o.MaxTurns = DefaultTriggerTurns
	}
	if o.Runs <= 0 {
		o.Runs = DefaultTriggerRuns
	}
	if o.Threshold <= 0 {
		o.Threshold = DefaultTriggerThreshold
	}
	if o.Log == ActivityUnset {
		o.Log = ActivityFull
	}
	return nil
}

// Event is one progress line: what the CLI prints and what the TUI renders.
type Event struct {
	Kind    string
	Lane    string
	Case    string
	Arm     string
	Message string
	Result  *ArmResult
	Path    string // activity.log behind an EventActivity line
	Err     error
}

// Event kinds.
const (
	EventPlan       = "plan"
	EventStart      = "start"
	EventCase       = "case"
	EventArmStart   = "arm-start"
	EventArmDone    = "arm-done"
	EventActivity   = "activity"
	EventTriggerRun = "trigger-run"
	EventMessage    = "message"
	EventScored     = "scored"
	EventDone       = "done"
)

// Runner holds the tree and the profiles it provisions.
type Runner struct {
	Root     string
	Store    *store.Store
	Profiles *profile.Manager
	Emit     func(Event)
}

// New builds a runner for a loaded tree.
func New(s *store.Store) *Runner {
	skills := s.SkillNames()
	return &Runner{
		Root:     s.Root,
		Store:    s,
		Profiles: profile.New(s.Root, skills),
		Emit:     func(Event) {},
	}
}

func (r *Runner) emit(e Event) {
	if r.Emit != nil {
		r.Emit(e)
	}
}

func (r *Runner) message(format string, args ...any) {
	r.emit(Event{Kind: EventMessage, Message: fmt.Sprintf(format, args...)})
}

// WorkspaceDir is <root>/<skill>-workspace — the workspace folder the guide puts beside the skill
// directory. It holds that skill's iterations and nothing else.
func (r *Runner) WorkspaceDir(skill string) string {
	return filepath.Join(r.Root, skill+"-workspace")
}

// TriggerWorkspace is <root>/trigger-workspace. The trigger lane measures the bundle (which of
// four skills answers a request), so its results belong to no single skill's workspace.
func (r *Runner) TriggerWorkspace() string {
	return filepath.Join(r.Root, "trigger-workspace")
}

// NextIteration is max(existing)+1 for one workspace's run dirs, so each workspace numbers its own
// iterations from 1.
func (r *Runner) NextIteration(workspace, prefix string) int {
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return 1
	}
	best := 0
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix+"-") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), prefix+"-"))
		if err == nil && n > best {
			best = n
		}
	}
	return best + 1
}

// IterDir is the directory one output iteration writes into, inside a skill's workspace.
func (r *Runner) IterDir(workspace string, iteration int) string {
	return filepath.Join(workspace, fmt.Sprintf("iteration-%d", iteration))
}

// TriggerIterDir is the directory one trigger iteration writes into.
func (r *Runner) TriggerIterDir(iteration int) string {
	return filepath.Join(r.TriggerWorkspace(), fmt.Sprintf("trigger-%d", iteration))
}

// ResolveSkills turns "all" (or an empty list) into every stage skill that has a case file.
func (r *Runner) ResolveSkills(requested []string) ([]string, error) {
	if len(requested) == 0 {
		return r.Store.SkillNames(), nil
	}
	for _, s := range requested {
		if !r.Profiles.IsSkill(s) {
			return nil, fmt.Errorf("unknown skill(s): %s (known: %s)", s,
				strings.Join(r.Profiles.Skills, ", "))
		}
	}
	return requested, nil
}

// ---------------------------------------------------------------- published results

// RunInfo is one finished (or in-progress) iteration, as the browser sees it.
type RunInfo struct {
	Name      string    `json:"name"`
	Skill     string    `json:"skill,omitempty"` // the workspace owner; empty for the trigger lane
	Lane      string    `json:"lane"`
	Iteration int       `json:"iteration"`
	Path      string    `json:"path"`
	Started   string    `json:"started,omitempty"`
	Model     string    `json:"model,omitempty"`
	Cases     int       `json:"cases"`
	Runs      int       `json:"runs,omitempty"`
	Threshold float64   `json:"threshold,omitempty"`
	Judged    int       `json:"judged,omitempty"`
	Passed    int       `json:"passed,omitempty"`
	PassRate  *float64  `json:"pass_rate"`
	HasReport bool      `json:"has_report"`
	Updated   time.Time `json:"-"`

	// tallies is the trigger lane's per-skill breakdown, kept out of the JSON because it is only
	// consulted to re-score one iteration for one skill (see ListRunsFor). An output iteration
	// lives in one skill's workspace, so it never needs it.
	tallies map[string]Tallies
}

// Ref is how a run is named in prose and on a command line: "source-research@iteration-2", so a
// number that exists in two workspaces still identifies one run.
func (i RunInfo) Ref() string {
	if i.Skill == "" {
		return i.Name
	}
	return i.Skill + "@" + i.Name
}

// ShortTime is how both faces print a run's started stamp: date and minute, because the table is
// read to compare runs, and a 25-character timestamp with an offset is compared by squinting. The
// full value stays in the run's own meta.json and report.
func ShortTime(s string) string {
	if s == "" {
		return "-"
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05-0700", "2006-01-02T15:04:05Z07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02 15:04")
		}
	}
	if len(s) > 16 {
		return s[:16]
	}
	return s
}

type metaFile struct {
	Iteration int      `json:"iteration"`
	Lane      string   `json:"lane"`
	Started   string   `json:"started"`
	Model     string   `json:"model"`
	Cases     []string `json:"cases"`
	Queries   []string `json:"queries"`
	Runs      int      `json:"runs"`
	Threshold float64  `json:"threshold"`
}

type triggerScoreFile struct {
	Score struct {
		Threshold float64 `json:"threshold"`
		Runs      int     `json:"runs"`
		Overall   struct {
			Judged int      `json:"judged"`
			Passed int      `json:"passed"`
			Rate   *float64 `json:"rate"`
		} `json:"overall"`
		// Skills is the same tally, split per skill: a trigger iteration is global (one
		// trigger-workspace/) but every query in it belongs to one skill, so this is what lets a
		// skill-scoped results view quote that skill instead of the iteration's overall rate.
		Skills map[string]struct {
			All Tallies `json:"all"`
		} `json:"skills"`
	} `json:"score"`
}

// Tallies mirrors the "all" tally of one skill's block in trigger.json.
type Tallies struct {
	Rows   int      `json:"rows"`
	Judged int      `json:"judged"`
	Passed int      `json:"passed"`
	Rate   *float64 `json:"rate"`
}

// ListRuns reads every workspace back: each skill's own iterations from <skill>-workspace/, then
// the trigger lane's from trigger-workspace/. Skills come in pipeline order and each workspace's
// iterations ascending, so one skill's history stays contiguous.
func (r *Runner) ListRuns() []RunInfo {
	var out []RunInfo
	for _, skill := range r.Profiles.Skills {
		out = append(out, r.listRunsIn(r.WorkspaceDir(skill), "iteration", LaneOutput, skill)...)
	}
	return append(out, r.listRunsIn(r.TriggerWorkspace(), "trigger", LaneTrigger, "")...)
}

// ListRunsFor is ListRuns scoped to one skill, the way the case lanes are scoped: the skill's own
// output iterations, then the trigger iterations that actually measured it. An empty skill is
// every skill — the "· all skills" scope, not an error.
//
// The trigger lane is global (one trigger-workspace/), so an iteration is not owned by a skill;
// each one is re-scored from its own per-skill tally instead. That is the difference between
// "trigger-4 passed 29 of 31 queries" and "trigger-4 passed 10 of slide-builder's 10", and the
// second is the only one that means anything while you are working on slide-builder.
func (r *Runner) ListRunsFor(skill string) []RunInfo {
	all := r.ListRuns()
	if skill == "" {
		return all
	}
	out := make([]RunInfo, 0, len(all))
	for _, info := range all {
		if info.Lane != LaneTrigger {
			if info.Skill == skill {
				out = append(out, info)
			}
			continue
		}
		tally, ok := info.tallies[skill]
		if !ok || tally.Rows == 0 {
			continue // this iteration never ran that skill's queries
		}
		info.Cases = tally.Rows
		info.Judged, info.Passed, info.PassRate = tally.Judged, tally.Passed, tally.Rate
		out = append(out, info)
	}
	return out
}

// listRunsIn reads one workspace's finished (or in-progress) run dirs.
func (r *Runner) listRunsIn(workspace, prefix, lane, skill string) []RunInfo {
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return nil
	}
	var out []RunInfo
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix+"-") {
			continue
		}
		iteration, err := strconv.Atoi(strings.TrimPrefix(e.Name(), prefix+"-"))
		if err != nil {
			continue
		}
		path := filepath.Join(workspace, e.Name())
		info := RunInfo{Name: e.Name(), Skill: skill, Lane: lane, Iteration: iteration, Path: path}
		if st, err := os.Stat(path); err == nil {
			info.Updated = st.ModTime()
		}
		info.HasReport = fileExists(filepath.Join(path, "report.md"))
		if b, err := os.ReadFile(filepath.Join(path, "meta.json")); err == nil {
			var meta metaFile
			if json.Unmarshal(b, &meta) == nil {
				info.Started, info.Model, info.Runs = meta.Started, meta.Model, meta.Runs
				info.Threshold = meta.Threshold
				info.Cases = len(meta.Cases)
				if lane == LaneTrigger {
					info.Cases = len(meta.Queries)
				}
			}
		}
		if lane == LaneTrigger {
			if b, err := os.ReadFile(filepath.Join(path, "trigger.json")); err == nil {
				var scored triggerScoreFile
				if json.Unmarshal(b, &scored) == nil {
					info.Judged = scored.Score.Overall.Judged
					info.Passed = scored.Score.Overall.Passed
					info.PassRate = scored.Score.Overall.Rate
					if scored.Score.Threshold > 0 {
						info.Threshold = scored.Score.Threshold
					}
					if scored.Score.Runs > 0 {
						info.Runs = scored.Score.Runs
					}
					if len(scored.Score.Skills) > 0 {
						info.tallies = make(map[string]Tallies, len(scored.Score.Skills))
						for skill, block := range scored.Score.Skills {
							info.tallies[skill] = block.All
						}
					}
				}
			}
			if info.Cases == 0 {
				if entries, err := os.ReadDir(path); err == nil {
					for _, c := range entries {
						if c.IsDir() && strings.HasPrefix(c.Name(), "q") {
							info.Cases++
						}
					}
				}
			}
		} else if info.Cases == 0 {
			if entries, err := os.ReadDir(path); err == nil {
				for _, c := range entries {
					if c.IsDir() {
						info.Cases++
					}
				}
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Iteration < out[j].Iteration })
	return out
}

// ReportPath is an iteration's report.md.
func (r *Runner) ReportPath(info RunInfo) string { return filepath.Join(info.Path, "report.md") }

// ResolveRun accepts an iteration ref: "3", "iteration-3", "source-research@3",
// "source-research@iteration-3", or "trigger-2".
//
// A bare number is refused once it exists in more than one workspace: each skill numbers its own
// iterations from 1, so guessing a workspace would open someone else's report.
func (r *Runner) ResolveRun(ref, lane string) (RunInfo, error) {
	raw := strings.TrimSpace(ref)
	skill := ""
	if at := strings.LastIndex(raw, "@"); at > 0 {
		skill, raw = strings.TrimSpace(raw[:at]), strings.TrimSpace(raw[at+1:])
	}
	prefix := "iteration"
	if lane == LaneTrigger {
		prefix = "trigger"
	}
	switch {
	case strings.HasPrefix(raw, "iteration-"):
		prefix, raw = "iteration", strings.TrimPrefix(raw, "iteration-")
	case strings.HasPrefix(raw, "trigger-"):
		prefix, raw = "trigger", strings.TrimPrefix(raw, "trigger-")
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return RunInfo{}, fmt.Errorf("bad iteration ref %q (want 3, source-research@3 or trigger-2)", ref)
	}
	var found []RunInfo
	for _, info := range r.ListRuns() {
		if info.Iteration != n || (info.Lane == LaneOutput) != (prefix == "iteration") {
			continue
		}
		if skill != "" && info.Skill != skill {
			continue
		}
		found = append(found, info)
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		if skill != "" {
			return RunInfo{}, fmt.Errorf("no %s-%d under %s-workspace", prefix, n, skill)
		}
		return RunInfo{}, fmt.Errorf("no %s-%d in any workspace", prefix, n)
	}
	refs := make([]string, 0, len(found))
	for _, info := range found {
		refs = append(refs, info.Ref())
	}
	return RunInfo{}, fmt.Errorf("%s-%d exists in %d workspaces (%s) — say which: <skill>@%d",
		prefix, n, len(found), strings.Join(refs, ", "), n)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
