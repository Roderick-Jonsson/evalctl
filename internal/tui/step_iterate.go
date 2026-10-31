package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// Step 3: the iteration.
//
// It is a ladder and a detail block, not four screens. The ladder is the loop as the draft draws it
// — run, result, revise, generalize — and the block underneath is whatever the selected rung is
// asking for, so the whole loop is legible in one screenful and the only thing that changes when
// you move is the paragraph you are reading.
//
// The states are read off the disk, never stored: a wording that has been written but not measured
// is a candidate with no train measurement, and that single fact drives rung 1 (run it), rung 2
// (nothing to read yet) and rung 3 (do not revise over an unmeasured wording).

var ladderRows = []string{"training run", "training result", "optimize", "generalize"}

const (
	subRun = iota
	subResult
	subOptimize
	subGeneralize
)

func (m *Model) iterBody(h int) []string {
	f := m.flow
	lines := []string{"", "  " + titleStyle.Render(m.iterHeadline()),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, m.width-4))))}
	for i, name := range ladderRows {
		mark := "  "
		style := dimStyle
		if i == f.sub {
			mark = cursorMark.Render(glyphs.Cursor)
			style = titleStyle
		}
		label := fmt.Sprintf("%d  %-15s", i+1, name)
		state := f.rungState(i)
		line := mark + style.Render(label) + "  " + dimStyle.Render(state)
		if i == f.sub {
			line = selected(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "  "+dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, m.width-4)))))
	lines = append(lines, m.rungDetail(f.sub)...)
	return fitRows(lines, h)
}

// iterHeadline is the loop's own counter: which wording is under the microscope, how far into the
// five iterations it is, and what the last train run scored.
func (m *Model) iterHeadline() string {
	f := m.flow
	n := len(f.iters)
	cand := "—"
	if f.pending() != nil {
		n++
		cand = fmt.Sprintf("#%d", f.pending().ID)
	} else if it := f.latest(); it != nil {
		cand = fmt.Sprintf("#%d", it.Cand.ID)
	}
	head := fmt.Sprintf("iteration %d of %d · candidate %s", max(1, n), iterationsRequired, cand)
	if it := f.latest(); it != nil {
		head += fmt.Sprintf(" · train %s", pct(it.Train.Train.Passed, it.Train.Train.Judged))
	}
	return head
}

// rungState is the one line beside each rung: the fact that decides whether it can be done.
func (f *flowState) rungState(i int) string {
	it := f.latest()
	pend := f.pending()
	switch i {
	case subRun:
		if pend != nil {
			return fmt.Sprintf("candidate #%d is waiting to be measured", pend.ID)
		}
		if it != nil {
			return fmt.Sprintf("measured in trigger-%d · %s", it.Train.Iteration, shortWhen(it.Train.Started))
		}
		return "nothing measured yet"
	case subResult:
		if pend != nil {
			return "nothing to read yet"
		}
		if it != nil {
			fails := it.Cand.FailedTrain()
			if len(fails) == 0 {
				return fmt.Sprintf("%d of %d pass · nothing failing", it.Train.Train.Passed, it.Train.Train.Judged)
			}
			return fmt.Sprintf("%d of %d pass · %s failing", it.Train.Train.Passed, it.Train.Train.Judged,
				fmt.Sprint(len(fails)))
		}
		return "nothing to read yet"
	case subOptimize:
		if pend != nil {
			return fmt.Sprintf("blocked — #%d is unmeasured", pend.ID)
		}
		if it != nil {
			return fmt.Sprintf("revise #%d into the next candidate", it.Cand.ID)
		}
		return "nothing to revise yet"
	case subGeneralize:
		if !f.gateOpen() {
			return fmt.Sprintf("locked · %d more iteration(s) · ctrl+g forces it", max(0, iterationsRequired-len(f.iters)))
		}
		return fmt.Sprintf("ready · %s measured", plural(len(f.iters), "iteration"))
	}
	return ""
}

// rungDetail is the block under the ladder: the rule the rung exists for, the facts, and the one
// key it wants. Every rung has the same shape, so reading the screen is one habit.
func (m *Model) rungDetail(i int) []string {
	f := m.flow
	d := &docWriter{w: max(28, m.width-4)}
	it := f.latest()
	pend := f.pending()
	switch i {
	case subRun:
		d.title("Run the train set against one wording")
		d.lead("The train run is the only measurement a revision may learn from. Validation is not run here — it judges the wording in step 4, and measuring it now would let the loop learn from it.")
		rows, runs := f.lab.TrainRows, f.lab.Runs
		d.fact("candidate", fmt.Sprintf("#%d · %s", pendingID(pend, it), plural(charsOf(pend, it), "character")))
		d.fact("scope", fmt.Sprintf("%s × %d runs per query = %d agent calls", pluralOf(rows, "train query", "train queries"), runs, rows*runs))
		d.fact("writes", fmt.Sprintf("trigger-workspace/trigger-%d/", nextTrigger(m.store)))
		d.action("enter", "start the training run")
	case subResult:
		if pend != nil || it == nil {
			d.title("Nothing to read yet")
			d.lead("The run comes first: a result is what the train set said about the wording, and there is no wording measured yet.")
			d.action("j k", "back to the training run")
			return d.lines
		}
		fails := it.Cand.FailedTrain()
		d.title("Identify the failures — train set only")
		d.lead("Only these rows may guide a revision. Each card says what the ask was, why the row is in the set, what loaded instead, and which way the wording is wrong.")
		d.fact("candidate", fmt.Sprintf("#%d · train %s (%d of %d pass)", it.Cand.ID,
			pct(it.Train.Train.Passed, it.Train.Train.Judged), it.Train.Train.Passed, it.Train.Train.Judged))
		if len(fails) == 0 {
			d.fact("failures", "none — this wording passes the train set")
			d.para("Nothing here may guide a revision, so the next move is either a revision on a hunch (rung 3) or generalization (rung 4).")
			d.action("enter", "go to the revision")
			return d.lines
		}
		d.fact("failing", idList(fails))
		for _, row := range fails[:min(2, len(fails))] {
			d.note(fmt.Sprintf("q%d · %s", row.ID, verdictOf(row)))
		}
		d.action("enter", fmt.Sprintf("read %s", plural(len(fails), "failure card")))
	case subOptimize:
		d.title("Revise the wording for the next iteration")
		d.lead("Broaden if should-trigger rows are missing, narrow if should-not-trigger rows fire. Never paste the words of a query into the description — the validation set is what catches a wording that learned the train set by heart.")
		if pend != nil {
			d.fact("blocked", fmt.Sprintf("#%d has not been measured — run it, then revise over it", pend.ID))
			d.action("j k", "to the training run")
			return d.lines
		}
		if it == nil {
			d.fact("blocked", "nothing measured yet — step 3 starts with a training run")
			return d.lines
		}
		d.fact("parent", fmt.Sprintf("#%d · %s · train %s", it.Cand.ID, plural(len([]rune(strings.TrimSpace(it.Cand.Text))), "character"),
			pct(it.Train.Train.Passed, it.Train.Train.Judged)))
		d.fact("failing", idList(it.Cand.FailedTrain()))
		if len(it.Train.Fixed) > 0 || len(it.Train.Broke) > 0 {
			d.fact("diff", fmt.Sprintf("fixed %s · broke %s", idListOf(it.Train.Fixed), idListOf(it.Train.Broke)))
		}
		d.action("enter", "open the wording — it becomes candidate #"+fmt.Sprint(it.Cand.ID+1))
	case subGeneralize:
		d.title("Generalize on the validation set")
		d.lead("Five measured wordings is the guide's bar: below that the validation rate is a coin and the difference between two iterations is noise. Step 4 runs the validation set once against every measured wording and ranks them by it.")
		d.fact("measured", fmt.Sprintf("%d of %d iterations", len(f.iters), iterationsRequired))
		if m.needsValidation() > 0 {
			d.fact("validation", fmt.Sprintf("%s to run", plural(m.needsValidation(), "iteration")))
		} else if len(f.iters) > 0 {
			d.fact("validation", "every measured iteration has been validated")
		}
		if f.gateOpen() {
			d.action("enter", "open generalization")
		} else {
			d.action("ctrl+g", "generalize anyway — the rate is unproven")
		}
	}
	return d.lines
}

// needsValidation counts the measured iterations whose validation run has not happened: the work
// step 4 has to do before it can rank anything.
func (m *Model) needsValidation() int {
	if m.flow == nil {
		return 0
	}
	n := 0
	for _, it := range m.flow.iters {
		if it.Valid == nil {
			n++
		}
	}
	return n
}

func pendingID(pend *runner.LabCandidate, it *iteration) int {
	if pend != nil {
		return pend.ID
	}
	if it != nil {
		return it.Cand.ID
	}
	return 0
}

func charsOf(pend *runner.LabCandidate, it *iteration) int {
	if pend != nil {
		return len([]rune(strings.TrimSpace(pend.Text)))
	}
	if it != nil {
		return it.Cand.Chars
	}
	return 0
}

func nextTrigger(s *store.Store) int {
	return runner.New(s).NextIteration(runner.New(s).TriggerWorkspace(), "trigger")
}

func shortWhen(s string) string {
	if len(s) >= 16 {
		return s[11:16]
	}
	return s
}

func idList(rows []runner.ScoreRow) string {
	if len(rows) == 0 {
		return "none"
	}
	var out []string
	for _, r := range rows {
		out = append(out, fmt.Sprintf("q%d", r.ID))
	}
	return strings.Join(out, " ")
}

func idListOf(ids []int) string {
	if len(ids) == 0 {
		return "none"
	}
	var out []string
	for _, id := range ids {
		out = append(out, fmt.Sprintf("q%d", id))
	}
	return strings.Join(out, " ")
}

// verdictOf is the one sentence a failing row earns: which way the wording is wrong. It is a
// reading of the run, not a recommendation — the fix is always the description.
func verdictOf(row runner.ScoreRow) string {
	switch {
	case row.ShouldTrigger && row.Triggers == 0:
		return "too NARROW — this is the skill's job, and the wording never names that context"
	case !row.ShouldTrigger && row.Triggers > 0:
		return "too BROAD — it fired on something that is not this skill's work"
	case row.ShouldTrigger:
		return fmt.Sprintf("unstable — fired in %d of %d runs", row.Triggers, row.Runs)
	default:
		return fmt.Sprintf("unstable — failed to hold in %d of %d runs", row.Runs-row.Triggers, row.Runs)
	}
}

func (m *Model) updateIter(msg tea.KeyMsg) tea.Cmd {
	f := m.flow
	switch msg.String() {
	case "j", "down":
		f.sub = min(f.sub+1, len(ladderRows)-1)
	case "k", "up":
		f.sub = max(0, f.sub-1)
	case "g", "home":
		f.sub = 0
	case "G", "end":
		f.sub = len(ladderRows) - 1
	case "enter":
		return m.rungAction()
	}
	return nil
}

// rungAction is the verb each rung owns. Nothing here asks twice: the run screen states what will be
// spent before anything is spent, and the revision screen states which candidate it writes.
func (m *Model) rungAction() tea.Cmd {
	f := m.flow
	switch f.sub {
	case subRun:
		return m.openRunAsk()
	case subResult:
		if f.pending() != nil {
			m.setStatus("the wording has not been run — rung 1 is the run", true)
			return nil
		}
		it := f.latest()
		if it == nil {
			m.setStatus("nothing has been measured yet", true)
			return nil
		}
		fails := it.Cand.FailedTrain()
		if len(fails) == 0 {
			m.setStatus("the train set passes — there is no failure card, so rung 3 is the next move", false)
			f.sub = subOptimize
			return nil
		}
		return m.openFailures(it)
	case subOptimize:
		if f.pending() != nil {
			m.setStatus(fmt.Sprintf("#%d is unmeasured — measure it before revising over it", f.pending().ID), true)
			return nil
		}
		if f.latest() == nil {
			m.setStatus("nothing measured yet — run the train set first", true)
			return nil
		}
		return m.openDraft()
	case subGeneralize:
		return m.gotoStep(StepGeneralize)
	}
	return nil
}

// openFailures is the evidence screen: one card per failing train row, and the rule that says a
// failure here is a fact about the wording and not a row to reword.
func (m *Model) openFailures(it *iteration) tea.Cmd {
	d := &docWriter{w: max(28, m.width-6)}
	d.title(fmt.Sprintf("candidate #%d · %s", it.Cand.ID,
		fmt.Sprintf("train %s · %d of %d pass", pct(it.Train.Train.Passed, it.Train.Train.Judged),
			it.Train.Train.Passed, it.Train.Train.Judged)))
	d.lead("A query that fails here is evidence about the description, not a query to reword. Validation is not shown: it judges the revision, so it must stay unread.")
	for _, row := range it.Cand.FailedTrain() {
		d.card(fmt.Sprintf("q%d · %s · fired %d of %d", row.ID,
			labelWord(row.ShouldTrigger), row.Triggers, row.Runs))
		d.fact("the ask", clipText(row.Query, max(40, m.width-20)))
		if row.Why != "" {
			d.fact("why it is here", clipText(row.Why, max(40, m.width-20)))
		}
		d.fact("what loaded", loadedInstead(row))
		d.fact("verdict", verdictOf(row))
	}
	m.doc = &docState{scope: "failures", title: "train failures", lines: d.lines}
	m.backTo = screenFlow
	m.mode = screenDoc
	return nil
}

// loadedInstead says what the agent reached for when the row failed: the other skills that loaded,
// or nothing at all when the wording did not reach anything.
func loadedInstead(row runner.ScoreRow) string {
	var other []string
	for _, set := range row.LoadedSets {
		other = append(other, set...)
	}
	other = append(other, row.Others...)
	if len(other) == 0 {
		return "nothing — the wording did not reach it"
	}
	return strings.Join(uniq(other), ", ")
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
