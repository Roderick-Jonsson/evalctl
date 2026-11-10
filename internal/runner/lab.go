package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"evalctl/internal/diff"
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
	// Full says, per split, whether every row of that split was judged — what tells a measurement
	// from a batch that stopped. It is set where the set's size is known, which is the join.
	TrainFull      bool `json:"train_full"`
	ValidationFull bool `json:"validation_full"`
	HoldoutFull    bool `json:"holdout_full"`
}

// LabCandidate is one wording of the description, the iterations that measured it, and what it is to
// the tree right now. It is assembled from the runs on disk — there is no ledger — so its id is the
// first trigger iteration that measured this text: "#7" means <skill>-workspace/trigger-7.
type LabCandidate struct {
	ID         int          `json:"id"`
	Parent     int          `json:"parent,omitempty"` // the wording measured before this one
	Change     string       `json:"change,omitempty"` // computed from the diff against Parent
	Text       string       `json:"text"`
	SHA        string       `json:"sha"`
	Chars      int          `json:"chars"`
	Created    string       `json:"created,omitempty"`
	Iterations []int        `json:"iterations,omitempty"`
	Measures   []LabMeasure `json:"measures"`
	InFile     bool         `json:"in_file"`
	Pending    bool         `json:"pending,omitempty"` // a draft in the workspace, never run
}

// judged reports whether a measurement judged rows of one split.
func judged(m *LabMeasure, split string) bool {
	if m == nil {
		return false
	}
	switch split {
	case SplitTrain:
		return m.Train.Judged > 0
	case SplitValidation:
		return m.Validation.Judged > 0
	case SplitHoldout:
		return m.Holdout.Judged > 0
	}
	return false
}

// CompleteTally says whether a rate was measured over the whole set it is a rate of.
//
// A stopped batch, or a query whose launches all died, leaves a tally over the rows that happened to
// land; 1/1 out of three validation queries reads as a clean pass and is not one. Every screen and
// every selection rule asks this one question, so the answer cannot differ between them.
func (l *Lab) CompleteTally(t *LabTally) bool {
	if t == nil {
		return false
	}
	rows := 0
	if l != nil {
		rows = l.ValidationRows
	}
	return complete(*t, rows)
}

// complete is CompleteTally against a known split size, which is what the join has while it flags
// each measurement.
func complete(t LabTally, rows int) bool {
	if t.Judged == 0 {
		return false
	}
	if rows == 0 {
		return true
	}
	return t.Judged >= rows
}

// Complete is CompleteTally for a whole measurement: its validation half judged the set.
func (l *Lab) Complete(m *LabMeasure) bool {
	if m == nil {
		return false
	}
	return l.CompleteTally(&m.Validation)
}

// Measure is the candidate's latest measurement, or nil when it has never been run.
func (c LabCandidate) Measure() *LabMeasure {
	if len(c.Measures) == 0 {
		return nil
	}
	return &c.Measures[len(c.Measures)-1]
}

// MeasureOn is the candidate's measurement that judged one split — the latest complete one, or the
// latest there is when none of them judged the whole split.
//
// It exists because the two halves of the loop are measured by two different runs: the iteration
// runs the train set, and generalization runs the validation set against the same wording. Joined by
// the description's hash they are two measurements of one candidate, and reading the newest one
// alone would throw away whichever half was run first.
//
// Complete beats newer, deliberately: a re-run stopped halfway through must not hide the finished
// measurement of the same wording that is already on disk.
func (c LabCandidate) MeasureOn(split string) *LabMeasure {
	var fallback *LabMeasure
	for i := len(c.Measures) - 1; i >= 0; i-- {
		m := &c.Measures[i]
		if !judged(m, split) {
			continue
		}
		if full(m, split) {
			return m
		}
		if fallback == nil {
			fallback = m
		}
	}
	return fallback
}

// full reports whether a measurement judged every row of one split.
func full(m *LabMeasure, split string) bool {
	switch split {
	case SplitTrain:
		return m.TrainFull
	case SplitValidation:
		return m.ValidationFull
	case SplitHoldout:
		return m.HoldoutFull
	}
	return false
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
//
// There is no ledger: the candidates are the wordings its trigger iterations recorded, and the one
// a draft in the workspace proposes. Everything the loop decides is computed from those runs, so a
// pruned workspace costs history, never the ability to read the current state.
type Lab struct {
	Skill          string         `json:"skill"`
	File           string         `json:"file"` // the description SKILL.md carries now
	FileSHA        string         `json:"file_sha"`
	FileChars      int            `json:"file_chars"`
	Candidates     []LabCandidate `json:"candidates"`
	Drifted        bool           `json:"drifted"` // the file's wording is not the newest measured one
	Checks         []LabCheck     `json:"checks"`
	Ready          bool           `json:"ready"`
	Best           *LabCandidate  `json:"best,omitempty"`
	Next           LabNext        `json:"next"`
	Iterations     int            `json:"iterations"` // trigger iterations that measured this skill
	Measured       int            `json:"measured"`
	TrainRows      int            `json:"train_rows"`
	ValidationRows int            `json:"validation_rows"`
	HoldoutRows    int            `json:"holdout_rows"`
	Runs           int            `json:"runs"`
	Threshold      float64        `json:"threshold"`
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

// DescriptionLab joins the skill's trigger iterations together and says what the guide would have
// you do next. The candidates are the wordings those runs recorded, plus the draft in the workspace
// if there is one — so nothing here can disagree with the measurements: they are the same read.
func (r *Runner) DescriptionLab(skill string) (*Lab, error) {
	if r.Store.Skill(skill) == nil {
		return nil, fmt.Errorf("unknown skill %q", skill)
	}
	cfg := r.TriggerConfigOf(skill)
	lab := &Lab{Skill: skill, Runs: cfg.Runs, Threshold: cfg.Threshold}
	lab.File = r.Store.SkillDescription(skill)
	lab.FileSHA = store.HashText(lab.File)
	lab.FileChars = len([]rune(lab.File))

	// The set's own sizes come first: they are what decides whether a measurement judged all of a
	// split, and that has to be known before any measurement is chosen for one.
	for _, q := range r.Store.QueriesOf(skill) {
		switch q.Split {
		case SplitTrain:
			lab.TrainRows++
		case SplitValidation:
			lab.ValidationRows++
		case SplitHoldout:
			lab.HoldoutRows++
		}
	}

	measured, err := r.descriptionMeasures(skill)
	if err != nil {
		return nil, err
	}

	// One candidate per distinct wording, in the order the runs first measured it. Every measurement
	// of a wording is kept, not just the newest: the train run and the validation run are two
	// measurements of one candidate, and joining by hash without keeping both would throw away
	// whichever half ran first.
	at := map[string]int{}
	for _, m := range measured {
		m.measure.TrainFull = complete(m.measure.Train, lab.TrainRows)
		m.measure.ValidationFull = complete(m.measure.Validation, lab.ValidationRows)
		m.measure.HoldoutFull = complete(m.measure.Holdout, lab.HoldoutRows)
		i, seen := at[m.sha]
		if !seen {
			lab.Candidates = append(lab.Candidates, LabCandidate{
				ID: m.measure.Iteration, SHA: m.sha, Text: m.text,
				Chars: len([]rune(store.FoldText(m.text))), Created: m.measure.Started,
			})
			i = len(lab.Candidates) - 1
			at[m.sha] = i
		}
		c := &lab.Candidates[i]
		c.Measures = append(c.Measures, m.measure)
		c.Iterations = append(c.Iterations, m.measure.Iteration)
	}

	fileMeasured := false
	for i := range lab.Candidates {
		c := &lab.Candidates[i]
		c.InFile = c.SHA == lab.FileSHA
		if c.InFile {
			fileMeasured = true
		}
		if i > 0 {
			prev := &lab.Candidates[i-1]
			c.Parent = prev.ID
			c.Change = changeSummary(prev.Text, c.Text)
		}
		if !c.Unmeasured() {
			lab.Measured++
		}
		// Iterations are measured wordings, which is what the loop counts and what the gate is
		// stated in: a candidate's train run and its validation run are two measurements of the same
		// iteration, so counting records would report the loop as twice as long as it is.
		if c.MeasureOn(SplitTrain) != nil {
			lab.Iterations++
		}
	}

	// Fixed/broke are the diff against the previous wording's own measurement: the record of what a
	// revision actually did, which is what stops the next revision from re-breaking what this one
	// fixed. The diff is computed split by split, because the two halves of the loop are two
	// measurements: comparing the newest measure of each side compares a validation run with a train
	// run the moment the halves ran in a different order.
	for i := 1; i < len(lab.Candidates); i++ {
		c, parent := &lab.Candidates[i], &lab.Candidates[i-1]
		if parent.Unmeasured() || c.Unmeasured() {
			continue
		}
		for _, split := range []string{SplitTrain, SplitValidation, SplitHoldout} {
			before, after := parent.MeasureOn(split), c.MeasureOn(split)
			if !judged(before, split) || !judged(after, split) {
				continue
			}
			passed := map[int]bool{}
			for _, row := range before.Rows {
				if row.Passed != nil && row.Split == split {
					passed[row.ID] = *row.Passed
				}
			}
			for _, row := range after.Rows {
				if row.Passed == nil || row.Split != split {
					continue
				}
				was, ok := passed[row.ID]
				switch {
				case ok && !was && *row.Passed:
					after.Fixed = append(after.Fixed, row.ID)
				case ok && was && !*row.Passed:
					after.Broke = append(after.Broke, row.ID)
				}
			}
			sort.Ints(after.Fixed)
			sort.Ints(after.Broke)
		}
	}

	// A draft in the workspace is the one candidate with no measurement: it stages a wording the
	// next run will measure, which is what the ledger's unmeasured candidates used to do.
	if draft, ok := r.PendingDescription(skill); ok && store.HashText(draft.Text) != lab.FileSHA && !seenSHA(lab.Candidates, store.HashText(draft.Text)) {
		parentID, parentText := 0, ""
		if n := len(lab.Candidates); n > 0 {
			parentID, parentText = lab.Candidates[n-1].ID, lab.Candidates[n-1].Text
		}
		lab.Candidates = append(lab.Candidates, LabCandidate{
			ID: r.NextIteration(r.TriggerWorkspace(skill), "trigger"), Text: draft.Text,
			SHA: store.HashText(draft.Text), Chars: len([]rune(store.FoldText(draft.Text))),
			Created: draft.At, Pending: true, Parent: parentID, Change: changeSummary(parentText, draft.Text),
		})
	}

	// Drift is the file having moved since the last run: the wording in SKILL.md has no measurement
	// of its own, so any rate beside it would describe text nobody has run.
	lab.Drifted = len(lab.Candidates) > 0 && !fileMeasured

	lab.Checks, lab.Ready = r.labChecks(skill)
	lab.Best = lab.best()
	lab.Next = lab.next()
	return lab, nil
}

func seenSHA(cands []LabCandidate, sha string) bool {
	for _, c := range cands {
		if c.SHA == sha {
			return true
		}
	}
	return false
}

// changeSummary is the table's CHANGE cell, computed rather than typed: how much this wording added
// and removed against the previous one. It is the honest version of the note a ledger used to carry,
// and it cannot go stale.
func changeSummary(before, after string) string {
	if strings.TrimSpace(before) == "" {
		return fmt.Sprintf("%d chars, first wording", len([]rune(store.FoldText(after))))
	}
	segs := diff.Segments(store.FoldText(before), store.FoldText(after))
	added, removed := diff.SegmentsStat(segs)
	switch {
	case added == 0 && removed == 0:
		return "unchanged"
	case removed == 0:
		return fmt.Sprintf("+%d chars", added)
	case added == 0:
		return fmt.Sprintf("−%d chars", removed)
	}
	return fmt.Sprintf("+%d/−%d chars", added, removed)
}

type measuredDescription struct {
	sha     string
	text    string
	measure LabMeasure
}

// descriptionMeasures reads every trigger iteration in this skill's workspace, with the description
// that iteration ran and the rates its own rows produced.
func (r *Runner) descriptionMeasures(skill string) ([]measuredDescription, error) {
	entries, err := os.ReadDir(r.TriggerWorkspace(skill))
	if err != nil {
		return nil, nil // no trigger run has ever happened for this skill
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
		dir := filepath.Join(r.TriggerWorkspace(skill), e.Name())
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
		out = append(out, measuredDescription{sha: store.HashText(desc.Description), text: desc.Description, measure: measure})
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
		// Only a full validation competes: a rate over part of the set is a stopped batch, and it
		// would win every comparison by having less to fail.
		if !l.Complete(c.MeasureOn(SplitValidation)) {
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
	train := 0
	validation := 0
	holdout := 0
	labelsOf := map[string]map[bool]bool{SplitTrain: {}, SplitValidation: {}, SplitHoldout: {}}
	missingWhy := 0
	for _, q := range r.Store.QueriesOf(skill) {
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
	cfg := r.TriggerConfigOf(skill)
	checks = append(checks, LabCheck{
		Key: "set.runs", OK: cfg.Runs >= 3, Blocking: false,
		Detail: fmt.Sprintf("%d run(s) per query — one cannot tell a flaky miss from a real one", cfg.Runs),
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
	last := l.latest()
	if last != nil && last.Pending {
		return LabNext{Step: StepEvaluate, Key: "evaluate",
			Line: fmt.Sprintf("evaluate #%d — %d chars, %d train + %d validation rows × %d runs",
				last.ID, last.Chars, l.Count(SplitTrain), l.Count(SplitValidation), l.Runs),
			Detail: "evaluating writes the draft into SKILL.md first: that is what the router reads"}
	}
	if l.Drifted {
		return LabNext{Step: StepEvaluate, Key: "evaluate",
			Line:   "the wording in SKILL.md has not been measured — evaluate it",
			Detail: "no trigger run recorded this text, so a rate beside it would describe something else"}
	}
	if last == nil {
		return LabNext{Step: StepBaseline, Key: "baseline",
			Line:   "evaluate the description SKILL.md carries",
			Detail: "the loop starts from what the router reads today; the first run is the baseline"}
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
	// The train set passes for this wording. Choose, then close the loop with fresh queries.
	inFile := l.inFile()
	if l.Best == nil || inFile == nil || l.Best.ID != inFile.ID {
		best := l.Best
		if best == nil {
			best = last
		}
		bv, bt := best.MeasureOn(SplitValidation), best.MeasureOn(SplitTrain)
		line := fmt.Sprintf("select #%d — validation not measured yet", best.ID)
		switch {
		case l.Complete(bv):
			line = fmt.Sprintf("select #%d — best validation %d/%d", best.ID, bv.Validation.Passed, bv.Validation.Judged)
			if bt != nil {
				line += fmt.Sprintf(" (train %d/%d)", bt.Train.Passed, bt.Train.Judged)
			}
		case bv != nil:
			// A stopped batch is not a rate: naming 1/1 here is how a partial run gets applied.
			line = fmt.Sprintf("select #%d — its validation judged %d of %d queries; run the set again first",
				best.ID, bv.Validation.Judged, l.ValidationRows)
		}
		return LabNext{Step: StepBest, Key: "best", Line: line,
			Detail: "the best description is not necessarily the last one; validation is the number that ranks them"}
	}
	if hold := inFile.MeasureOn(SplitHoldout); hold != nil && hold.Holdout.Judged > 0 {
		return LabNext{Step: StepApply, Key: "closed",
			Line: fmt.Sprintf("loop closed: #%d applied · fresh queries %d/%d (%s)",
				inFile.ID, hold.Holdout.Passed, hold.Holdout.Judged, pctOf(hold.Holdout, true)),
			Detail: "reopen the loop when the set changes; the applied description stays the record of why this wording won"}
	}
	if l.Count(SplitHoldout) == 0 {
		return LabNext{Step: StepApply, Key: "apply",
			Line:   fmt.Sprintf("#%d is applied and the train set is clean — write 5–10 fresh queries", inFile.ID),
			Detail: "queries that were never part of the loop are the only honest check that the wording generalizes"}
	}
	return LabNext{Step: StepApply, Key: "apply",
		Line:   "5–10 fresh queries are written but have not been run — run them once",
		Detail: "a fresh set that gets re-run becomes a validation set, and the check stops being honest"}
}

// inFile is the candidate whose wording SKILL.md carries, or nil when the file has moved since the
// last run (which is what Drifted says).
func (l *Lab) inFile() *LabCandidate {
	for i := range l.Candidates {
		if l.Candidates[i].InFile {
			return &l.Candidates[i]
		}
	}
	return nil
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

// flattened is the guide's fifth-iteration rule: five revisions in and the best train rate has not
// moved means the description is not what is failing.
//
// It compares train rates and nothing else. The loop rate of a wording whose newest run happened to
// be the validation half is a rate over the validation rows — comparing it with a train rate would
// report "no gain" between two different instruments.
func (l *Lab) flattened() bool {
	if l.Measured < 5 {
		return false
	}
	var rates []float64
	for _, c := range l.Candidates {
		m := c.MeasureOn(SplitTrain)
		if m == nil || m.Train.Judged == 0 {
			continue
		}
		rates = append(rates, m.Train.Rate)
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

// PendingDescription is a draft wording waiting for its run: the loop's staging slot. It lives in
// the workspace, because it is work in progress, not test data.
type PendingDescription struct {
	Text string `json:"text"`
	At   string `json:"at,omitempty"`
}

// PendingDescriptionPath is <skill>-workspace/pending-description.json.
func (r *Runner) PendingDescriptionPath(skill string) string {
	return filepath.Join(r.TriggerWorkspace(skill), "pending-description.json")
}

// PendingDescription reads the draft, if the workspace holds one.
func (r *Runner) PendingDescription(skill string) (PendingDescription, bool) {
	var p PendingDescription
	if err := readJSONFile(r.PendingDescriptionPath(skill), &p); err != nil {
		return PendingDescription{}, false
	}
	if strings.TrimSpace(p.Text) == "" {
		return PendingDescription{}, false
	}
	return p, true
}

// SaveDescriptionCandidate stages a draft: the wording a run should measure next. It refuses the
// shapes the lab cannot measure — empty text, over the format's ceiling, or a wording the runs
// already recorded (a revision that changes nothing cannot be measured apart from its parent).
func (r *Runner) SaveDescriptionCandidate(skill, text string) (PendingDescription, error) {
	text = store.FoldText(text)
	if text == "" {
		return PendingDescription{}, fmt.Errorf("the description is empty")
	}
	if n := len([]rune(text)); n > store.DescriptionLimit {
		return PendingDescription{}, fmt.Errorf("the description is %d chars — the limit is %d", n, store.DescriptionLimit)
	}
	lab, err := r.DescriptionLab(skill)
	if err != nil {
		return PendingDescription{}, err
	}
	sha := store.HashText(text)
	if sha == lab.FileSHA {
		return PendingDescription{}, fmt.Errorf("this is already the wording in SKILL.md — a revision that changes nothing cannot be measured apart from it")
	}
	for _, c := range lab.Candidates {
		if c.SHA == sha {
			return PendingDescription{}, fmt.Errorf("#%d already carries this wording — a revision that changes nothing cannot be measured apart from it", c.ID)
		}
	}
	p := PendingDescription{Text: text, At: timestamp()}
	if err := os.MkdirAll(r.TriggerWorkspace(skill), 0o755); err != nil {
		return PendingDescription{}, err
	}
	if err := writeJSONFile(r.PendingDescriptionPath(skill), p); err != nil {
		return PendingDescription{}, err
	}
	return p, nil
}

// DiscardPending drops the staged draft.
func (r *Runner) DiscardPending(skill string) error {
	err := os.Remove(r.PendingDescriptionPath(skill))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// PrepareMeasure puts a candidate's wording into SKILL.md, because the router reads the file and
// nothing else. It says what it replaced: a silent rewrite of the file the agent is about to be
// given is the one administrative step a person must not have to wonder about.
func (r *Runner) PrepareMeasure(skill string, id int) (string, error) {
	lab, err := r.DescriptionLab(skill)
	if err != nil {
		return "", err
	}
	c := lab.Candidate(id)
	if c == nil {
		return "", fmt.Errorf("#%d is not one of this skill's wordings", id)
	}
	if c.InFile {
		if c.Pending {
			_ = r.DiscardPending(skill)
		}
		return fmt.Sprintf("#%d is already the description in SKILL.md", id), nil
	}
	if err := r.Store.SetSkillDescription(skill, c.Text); err != nil {
		return "", err
	}
	note := fmt.Sprintf("#%d written into %s/SKILL.md (previous wording in .evalctl/backups/)", id, skill)
	if c.Pending {
		if err := r.DiscardPending(skill); err != nil {
			return "", err
		}
		note += " — the draft became the file"
	}
	return note, nil
}

// ApplyDescription settles the loop on one wording: the file is brought to it if some later
// measurement left another one there. Nothing is recorded anywhere — the file IS the record, and the
// numbers travel in the commit message the caller prints, so git keeps what the workspace may lose.
func (r *Runner) ApplyDescription(skill string, id int) (string, error) {
	lab, err := r.DescriptionLab(skill)
	if err != nil {
		return "", err
	}
	c := lab.Candidate(id)
	if c == nil {
		return "", fmt.Errorf("#%d is not one of this skill's wordings", id)
	}
	if c.Unmeasured() {
		return "", fmt.Errorf("#%d has not been measured — apply is a choice between measured descriptions", id)
	}
	// Each half of the loop is its own measurement, and which one ran last is an accident of the
	// operator's order: asking the newest one for a train rate rejects a wording whose last run was
	// the validation run.
	train := c.MeasureOn(SplitTrain)
	if train == nil || train.Train.Judged == 0 {
		return "", fmt.Errorf("#%d was measured on no train rows — there is nothing to apply", id)
	}
	notes := []string{}
	if !c.InFile {
		if err := r.Store.SetSkillDescription(skill, c.Text); err != nil {
			return "", err
		}
		notes = append(notes, fmt.Sprintf("#%d written into %s/SKILL.md", id, skill))
	}
	subject := fmt.Sprintf("description #%d: train %s", id, pctOf(train.Train, true))
	switch valid := c.MeasureOn(SplitValidation); {
	case lab.Complete(valid):
		subject += fmt.Sprintf(" · validation %s", pctOf(valid.Validation, true))
		notes = append(notes, fmt.Sprintf("applied #%d · train %d/%d · validation %d/%d",
			id, train.Train.Passed, train.Train.Judged, valid.Validation.Passed, valid.Validation.Judged))
	case valid != nil:
		subject += fmt.Sprintf(" · validation %d/%d of %d judged", valid.Validation.Passed, valid.Validation.Judged, lab.ValidationRows)
		notes = append(notes, fmt.Sprintf("applied #%d · validation judged %d of %d — a stopped batch, so the rate is unproven",
			id, valid.Validation.Judged, lab.ValidationRows))
	default:
		subject += " · validation not measured"
		notes = append(notes, fmt.Sprintf("applied #%d · validation not measured", id))
	}
	notes = append(notes, "commit it: `"+subject+"`")
	return strings.Join(notes, " — "), nil
}

// AddHoldoutQuery appends a fresh query to one skill's set, labelled holdout: measured once at the
// end of the loop, never part of its rates.
func (r *Runner) AddHoldoutQuery(skill, query, why string, shouldTrigger bool) (int, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return 0, fmt.Errorf("the query is empty")
	}
	if _, ok := r.Store.TriggerOf(skill); !ok {
		return 0, fmt.Errorf("missing %s/evals/%s", skill, store.TriggerFileName)
	}
	id := r.Store.NextTriggerID(skill)
	q := model.TriggerQuery{ID: id, Skill: skill, ShouldTrigger: shouldTrigger, Split: store.SplitHoldout,
		Why: strings.TrimSpace(why), Query: query}
	if err := r.Store.AddTrigger(skill, q); err != nil {
		return 0, err
	}
	return id, r.Store.SaveTrigger(skill)
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
