package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"evalctl/internal/model"
	"evalctl/internal/store"
)

// The description lab is the agentskills.io optimization loop as state rather than as advice.
//
// The guide's loop is five moves — evaluate the current description on both splits, triage the
// train failures only, revise toward the concept rather than the wording, repeat until the train
// set is clean or flat, then pick the iteration with the best VALIDATION rate (which is not
// necessarily the last one) and check it against fresh queries. Every one of those moves is
// arithmetic over files this tree already holds: a trigger iteration records the description it
// ran in meta.json, and trigger.json scores every query. So the lab computes the loop instead of
// remembering it, and the TUI's job is to ask the one question a machine cannot answer — what
// category of task did that failing query represent?
//
// Two rules keep the file and the numbers honest:
//   - a candidate is measured by being in SKILL.md, because that is what the router reads: the lab
//     writes it before the run and says so;
//   - a measurement is joined by the description's hash, never copied into a second ledger that
//     could drift from the iteration it came from.

// LabStep is where in the guide's loop a skill stands, numbered by the guide's own sections rather
// than by anything invented here: the set you test with, the description you start from, and then
// "The optimization loop" and "Applying the result" in their own order — evaluate, identify the
// train failures, revise, repeat, select the best by validation, apply, check with fresh queries.
//
// Steps 3, 4 and 5 are a cycle: the guide's "repeat steps 1-3 until all train set queries pass or you
// stop seeing meaningful improvement". Everything after the cycle is done once, on the winner.
const (
	StepSet = iota + 1
	StepBaseline
	StepEvaluate
	StepFailures
	StepRevise
	StepBest
	StepApply
)

// Steps is the order the rail draws: the guide's loop, with StepRevise and StepEvaluate forming the
// cycle the ↻ mark in the middle of the strip stands for.
var Steps = []int{StepSet, StepBaseline, StepEvaluate, StepFailures, StepRevise, StepBest, StepApply}

// StepNames are the rail's short labels.
var StepNames = map[int]string{
	StepSet:      "set",
	StepBaseline: "baseline",
	StepEvaluate: "evaluate",
	StepFailures: "failures",
	StepRevise:   "revise",
	StepBest:     "best",
	StepApply:    "apply",
}

// StepTitles are the steps in the guide's words: the heading every step screen opens with, so the
// screen says what the section says and a person can follow the page with the page's own names.
var StepTitles = map[int]string{
	StepSet:      "Design the trigger set",
	StepBaseline: "Start from the description the router reads",
	StepEvaluate: "Evaluate on train and validation",
	StepFailures: "Identify the failures — train set only",
	StepRevise:   "Revise the description",
	StepBest:     "Select the best by its validation rate",
	StepApply:    "Apply the result",
}

// StepLead is the one sentence each step exists for: the guide's rule, in the loop's situation.
var StepLead = map[int]string{
	StepSet:      "The loop tunes on the train split and is judged by the validation split. A set that cannot tell those apart cannot guide anything.",
	StepBaseline: "The description carries the whole burden of triggering, and the loop has to start from the text the router actually reads — not a paraphrase of it.",
	StepEvaluate: "Run the whole set against one wording, several times per query, and read the rate. Nothing else is compared but this number.",
	StepFailures: "Only the train failures may guide a revision. Validation is what judges it — which is why it is hidden here.",
	StepRevise:   "Broaden if should-trigger rows are missing, narrow if should-not-trigger rows fire, and never paste the words of a query into the description.",
	StepBest:     "The best description is the one with the best validation rate, which is often not the last one written.",
	StepApply:    "Write the winner into SKILL.md, check the limit, then test it against queries that were never part of the loop.",
}

// LabCheck is one precondition of the loop, as the guide states it. Blocking checks are the ones
// the runner itself would refuse; the rest are the guide's advice with a number attached, because
// "roughly 60/40" is not actionable and "8 train / 3 validation" is.
type LabCheck struct {
	Key      string `json:"key"`
	OK       bool   `json:"ok"`
	Blocking bool   `json:"blocking"`
	Detail   string `json:"detail"`
	Fix      string `json:"fix,omitempty"` // the key that fixes it
}

// LabTally is one split's verdict: passed of judged, with the rate the guide's threshold decides.
// It is a copy of Tally with the printing done, because the screens never do arithmetic on a rate.
type LabTally struct {
	Rows   int     `json:"rows"`
	Judged int     `json:"judged"`
	Passed int     `json:"passed"`
	Rate   float64 `json:"rate"`
}

// LabMeasure is one candidate's result in one trigger iteration.
type LabMeasure struct {
	Iteration  int        `json:"iteration"`
	Started    string     `json:"started"`
	Runs       int        `json:"runs"`
	Threshold  float64    `json:"threshold"`
	Loop       LabTally   `json:"loop"`
	Train      LabTally   `json:"train"`
	Validation LabTally   `json:"validation"`
	Holdout    LabTally   `json:"holdout"`
	Rows       []ScoreRow `json:"rows"`  // this skill's rows in that iteration
	Fixed      []int      `json:"fixed"` // failed against the parent, passes here
	Broke      []int      `json:"broke"` // passed against the parent, fails here
}

// LabCandidate is one wording, the measurements of it, and what it is to the tree right now.
type LabCandidate struct {
	model.DescriptionCandidate
	SHA      string       `json:"sha"`
	Chars    int          `json:"chars"`
	Measures []LabMeasure `json:"measures"`
	Applied  bool         `json:"applied"`
	InFile   bool         `json:"in_file"`
}

// Measure is the candidate's latest measurement, or nil when it has never been run.
func (c LabCandidate) Measure() *LabMeasure {
	if len(c.Measures) == 0 {
		return nil
	}
	return &c.Measures[len(c.Measures)-1]
}

// MeasureOn is the candidate's latest measurement that actually judged one split.
//
// It exists because the two halves of the loop are measured by two different runs: the iteration
// runs the train set, and generalization runs the validation set against the same wording. Joined by
// the description's hash they are two measurements of one candidate, and reading the newest one
// alone would throw away whichever half was run first.
func (c LabCandidate) MeasureOn(split string) *LabMeasure {
	for i := len(c.Measures) - 1; i >= 0; i-- {
		m := &c.Measures[i]
		switch split {
		case SplitTrain:
			if m.Train.Judged > 0 {
				return m
			}
		case SplitValidation:
			if m.Validation.Judged > 0 {
				return m
			}
		case SplitHoldout:
			if m.Holdout.Judged > 0 {
				return m
			}
		}
	}
	return nil
}

// FailedTrain is the train rows this candidate does not pass right now — the only rows the guide
// allows a revision to be guided by.
func (c LabCandidate) FailedTrain() []ScoreRow {
	m := c.MeasureOn(SplitTrain)
	if m == nil {
		return nil
	}
	var out []ScoreRow
	for _, row := range m.Rows {
		if row.Split != SplitTrain {
			continue
		}
		if row.Passed != nil && !*row.Passed {
			out = append(out, row)
		}
	}
	return out
}

// Unmeasured is true when the latest measurement has never happened: the draft is waiting.
func (c LabCandidate) Unmeasured() bool { return len(c.Measures) == 0 }

// LabNext is the guide's next move, computed. The TUI prints Line; Detail says why in one
// sentence, and Step tells the rail which chip to light.
type LabNext struct {
	Step   int    `json:"step"`
	Key    string `json:"key"`
	Line   string `json:"line"`
	Detail string `json:"detail,omitempty"`
}

// Lab is a skill's whole state in the loop.
type Lab struct {
	Skill          string                    `json:"skill"`
	File           string                    `json:"file"` // the description SKILL.md carries now
	FileSHA        string                    `json:"file_sha"`
	FileChars      int                       `json:"file_chars"`
	FileInLedger   bool                      `json:"file_in_ledger"`
	Candidates     []LabCandidate            `json:"candidates"`
	Applied        *model.AppliedDescription `json:"applied,omitempty"`
	Drifted        bool                      `json:"drifted"`
	Checks         []LabCheck                `json:"checks"`
	Ready          bool                      `json:"ready"`
	Best           *LabCandidate             `json:"best,omitempty"`
	Next           LabNext                   `json:"next"`
	Iterations     int                       `json:"iterations"` // trigger iterations that measured this skill
	Measured       int                       `json:"measured"`
	TrainRows      int                       `json:"train_rows"`
	ValidationRows int                       `json:"validation_rows"`
	HoldoutRows    int                       `json:"holdout_rows"`
	Runs           int                       `json:"runs"`
	Threshold      float64                   `json:"threshold"`
}

// Candidate finds one candidate by id.
func (l *Lab) Candidate(id int) *LabCandidate {
	for i := range l.Candidates {
		if l.Candidates[i].ID == id {
			return &l.Candidates[i]
		}
	}
	return nil
}

// LoopRows are the rows the loop is allowed to tune on: train and validation, never the holdout.
func LoopRows(rows []ScoreRow) []ScoreRow {
	out := make([]ScoreRow, 0, len(rows))
	for _, row := range rows {
		if row.Split != SplitHoldout {
			out = append(out, row)
		}
	}
	return out
}

// DescriptionLab reads the skill's ledger, joins it against every trigger iteration that recorded
// the skill's description, and says what the guide would have you do next.
func (r *Runner) DescriptionLab(skill string) (*Lab, error) {
	if r.Store.Skill(skill) == nil {
		return nil, fmt.Errorf("unknown skill %q", skill)
	}
	ledger, _, err := r.Store.LoadDescriptionLedger(skill)
	if err != nil {
		return nil, err
	}
	lab := &Lab{Skill: skill, Applied: ledger.Applied}
	lab.File = r.Store.SkillDescription(skill)
	lab.FileSHA = store.HashText(lab.File)
	lab.FileChars = len([]rune(lab.File))
	cfg := r.TriggerConfigOf()
	lab.Runs, lab.Threshold = cfg.Runs, cfg.Threshold

	measured, err := r.descriptionMeasures(skill)
	if err != nil {
		return nil, err
	}
	lab.Iterations = len(measured)

	// Every measurement of a wording is kept, not just the newest: the train run and the
	// validation run are two measurements of one candidate, and joining by hash without keeping
	// both would throw away whichever half ran first.
	bySHA := map[string][]LabMeasure{}
	for _, m := range measured {
		bySHA[m.sha] = append(bySHA[m.sha], m.measure)
	}
	for _, c := range ledger.Candidates {
		view := LabCandidate{DescriptionCandidate: c, SHA: store.HashText(c.Text),
			Chars: len([]rune(store.FoldText(c.Text)))}
		if ms, ok := bySHA[view.SHA]; ok {
			view.Measures = append(view.Measures, ms...)
		}
		view.InFile = view.SHA == lab.FileSHA
		view.Applied = lab.Applied != nil && lab.Applied.Candidate == c.ID
		if view.Applied && lab.Applied != nil && lab.Applied.SHA256 != "" && lab.Applied.SHA256 != lab.FileSHA {
			lab.Drifted = true
		}
		lab.Candidates = append(lab.Candidates, view)
	}
	for i := range lab.Candidates {
		if !lab.Candidates[i].Unmeasured() {
			lab.Measured++
		}
	}
	// Fixed/broke are the diff against the parent's own measurement: the record of what a revision
	// actually did, which is what stops the next revision from re-breaking what this one fixed.
	for i := range lab.Candidates {
		c := &lab.Candidates[i]
		if c.Parent == 0 {
			continue
		}
		parent := lab.Candidate(c.Parent)
		if parent == nil || parent.Unmeasured() || c.Unmeasured() {
			continue
		}
		before := map[int]bool{}
		for _, row := range parent.Measure().Rows {
			if row.Passed != nil {
				before[row.ID] = *row.Passed
			}
		}
		m := &c.Measures[len(c.Measures)-1]
		for _, row := range m.Rows {
			if row.Passed == nil {
				continue
			}
			was, ok := before[row.ID]
			switch {
			case ok && !was && *row.Passed:
				m.Fixed = append(m.Fixed, row.ID)
			case ok && was && !*row.Passed:
				m.Broke = append(m.Broke, row.ID)
			}
		}
		sort.Ints(m.Fixed)
		sort.Ints(m.Broke)
	}

	lab.FileInLedger = false
	for _, c := range lab.Candidates {
		if c.InFile {
			lab.FileInLedger = true
		}
	}
	for _, e := range r.Store.Rows(store.LaneTrigger, store.Filter{Skill: skill}) {
		switch r.Store.Trigger.Queries[e.Index].Split {
		case SplitTrain:
			lab.TrainRows++
		case SplitValidation:
			lab.ValidationRows++
		case SplitHoldout:
			lab.HoldoutRows++
		}
	}
	lab.Checks, lab.Ready = r.labChecks(skill)
	lab.Best = lab.best()
	lab.Next = lab.next()
	return lab, nil
}

type measuredDescription struct {
	sha     string
	measure LabMeasure
}

// descriptionMeasures reads every trigger iteration that recorded this skill's description, with
// that skill's own rows. The iteration is global and the skill scoped, exactly like the results tab.
func (r *Runner) descriptionMeasures(skill string) ([]measuredDescription, error) {
	entries, err := os.ReadDir(r.TriggerWorkspace())
	if err != nil {
		return nil, nil // no trigger run has ever happened here
	}
	var out []measuredDescription
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "trigger-") {
			continue
		}
		iteration, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "trigger-"))
		if err != nil {
			continue
		}
		dir := filepath.Join(r.TriggerWorkspace(), e.Name())
		var meta metaTrigger
		if err := readJSONFile(filepath.Join(dir, "meta.json"), &meta); err != nil {
			continue
		}
		desc, ok := meta.Provenance.Descriptions[skill]
		if !ok || desc.Description == "" {
			continue
		}
		var file struct {
			Score TriggerScore `json:"score"`
		}
		if err := readJSONFile(filepath.Join(dir, "trigger.json"), &file); err != nil {
			continue
		}
		rows := make([]ScoreRow, 0)
		for _, row := range file.Score.Rows {
			if row.Skill == skill {
				rows = append(rows, row)
			}
		}
		if len(rows) == 0 {
			continue
		}
		measure := LabMeasure{
			Iteration: iteration, Started: meta.Started, Runs: meta.Runs, Threshold: meta.Threshold,
			Loop:       labTally(tally(LoopRows(rows))),
			Train:      labTally(tally(splitRows(rows, SplitTrain))),
			Validation: labTally(tally(splitRows(rows, SplitValidation))),
			Holdout:    labTally(tally(splitRows(rows, SplitHoldout))),
			Rows:       rows,
		}
		out = append(out, measuredDescription{sha: store.HashText(desc.Description), measure: measure})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].measure.Iteration < out[j].measure.Iteration })
	return out, nil
}

func splitRows(rows []ScoreRow, split string) []ScoreRow {
	var out []ScoreRow
	for _, row := range rows {
		if row.Split == split {
			out = append(out, row)
		}
	}
	return out
}

func labTally(t Tally) LabTally {
	out := LabTally{Rows: t.Rows, Judged: t.Judged, Passed: t.Passed}
	if t.Rate != nil {
		out.Rate = *t.Rate
	}
	return out
}

// best is the guide's selection rule: the iteration with the best VALIDATION rate, because
// validation is the half nobody tuned against. Train breaks the tie, then the shorter wording.
func (l *Lab) best() *LabCandidate {
	var best *LabCandidate
	for i := range l.Candidates {
		c := &l.Candidates[i]
		if c.MeasureOn(SplitValidation) == nil {
			continue
		}
		if best == nil || betterPick(c, best) {
			best = c
		}
	}
	return best
}

func betterPick(a, b *LabCandidate) bool {
	av, bv := a.MeasureOn(SplitValidation), b.MeasureOn(SplitValidation)
	switch {
	case av.Validation.Rate != bv.Validation.Rate:
		return av.Validation.Rate > bv.Validation.Rate
	}
	at, bt := a.MeasureOn(SplitTrain), b.MeasureOn(SplitTrain)
	if at != nil && bt != nil && at.Train.Rate != bt.Train.Rate {
		return at.Train.Rate > bt.Train.Rate
	}
	if a.Chars != b.Chars {
		return a.Chars < b.Chars
	}
	return a.ID < b.ID
}

// labChecks is the guide's "designing trigger eval queries" section, tested.
func (r *Runner) labChecks(skill string) ([]LabCheck, bool) {
	var checks []LabCheck
	rows := r.Store.Rows(store.LaneTrigger, store.Filter{Skill: skill})
	train := 0
	validation := 0
	holdout := 0
	labelsOf := map[string]map[bool]bool{SplitTrain: {}, SplitValidation: {}, SplitHoldout: {}}
	missingWhy := 0
	for _, e := range rows {
		q := r.Store.Trigger.Queries[e.Index]
		labelsOf[q.Split][q.ShouldTrigger] = true
		switch q.Split {
		case SplitTrain:
			train++
		case SplitValidation:
			validation++
		case SplitHoldout:
			holdout++
		}
		if strings.TrimSpace(q.Why) == "" {
			missingWhy++
		}
	}
	loop := train + validation

	checks = append(checks, LabCheck{
		Key: "set.train", OK: len(labelsOf[SplitTrain]) == 2, Blocking: true,
		Detail: fmt.Sprintf("%d rows, %s", train, labelWord(labelsOf[SplitTrain], train)),
		Fix:    "n",
	})
	checks = append(checks, LabCheck{
		Key: "set.validation", OK: len(labelsOf[SplitValidation]) == 2, Blocking: true,
		Detail: fmt.Sprintf("%d rows, %s", validation, labelWord(labelsOf[SplitValidation], validation)),
		Fix:    "n",
	})
	share := 0.0
	if loop > 0 {
		share = float64(validation) / float64(loop)
	}
	checks = append(checks, LabCheck{
		Key: "set.split", OK: loop > 0 && share >= 0.3 && share <= 0.45, Blocking: false,
		Detail: fmt.Sprintf("%.0f%% validation — the guide asks for roughly 40", share*100),
		Fix:    "s",
	})
	checks = append(checks, LabCheck{
		Key: "set.size", OK: loop >= 12, Blocking: false,
		Detail: fmt.Sprintf("%d loop rows — a rate over fewer than a dozen is noise", loop),
		Fix:    "n",
	})
	checks = append(checks, LabCheck{
		Key: "set.why", OK: missingWhy == 0, Blocking: false,
		Detail: fmt.Sprintf("%d row(s) carry no `why` — the near-miss record is what a revision reads", missingWhy),
		Fix:    "e",
	})
	checks = append(checks, LabCheck{
		Key: "set.runs", OK: r.TriggerConfigOf().Runs >= 3, Blocking: false,
		Detail: fmt.Sprintf("%d run(s) per query — one cannot tell a flaky miss from a real one", r.TriggerConfigOf().Runs),
	})
	checks = append(checks, LabCheck{
		Key: "set.holdout", OK: holdout > 0, Blocking: false,
		Detail: fmt.Sprintf("%d holdout row(s) — the fresh check the loop closes with", holdout),
		Fix:    "h",
	})
	ready := true
	for _, c := range checks {
		if c.Blocking && !c.OK {
			ready = false
		}
	}
	return checks, ready
}

func labelWord(labels map[bool]bool, n int) string {
	if n == 0 {
		return "— no rows"
	}
	if len(labels) == 2 {
		return "present"
	}
	if labels[true] {
		return "missing should-not-trigger"
	}
	return "missing should-trigger"
}

// next is the whole point of the lab: one line, computed from the state, that is what the guide
// would tell you to do at this moment. It is recomputed on every read, so it cannot go stale.
//
// The order is the guide's own: the set, the wording it reads, evaluate, identify the train
// failures, revise (and back to evaluate), select the best by validation, apply, fresh queries.
func (l *Lab) next() LabNext {
	if !l.Ready {
		for _, c := range l.Checks {
			if c.Blocking && !c.OK {
				return LabNext{Step: StepSet, Key: "set",
					Line:   "the set cannot be measured yet: " + c.Detail,
					Detail: "the runner refuses this file — fix the set before drafting"}
			}
		}
	}
	if l.Drifted && l.Applied != nil {
		return LabNext{Step: StepEvaluate, Key: "evaluate",
			Line:   fmt.Sprintf("SKILL.md has moved since #%d was applied — evaluate it again", l.Applied.Candidate),
			Detail: "the applied description is not the text in the file, so the rate beside it describes something else"}
	}
	if len(l.Candidates) == 0 || !l.FileInLedger {
		line := "adopt the description SKILL.md carries as candidate #1"
		if len(l.Candidates) > 0 {
			line = fmt.Sprintf("SKILL.md's wording is not in the ledger — adopt it as candidate #%d", l.Candidates[len(l.Candidates)-1].ID+1)
		}
		return LabNext{Step: StepBaseline, Key: "baseline", Line: line,
			Detail: "the loop starts from what the router reads today, so the first candidate is the file itself"}
	}
	last := l.latest()
	if last == nil {
		return LabNext{Step: StepBaseline, Key: "baseline", Line: "write the baseline wording"}
	}
	if last.Unmeasured() {
		return LabNext{Step: StepEvaluate, Key: "evaluate",
			Line:   fmt.Sprintf("evaluate #%d — %d chars, %d train + %d validation rows × %d runs", last.ID, last.Chars, l.Count(SplitTrain), l.Count(SplitValidation), l.Runs),
			Detail: "evaluating writes the wording into SKILL.md first: that is what the router reads"}
	}
	failed := last.FailedTrain()
	if len(failed) > 0 {
		if l.flattened() {
			return LabNext{Step: StepFailures, Key: "stuck",
				Line:   fmt.Sprintf("stop revising: %d iterations, no train gain — the queries may be the problem", l.Measured),
				Detail: "the guide: if performance is not improving, the issue may be with the queries (too easy, too hard, or mislabeled) rather than the description"}
		}
		return LabNext{Step: StepFailures, Key: "failures",
			Line:   fmt.Sprintf("%d train row(s) fail on #%d — %s", len(failed), last.ID, idsLine(failed)),
			Detail: "these guide the revision; the validation rows judge it. V shows them, and should not, until the next draft exists"}
	}
	// The train set passes for this candidate. Choose, then close the loop with fresh queries.
	if l.Applied == nil || l.Best == nil || l.Applied.Candidate != l.Best.ID {
		best := l.Best
		if best == nil {
			best = last
		}
		bv, bt := best.MeasureOn(SplitValidation), best.MeasureOn(SplitTrain)
		line := fmt.Sprintf("select #%d — validation not measured yet", best.ID)
		if bv != nil {
			line = fmt.Sprintf("select #%d — best validation %d/%d", best.ID, bv.Validation.Passed, bv.Validation.Judged)
			if bt != nil {
				line += fmt.Sprintf(" (train %d/%d)", bt.Train.Passed, bt.Train.Judged)
			}
		}
		return LabNext{Step: StepBest, Key: "best", Line: line,
			Detail: "the best description is not necessarily the last one; validation is the number that ranks them"}
	}
	if hold := last.MeasureOn(SplitHoldout); hold != nil && hold.Holdout.Judged > 0 {
		return LabNext{Step: StepApply, Key: "closed",
			Line: fmt.Sprintf("loop closed: #%d applied · fresh queries %d/%d (%s)",
				l.Applied.Candidate, hold.Holdout.Passed, hold.Holdout.Judged, pctOf(hold.Holdout, true)),
			Detail: "reopen the loop when the set changes; the applied description stays the record of why this wording won"}
	}
	if l.Count(SplitHoldout) == 0 {
		return LabNext{Step: StepApply, Key: "apply",
			Line:   fmt.Sprintf("#%d is applied and the train set is clean — write 5–10 fresh queries", l.Applied.Candidate),
			Detail: "queries that were never part of the loop are the only honest check that the wording generalizes"}
	}
	return LabNext{Step: StepApply, Key: "apply",
		Line:   "5–10 fresh queries are written but have not been run — run them once",
		Detail: "a fresh set that gets re-run becomes a validation set, and the check stops being honest"}
}

// Count is how many of this skill's rows a split holds, read off the set rather than off a run: a
// preflight has to be right before anything has been measured.
func (l *Lab) Count(split string) int {
	switch split {
	case SplitTrain:
		return l.TrainRows
	case SplitValidation:
		return l.ValidationRows
	case SplitHoldout:
		return l.HoldoutRows
	}
	return 0
}

// LatestCandidate is the highest-numbered candidate: the draft the loop is currently working on.
func (l *Lab) LatestCandidate() *LabCandidate { return l.latest() }

// LatestID is that candidate's id, or 0 when the ledger is empty.
func (l *Lab) LatestID() int {
	if c := l.latest(); c != nil {
		return c.ID
	}
	return 0
}

func (l *Lab) latest() *LabCandidate {
	var last *LabCandidate
	for i := range l.Candidates {
		if last == nil || l.Candidates[i].ID > last.ID {
			last = &l.Candidates[i]
		}
	}
	return last
}

// flattened is the guide's fifth-iteration rule: five revisions in and the best loop rate has not
// moved means the description is not what is failing.
func (l *Lab) flattened() bool {
	if l.Measured < 5 {
		return false
	}
	var rates []float64
	for _, c := range l.Candidates {
		if c.Unmeasured() {
			continue
		}
		rates = append(rates, c.Measure().Loop.Rate)
	}
	if len(rates) < 2 {
		return false
	}
	first, bestRate := rates[0], rates[0]
	for _, r := range rates {
		if r > bestRate {
			bestRate = r
		}
	}
	return bestRate <= first
}

func idsLine(rows []ScoreRow) string {
	ids := make([]string, 0, 3)
	for i, row := range rows {
		if i == 3 {
			ids = append(ids, fmt.Sprintf("+%d", len(rows)-3))
			break
		}
		ids = append(ids, fmt.Sprintf("q%02d", row.ID))
	}
	return strings.Join(ids, ", ")
}

func pctOf(t LabTally, dash bool) string {
	if t.Judged == 0 {
		if dash {
			return "—"
		}
		return "not measured"
	}
	return fmt.Sprintf("%.0f%% (%d/%d)", t.Rate*100, t.Passed, t.Judged)
}

// ---------------------------------------------------------------- writes

// AdoptFileDescription copies the description SKILL.md carries into the ledger as the loop's first
// candidate. It is how the loop starts from the truth rather than from a retyped paraphrase.
func (r *Runner) AdoptFileDescription(skill string) (model.DescriptionCandidate, error) {
	text := store.FoldText(r.Store.SkillDescription(skill))
	if text == "" {
		return model.DescriptionCandidate{}, fmt.Errorf("%s/SKILL.md has no description to adopt", skill)
	}
	ledger, _, err := r.Store.LoadDescriptionLedger(skill)
	if err != nil {
		return model.DescriptionCandidate{}, err
	}
	sha := store.HashText(text)
	for _, c := range ledger.Candidates {
		if store.HashText(c.Text) == sha {
			return c, fmt.Errorf("#%d already carries this description", c.ID)
		}
	}
	c := model.DescriptionCandidate{ID: r.Store.NextDescriptionCandidateID(skill), Text: text,
		Origin: model.CandidateFromFile, Note: "the description in SKILL.md when the loop started",
		Created: timestamp()}
	ledger.Skill = skill
	ledger.Candidates = append(ledger.Candidates, c)
	if err := r.Store.SaveDescriptionLedger(ledger); err != nil {
		return model.DescriptionCandidate{}, err
	}
	return c, nil
}

// SaveDescriptionCandidate records a revision: the text, the one-line note the guide's step 3 asks
// for ("what changed, and toward what"), and the candidate it revises.
func (r *Runner) SaveDescriptionCandidate(skill, text, note string, parent int) (model.DescriptionCandidate, error) {
	text = store.FoldText(text)
	if text == "" {
		return model.DescriptionCandidate{}, fmt.Errorf("the description is empty")
	}
	if n := len([]rune(text)); n > store.DescriptionLimit {
		return model.DescriptionCandidate{}, fmt.Errorf("the description is %d chars — the limit is %d", n, store.DescriptionLimit)
	}
	ledger, _, err := r.Store.LoadDescriptionLedger(skill)
	if err != nil {
		return model.DescriptionCandidate{}, err
	}
	if parent != 0 && ledger.Candidates != nil {
		found := false
		for _, c := range ledger.Candidates {
			if c.ID == parent {
				found = true
				break
			}
		}
		if !found {
			return model.DescriptionCandidate{}, fmt.Errorf("candidate #%d is not in this ledger", parent)
		}
	}
	sha := store.HashText(text)
	for _, c := range ledger.Candidates {
		if store.HashText(c.Text) == sha {
			return model.DescriptionCandidate{}, fmt.Errorf("#%d already carries this wording — a revision that changes nothing cannot be measured apart from it", c.ID)
		}
	}
	c := model.DescriptionCandidate{ID: r.Store.NextDescriptionCandidateID(skill), Parent: parent,
		Origin: model.CandidateFromRevision, Note: strings.TrimSpace(note), Text: text, Created: timestamp()}
	ledger.Skill = skill
	ledger.Candidates = append(ledger.Candidates, c)
	if err := r.Store.SaveDescriptionLedger(ledger); err != nil {
		return model.DescriptionCandidate{}, err
	}
	return c, nil
}

// PrepareMeasure puts a candidate's wording into SKILL.md, because the router reads the file and
// nothing else. It says what it replaced: a silent rewrite of the file the agent is about to be
// given is the one administrative step a person must not have to wonder about.
func (r *Runner) PrepareMeasure(skill string, id int) (string, error) {
	ledger, _, err := r.Store.LoadDescriptionLedger(skill)
	if err != nil {
		return "", err
	}
	var text string
	for _, c := range ledger.Candidates {
		if c.ID == id {
			text = c.Text
		}
	}
	if text == "" {
		return "", fmt.Errorf("candidate #%d is not in this ledger", id)
	}
	before := r.Store.SkillDescription(skill)
	if store.HashText(before) == store.HashText(text) {
		return fmt.Sprintf("#%d is already the description in SKILL.md", id), nil
	}
	if err := r.Store.SetSkillDescription(skill, text); err != nil {
		return "", err
	}
	return fmt.Sprintf("#%d written into %s/SKILL.md (previous wording in .evalctl/backups/)", id, skill), nil
}

// ApplyDescription settles the loop on one candidate: the file is brought to that wording if some
// later measurement left another one there, and the ledger records which iteration measured it.
func (r *Runner) ApplyDescription(skill string, id int) (string, error) {
	ledger, _, err := r.Store.LoadDescriptionLedger(skill)
	if err != nil {
		return "", err
	}
	var chosen *model.DescriptionCandidate
	for i := range ledger.Candidates {
		if ledger.Candidates[i].ID == id {
			chosen = &ledger.Candidates[i]
		}
	}
	if chosen == nil {
		return "", fmt.Errorf("candidate #%d is not in this ledger", id)
	}
	lab, err := r.DescriptionLab(skill)
	if err != nil {
		return "", err
	}
	view := lab.Candidate(id)
	if view == nil || view.Unmeasured() {
		return "", fmt.Errorf("#%d has not been measured — apply is a choice between measured descriptions", id)
	}
	if view.Measure().Train.Judged == 0 {
		return "", fmt.Errorf("#%d was measured on no train rows — there is nothing to apply", id)
	}
	notes := []string{}
	if !view.InFile {
		if err := r.Store.SetSkillDescription(skill, chosen.Text); err != nil {
			return "", err
		}
		notes = append(notes, fmt.Sprintf("#%d written into %s/SKILL.md", id, skill))
	}
	ledger.Applied = &model.AppliedDescription{Candidate: id, Iteration: view.Measure().Iteration,
		SHA256: view.SHA, At: timestamp()}
	if err := r.Store.SaveDescriptionLedger(ledger); err != nil {
		return "", err
	}
	notes = append(notes, fmt.Sprintf("applied #%d · validation %d/%d · measured in trigger-%d",
		id, view.Measure().Validation.Passed, view.Measure().Validation.Judged, view.Measure().Iteration))
	return strings.Join(notes, " — "), nil
}

// DeleteDescriptionCandidate removes one draft from the ledger; a candidate that was measured keeps
// its iteration on disk, which is the point of never copying measurements into the ledger.
func (r *Runner) DeleteDescriptionCandidate(skill string, id int) error {
	ledger, _, err := r.Store.LoadDescriptionLedger(skill)
	if err != nil {
		return err
	}
	kept := make([]model.DescriptionCandidate, 0, len(ledger.Candidates))
	found := false
	for _, c := range ledger.Candidates {
		if c.ID == id {
			found = true
			continue
		}
		kept = append(kept, c)
	}
	if !found {
		return fmt.Errorf("candidate #%d is not in this ledger", id)
	}
	if ledger.Applied != nil && ledger.Applied.Candidate == id {
		ledger.Applied = nil
	}
	ledger.Candidates = kept
	return r.Store.SaveDescriptionLedger(ledger)
}

// AddHoldoutQuery appends a fresh query to the set, labelled holdout: measured once at the end of
// the loop, never part of its rates.
func (r *Runner) AddHoldoutQuery(skill, query, why string, shouldTrigger bool) (int, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return 0, fmt.Errorf("the query is empty")
	}
	if !r.Store.TriggerOK {
		return 0, fmt.Errorf("missing trigger/%s", store.TriggerFileName)
	}
	id := r.Store.NextTriggerID()
	q := model.TriggerQuery{ID: id, Skill: skill, ShouldTrigger: shouldTrigger, Split: store.SplitHoldout,
		Why: strings.TrimSpace(why), Query: query}
	if err := r.Store.AddTrigger(q); err != nil {
		return 0, err
	}
	return id, r.Store.SaveTrigger()
}

// LiftedWords are the words a draft took straight out of the queries it is meant to fix. The guide
// names this as the one wrong move in a revision: a description that contains the test's own words
// matches the sample instead of the task. A word already in the parent's wording is not lifted —
// only what this revision added, from the failing queries, counts.
func LiftedWords(draft, parent string, failures []ScoreRow) []string {
	before := wordSet(parent)
	added := map[string]bool{}
	for _, word := range words(draft) {
		if !before[word] {
			added[word] = true
		}
	}
	hits := map[string]bool{}
	for _, row := range failures {
		for _, word := range words(row.Query) {
			if added[word] && !stopWords[word] {
				hits[word] = true
			}
		}
	}
	out := make([]string, 0, len(hits))
	for word := range hits {
		out = append(out, word)
	}
	sort.Strings(out)
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}

// words is the comparison unit: lower-case runs of letters and digits, at least four long, so a
// lifted noun is caught and a lifted "the" is not.
func words(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if word := strings.ToLower(strings.Trim(cur.String(), "-")); len(word) >= 4 {
			out = append(out, word)
		}
		cur.Reset()
	}
	for _, r := range s {
		// A hyphen stays inside a word: "controller-runtime" is one thing a revision can lift, and
		// splitting it into three flags is noise where one warning would do.
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func wordSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range words(s) {
		out[w] = true
	}
	return out
}

// stopWords are the words a prompt and a description share no matter what the description says.
var stopWords = map[string]bool{
	"this": true, "that": true, "with": true, "when": true, "they": true, "them": true,
	"their": true, "from": true, "into": true, "have": true, "will": true, "want": true,
	"needs": true, "need": true, "here": true, "there": true, "what": true, "which": true,
	"your": true, "should": true, "could": true, "would": true, "just": true, "like": true,
	"even": true, "does": true, "doesn": true, "already": true, "something": true,
}

// stamp is the ledger's own clock, kept here so the file's dates and a run's dates cannot be written
// in different formats: candidate.Created is what the ledger sorts a loop's history by.
