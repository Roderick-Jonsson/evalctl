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
	// Response is what the arm actually wrote — the thing a human reviews, and the only thing a
	// feedback sentence can be about. It is read from the arm's grading.json, so the pane needs no
	// second read to show it.
	Response   string  `json:"response,omitempty"`
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
	Skill      string `json:"skill"`
	Cases      int    `json:"cases"`
	Assertions int    `json:"assertions"`
	// Unasserted counts the cases with nothing to be judged on. The initialization check is per
	// case and not a total: three assertions on one case leave two cases whose grading can carry no
	// verdict, so "9 assertions" is not a set the loop can measure.
	Unasserted int `json:"unasserted"`
	// InitPending is whether the loop is still closed: no set, a case with no assertion, nothing
	// run, or a newest run with no verdict at all. A run grades itself, so the last two are one move
	// — but they are not one state: an iteration written by `--no-grade` or by a build older than the
	// fold carries outputs and no verdicts, and that one is `g`'s to finish.
	InitPending bool               `json:"init_pending"`
	Iterations  []QualityIteration `json:"iterations"`
	Best        int                `json:"best"` // iteration number, 0 = no complete one
	Flat        bool               `json:"flat"` // the last two complete iterations tie
	Ungraded    int                `json:"ungraded_arms"`
	Checks      []LabCheck         `json:"checks"`
	Next        LabNext            `json:"next"`
}

// QualityLabOf reads one skill's output loop back from disk.
func (r *Runner) QualityLab(skill string) (*QualityLab, error) {
	sk := r.Store.Skill(skill)
	if sk == nil {
		return nil, fmt.Errorf("unknown skill %q", skill)
	}
	// A skill with a SKILL.md and no eval file yet has no set: that is a place to start, not an
	// error, and the screens need a lab to say so.
	if !sk.OnDisk {
		return &QualityLab{Skill: skill, InitPending: true,
			Checks: qualityChecks(&QualityLab{Skill: skill}), Next: qualityNext(&QualityLab{Skill: skill})}, nil
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
		if len(c.Assertions) == 0 {
			lab.Unasserted++
		}
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
	lab.InitPending = initPending(lab)
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

// qualityChecks is the loop's entry, in four steps and in the order they have to be done: declare
// the set, give every case something to be judged on, run it once, grade that run. The first two are
// the declaration (a file); the last two are the only proof that the set can be measured at all.
//
// It used to be three checks with `graded` merging "nothing has run" with "nothing was judged", which
// is the distinction the whole entry turns on: a run is outputs, and the loop has nothing to revise
// from until a judge has read them.
func qualityChecks(lab *QualityLab) []LabCheck {
	var checks []LabCheck
	checks = append(checks, LabCheck{
		Key: "cases", OK: lab.Cases > 0, Blocking: lab.Cases == 0,
		Detail: declaredDetail(lab.Cases), Fix: "S",
	})
	last, has := lastIteration(lab)
	// The run comes before the assertions, because that is the order the work is done in: the first
	// run exists to show what the agent writes, and what it writes is what the assertions are written
	// from. A run over a set with no assertion is not a measurement — it is the look that a sentence
	// is written against — and it is the one spend this lane offers before anything can be judged.
	run := LabCheck{Key: "run", OK: has, Blocking: !has, Fix: "R", Detail: "nothing run yet"}
	switch {
	case has && !last.SetComplete:
		// A run that reached part of the set still opened it; the row says what it missed, because
		// the next run has to cover the set to be comparable with it.
		run.Detail = fmt.Sprintf("iteration %d ran %d of %s", last.N, last.Cases, plural(lab.Cases, "case"))
	case has:
		run.Detail = fmt.Sprintf("iteration %d · %s · outputs to read", last.N, plural(last.Cases, "case"))
	}
	checks = append(checks, run)
	checks = append(checks, LabCheck{
		Key:      "assertions",
		OK:       lab.Cases > 0 && lab.Unasserted == 0,
		Blocking: lab.Cases == 0 || lab.Unasserted > 0,
		Detail:   assertionDetail(lab),
		Fix:      "e",
	})
	// The judge is part of the run, so there is no grade step until there is a run to grade. Once one
	// exists, this row is the recovery — a run made before the assertions were written, one made with
	// --no-grade, or one whose grade was cancelled or failed — and `g` is what answers it.
	if !has {
		return checks
	}
	grade := LabCheck{Key: "grade", OK: gradedArms(last) > 0,
		Blocking: gradedArms(last) == 0, Fix: "g",
		Detail: fmt.Sprintf("iteration %d · %d of 2 arms graded", last.N, gradedArms(last))}
	if gradedArms(last) == 0 {
		grade.Detail = fmt.Sprintf("iteration %d carries no verdicts — g grades it", last.N)
	}
	checks = append(checks, grade)
	return checks
}

// declaredDetail is the cases row: the count, said as a declaration and not as a repetition of the
// row's own label.
func declaredDetail(n int) string {
	if n == 0 {
		return "none declared"
	}
	return fmt.Sprintf("%d declared", n)
}

// assertionDetail is the one line under the assertions step: a total is not the question, because a
// case with no assertion produces no verdict however many the other cases carry. Once a run is on
// disk the line says where the sentences are written from, because that is the step the view is on.
func assertionDetail(lab *QualityLab) string {
	last, has := lastIteration(lab)
	switch {
	case lab.Cases == 0:
		return "no case to judge yet"
	case lab.Unasserted == lab.Cases && has:
		return fmt.Sprintf("none of %s carry one — write them from iteration %d's outputs",
			plural(lab.Cases, "case"), last.N)
	case lab.Unasserted == lab.Cases:
		return fmt.Sprintf("none of %s carry one", plural(lab.Cases, "case"))
	case lab.Unasserted > 0:
		return fmt.Sprintf("%d of %s carry none · %s declared",
			lab.Unasserted, plural(lab.Cases, "case"), plural(lab.Assertions, "assertion"))
	}
	return fmt.Sprintf("%s across %s", plural(lab.Assertions, "assertion"), plural(lab.Cases, "case"))
}

// lastIteration is the newest iteration on disk, which is the one the entry reads: an older run that
// was graded says nothing about the run the loop is holding now.
func lastIteration(lab *QualityLab) (QualityIteration, bool) {
	if len(lab.Iterations) == 0 {
		return QualityIteration{}, false
	}
	return lab.Iterations[len(lab.Iterations)-1], true
}

// initPending is whether the loop is still closed — the one predicate the entry screen is drawn
// from. Anything the loop needs and does not have closes it, including the grade: a run whose outputs
// nobody read is not an iteration, it is a directory.
func initPending(lab *QualityLab) bool {
	last, has := lastIteration(lab)
	if !has {
		return true
	}
	return lab.Cases == 0 || lab.Unasserted > 0 || gradedArms(last) == 0
}

// qualityNext is one line: the move the guide asks for next, given what is on disk. Its branches are
// the entry's, in the entry's order — the cases, the first run, an assertion per case, then the grade
// — because the first run is what the assertions are written from: a line that says "write the
// assertions" over a set nobody has run is the instruction that makes them guesses.
func qualityNext(lab *QualityLab) LabNext {
	last, has := lastIteration(lab)
	switch {
	case lab.Cases == 0:
		return LabNext{Key: "set", Line: "write the cases", Detail: lab.Skill + "/evals/evals.json"}
	case !has && lab.Unasserted == lab.Cases:
		return LabNext{Key: "run",
			Line:   fmt.Sprintf("run iteration 1 — %s × 2 arms; the outputs are what the assertions are written from", plural(lab.Cases, "case")),
			Detail: "nothing is judged yet: there is nothing to judge, and the first run is the look"}
	case !has:
		return LabNext{Key: "run", Line: fmt.Sprintf("run iteration 1 — %s × 2 arms, then the judge",
			plural(lab.Cases, "case")),
			Detail: "the run grades itself; the verdicts are what a revision is written from"}
	case lab.Unasserted > 0:
		return LabNext{Key: "assertions",
			Line: fmt.Sprintf("%d of %s carry no assertion — e writes them from iteration %d's outputs",
				lab.Unasserted, plural(lab.Cases, "case"), last.N),
			Detail: "a grade is made of assertions; a case without one has no verdict to give"}
	case gradedArms(last) == 0:
		return LabNext{Key: "grade",
			Line:   fmt.Sprintf("iteration %d carries no verdicts — g grades it", last.N),
			Detail: "the assertions are declared now; the judge reads each arm against them"}
	}
	if !last.SetComplete {
		// An open iteration is finished, not restarted: the runs it already holds are a measurement of
		// those cases, and re-running the set to reach the three that make it whole would buy them again.
		return LabNext{Key: "set", Line: fmt.Sprintf("iteration %d holds %d of %d cases — R runs the %d that are open",
			last.N, last.Cases, lab.Cases, lab.Cases-last.Cases),
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
				last.FailedAssertions(), last.N+1),
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

// QualityCase is one declared case as the setup view reads it: the declaration, the two arms of the
// iteration on screen, and — once a judge has answered — the verdicts per assertion.
//
// Ran is whether the iteration on screen actually reached the case. A case that has never been run and
// a case whose run has not been judged yet both carry no verdicts, and the set view's next move is
// different for the two: the first is a run, the second is an assertion.
type QualityCase struct {
	Skill          string             `json:"skill"`
	ID             int                `json:"id"`
	Slug           string             `json:"slug"`
	Prompt         string             `json:"prompt"`
	ExpectedOutput string             `json:"expected_output"`
	Files          []string           `json:"files"`
	Assertions     int                `json:"assertions"`
	Ran            bool               `json:"ran,omitempty"`
	With           QualityArm         `json:"with_skill"`
	Without        QualityArm         `json:"without_skill"`
	Rows           []QualityAssertion `json:"rows,omitempty"`
	Failed         []QualityFailure   `json:"failed,omitempty"`
	Feedback       string             `json:"feedback,omitempty"`
}

// Graded reports whether a judge has answered for either arm of this case.
func (c QualityCase) Graded() bool { return c.With.Judged > 0 || c.Without.Judged > 0 }

// Unjudged is how many of the case's declared assertions carry no verdict in either arm while the
// judge has read the case at least once: the sentences that were written or rewritten after the grade.
// A verdict belongs to the words it was written against, so an edited assertion is not graded — and an
// iteration that is "complete" by count can still owe verdicts for what the list says now. That is the
// guide's order: re-grade after editing the assertions, and revise from the verdicts you can trust.
func (c QualityCase) Unjudged() int {
	if !c.Graded() {
		return 0
	}
	n := 0
	for _, a := range c.Rows {
		if a.With == nil && a.Without == nil {
			n++
		}
	}
	return n
}

// FailedAssertions is how many of the case's declared assertions failed, each counted once. A failure
// is recorded per assertion *and* arm, because the judge's evidence differs by arm, so an assertion
// wrong in both arms is one failed assertion and two pieces of evidence. Anything printed beside an
// assertion total counts assertions, never arm-pairs.
func (c *QualityCase) FailedAssertions() int { return distinctAssertions(c.Failed) }

// FailedAssertions is the same count over a whole iteration, keyed by case as well: two cases may
// declare the same sentence, and that is two failures, not one.
func (it *QualityIteration) FailedAssertions() int {
	seen := make(map[string]bool, len(it.Failed))
	for _, f := range it.Failed {
		seen[f.Case+"\x00"+f.Text] = true
	}
	return len(seen)
}

func distinctAssertions(fails []QualityFailure) int {
	seen := make(map[string]bool, len(fails))
	for _, f := range fails {
		seen[f.Text] = true
	}
	return len(seen)
}

// QualityAssertion is one declared assertion with each arm's verdict. A nil verdict is "no verdict",
// not a pass: an assertion written after the arm was graded has never been judged, and the screen
// says so instead of borrowing the neighbour's mark.
type QualityAssertion struct {
	Text    string `json:"text"`
	With    *bool  `json:"with_skill,omitempty"`
	Without *bool  `json:"without_skill,omitempty"`
	// Evidence is the judge's words for the arms that failed it: "with_skill" / "without_skill".
	Evidence map[string]string `json:"evidence,omitempty"`
}

// verdictMark is one arm's verdict as the screens print it: ✓, ✗, or a dash for a verdict nobody
// has given.
func (a QualityAssertion) Verdict(arm string) *bool {
	if arm == ArmWith {
		return a.With
	}
	return a.Without
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
		// A case the set declares but no run has reached yet is still a case, and it is the one the
		// setup section exists for: the assertions of iteration 1 are written before iteration 1
		// exists, so the list has to hold a declaration with no arm behind it. Its verdict cells
		// read as dashes, which is what "not judged" looks like.
		row := QualityCase{Skill: skill, ID: c.ID, Slug: c.Slug, Prompt: c.Prompt,
			ExpectedOutput: c.ExpectedOutput, Files: append([]string(nil), c.Files...),
			Assertions: len(c.Assertions), Feedback: strings.TrimSpace(feedback[c.Slug])}
		caseDir := filepath.Join(dir, c.Slug)
		if info, err := os.Stat(caseDir); err == nil && info.IsDir() {
			row.Ran = true
		}
		// The judge's answers per arm, kept whole: the pane shows every assertion with both marks,
		// not just the failures, because "passes in both arms" is the pattern a revision starts from.
		verdicts := map[string][]AssertionResult{}
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
			target.Response = grading.Response
			if grading.Summary == nil {
				continue
			}
			target.Judged, target.Passed, target.Rate, target.Cases =
				grading.Summary.Total, grading.Summary.Passed, grading.Summary.PassRate, 1
			verdicts[arm] = grading.AssertionResults
			for _, res := range grading.AssertionResults {
				if !res.Passed {
					row.Failed = append(row.Failed, QualityFailure{Case: c.Slug, Arm: arm,
						Text: res.Text, Evidence: res.Evidence})
				}
			}
		}
		row.Rows = assertionRows(c.Assertions, verdicts)
		out = append(out, row)
	}
	return out, nil
}

// assertionRows joins the declared list to each arm's verdicts. The join is by position while the
// judge answered the whole list in order — that is how grading writes it, one result per declared
// assertion — and by text once the list has changed under the verdicts: an assertion written after
// the arm was graded has no verdict, and text is the only key the two readings still share.
func assertionRows(declared []string, verdicts map[string][]AssertionResult) []QualityAssertion {
	if len(declared) == 0 {
		return nil
	}
	out := make([]QualityAssertion, 0, len(declared))
	for i, text := range declared {
		qa := QualityAssertion{Text: text}
		for _, arm := range AllArms {
			res, ok := verdictFor(verdicts[arm], i, text)
			if !ok {
				continue
			}
			passed := res.Passed
			if arm == ArmWith {
				qa.With = &passed
			} else {
				qa.Without = &passed
			}
			if !res.Passed {
				if qa.Evidence == nil {
					qa.Evidence = map[string]string{}
				}
				qa.Evidence[arm] = res.Evidence
			}
		}
		out = append(out, qa)
	}
	return out
}

// verdictFor finds one assertion's result in one arm: the same slot first, the same words second,
// and nothing at all when the arm never saw this assertion.
func verdictFor(results []AssertionResult, i int, text string) (AssertionResult, bool) {
	if i < len(results) && results[i].Text == text {
		return results[i], true
	}
	for _, r := range results {
		if r.Text == text {
			return r, true
		}
	}
	return AssertionResult{}, false
}

func tokensOrZero(v *int) int64 {
	if v == nil {
		return 0
	}
	return int64(*v)
}
