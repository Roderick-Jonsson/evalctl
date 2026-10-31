package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// The loop's four steps, and the state every one of them reads.
//
// The steps are the draft's own: the train set, the validation set, the iteration (run → result →
// revise), and generalization. What the screens need to know about a skill is derived from the
// ledger and the trigger iterations on every reload, so there is no progress file to fall out of
// step with the measurements — the disk is the progress, and the rail is a rendering of it.

const (
	StepTrain = iota + 1
	StepValidation
	StepIterate
	StepGeneralize
)

// iterationsRequired is the guide's "if >= 5 iterations, ready to generalize": below five measured
// wordings the validation rate is a coin and the comparison between iterations is noise.
const iterationsRequired = 5

var stepNames = map[int]string{
	StepTrain:      "train set",
	StepValidation: "validation set",
	StepIterate:    "iterate",
	StepGeneralize: "generalize",
}

var stepOrder = []int{StepTrain, StepValidation, StepIterate, StepGeneralize}

// iteration is one measured wording: the candidate, its train measurement, and its validation
// measurement when the generalization step has run.
//
// A candidate is measured twice in the loop, and that is the point of it: the train run is what a
// revision is allowed to learn from, and the validation run — later, and only in step 4 — is what
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
	step int
	lab  *runner.Lab
	// iters is the measured candidates in ledger order: the loop's own numbering, which is what
	// step 4's result list is keyed on.
	iters []iteration
	// rows is the split the current step edits (steps 1 and 2); empty on the other steps.
	rows []store.Entry
	sel  int
	top  int
	sub  int // the selected rung of the iteration ladder
	res  int // the selected row of the result list
	// forced remembers ctrl+g: generalization before five iterations, on purpose, said out loud.
	forced bool
	gen    *genState
}

// split is the split the current step edits, or "" for the steps that edit no rows.
func (f *flowState) split() string {
	switch f.step {
	case StepTrain:
		return store.SplitTrain
	case StepValidation:
		return store.SplitValidation
	}
	return ""
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

// allValidated reports whether every measured wording has been through the validation set: the
// gate on step 4's result list.
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

// resume puts the flow on the step the disk says the work is at.
func (f *flowState) resume(m *Model) {
	switch {
	case f.lab == nil:
		f.step = StepTrain
	case f.lab.TrainRows == 0:
		f.step = StepTrain
	case f.lab.ValidationRows == 0:
		f.step = StepValidation
	case len(f.iters) > 0 && allValidated(f.lab):
		f.step = StepGeneralize
	default:
		f.step = StepIterate
	}
	f.sub, f.res, f.sel, f.top = 0, 0, 0, 0
	f.enterStep(m)
}

// enterStep resets the cursors a step owns, so arriving somewhere always shows its first row rather
// than a cursor remembered from a previous visit to a different split.
func (f *flowState) enterStep(m *Model) {
	switch f.step {
	case StepTrain, StepValidation:
		f.sel, f.top = 0, 0
		m.reload()
	case StepIterate:
		f.sub = f.defaultSub()
	case StepGeneralize:
		f.res = 0
		if f.gen == nil {
			f.gen = &genState{}
		}
	}
}

// defaultSub is where the iteration step opens: the rung the loop is actually waiting on.
func (f *flowState) defaultSub() int {
	switch {
	case f.pending() != nil:
		return 0 // a wording is waiting to be measured
	case f.latest() == nil:
		return 0
	case !f.gateOpen():
		return 2 // revise: the loop's next productive move
	default:
		return 3 // generalize
	}
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

func (f *flowState) stepState(step int) subState {
	if step == f.step {
		return stateCurrent
	}
	switch step {
	case StepTrain:
		if f.lab != nil && f.lab.TrainRows > 0 {
			return stateDone
		}
	case StepValidation:
		if f.lab != nil && f.lab.ValidationRows > 0 {
			return stateDone
		}
	case StepIterate:
		if len(f.iters) >= iterationsRequired {
			return stateDone
		}
		if len(f.iters) > 0 && f.step == StepGeneralize {
			return stateDone
		}
	case StepGeneralize:
		if !f.gateOpen() {
			return stateLocked
		}
		if f.lab != nil && len(f.iters) > 0 && allValidated(f.lab) {
			return stateDone
		}
	}
	return stateWaiting
}

// rail is the navigation: four steps, what state each one is in, and the one gate in the flow.
func (f *flowState) rail(w int) string {
	var b strings.Builder
	b.WriteString(" ")
	for i, step := range stepOrder {
		if i > 0 {
			b.WriteString(dimStyle.Render(" " + strings.Repeat(glyphs.Rule, 3) + " "))
		}
		label := fmt.Sprintf("%d %s", step, stepNames[step])
		switch f.stepState(step) {
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

// stateLine is the fact pair the bar carries beside the scope: where the loop is, in numbers.
func (f *flowState) stateLine() string {
	if f.lab == nil {
		return ""
	}
	switch f.step {
	case StepTrain, StepValidation:
		return fmt.Sprintf("%s · %s", stepNames[f.step], plural(len(f.rows), "row"))
	case StepIterate:
		n := len(f.iters)
		if f.pending() != nil {
			n++
		}
		if it := f.latest(); it != nil && f.pending() == nil {
			return fmt.Sprintf("iteration %d of %d · train %s", n, iterationsRequired,
				pct(it.Train.Train.Passed, it.Train.Train.Judged))
		}
		return fmt.Sprintf("iteration %d of %d", max(1, n), iterationsRequired)
	case StepGeneralize:
		if len(f.iters) == 0 {
			return "nothing to generalize yet"
		}
		return plural(len(f.iters), "iteration") + " measured"
	}
	return ""
}

// stateText is the status row's own line: the constraint the step is working under, which is the
// thing a person forgets between sessions.
func (f *flowState) stateText() string {
	switch f.step {
	case StepTrain:
		return "the wording may learn from these rows — nothing else shapes a revision"
	case StepValidation:
		return "these rows judge the wording; they are never used to write one"
	case StepIterate:
		if f.pending() != nil {
			return "candidate #" + fmt.Sprint(f.pending().ID) + " is written but not measured"
		}
		if it := f.latest(); it != nil {
			return "train " + pct(it.Train.Train.Passed, it.Train.Train.Judged) +
				" · validation stays withheld until step 4"
		}
		return "nothing measured yet — rung 1 is the training run"
	case StepGeneralize:
		if f.lab != nil && f.lab.ValidationRows == 0 {
			return "no validation queries — step 2 defines what judges the wording"
		}
		if len(f.iters) == 0 {
			return "nothing has been measured — the loop starts in step 3"
		}
		if !f.gateOpen() {
			return fmt.Sprintf("%d of %d iterations — ctrl+g generalizes early, unproven", len(f.iters), iterationsRequired)
		}
		return "validation is run once per iteration, and it is the only thing that ranks them"
	}
	return ""
}

func (f *flowState) body(m *Model, h int) []string {
	switch f.step {
	case StepTrain, StepValidation:
		return m.setBody(h)
	case StepIterate:
		return m.iterBody(h)
	case StepGeneralize:
		return m.genBody(h)
	}
	return nil
}

func (f *flowState) hints(m *Model) []hint {
	switch f.step {
	case StepTrain, StepValidation:
		h := []hint{{"enter", "next step"}, {"n", "new"}, {"e", "edit"}, {"d", "delete"},
			{"space", "label"}, {"esc", "skills"}}
		if f.step == StepTrain {
			h[0].act = "to the validation set"
		} else {
			h[0].act = "to the loop"
		}
		return h
	case StepIterate:
		h := []hint{{"j k", "sub-step"}, {"enter", "do it"}}
		if !f.gateOpen() {
			h = append(h, hint{"ctrl+g", "generalize early"})
		}
		return append(h, hint{"esc", "skills"})
	case StepGeneralize:
		if f.lab != nil && f.lab.ValidationRows == 0 {
			return []hint{{"2", "validation set"}, {"esc", "skills"}}
		}
		if !f.gateOpen() {
			return []hint{{"j k", "iterate"}, {"ctrl+g", "generalize early"}, {"esc", "skills"}}
		}
		if len(f.iters) == 0 {
			return []hint{{"1", "back to the loop"}, {"esc", "skills"}}
		}
		if !allValidated(f.lab) {
			return []hint{{"enter", "run the validation set"}, {"esc", "back"}}
		}
		return []hint{{"j k", "move"}, {"enter", "what it failed"}, {"a", "apply"}, {"h", "holdout"}, {"esc", "skills"}}
	}
	return nil
}

// ---------------------------------------------------------------- update

func (m *Model) updateFlow(msg tea.KeyMsg) tea.Cmd {
	f := m.flow
	if f == nil {
		return nil
	}
	key := msg.String()
	// The rail is also a jump: the number keys are the step numbers, and the gate is enforced here
	// rather than only drawn.
	if key >= "1" && key <= "4" {
		step := int(key[0] - '0')
		return m.gotoStep(step)
	}
	if key == "ctrl+g" {
		if f.gateOpen() {
			m.setStatus("already open — five iterations measured", false)
			return nil
		}
		f.forced = true
		m.setStatus(fmt.Sprintf("generalizing after %d of %d iterations — the validation rate is unproven",
			len(f.iters), iterationsRequired), true)
		return m.gotoStep(StepGeneralize)
	}
	switch f.step {
	case StepTrain, StepValidation:
		return m.updateSet(msg)
	case StepIterate:
		return m.updateIter(msg)
	case StepGeneralize:
		return m.updateGen(msg)
	}
	return nil
}

// gotoStep enforces the two rules the rail draws: the gate on step 4, and a query set before the
// loop that measures it.
func (m *Model) gotoStep(step int) tea.Cmd {
	f := m.flow
	if step == f.step {
		return nil
	}
	switch {
	case step >= StepIterate && f.lab != nil && f.lab.TrainRows == 0:
		m.setStatus("the train set is empty — step 1 defines what the loop measures", true)
		return nil
	case step >= StepIterate && f.lab != nil && f.lab.ValidationRows == 0:
		m.setStatus("the validation set is empty — step 2 defines what judges the wording", true)
		return nil
	case step == StepGeneralize && !f.gateOpen():
		m.setStatus(fmt.Sprintf("%d of %d iterations — ctrl+g generalizes early, with the warning",
			len(f.iters), iterationsRequired), true)
		return nil
	}
	f.step = step
	f.enterStep(m)
	return nil
}
