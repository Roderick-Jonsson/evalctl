package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"evalctl/internal/runner"
)

// Step 4: generalization.
//
// The validation set is the instrument the whole loop protects: it is read here, once, against every
// wording the loop produced, and it is the only thing that ranks them. The guide is blunt about why
// — the best description is often not the last one written, and a loop that keeps revising until the
// train set passes will have learned the train set.
//
// So this step has two states and they are both honest about it: before the validation runs, it
// states what is about to be measured and what it costs; after, it is the result list, one line per
// iteration, train beside validation and the gap between them.

// validateBody is the generalization section's second step: the gate, the empty set, or the batch.
//
// The validation set is the instrument the whole loop protects — read once, against every wording the
// iteration produced, and the only thing that ranks them. So this screen says what is about to be
// measured and what it costs before anything is spent, and it is where the five-iteration bar is
// either satisfied or forced.
func (m *Model) validateBody(h int) []string {
	f := m.flow
	if !f.gateOpen() {
		return m.lockedBody(h)
	}
	if f.lab != nil && f.lab.ValidationRows == 0 {
		return m.emptyValidationBody(h)
	}
	if m.needsValidation() > 0 {
		return m.validationBody(h)
	}
	// Everything has been validated: the step's work is done, and the results are the next step.
	d := &docWriter{w: max(28, m.width-4)}
	d.title("Every measured wording has been validated")
	d.lead("Nothing is left to run against the validation set. The result list ranks the wordings by it, and there is where one gets applied.")
	d.rule()
	d.fact("iterations", plural(len(f.iters), "measured wording"))
	d.action("enter", "open the results")
	return fitRows(d.lines, h)
}

// emptyValidationBody is the state a loop reaches when the train set was built and the validation
// set never was: there is nothing to rank against, and the fix is a query set, not a run.
func (m *Model) emptyValidationBody(h int) []string {
	rows := 0
	if m.flow.lab != nil {
		rows = m.flow.lab.TrainRows
	}
	d := &docWriter{w: max(28, m.width-4)}
	d.title("The validation set is empty")
	d.lead("Generalization runs the validation queries against every wording the loop produced. With none defined there is nothing to run, and a wording chosen without them is chosen on the train set alone — which is the thing the loop exists to avoid.")
	d.rule()
	d.fact("train", plural(rows, "row")+" of this skill's queries")
	d.fact("validation", "none — the section's first step defines them")
	d.blank()
	d.para("Write queries the wording must not have seen: a few that should trigger and a few that should not, drawn from the same work as the train rows but not from the same asks.")
	d.action("1", "go to the validation set")
	return fitRows(d.lines, h)
}

// lockedBody is the gate, in the guide's own terms: not "you cannot", but "not yet, and here is the
// number".
func (m *Model) lockedBody(h int) []string {
	f := m.flow
	d := &docWriter{w: max(28, m.width-4)}
	d.title("Validation is locked until the loop has run")
	d.lead("Five measured iterations is the guide's bar: below that the validation rate is a coin, and the difference between two iterations is noise. The gate exists so that a wording does not get picked because it was the third one tried.")
	d.rule()
	d.fact("measured", fmt.Sprintf("%d of %d iterations", len(f.iters), iterationsRequired))
	d.fact("train", fmt.Sprintf("%s", trainSummary(f)))
	d.fact("validation", fmt.Sprintf("%s held back, never read", pluralOf(f.lab.ValidationRows, "query", "queries")))
	d.blank()
	d.para("If the loop has stopped moving — the same failures for two revisions, or nothing to change — the answer is the query set, not the wording. ctrl+g forces generalization and says so in the report.")
	d.action("ctrl+g", "validate anyway — the rate is unproven")
	return fitRows(d.lines, h)
}

func trainSummary(f *flowState) string {
	if it := f.latest(); it != nil {
		return pct(it.Train.Train.Passed, it.Train.Train.Judged) + " on the last wording"
	}
	return "nothing measured"
}

// validationBody is the batch before it runs: which iterations, how many agent calls, and the fact
// that each one is written into SKILL.md in turn.
func (m *Model) validationBody(h int) []string {
	f := m.flow
	n := m.needsValidation()
	d := &docWriter{w: max(28, m.width-4)}
	d.title("Run the validation set")
	d.lead("Every measured wording has to see the validation set once, or the result list has nothing to rank. This runs each candidate in turn: the wording is written into SKILL.md, the validation queries are run against it, and the next candidate follows.")
	d.rule()
	for _, it := range f.iters {
		state := "to run"
		if it.Valid != nil {
			state = "done · " + pct(it.Valid.Validation.Passed, it.Valid.Validation.Judged)
		}
		d.fact(fmt.Sprintf("iteration %d", it.N),
			fmt.Sprintf("#%d · train %s · validation %s", it.Cand.ID,
				pct(it.Train.Train.Passed, it.Train.Train.Judged), state))
	}
	d.fact("cost", fmt.Sprintf("%d validation queries × %d runs × %s to run = %d agent calls",
		f.lab.ValidationRows, f.lab.Runs, plural(n, "iteration"),
		f.lab.ValidationRows*f.lab.Runs*n))
	d.blank()
	d.para("SKILL.md is left holding the last candidate measured. Nothing is applied until you choose one on the result list.")
	d.action("enter", fmt.Sprintf("run the validation set for %s", plural(n, "iteration")))
	return fitRows(d.lines, h)
}

// resultsName is the result list's name, in the register every region uses. Its head carries the
// fact that the sentence used to: what the ranking is by.
const resultsName = "RESULTS"

// resultBody is the draft's result list: iteration, train, validation.
func (m *Model) resultBody(h int) []string {
	f := m.flow
	if len(f.iters) == 0 {
		d := &docWriter{w: max(28, m.width-4)}
		d.title("Nothing measured yet")
		d.lead("The result list is the loop's own history — it is empty until a wording has been run on the train set.")
		return fitRows(d.lines, h)
	}
	f.res = min(f.res, len(f.iters)-1)
	best := bestIteration(f)
	head := "  " + dimStyle.Render(fitTo("ITERATION", 10)) + " " + dimStyle.Render(fitTo("TRAIN", 12)) + "  " +
		dimStyle.Render(fitTo("VALIDATION", 12)) + " " + dimStyle.Render(fitTo("GAP", 6)) + " " +
		dimStyle.Render(fitTo("CHARS", 6)) + "  " + dimStyle.Render("NOTE")
	lines := []string{"", regionHead(m.width, resultsName, "ranked by the validation set", true),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, m.width-4)))), head}
	room := max(1, h-len(lines)-3)
	top := max(0, f.res-room+1)
	for i := top; i < len(f.iters) && len(lines) < 4+room; i++ {
		lines = append(lines, m.resultRow(f.iters[i], i == f.res, best != nil && best.Cand.ID == f.iters[i].Cand.ID))
	}
	for len(lines) < 4+room {
		lines = append(lines, "")
	}
	lines = append(lines, "  "+dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, m.width-4)))))
	if best != nil {
		lines = append(lines,
			"  "+styleOn.Render(glyphs.Done+" best by validation")+"  "+
				fmt.Sprintf("#%d · validation %s · train %s", best.Cand.ID,
					pct(best.Valid.Validation.Passed, best.Valid.Validation.Judged),
					pct(best.Train.Train.Passed, best.Train.Train.Judged)))
	} else {
		lines = append(lines, "")
	}
	for _, line := range wrapPlain("validation is the ranking; train beside it is how much of that rate the wording learned rather than earned", max(30, m.width-4)) {
		lines = append(lines, "  "+dimStyle.Render(line))
	}
	return fitRows(lines, h)
}

func (m *Model) resultRow(it iteration, focused, best bool) string {
	mark := "  "
	if focused {
		mark = cursorMark.Render(glyphs.Cursor)
	}
	trainTxt := fmt.Sprintf("%d/%d %s", it.Train.Train.Passed, it.Train.Train.Judged,
		pct(it.Train.Train.Passed, it.Train.Train.Judged))
	validTxt := "—"
	if v := it.valid(); v != nil {
		validTxt = fmt.Sprintf("%d/%d %s", v.Passed, v.Judged, pct(v.Passed, v.Judged))
	}
	prefix := mark + colID.Render(fitTo(fmt.Sprint(it.N), 10)) + " " +
		colText.Render(fitTo(trainTxt, 12)) + "  " +
		colText.Render(fitTo(validTxt, 12)) + " " +
		colSplit.Render(fitTo(gapOf(&it.Train.Train, it.valid()), 6)) + " " +
		colSplit.Render(fitTo(fmt.Sprint(it.Cand.Chars), 6)) + "  "
	// The marks go last and the note takes what is left: a note that ate the row's evidence would
	// be the one thing on this line that could not be cut.
	marks := ""
	if best {
		marks += " " + styleOn.Render(glyphs.Done)
	}
	if it.Cand.InFile {
		marks += dimStyle.Render(" in file")
	}
	room := m.width - lipgloss.Width(prefix) - lipgloss.Width(marks) - 1
	note := oneLine(it.Cand.Note)
	if note == "" {
		note = "—"
	}
	line := prefix + colText.Render(ellipsis(note, max(6, room))) + marks
	if focused {
		line = selected(line)
	}
	return line
}

// bestIteration is the guide's rule, computed: the best validation rate wins, and a tie goes to the
// earlier wording — the one that got there with less text and fewer revisions.
func bestIteration(f *flowState) *iteration {
	var best *iteration
	for i := range f.iters {
		it := &f.iters[i]
		if it.Valid == nil {
			continue
		}
		if best == nil {
			best = it
			continue
		}
		a, b := it.Valid.Validation, best.Valid.Validation
		ra := pctInt(a.Passed, a.Judged)
		rb := pctInt(b.Passed, b.Judged)
		if ra > rb || (ra == rb && it.Cand.ID < best.Cand.ID) {
			best = it
		}
	}
	return best
}

// updateValidate is the batch step's one key: run, or move on when there is nothing left to run.
func (m *Model) updateValidate(msg tea.KeyMsg) tea.Cmd {
	f := m.flow
	if !f.gateOpen() || f.lab == nil || f.lab.ValidationRows == 0 {
		return nil
	}
	if msg.String() != "enter" {
		return nil
	}
	if m.needsValidation() > 0 {
		return m.startGeneralization()
	}
	return m.jumpStep(3)
}

// updateResults is the result list: pick a wording, read why it failed, apply it, or run the fresh
// queries once it is in.
func (m *Model) updateResults(msg tea.KeyMsg) tea.Cmd {
	f := m.flow
	switch msg.String() {
	case "j", "down":
		f.res = min(f.res+1, max(0, len(f.iters)-1))
	case "k", "up":
		f.res = max(0, f.res-1)
	case "g", "home":
		f.res = 0
	case "G", "end":
		f.res = max(0, len(f.iters)-1)
	case "enter":
		if len(f.iters) > 0 {
			return m.openIterationDoc(f.iters[f.res])
		}
	case "a":
		if len(f.iters) > 0 {
			m.askApply(f.iters[f.res])
		}
	case "h":
		return m.openHoldout()
	}
	return nil
}

// askApply is the write into SKILL.md, confirmed: it is the one action in the loop that changes what
// ships, and the ledger records it beside the measurement that earned it.
func (m *Model) askApply(it iteration) {
	validTxt := "— (not measured on the validation set)"
	if v := it.valid(); v != nil {
		validTxt = pct(v.Passed, v.Judged)
	}
	m.ask = &askState{
		scope: "apply",
		title: fmt.Sprintf("apply candidate #%d to %s/SKILL.md?", it.Cand.ID, m.skill),
		lines: []string{
			fmt.Sprintf("train %s · validation %s", pct(it.Train.Train.Passed, it.Train.Train.Judged), validTxt),
			"",
			dimStyle.Render(clipText(it.Cand.Text, min(88, max(30, m.width-6)))),
			"",
			dimStyle.Render("the frontmatter description is rewritten and the old file backed up; nothing else in SKILL.md changes"),
		},
		yes: "apply it",
		then: func(m *Model) tea.Cmd {
			msg, err := runner.New(m.store).ApplyDescription(m.skill, it.Cand.ID)
			if err != nil {
				m.setStatus(err.Error(), true)
				return nil
			}
			m.reload()
			m.setStatus(msg+" — h runs the fresh queries once", false)
			return nil
		},
	}
	m.mode = screenAsk
}

// openHoldout is the guide's last move: 5–10 fresh queries, measured once, reported apart from the
// rates the loop tuned. They are not a second validation set.
func (m *Model) openHoldout() tea.Cmd {
	f := m.flow
	if f.lab.HoldoutRows == 0 {
		m.setStatus("no holdout queries — add a few fresh ones to trigger/trigger-evals.json with split holdout", true)
		return nil
	}
	if f.lab.Applied == nil {
		m.setStatus("apply a candidate first — the holdout measures what ships", true)
		return nil
	}
	m.run = &runState{
		kind:  runHoldout,
		runs:  f.lab.Runs,
		dry:   f.dry,
		scope: "holdout run",
		hint:  "the fresh queries are measured once and never folded into the loop's rates",
	}
	return m.startRun()
}

// openIterationDoc is one iteration in full: what it failed on the train set, and what the revision
// fixed or broke against its parent.
func (m *Model) openIterationDoc(it iteration) tea.Cmd {
	d := &docWriter{w: max(28, m.width-6)}
	valid := it.valid()
	// The reader's head carries which iteration and which candidate; the body opens with what the
	// two rates mean, so the title is not said twice.
	d.lead("Train is what the wording was revised against; validation is what it was measured on. The gap is the part of the train rate that did not carry over.")
	d.rule()
	d.fact("train", fmt.Sprintf("%s (%d of %d)", pct(it.Train.Train.Passed, it.Train.Train.Judged),
		it.Train.Train.Passed, it.Train.Train.Judged))
	if valid != nil {
		d.fact("validation", fmt.Sprintf("%s (%d of %d)", pct(valid.Passed, valid.Judged), valid.Passed, valid.Judged))
	} else {
		d.fact("validation", "not measured — validate runs it")
	}
	d.fact("gap", gapOf(&it.Train.Train, valid))
	d.fact("chars", fmt.Sprint(it.Cand.Chars))
	if it.Cand.Note != "" {
		d.fact("note", it.Cand.Note)
	}
	if len(it.Train.Fixed) > 0 || len(it.Train.Broke) > 0 {
		d.fact("diff", fmt.Sprintf("fixed %s · broke %s", idListOf(it.Train.Fixed), idListOf(it.Train.Broke)))
	}
	if fails := it.Cand.FailedTrain(); len(fails) > 0 {
		for _, row := range fails {
			d.card(fmt.Sprintf("q%d · %s · fired %d of %d", row.ID, labelWord(row.ShouldTrigger), row.Triggers, row.Runs))
			d.fact("the ask", clipText(row.Query, max(40, m.width-20)))
			d.fact("verdict", verdictOf(row))
		}
	}
	d.card("the wording")
	for _, line := range wrapPlain(oneLine(it.Cand.Text), max(30, m.width-8)) {
		d.lines = append(d.lines, "    "+line)
	}
	m.doc = &docState{scope: "iteration", title: fmt.Sprintf("iteration %d · candidate #%d", it.N, it.Cand.ID),
		lines: d.lines}
	m.backTo = screenFlow
	m.mode = screenDoc
	return nil
}
