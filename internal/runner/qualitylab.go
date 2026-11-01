package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The output lab is the evaluating-skills loop as state rather than as advice.
//
// The guide's loop is: run every case with and without the skill, grade the assertions, read the
// failures and the human feedback, revise SKILL.md, and rerun into a new iteration — stopping when
// the work is satisfactory or no longer improving. Every one of those moves is arithmetic over
// files this tree already holds: the iteration directory has one grading.json per arm, and the
// skill's evals.json declares the assertions. So the lab computes the loop instead of remembering
// it, and the one question left for a person is the guide's own: what should the skill say instead?
//
// Two rules keep the numbers honest, and they are the trigger lab's rules:
//   - a rate is judged over assertions, never over vibes: an arm with no verdicts has no rate, and a
//     partial grade says so instead of ranking;
//   - the best iteration is the best WITH-SKILL pass rate among iterations whose every case was
//     graded on every declared assertion, and a tie goes to the later one because it is the same
//     result on a newer reading.

// QualityArm is one arm's result in one iteration.
type QualityArm struct {
	Judged     int     `json:"judged"`
	Passed     int     `json:"passed"`
	Rate       float64 `json:"rate"`
	Cases      int     `json:"cases"`      // cases with a verdict
	CaseCount  int     `json:"case_count"` // cases the iteration ran
	DurationMS int64   `json:"duration_ms"`
	Tokens     int64   `json:"tokens"`
}

// Graded reports whether any verdict exists for the arm.
func (a QualityArm) Graded() bool { return a.Judged > 0 }

// QualityFailure is one failed assertion, with where it failed and what the judge saw.
type QualityFailure struct {
	Case     string `json:"case"`
	Arm      string `json:"arm"`
	Text     string `json:"text"`
	Evidence string `json:"evidence"`
}

// QualityIteration is one iteration-N directory, read back.
type QualityIteration struct {
	N       int        `json:"iteration"`
	Started string     `json:"started,omitempty"`
	Tool    string     `json:"tool,omitempty"`
	Path    string     `json:"path"`
	Cases   int        `json:"cases"`
	With    QualityArm `json:"with_skill"`
	Without QualityArm `json:"without_skill"`
	Delta   float64    `json:"delta"` // with − without, 0 when either arm is ungraded
	// SetComplete is whether the iteration ran every case the skill declares. An iteration that ran
	// a subset is not comparable with one that ran the set: its rate is over different work.
	SetComplete bool             `json:"set_complete"`
	Complete    bool             `json:"complete"`
	Failed      []QualityFailure `json:"failed,omitempty"`
	Feedback    int              `json:"feedback"` // non-empty review notes
	Partial     bool             `json:"partial"`  // verdicts exist, but not for every case
}

// QualityLab is a skill's whole state in the output loop.
type QualityLab struct {
	Skill      string             `json:"skill"`
	Cases      int                `json:"cases"`
	Assertions int                `json:"assertions"`
	Iterations []QualityIteration `json:"iterations"`
	Best       int                `json:"best"` // iteration number, 0 = no complete one
	Flat       bool               `json:"flat"` // the last two complete iterations tie
	Ungraded   int                `json:"ungraded_arms"`
	Checks     []LabCheck         `json:"checks"`
	Next       LabNext            `json:"next"`
}

// QualityLabOf reads one skill's output loop back from disk.
func (r *Runner) QualityLab(skill string) (*QualityLab, error) {
	sk := r.Store.Skill(skill)
	if sk == nil {
		return nil, fmt.Errorf("unknown skill %q", skill)
	}
	cases, err := LoadCases(r.Store, []string{skill}, nil)
	if err != nil {
		return nil, err
	}
	lab := &QualityLab{Skill: skill, Cases: len(cases)}
	// A case's assertion count is what decides whether a grading is complete, so the lab reads the
	// declared set once and keeps it by slug.
	declared := map[string]int{}
	for _, c := range cases {
		lab.Assertions += len(c.Assertions)
		declared[c.Slug] = len(c.Assertions)
	}
	workspace := r.WorkspaceDir(skill)
	for _, n := range iterationNumbers(workspace) {
		it := r.readQualityIteration(workspace, n, declared)
		// An iteration whose every case was retired is not this set's history: it cannot be graded,
		// ranked or compared, and printing it as "no verdicts" would blame the operator for it.
		if it.Cases == 0 {
			continue
		}
		lab.Iterations = append(lab.Iterations, it)
	}
	lab.Best, lab.Flat = bestQuality(lab.Iterations)
	for _, it := range lab.Iterations {
		if !it.Complete {
			lab.Ungraded += 2 - gradedArms(it)
		}
	}
	lab.Checks = qualityChecks(lab)
	lab.Next = qualityNext(lab)
	return lab, nil
}

// readQualityIteration reads one iteration's arms. The grading files are the authority, not
// benchmark.json: a benchmark can be stale after a hand edit, and a rate must come from the
// verdicts it claims to summarize.
func (r *Runner) readQualityIteration(workspace string, n int, declared map[string]int) QualityIteration {
	dir := r.IterDir(workspace, n)
	it := QualityIteration{N: n, Path: dir}
	var meta struct {
		Started string `json:"started"`
		Tool    struct {
			Version string `json:"version"`
		} `json:"tool"`
	}
	_ = readJSONFile(filepath.Join(dir, "meta.json"), &meta)
	it.Started, it.Tool = meta.Started, meta.Tool.Version

	entries, _ := os.ReadDir(dir)
	var ratesWith, ratesWithout []float64
	feedback := map[string]string{}
	_ = readJSONFile(filepath.Join(dir, "feedback.json"), &feedback)
	for _, slug := range feedback {
		if strings.TrimSpace(slug) != "" {
			it.Feedback++
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, ok := declared[e.Name()]; !ok {
			continue // a result dir whose case was retired: not this set's reading
		}
		it.Cases++
		for _, arm := range AllArms {
			armDir := filepath.Join(dir, e.Name(), arm)
			if info, err := os.Stat(armDir); err != nil || !info.IsDir() {
				continue
			}
			var grading Grading
			if err := readJSONFile(filepath.Join(armDir, "grading.json"), &grading); err != nil {
				continue
			}
			target := &it.With
			if arm == ArmWithout {
				target = &it.Without
			}
			target.CaseCount = it.Cases
			var timing Timing
			_ = readJSONFile(filepath.Join(armDir, "timing.json"), &timing)
			target.DurationMS += timing.DurationMS
			if timing.TotalTokens != nil {
				target.Tokens += int64(*timing.TotalTokens)
			}
			if grading.Summary == nil || grading.Summary.Total == 0 {
				continue
			}
			target.Judged += grading.Summary.Total
			target.Passed += grading.Summary.Passed
			target.Cases++
			want := declared[e.Name()]
			if want > 0 && grading.Summary.Total >= want {
				if arm == ArmWith {
					ratesWith = append(ratesWith, grading.Summary.PassRate)
				} else {
					ratesWithout = append(ratesWithout, grading.Summary.PassRate)
				}
			}
			for _, res := range grading.AssertionResults {
				if !res.Passed {
					it.Failed = append(it.Failed, QualityFailure{Case: e.Name(), Arm: arm,
						Text: res.Text, Evidence: res.Evidence})
				}
			}
		}
	}
	it.With.CaseCount = it.Cases
	it.Without.CaseCount = it.Cases
	it.With.Rate = meanFloat(ratesWith)
	it.Without.Rate = meanFloat(ratesWithout)
	if it.With.Graded() && it.Without.Graded() {
		it.Delta = it.With.Rate - it.Without.Rate
	}
	it.SetComplete = len(declared) > 0 && it.Cases == len(declared)
	it.Complete = it.SetComplete && len(ratesWith) == it.Cases && len(ratesWithout) == it.Cases
	it.Partial = (it.With.Graded() || it.Without.Graded()) && !it.Complete
	return it
}

// gradedArms is how many of the two arms judged every case of the iteration.
func gradedArms(it QualityIteration) int {
	n := 0
	if it.Complete {
		return 2
	}
	if it.Cases > 0 && it.With.Cases == it.Cases {
		n++
	}
	if it.Cases > 0 && it.Without.Cases == it.Cases {
		n++
	}
	return n
}

// iterationNumbers lists an output workspace's iteration dirs, ascending.
func iterationNumbers(workspace string) []int {
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "iteration-") {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "iteration-")); err == nil {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

// bestQuality picks the best with-skill pass rate among complete iterations. A partial grade never
// wins: it is not a measurement of the same set, and the trigger lab learned that the hard way.
func bestQuality(iters []QualityIteration) (int, bool) {
	best, bestRate := 0, -1.0
	var complete []QualityIteration
	for _, it := range iters {
		if !it.Complete {
			continue
		}
		complete = append(complete, it)
		if it.With.Rate >= bestRate {
			best, bestRate = it.N, it.With.Rate
		}
	}
	flat := false
	if len(complete) >= 2 {
		a, b := complete[len(complete)-2], complete[len(complete)-1]
		flat = a.With.Rate == b.With.Rate && a.With.Judged == b.With.Judged
	}
	return best, flat
}

// qualityChecks is the guide's preconditions, with numbers: a set that cannot grade cannot guide.
func qualityChecks(lab *QualityLab) []LabCheck {
	var checks []LabCheck
	checks = append(checks, LabCheck{
		Key: "cases", OK: lab.Cases > 0, Blocking: lab.Cases == 0,
		Detail: plural(lab.Cases, "case"),
		Fix:    "cases",
	})
	checks = append(checks, LabCheck{
		Key: "assertions", OK: lab.Assertions > 0, Blocking: lab.Assertions == 0,
		Detail: fmt.Sprintf("%d assertion(s) across %d case(s)", lab.Assertions, lab.Cases),
		Fix:    "assertions",
	})
	checks = append(checks, LabCheck{
		Key: "graded", OK: lab.Ungraded == 0 && len(lab.Iterations) > 0,
		Detail: gradedDetail(lab),
		Fix:    "grade",
	})
	return checks
}

func gradedDetail(lab *QualityLab) string {
	if len(lab.Iterations) == 0 {
		return "nothing measured yet"
	}
	if lab.Ungraded == 0 {
		return "every iteration's arms are graded"
	}
	return fmt.Sprintf("%d arm(s) without verdicts", lab.Ungraded)
}

// qualityNext is one line: the move the guide asks for next, given what is on disk.
func qualityNext(lab *QualityLab) LabNext {
	switch {
	case lab.Cases == 0:
		return LabNext{Key: "set", Line: "write the cases", Detail: lab.Skill + "/evals/evals.json"}
	case len(lab.Iterations) == 0:
		return LabNext{Key: "run", Line: fmt.Sprintf("run iteration 1 — %s × 2 arms", plural(lab.Cases, "case")),
			Detail: "nothing measured yet; the first run is also what shows which assertions to write"}
	case lab.Assertions == 0:
		return LabNext{Key: "assertions",
			Line:   "read the first run, then write assertions into evals.json",
			Detail: "without them there is no pass rate and nothing to optimize"}
	}
	last := lab.Iterations[len(lab.Iterations)-1]
	if !last.SetComplete {
		return LabNext{Key: "set", Line: fmt.Sprintf("iteration %d ran %d of %d cases — rerun the whole set",
			last.N, last.Cases, lab.Cases),
			Detail: "a rate over a subset is not comparable with one over the set"}
	}
	if !last.Complete {
		owed := 2 - gradedArms(last)
		return LabNext{Key: "grade", Line: fmt.Sprintf("grade iteration %d — %d arm(s) owe verdicts", last.N, owed),
			Detail: "the judge reads each arm's outputs against the declared assertions"}
	}
	if len(last.Failed) > 0 {
		return LabNext{Key: "revise",
			Line: fmt.Sprintf("revise SKILL.md against %d failed assertion(s), then run iteration %d",
				len(last.Failed), last.N+1),
			Detail: "fix the cause, not the case: the skill is used on prompts that are not in this set"}
	}
	if lab.Flat {
		return LabNext{Key: "flat",
			Line:   fmt.Sprintf("no improvement since iteration %d — stop, or sharpen the assertions", last.N-1),
			Detail: "a plateau is the guide's stop signal as much as a clean set is"}
	}
	return LabNext{Key: "clean",
		Line:   fmt.Sprintf("iteration %d passes every assertion — add a harder case, or stop", last.N),
		Detail: "a set that only passes measures nothing"}
}

// plural is the lab's own count spelling; the store's format.Plural lives in the CLI's layer.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// QualityCase is one case of one iteration, as the workbench reads it: both arms' verdicts, the
// failed assertions, and the human note. The lab ranks iterations; this is the evidence behind a
// row, which is what the operator actually edits a skill from.
type QualityCase struct {
	Skill          string           `json:"skill"`
	ID             int              `json:"id"`
	Slug           string           `json:"slug"`
	Prompt         string           `json:"prompt"`
	ExpectedOutput string           `json:"expected_output"`
	Assertions     int              `json:"assertions"`
	With           QualityArm       `json:"with_skill"`
	Without        QualityArm       `json:"without_skill"`
	Failed         []QualityFailure `json:"failed,omitempty"`
	Feedback       string           `json:"feedback,omitempty"`
}

// QualityCases reads one iteration's cases in the skill's own case order.
func (r *Runner) QualityCases(skill string, iteration int) ([]QualityCase, error) {
	cases, err := LoadCases(r.Store, []string{skill}, nil)
	if err != nil {
		return nil, err
	}
	dir := r.IterDir(r.WorkspaceDir(skill), iteration)
	feedback := map[string]string{}
	_ = readJSONFile(filepath.Join(dir, "feedback.json"), &feedback)
	var out []QualityCase
	for _, c := range cases {
		caseDir := filepath.Join(dir, c.Slug)
		if info, err := os.Stat(caseDir); err != nil || !info.IsDir() {
			continue
		}
		row := QualityCase{Skill: skill, ID: c.ID, Slug: c.Slug, Prompt: c.Prompt,
			ExpectedOutput: c.ExpectedOutput, Assertions: len(c.Assertions),
			Feedback: strings.TrimSpace(feedback[c.Slug])}
		for _, arm := range AllArms {
			armDir := filepath.Join(caseDir, arm)
			if info, err := os.Stat(armDir); err != nil || !info.IsDir() {
				continue
			}
			target := &row.With
			if arm == ArmWithout {
				target = &row.Without
			}
			target.CaseCount = 1
			var grading Grading
			if err := readJSONFile(filepath.Join(armDir, "grading.json"), &grading); err != nil {
				continue
			}
			var timing Timing
			_ = readJSONFile(filepath.Join(armDir, "timing.json"), &timing)
			target.DurationMS, target.Tokens = timing.DurationMS, tokensOrZero(timing.TotalTokens)
			if grading.Summary == nil {
				continue
			}
			target.Judged, target.Passed, target.Rate, target.Cases =
				grading.Summary.Total, grading.Summary.Passed, grading.Summary.PassRate, 1
			for _, res := range grading.AssertionResults {
				if !res.Passed {
					row.Failed = append(row.Failed, QualityFailure{Case: c.Slug, Arm: arm,
						Text: res.Text, Evidence: res.Evidence})
				}
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func tokensOrZero(v *int) int64 {
	if v == nil {
		return 0
	}
	return int64(*v)
}
