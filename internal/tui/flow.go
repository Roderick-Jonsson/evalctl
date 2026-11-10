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
// Iteration is one screen: the set, the wording, the last run and the revision live together, because
// three of those four are not steps — an action, its output, and an edit.
var iterationSteps = []string{"the loop"}
var generalizationSteps = []string{"validation set", "ranking"}

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
	// iters is the measured wordings in iteration order: the loop's own numbering, which is what the
	// ranking is keyed on.
	iters []iteration
	// rows is the split the section's first step edits; empty on the other steps.
	rows []store.Entry
	sel  int
	top  int
	res  int // the selected row of the ranking
	// forced remembers ctrl+g: generalization before five iterations, on purpose, said out loud.
	forced bool
	gen    *genState
	// focus is which quadrant has the keys on the iteration screen, and draft is the wording being
	// edited there — the one piece of state in the app that is not a rendering of a file, because a
	// sentence half-typed is not a file yet.
	focus int
	draft *iterDraft
	// proposal is a description the model wrote for this iteration, waiting in the pane it will be
	// edited in. It is not a candidate and not a file: y puts it into the draft, and the draft is
	// what gets saved and run.
	proposal *describeState
	// discardArmed is the second esc on a workbench holding an unsaved sentence: the first one says
	// it would be lost, the second one means it.
	discardArmed bool
	// dry is the run's one non-file setting: it prints the commands a run would issue and writes
	// nothing, so it belongs to the session and not to the tree. It is set beside the set, where the
	// other run knob lives, and it applies to the next run and no further.
	dry bool
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
	return splitOf(f.sec)
}

// splitOf is the split a section works: iteration learns from the train set, generalization judges on
// the validation set. The section screen's pane reads it too, so the pane and the step that follows
// can never disagree about which set is on screen.
func splitOf(sec int) string {
	if sec == secGeneralization {
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

// pending is the wording the next training run is about: the draft staged in the workspace. There is
// one staging slot, so a later draft replaces an earlier one instead of stranding it, and a wording
// the runs already measured has no draft at all.
func (f *flowState) pending() *runner.LabCandidate {
	c, live := f.draftOf()
	if !live {
		return nil
	}
	return c
}

// draftOf is the wording the next train run is about: the draft staged in the workspace if there is
// one, otherwise the wording SKILL.md carries when it has been through the validation set but never
// through a train run. With one staging slot nothing is ever superseded — a draft is replaced, not
// stepped over — so a staged wording is always the live one.
func (f *flowState) draftOf() (*runner.LabCandidate, bool) {
	if f.lab == nil {
		return nil, false
	}
	for i := range f.lab.Candidates {
		if f.lab.Candidates[i].Pending {
			return &f.lab.Candidates[i], true
		}
	}
	for i := range f.lab.Candidates {
		c := &f.lab.Candidates[i]
		if c.InFile && c.MeasureOn(store.SplitTrain) == nil {
			return c, true
		}
	}
	return nil, false
}

// complete says whether a validation measurement judged the whole validation set.
//
// A batch that was cancelled, or a query whose launches all died, leaves a rate over the queries
// that happened to land — 1/1 out of a three-query set reads like a clean pass and is not one. The
// set's own row count is the denominator: below it the measurement is partial, it is drawn as
// partial, and it never decides the ranking.
func (f *flowState) complete(v *runner.LabTally) bool {
	if f.lab == nil {
		return v != nil && v.Judged > 0
	}
	return f.lab.CompleteTally(v)
}

// allValidated reports whether every measured wording has been through the validation set — and
// through all of it. A rate over part of the set is not a validation: it is a batch that stopped.
func allValidated(lab *runner.Lab) bool {
	it := iterationsOf(lab)
	if len(it) == 0 {
		return false
	}
	f := &flowState{lab: lab}
	for _, i := range it {
		if !f.complete(i.valid()) {
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
// and the runs alone. The chooser and the flow both read it, so the chooser cannot promise a step
// the flow will not open.
func loopOpen(lab *runner.Lab) (sec, step int) {
	iters := iterationsOf(lab)
	switch {
	case lab == nil || lab.TrainRows == 0:
		return secIteration, 1
	// Below the gate the work is in the loop: revising is what moves the numbers, and validation on
	// four wordings would rank nothing worth ranking.
	case len(iters) < iterationsRequired:
		return secIteration, 1
	case lab.ValidationRows == 0:
		return secGeneralization, 1
	}
	// Everything past the set lands on the ranking: validated or not, the table is the screen that
	// says which wordings have a validation rate, runs the ones that do not, and applies the pick.
	return secGeneralization, 2
}

// resumeIn puts the flow on the step inside this section the disk says the work is at.
func (f *flowState) resumeIn(m *Model) {
	if f.sec == secIteration {
		f.step = 1
	} else {
		switch {
		case f.lab == nil || f.lab.ValidationRows == 0:
			f.step = 1
		case len(f.iters) > 0 && allValidated(f.lab):
			f.step = 2
		case !f.gateOpen():
			// Land where the work can be done: the set is writable at any time, the run is not.
			f.step = 1
		default:
			f.step = 2
		}
	}
	f.res, f.sel, f.top = 0, 0, 0
	f.enterStep(m)
}

// enterStep resets the cursors a step owns, so arriving somewhere always shows its first row rather
// than a cursor remembered from a previous visit to a different split.
func (f *flowState) enterStep(m *Model) {
	switch {
	case f.sec == secIteration, f.step == 1:
		f.sel, f.top = 0, 0
		f.focus = focusSet
		m.reload()
		m.ensureDraft()
	case f.step == 2:
		// The ranking opens on its own recommendation: the point of the table is that the pick is
		// already made, and walking to it by hand would be the reader repeating the arithmetic.
		m.reload()
		f.res = f.recommended()
	}
}

// recommended is the row the table opens on: the best validation rate, or the top row when nothing
// has been validated yet — the table still has to be something you can walk from.
func (f *flowState) recommended() int {
	best := bestIteration(f)
	if best == nil {
		return 0
	}
	for i := range f.iters {
		if f.iters[i].Cand.ID == best.Cand.ID {
			return i
		}
	}
	return 0
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
	// Generalization: its ranking is behind the five-iteration gate.
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
	}
	return stateWaiting
}

// rail is the navigation inside a section: its steps, what state each one is in, and the one gate in
// the flow. It lists only this section's steps — the other section is one screen up.
func (f *flowState) rail(m *Model, w int) string {
	if len(f.steps()) == 1 {
		// One screen, so there is nothing to walk: the row says where the loop is instead. But only
		// where nothing else already says it — the iterations quadrant counts the loop in its head
		// and rates the last run in its newest row, and a screen that states its own totals twice
		// reads as two counters that might disagree. The row is left empty then, and the body takes
		// the space.
		if !m.itersTableDrawn() {
			return " " + dimStyle.Render(f.stateLine())
		}
		return ""
	}
	var b strings.Builder
	b.WriteString(" ")
	for i, name := range f.steps() {
		if i > 0 {
			b.WriteString(dimStyle.Render(" " + strings.Repeat(glyphs.Rule, 3) + " "))
		}
		label := fmt.Sprintf("%d %s", i+1, name)
		switch f.stepState(i + 1) {
		case stateCurrent:
			// The rail is where you are in the flow, not where your keys go: a filled dot, the
			// same family as the tick and the lock, and never the arrow the cursor uses — a second
			// arrow on the screen is a second cursor.
			b.WriteString(stepHere.Render(glyphs.Here + " " + label))
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
	if f.sec == secIteration {
		n := max(1, len(f.iters)+boolInt(f.pending() != nil))
		head := fmt.Sprintf("iteration %d of %d", n, iterationsRequired)
		if it := f.latest(); it != nil && f.pending() == nil {
			return head + fmt.Sprintf(" · %d of %d pass (%s)", it.Train.Train.Passed, it.Train.Train.Judged,
				pct(it.Train.Train.Passed, it.Train.Train.Judged))
		}
		return head + " · unmeasured"
	}
	if f.step == 1 {
		return plural(len(f.rows), "row")
	}
	if len(f.iters) == 0 {
		return "nothing measured yet"
	}
	done := 0
	for _, it := range f.iters {
		if f.complete(it.valid()) {
			done++
		}
	}
	return fmt.Sprintf("%d of %d validated", done, len(f.iters))
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
	// A proposal is a decision waiting to be made, so it is the only thing the row has to say.
	if f.proposal != nil {
		return "a proposal — y keeps it in the field, p asks again, esc drops it"
	}
	// A dry run is the one setting that changes what a keystroke costs, so while it is on it is the
	// only thing the status row has to say.
	if f.dry && f.sec == secIteration && f.focus == focusSet {
		return "dry run — the next run prints the commands it would issue and writes nothing"
	}
	if f.step == 1 {
		if f.sec == secGeneralization {
			return "these rows judge the wording; they are never used to write one"
		}
		return "the wording may learn from these rows — nothing else shapes a revision"
	}
	if f.sec == secIteration {
		if f.focus != focusSet {
			return "editing the description — ctrl+s saves it as the next candidate, esc gives the set back"
		}
		if f.pending() != nil {
			return "candidate #" + fmt.Sprint(f.pending().ID) + " is written but not measured"
		}
		if it := f.latest(); it != nil {
			return "train " + pct(it.Train.Train.Passed, it.Train.Train.Judged) +
				" · validation is measured in generalization, never here"
		}
		return "nothing measured yet — enter runs the train set"
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
	// The rule and the key are already said above — the head names what ranks the table and the
	// action line carries the run — so the corner is the one thing neither says: how much of the
	// table is ranked at all.
	done, total := f.ranked()
	return fmt.Sprintf("%d of %d ranked", done, total)
}

// ranked counts the wordings whose validation rate covers the whole set: the ones the table can
// actually put in an order.
func (f *flowState) ranked() (done, total int) {
	for _, it := range f.iters {
		if f.complete(it.valid()) {
			done++
		}
	}
	return done, len(f.iters)
}

// anyIncomplete reports whether some measured wording still owes a full validation rate.
func (f *flowState) anyIncomplete() bool {
	done, total := f.ranked()
	return done < total
}

// body is the work surface, whichever half of the set is on it: the workbench holds the rows, the
// wording and the wordings measured, and the split is which rows the first block is showing. The
// step screens it used to hand off to are retired — a set editor that replaced the whole workbench
// was a second screen for a toggle.
func (f *flowState) body(m *Model, h int) []string {
	return m.iterBody(h)
}

func (f *flowState) hints(m *Model) []hint {
	if m.formActive() {
		return m.form.hints()
	}
	// A proposal has the keys: the field is not being typed into, the set is not being walked, and
	// the only three moves are the ones that decide what happens to a paragraph somebody paid for.
	if f.proposal != nil {
		return []hint{{"y", "keep it in the field"}, {"p", "another"}, {"j k", "scroll"}, {"esc", "discard"}}
	}
	switch f.focus {
	case focusWording:
		return []hint{{"type", "the description"}, {"ctrl+s", "save"}, {"esc", "the set"}}
	}
	act := "save & run"
	if !f.draftChanged() {
		act = "run the " + setRegionWord(f.sec == secIteration)
	}
	// The band offers the ladder key, the new action, and the keys that are legal in any state. `f`
	// is not in the iteration half of it: the failure pane is already on screen, and a band that
	// names every reader ends up cutting the way out — `?` still has it, and so does the pane's own
	// head. In the generalization half `f` is the only door onto one wording in full, and `h` the
	// only door onto the holdout, so those two are named there and the model's proposal is not.
	if f.sec == secGeneralization {
		return []hint{{"v", "validate"}, {"a", "apply"}, {"f", "in full"}, {"h", "holdout"},
			{"s", "the other half"}, {"esc", "the work"}}
	}
	return []hint{{"enter", act}, {"tab", "the description"}, {"p", "a model"}, {"s", "the other half"},
		{"v", "validate"}, {"a", "apply"}, {"esc", "the work"}}
}

// ---------------------------------------------------------------- update

func (m *Model) updateFlow(msg tea.KeyMsg) tea.Cmd {
	f := m.flow
	if f == nil {
		return nil
	}
	key := msg.String()
	// ctrl+g is the one door the gate leaves open: generalizing before five wordings are measured is
	// allowed, and it says so rather than refusing. Everything else is the workbench's.
	if key == "ctrl+g" {
		return m.forceGeneralization()
	}
	return m.updateIter(msg)
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
