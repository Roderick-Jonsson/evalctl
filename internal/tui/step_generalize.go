package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"evalctl/internal/runner"
)

// Generalization: the validation set, and the ranking.
//
// The validation set is the instrument the whole loop protects: it is read against every wording the
// loop produced, and it is the only thing that ranks them. The guide is blunt about why — the best
// description is often not the last one written, and a loop that keeps revising until the train set
// passes will have learned the train set.
//
// So the ranking is one screen and one table. It used to be two — a batch screen that listed the
// wordings still to run, and a result list you were sent to afterwards — and that was the same table
// twice with a keypress between them: the batch screen had to draw every iteration's state to say
// what it was about to do, and the result list had to draw the same rows again to show what it did.
// Now the table is the whole of it. A wording with no validation rate sits in its row with a dash in
// that column, the rows that are missing a rate are run from the same screen (`v`), the table fills
// itself in when the batch lands, and the row the rule recommends is already under the cursor.

const rankingName = "RANKING"

// valCol is the validation column's width: "1/1 100% of 3" is the widest cell the column has to
// hold, and a partial rate is worth two extra cells — it is the difference between a rank and a
// rounding of one. colTrain is the same width for the same reason.
const (
	valCol   = 16
	colTrain = 16
)

// rankingBody is the generalization section's second step: the gate, the empty set, the empty loop,
// or the ranking with whatever it still owes.
func (m *Model) rankingBody(h int) []string {
	f := m.flow
	if !f.gateOpen() {
		return m.lockedBody(h)
	}
	if f.lab == nil || f.lab.ValidationRows == 0 {
		return m.emptyValidationBody(h)
	}
	if len(f.iters) == 0 {
		d := &docWriter{w: max(28, m.width-4)}
		d.title("Nothing measured yet")
		d.lead("The ranking is the loop's own history — it is empty until a wording has been run on the train set.")
		d.rule()
		d.fact("iterations", "none measured")
		d.blank()
		d.para("Iteration is the other half of the loop: a wording has to be written and measured before the validation set has anything to judge.")
		d.action("esc", "back to the sections")
		return fitRows(d.lines, h)
	}

	f.res = min(max(f.res, 0), len(f.iters)-1)
	best := bestIteration(f)
	pending := m.needsValidation()
	// The wordings the ledger holds and no train run ever measured are not rows here — they are not
	// iterations yet — but they are the answer to "why does this table have fewer rows than the
	// ledger", so the tail names them instead of leaving the count to be guessed.
	draft, live := f.draftOf()

	// The head says what the table is ranked by, and says it as a fact rather than as an intention:
	// with no rate measured yet there is nothing here to rank.
	right := "ranked by the validation set"
	if best == nil {
		right = "nothing validated yet"
	}
	lines := []string{
		"",
		regionHead(m.width, rankingName, right, true),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, m.width-4)))),
		"  " + dimStyle.Render(fitTo("ITERATION", 10)) + " " + dimStyle.Render(fitTo("TRAIN", colTrain)) + "  " +
			dimStyle.Render(fitTo("VALIDATION", valCol)) + " " + dimStyle.Render(fitTo("GAP", 6)) + " " +
			dimStyle.Render(fitTo("CHARS", 6)) + "  " + dimStyle.Render("NOTE"),
	}
	headRows := len(lines)

	// The tail is what the table still owes, and a legend only where a cell uses its mark. The pick
	// is not restated here: it is a tag on its own row, and the columns are named by their own head
	// — under a table that already says both, a paragraph saying them again is the screen reading
	// itself out loud.
	var tail []string
	tail = append(tail, "  "+dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, m.width-4)))))
	if pending > 0 {
		// The missing rates are the screen's one forward move, so the line that offers it says what
		// it costs: the validation queries, the runs, and the calls that multiplies out to.
		cost := f.lab.ValidationRows * f.lab.Runs * pending
		tail = append(tail, "  "+cursorMark.Render(glyphs.Arrow)+" "+titleStyle.Render("v")+"  "+
			ellipsis(fmt.Sprintf("validate %s · %d queries × %d runs = %d agent calls",
				plural(pending, "iteration"), f.lab.ValidationRows, f.lab.Runs, cost),
				max(20, m.width-8)))
	}
	if draft != nil {
		word := "the workbench's run measures it"
		if !live {
			word = "a later revision stepped over it"
		}
		tail = append(tail, "  "+dimStyle.Render(ellipsis(fmt.Sprintf(
			"#%d is in the ledger but not this table — %s", draft.ID, word), max(30, m.width-4))))
	}
	if m.anyPartial() {
		tail = append(tail, "  "+dimStyle.Render(ellipsis(
			"of N — judged of the split; a stopped batch never ranks", max(30, m.width-4))))
	}

	room := max(1, h-len(lines)-len(tail))
	top := max(0, f.res-room+1)
	for i := top; i < len(f.iters) && len(lines) < headRows+room; i++ {
		lines = append(lines, m.rankingRow(f.iters[i], i == f.res, best != nil && best.Cand.ID == f.iters[i].Cand.ID))
	}
	for len(lines) < headRows+room {
		lines = append(lines, "")
	}
	return fitRows(append(lines, tail...), h)
}

func (m *Model) rankingRow(it iteration, focused, best bool) string {
	mark := "  "
	if focused {
		mark = cursorMark.Render(glyphs.Cursor)
	}
	trainTxt := fmt.Sprintf("%d/%d %s", it.Train.Train.Passed, it.Train.Train.Judged,
		pct(it.Train.Train.Passed, it.Train.Train.Judged))
	// The train column is marked the same way: a run whose later queries died judges fewer rows than
	// the set holds, and 1/1 100% beside 4/4 100% would read as the better wording.
	if !it.Train.TrainFull && m.flow.lab != nil {
		trainTxt += fmt.Sprintf(" of %d", m.flow.lab.TrainRows)
	}
	// A wording the validation set has not seen is drawn and not skipped: the dash is the fact this
	// screen exists to show, and a row that vanished until it was measured would hide how much of
	// the table is still unranked. A rate over part of the set is drawn as a rate and marked, because
	// it is the one number on this screen that can look better than it is.
	validTxt := "—"
	partial := false
	if v := it.valid(); v != nil {
		validTxt = fmt.Sprintf("%d/%d %s", v.Passed, v.Judged, pct(v.Passed, v.Judged))
		partial = !m.flow.complete(v)
		if partial && m.flow.lab != nil {
			validTxt += fmt.Sprintf(" of %d", m.flow.lab.ValidationRows)
		}
	}
	// The row's key is the iteration — the measured order — and the candidate's own id is printed
	// beside it only when the two differ, which is the case a draft in the ledger created: without
	// it "iteration 5" and "candidate #5" would be two different wordings on one screen.
	idTxt := fmt.Sprint(it.N)
	if it.Cand.ID != it.N {
		idTxt += "·#" + fmt.Sprint(it.Cand.ID)
	}
	prefix := mark + colID.Render(fitTo(idTxt, 10)) + " " +
		colText.Render(fitTo(trainTxt, colTrain)) + "  " +
		colText.Render(fitTo(validTxt, valCol)) + " " +
		colSplit.Render(fitTo(gapOf(&it.Train.Train, it.valid()), 6)) + " " +
		colSplit.Render(fitTo(fmt.Sprint(it.Cand.Chars), 6)) + "  "
	// The marks go last and the note takes what is left: a note that ate the row's evidence would
	// be the one thing on this line that could not be cut. The recommendation is a word in amber
	// and not a tick: the tick read as the rate's own pass mark, and the row it sits on is the
	// one row on the table that is a decision.
	marks := ""
	// "in file" is a quiet fact and the tag is the loud one, so the tag goes last: the right edge
	// of the row is where the eye lands when it is scanning for what to do.
	if it.Cand.InFile {
		marks += dimStyle.Render(" in file")
	}
	if best {
		marks += " " + tagRecommend.Render("Recommended")
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

// anyPartial reports whether some row in the table carries a rate over part of its split, so the
// legend for that cell is only drawn when a cell uses it.
func (m *Model) anyPartial() bool {
	if m.flow == nil {
		return false
	}
	for _, it := range m.flow.iters {
		if !it.Train.TrainFull {
			return true
		}
		if v := it.valid(); v != nil && !m.flow.complete(v) {
			return true
		}
	}
	return false
}

// bestIteration is the guide's rule, computed: the best validation rate wins, and a tie goes to the
// earlier wording — the one that got there with less text and fewer revisions. It is the table's
// recommendation, and the row the cursor opens on.
//
// A rate over part of the validation set does not compete. Comparing 1/1 against 3/3 is comparing a
// batch that stopped against one that finished, and the stopped one always looks perfect.
func bestIteration(f *flowState) *iteration {
	var best *iteration
	for i := range f.iters {
		it := &f.iters[i]
		if !f.complete(it.valid()) {
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
	if f.lab != nil {
		d.fact("validation", fmt.Sprintf("%s held back, never read", pluralOf(f.lab.ValidationRows, "query", "queries")))
	}
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

// updateRanking is the ranking's keys: pick a row, run the rates it is missing, read why a wording
// failed, apply the pick, or run the fresh queries once one is in.
func (m *Model) updateRanking(msg tea.KeyMsg) tea.Cmd {
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
	case "enter", "a":
		// The selection is the write: choose the wording, confirm, and SKILL.md carries it.
		if len(f.iters) > 0 {
			m.askApply(f.iters[f.res])
		}
	case "f":
		if len(f.iters) > 0 {
			return m.openIterationDoc(f.iters[f.res])
		}
	case "v":
		if !f.gateOpen() || f.lab == nil || f.lab.ValidationRows == 0 {
			return nil
		}
		return m.startGeneralization()
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
		if !m.flow.complete(v) && m.flow.lab != nil {
			validTxt += fmt.Sprintf(" over %d of %d queries — a stopped batch", v.Judged, m.flow.lab.ValidationRows)
		}
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
	if !it.Train.TrainFull && m.flow.lab != nil {
		d.fact("partial", fmt.Sprintf("the train batch stopped — %d of %d train rows were judged",
			it.Train.Train.Judged, m.flow.lab.TrainRows))
	}
	if valid != nil {
		d.fact("validation", fmt.Sprintf("%s (%d of %d)", pct(valid.Passed, valid.Judged), valid.Passed, valid.Judged))
		if !m.flow.complete(valid) && m.flow.lab != nil {
			d.fact("partial", fmt.Sprintf("the batch stopped — %d of %d validation queries were judged; v runs it again",
				valid.Judged, m.flow.lab.ValidationRows))
		}
	} else {
		d.fact("validation", "not measured — v runs it")
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
			d.fact("grade", verdictOf(row))
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
