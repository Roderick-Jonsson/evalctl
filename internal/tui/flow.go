package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// A skill's loop is two activities, not four steps in a row.
//
//   ITERATION       works the train set: define it, run the wording against it, read the failures,
//                   revise. That is the half of the loop where the description is written.
//   GENERALIZATION  works the validation set: define it, run it against every wording the iteration
//                   produced, and rank them by it. That is the half where the wording is judged.
//
// The split matters because the two sets are used for opposite things — one is what a revision may
// learn from, the other is what must never shape one — and a single four-step rail made them look
// like consecutive chores instead of two instruments with different rules. Each section owns its set
// and its own screens; the section screen is where a skill opens, and the rail inside a section only
// ever lists that section's steps.

const (
	secIteration = iota
	secGeneralization
)

var secNames = []string{"iteration", "generalization"}

// The steps each section owns, in the order the work is done.
var iterationSteps = []string{"train set", "iterate"}
var generalizationSteps = []string{"validation set", "validate", "results"}

// iterationsRequired is the guide's "if >= 5 iterations, ready to generalize": below five measured
// wordings the validation rate is a coin and the comparison between iterations is noise.
const iterationsRequired = 5

// iteration is one measured wording: the candidate, its train measurement, and its validation
// measurement when generalization has run.
//
// A candidate is measured twice, and that is the point of it: the train run is what a revision is
// allowed to learn from, and the validation run — later, and only in the other section — is what
// says whether the revision generalizes or memorized.
type iteration struct {
	N     int
	Cand  runner.LabCandidate
	Train *runner.LabMeasure
	Valid *runner.LabMeasure
}

func (it iteration) train() *runner.LabTally { return &it.Train.Train }
func (it iteration) valid() *runner.LabTally {
	if it.Valid == nil {
		return nil
	}
	return &it.Valid.Validation
}

type flowState struct {
	sec  int
	step int // 1-based within the section
	lab  *runner.Lab
	// iters is the measured candidates in ledger order: the loop's own numbering, which is what the
	// result list is keyed on.
	iters []iteration
	// rows is the split the section's first step edits; empty on the other steps.
	rows []store.Entry
	sel  int
	top  int
	sub  int // the selected rung of the iteration ladder
	res  int // the selected row of the result list
	// forced remembers ctrl+g: generalization before five iterations, on purpose, said out loud.
	forced bool
	gen    *genState
}

func stepsOf(sec int) []string {
	if sec == secGeneralization {
		return generalizationSteps
	}
	return iterationSteps
}

func (f *flowState) steps() []string { return stepsOf(f.sec) }

func (f *flowState) stepCount() int { return len(f.steps()) }

func (f *flowState) stepName() string { return stepNameAt(f.sec, f.step) }

// stepNameAt is the name of a step in a section, for the screens that describe a section they are not
// inside.
func stepNameAt(sec, step int) string {
	steps := iterationSteps
	if sec == secGeneralization {
		steps = generalizationSteps
	}
	if step < 1 || step > len(steps) {
		return ""
	}
	return steps[step-1]
}

func (f *flowState) sectionName() string {
	if f.sec == secGeneralization {
		return secNames[secGeneralization]
	}
	return secNames[secIteration]
}

// split is the split the section's first step edits, or "" for the steps that edit no rows.
func (f *flowState) split() string {
	if f.step != 1 {
		return ""
	}
	if f.sec == secGeneralization {
		return store.SplitValidation
	}
	return store.SplitTrain
}

// iterationsOf reads the loop out of a lab: a candidate becomes an iteration when it has a train
// measurement, and it carries whichever validation measurement exists for it. Ordering is by
// candidate id, which is the order the wordings were written, so "iteration 3" means the third
// wording tried and never the third row of a map.
func iterationsOf(lab *runner.Lab) []iteration {
	if lab == nil {
		return nil
	}
	cands := append([]runner.LabCandidate(nil), lab.Candidates...)
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].ID < cands[j].ID })
	var out []iteration
	for _, c := range cands {
		var train, valid *runner.LabMeasure
		for k := range c.Measures {
			mm := &c.Measures[k]
			if mm.Train.Judged > 0 && (train == nil || mm.Iteration > train.Iteration) {
				train = mm
			}
			if mm.Validation.Judged > 0 && (valid == nil || mm.Iteration > valid.Iteration) {
				valid = mm
			}
		}
		if train == nil {
			continue
		}
		out = append(out, iteration{N: len(out) + 1, Cand: c, Train: train, Valid: valid})
	}
	return out
}

// latest is the newest measured wording, or nil when nothing has been measured.
func (f *flowState) latest() *iteration {
	if len(f.iters) == 0 {
		return nil
	}
	return &f.iters[len(f.iters)-1]
}

// pending is the wording the next training run is about: the latest candidate with no TRAIN
// measurement. It is the train run that makes a candidate an iteration, so a wording that has only
// been through the validation set is still waiting to be measured — which is the state a skill is in
// when its description was already validated in some earlier run.
func (f *flowState) pending() *runner.LabCandidate {
	if f.lab == nil || len(f.lab.Candidates) == 0 {
		return nil
	}
	c := f.lab.Candidates[len(f.lab.Candidates)-1]
	if c.MeasureOn(store.SplitTrain) != nil {
		return nil
	}
	return &f.lab.Candidates[len(f.lab.Candidates)-1]
}

// allValidated reports whether every measured wording has been through the validation set.
func allValidated(lab *runner.Lab) bool {
	it := iterationsOf(lab)
	if len(it) == 0 {
		return false
	}
	for _, i := range it {
		if i.Valid == nil {
			return false
		}
	}
	return true
}

func noCandidateLeft(lab *runner.Lab) bool {
	if lab == nil || len(lab.Candidates) == 0 {
		return false
	}
	return lab.Candidates[len(lab.Candidates)-1].MeasureOn(store.SplitTrain) != nil
}

// gateOpen is the five-iteration rule, with ctrl+g as the stated exception.
func (f *flowState) gateOpen() bool { return len(f.iters) >= iterationsRequired || f.forced }

// loopOpen is where a skill's work opens: the section and the step inside it, computed from the set
// and the ledger alone. The chooser and the flow both read it, so the chooser cannot promise a step
// the flow will not open.
func loopOpen(lab *runner.Lab) (sec, step int) {
	switch {
	case lab == nil || lab.TrainRows == 0:
		return secIteration, 1
	case len(iterationsOf(lab)) == 0:
		return secIteration, 2
	case lab.ValidationRows == 0:
		return secGeneralization, 1
	case allValidated(lab):
		return secGeneralization, 3
	}
	return secGeneralization, 2
}

// resumeIn puts the flow on the step inside this section the disk says the work is at.
func (f *flowState) resumeIn(m *Model) {
	if f.sec == secIteration {
		f.step = 2
		if f.lab == nil || f.lab.TrainRows == 0 {
			f.step = 1
		}
	} else {
		switch {
		case f.lab == nil || f.lab.ValidationRows == 0:
			f.step = 1
		case len(f.iters) > 0 && allValidated(f.lab):
			f.step = 3
		default:
			f.step = 2
		}
	}
	f.sub, f.res, f.sel, f.top = 0, 0, 0, 0
	f.enterStep(m)
}

// enterStep resets the cursors a step owns, so arriving somewhere always shows its first row rather
// than a cursor remembered from a previous visit to a different split.
func (f *flowState) enterStep(m *Model) {
	switch {
	case f.step == 1:
		f.sel, f.top = 0, 0
		m.reload()
	case f.sec == secIteration && f.step == 2:
		f.sub = f.defaultSub()
	case f.sec == secGeneralization && f.step == 3:
		f.res = 0
	}
}

// defaultSub is where the iteration ladder opens: the rung the loop is actually waiting on.
func (f *flowState) defaultSub() int {
	switch {
	case f.pending() != nil:
		return subRun // a wording is waiting to be measured
	case f.latest() == nil:
		return subRun
	case len(f.latest().Cand.FailedTrain()) > 0:
		return subResult
	}
	return subOptimize
}

// ---------------------------------------------------------------- the rail

// subState is one step's state in the rail: done, current, waiting, or locked behind the gate.
type subState int

const (
	stateWaiting subState = iota
	stateDone
	stateCurrent
	stateLocked
)

func (f *flowState) stepState(i int) subState {
	if i == f.step {
		return stateCurrent
	}
	if f.sec == secIteration {
		switch i {
		case 1:
			if f.lab != nil && f.lab.TrainRows > 0 {
				return stateDone
			}
		case 2:
			if len(f.iters) >= iterationsRequired {
				return stateDone
			}
		}
		return stateWaiting
	}
	// Generalization: its last two steps are behind the five-iteration gate.
	switch i {
	case 1:
		if f.lab != nil && f.lab.ValidationRows > 0 {
			return stateDone
		}
	case 2:
		if !f.gateOpen() {
			return stateLocked
		}
		if f.lab != nil && len(f.iters) > 0 && allValidated(f.lab) {
			return stateDone
		}
	case 3:
		if !f.gateOpen() {
			return stateLocked
		}
		if f.lab != nil && f.lab.Applied != nil {
			return stateDone
		}
	}
	return stateWaiting
}

// rail is the navigation inside a section: its steps, what state each one is in, and the one gate in
// the flow. It lists only this section's steps — the other section is one screen up.
func (f *flowState) rail(w int) string {
	var b strings.Builder
	b.WriteString(" ")
	for i, name := range f.steps() {
		if i > 0 {
			b.WriteString(dimStyle.Render(" " + strings.Repeat(glyphs.Rule, 3) + " "))
		}
		label := fmt.Sprintf("%d %s", i+1, name)
		switch f.stepState(i + 1) {
		case stateCurrent:
			b.WriteString(cursorMark.Render(glyphs.Cursor + label))
		case stateDone:
			b.WriteString(styleOn.Render(glyphs.Done + " " + label))
		case stateLocked:
			b.WriteString(dimStyle.Render(label + " · locked"))
		default:
			b.WriteString(dimStyle.Render(label))
		}
	}
	return ellipsis(b.String(), w)
}

// stateLine is the loop's numbers, at the right end of the rail row. The step's name belongs to the
// rail and to the bar's path, so this says only what the step has measured — and it separates the two
// facts with a parenthesis rather than a dot, so the row never looks like a second breadcrumb.
func (f *flowState) stateLine() string {
	if f.lab == nil {
		return ""
	}
	if f.step == 1 {
		return plural(len(f.rows), "row")
	}
	if f.sec == secIteration {
		n := max(1, len(f.iters)+boolInt(f.pending() != nil))
		if it := f.latest(); it != nil && f.pending() == nil {
			return fmt.Sprintf("iteration %d of %d (train %s)", n, iterationsRequired,
				pct(it.Train.Train.Passed, it.Train.Train.Judged))
		}
		return fmt.Sprintf("iteration %d of %d (unmeasured)", n, iterationsRequired)
	}
	if len(f.iters) == 0 {
		return "nothing measured yet"
	}
	if f.step == 2 {
		done := 0
		for _, it := range f.iters {
			if it.Valid != nil {
				done++
			}
		}
		return fmt.Sprintf("%d of %d validated", done, len(f.iters))
	}
	return plural(len(f.iters), "iteration") + " measured"
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// stateText is the status row's own line: the constraint the step is working under, which is the
// thing a person forgets between sessions.
func (f *flowState) stateText() string {
	if f.step == 1 {
		if f.sec == secGeneralization {
			return "these rows judge the wording; they are never used to write one"
		}
		return "the wording may learn from these rows — nothing else shapes a revision"
	}
	if f.sec == secIteration {
		if f.pending() != nil {
			return "candidate #" + fmt.Sprint(f.pending().ID) + " is written but not measured"
		}
		if it := f.latest(); it != nil {
			return "train " + pct(it.Train.Train.Passed, it.Train.Train.Judged) +
				" · the validation set stays withheld until generalization"
		}
		return "nothing measured yet — rung 1 is the training run"
	}
	if f.lab != nil && f.lab.ValidationRows == 0 {
		return "no validation queries — step 1 defines what judges the wording"
	}
	if len(f.iters) == 0 {
		return "nothing measured yet — iteration comes first"
	}
	if !f.gateOpen() {
		return fmt.Sprintf("%d of %d iterations — ctrl+g validates early, unproven", len(f.iters), iterationsRequired)
	}
	return "validation is run once per iteration, and it is the only thing that ranks them"
}

func (f *flowState) body(m *Model, h int) []string {
	switch {
	case f.step == 1:
		return m.setBody(h)
	case f.sec == secIteration:
		return m.iterBody(h)
	case f.step == 2:
		return m.validateBody(h)
	}
	return m.resultBody(h)
}

func (f *flowState) hints(m *Model) []hint {
	if f.step == 1 {
		h := []hint{{"enter", "next step"}, {"n", "new"}, {"e", "edit"}, {"d", "delete"},
			{"space", "label"}, {"esc", "sections"}}
		if f.sec == secGeneralization {
			h[0].act = "to validate"
		} else {
			h[0].act = "to the loop"
		}
		return h
	}
	if f.sec == secIteration {
		return []hint{{"j k", "rung"}, {"enter", "do it"}, {"esc", "sections"}}
	}
	if f.step == 2 {
		switch {
		case f.lab != nil && f.lab.ValidationRows == 0:
			return []hint{{"1", "validation set"}, {"esc", "sections"}}
		case !f.gateOpen():
			return []hint{{"ctrl+g", "validate early, unproven"}, {"esc", "sections"}}
		case m.needsValidation() == 0:
			return []hint{{"enter", "open the results"}, {"esc", "sections"}}
		}
		return []hint{{"enter", "run the validation set"}, {"esc", "sections"}}
	}
	return []hint{{"j k", "move"}, {"enter", "what it failed"}, {"a", "apply"}, {"h", "holdout"}, {"esc", "sections"}}
}

// ---------------------------------------------------------------- update

func (m *Model) updateFlow(msg tea.KeyMsg) tea.Cmd {
	f := m.flow
	if f == nil {
		return nil
	}
	key := msg.String()
	// The rail is also a jump: the number keys are this section's step numbers, and the gate is
	// enforced here rather than only drawn.
	if key >= "1" && key <= "9" {
		return m.jumpStep(int(key[0] - '0'))
	}
	if key == "ctrl+g" {
		return m.forceGeneralization()
	}
	switch {
	case f.step == 1:
		return m.updateSet(msg)
	case f.sec == secIteration:
		return m.updateIter(msg)
	case f.step == 2:
		return m.updateValidate(msg)
	}
	return m.updateResults(msg)
}

// forceGeneralization is ctrl+g: the gate is a bar, not a wall, and using it says so.
func (m *Model) forceGeneralization() tea.Cmd {
	f := m.flow
	if f.gateOpen() {
		m.setStatus("already open — five iterations are measured", false)
		return nil
	}
	f.forced = true
	m.setStatus(fmt.Sprintf("generalizing after %d of %d iterations — the validation rate is unproven",
		len(f.iters), iterationsRequired), true)
	f.sec, f.step = secGeneralization, 2
	f.enterStep(m)
	return nil
}

// jumpStep moves inside the section and refuses what the rules refuse: a step that needs a set it
// does not have, and a generalization step behind a closed gate.
func (m *Model) jumpStep(n int) tea.Cmd {
	f := m.flow
	if n < 1 || n > f.stepCount() || n == f.step {
		return nil
	}
	if f.lab != nil {
		switch {
		case n >= 2 && f.sec == secIteration && f.lab.TrainRows == 0:
			m.setStatus("the train set is empty — the iteration section's first step defines it", true)
			return nil
		case n >= 2 && f.sec == secGeneralization && f.lab.ValidationRows == 0:
			m.setStatus("the validation set is empty — the section's first step defines it", true)
			return nil
		}
	}
	if f.sec == secGeneralization && n >= 2 && !f.gateOpen() {
		m.setStatus(fmt.Sprintf("%d of %d iterations — ctrl+g validates early, with the warning",
			len(f.iters), iterationsRequired), true)
		return nil
	}
	f.step = n
	f.enterStep(m)
	return nil
}
