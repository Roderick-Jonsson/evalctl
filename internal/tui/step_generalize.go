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
// The ranking is one table: every measured wording with its train rate, its validation rate and the
// gap between them, the rows that are missing a rate still drawn with a dash, the recommended row
// tagged, and the tail saying what the table still owes. It is a document with a cursor rather than a
// screen with a rail, and the keys that act on it — apply, validate, the record of one iteration, the
// holdout — are in its band. The workbench's ITERATIONS block draws the same two rates in its own
// columns, so the numbers are readable without leaving the wording they are about.

const rankingName = "RANKING"

// valCol is the validation column's width: "1/1 100% of 3" is the widest cell the column has to
// hold, and a partial rate is worth two extra cells — it is the difference between a rank and a
// rounding of one. colTrain is the same width for the same reason.
const (
	valCol   = 16
	colTrain = 16
)

var rankingHints = []hint{
	{"j k", "move"}, {"enter a", "apply"}, {"f", "in full"},
	{"v", "validate"}, {"h", "holdout"}, {"esc", "back"},
}

// openRanking is the generalization half's reading. It is where `v` goes when there is nothing left
// to validate: the key used to answer with a status line, so a loop with every rate measured had no
// screen that showed the rates — which reads as a blank result, not as a finished job.
func (m *Model) openRanking() tea.Cmd {
	f := m.flow
	if f == nil || f.lab == nil {
		return nil
	}
	doc := &docState{scope: "ranking", title: "ranking · ranked by the validation set", act: m.rankingKey}
	switch {
	case f.lab.ValidationRows == 0:
		doc.title, doc.lines = "the validation set is empty", m.emptyValidationBody(m.bodyHeight())
		doc.hints = []hint{{"1", "the validation set"}, {"esc", "back"}}
	case !f.gateOpen():
		doc.title, doc.lines = "validation is locked until the loop has run", m.lockedBody(m.bodyHeight())
		doc.hints = []hint{{"ctrl+g", "validate anyway"}, {"esc", "back"}}
	case len(f.iters) == 0:
		doc.title = "nothing measured yet"
		doc.lines = []string{
			"",
			"  " + dimStyle.Render("The ranking is the loop's own history: a wording has to be written"),
			"  " + dimStyle.Render("and run on the train set before there is anything to rank."),
			"",
			"  " + dimStyle.Render("Iteration is the other half — a wording, a run, a revision from what failed."),
		}
		doc.hints = []hint{{"esc", "back"}}
	default:
		// A finished validation batch lands here, so the recommendation is where the reader was
		// already looking: the point of the table is that the pick is made, not that it is offered.
		f.res = f.recommended()
		doc.lines = m.rankingLines()
		doc.hints = rankingHints
		doc.table = true
	}
	m.doc = doc
	m.backTo = screenFlow
	m.mode = screenDoc
	return nil
}

// rankingLines is the table at the cursor's position. It is rebuilt whenever the cursor moves: the
// rows are the reading, and a document whose selected row never changes is a list with a lie on it.
func (m *Model) rankingLines() []string {
	f := m.flow
	w := max(40, m.width-2)
	head := "  " + dimStyle.Render(fitTo("ITERATION", 10)) + " " + dimStyle.Render(fitTo("TRAIN", colTrain)) + "  " +
		dimStyle.Render(fitTo("VALIDATION", valCol)) + " " + dimStyle.Render(fitTo("GAP", 6)) + " " +
		dimStyle.Render(fitTo("CHARS", 6)) + "  " + dimStyle.Render("NOTE")
	rule := "  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, w-2))))
	lines := []string{"", head, rule}
	best := bestIteration(f)
	f.res = min(max(f.res, 0), max(0, len(f.iters)-1))
	for i := range f.iters {
		lines = append(lines, m.rankingRow(f.iters[i], w, i == f.res, best != nil && best.Cand.ID == f.iters[i].Cand.ID))
	}
	lines = append(lines, rule)
	return append(lines, m.rankingTail(w)...)
}

// rankingRow is one wording: the two rates it was measured at, what it cost in characters, and why it
// was written. The marks go last and the note takes what is left — a note that ate the row's
// evidence would be the one thing on this line that could not be cut.
func (m *Model) rankingRow(it iteration, w int, focused, best bool) string {
	mark := "  "
	if focused {
		mark = cursorMark.Render(glyphs.Cursor)
	}
	trainTxt := fmt.Sprintf("%d/%d %s", it.Train.Train.Passed, it.Train.Train.Judged,
		pct(it.Train.Train.Passed, it.Train.Train.Judged))
	// A run whose later queries died judges fewer rows than the set holds, and 1/1 100% beside
	// 4/4 100% would read as the better wording.
	if !it.Train.TrainFull && m.flow.lab != nil {
		trainTxt += fmt.Sprintf(" of %d", m.flow.lab.TrainRows)
	}
	// A wording the validation set has not seen is drawn and not skipped: the dash is the fact this
	// screen exists to show, and a row that vanished until it was measured would hide how much of
	// the table is still unranked. A rate over part of the set is drawn as a rate and marked, because
	// it is the one number on this screen that can look better than it is.
	validTxt := "—"
	if v := it.valid(); v != nil {
		validTxt = fmt.Sprintf("%d/%d %s", v.Passed, v.Judged, pct(v.Passed, v.Judged))
		if !m.flow.complete(v) && m.flow.lab != nil {
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
	// "in file" is a quiet fact and the recommendation is the loud one, so the recommendation goes
	// last: the right edge of the row is where the eye lands when it is scanning for what to do. The
	// recommendation is a word in amber and not a tick — a tick reads as the rate's own pass mark, and
	// the row it sits on is the one row on the table that is a decision.
	marks := ""
	if it.Cand.InFile {
		marks += dimStyle.Render(" in file")
	}
	if best {
		marks += " " + tagRecommend.Render("Recommended")
	}
	room := w - lipgloss.Width(prefix) - lipgloss.Width(marks) - 1
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

// rankingTail is what the table still owes, said under it: the batch that would fill the dashes and
// what it costs, a tie the set cannot break, the draft the ledger holds that is not a row here, and
// the legend for a partial cell — each only when it applies.
func (m *Model) rankingTail(w int) []string {
	f := m.flow
	room := max(20, w-2)
	var out []string
	say := func(text string) {
		out = append(out, "  "+dimStyle.Render(ellipsis(text, room)))
	}
	if pending := m.needsValidation(); pending > 0 {
		cost := f.lab.ValidationRows * f.lab.Runs * pending
		out = append(out, "  "+cursorMark.Render(glyphs.Arrow)+" "+titleStyle.Render("v")+"  "+
			ellipsis(fmt.Sprintf("validate %s · %d queries × %d runs = %d agent calls",
				plural(pending, "iteration"), f.lab.ValidationRows, f.lab.Runs, cost), max(20, room-6)))
	}
	if rate, n, tied := tieOf(f); tied {
		// The fact and its consequence on two lines: a tie sentence long enough to be true is longer
		// than the pane, and the ellipsis would eat the one number a tie produces — the pick.
		say(fmt.Sprintf("all %d validated wordings tie at %d%% of %d — the validation set does not separate them",
			n, rate, f.lab.ValidationRows))
		if best := bestIteration(f); best != nil {
			say(fmt.Sprintf("a tie goes to the earliest wording: #%d, %d chars", best.Cand.ID, best.Cand.Chars))
		}
	} else if best := bestIteration(f); best != nil {
		if v := best.valid(); v != nil {
			say(fmt.Sprintf("the pick is #%d — validation %d of %d, train %d of %d",
				best.Cand.ID, v.Passed, v.Judged, best.Train.Train.Passed, best.Train.Train.Judged))
		}
	}
	if draft, live := f.draftOf(); draft != nil {
		word := "the workbench's run measures it"
		if !live {
			word = "a later revision stepped over it"
		}
		say(fmt.Sprintf("#%d is in the ledger but not this table — %s", draft.ID, word))
	}
	if m.anyPartial() {
		say("of N — judged of the split; a stopped batch never ranks")
	}
	return out
}

// tieOf reports the best validation rate and how many wordings hold it, when more than one does.
// A table where every row carries the same rate ranks nothing, and the recommendation then falls to
// the earliest wording — a fact the reader must be told, not left to infer from a tag on row one.
func tieOf(f *flowState) (rate, n int, tied bool) {
	seen := 0
	for _, it := range f.iters {
		v := it.valid()
		if !f.complete(v) {
			continue
		}
		seen++
		r := pctInt(v.Passed, v.Judged)
		switch {
		case seen == 1 || r > rate:
			rate, n = r, 1
		case r == rate:
			n++
		}
	}
	return rate, n, n > 1
}

// rankingKey is the ranking document's own keys. A reader that offers `a` and does nothing with it
// is the same lie as a help page describing a screen that was deleted.
func (m *Model) rankingKey(key string) tea.Cmd {
	f := m.flow
	if f == nil {
		return nil
	}
	rows := len(f.iters)
	switch key {
	case "j", "down":
		f.res = min(f.res+1, max(0, rows-1))
	case "k", "up":
		f.res = max(0, f.res-1)
	case "g", "home":
		f.res = 0
	case "G", "end":
		f.res = max(0, rows-1)
	case "ctrl+d", " ":
		m.doc.top = min(m.doc.top+max(1, m.doc.view/2), max(0, len(m.doc.lines)-1))
	case "ctrl+u":
		m.doc.top = max(0, m.doc.top-max(1, m.doc.view/2))
	case "enter", "a":
		if rows == 0 {
			return nil
		}
		m.askApply(f.iters[min(f.res, rows-1)])
	case "f":
		if rows == 0 {
			return nil
		}
		return m.openIterationRecord(f.iters[min(f.res, rows-1)])
	case "v":
		if len(m.unvalidatedIDs()) == 0 {
			m.setStatus("every measured wording has a full validation rate — there is nothing to run", false)
			return nil
		}
		return m.startValidationBatch()
	case "h":
		return m.openHoldout()
	case "1":
		f.sec, f.step = secGeneralization, 1
		f.enterStep(m)
		m.doc, m.mode = nil, screenFlow
		return nil
	case "ctrl+g":
		m.forceGeneralization()
		return m.openRanking()
	}
	// Every other key redraws the table where the cursor stands: a row mark that does not follow the
	// keys is worse than no mark at all.
	if m.doc != nil && m.doc.table {
		m.doc.lines = m.rankingLines()
	}
	return nil
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
	d.lead("Five measured iterations is the guide's bar: below that the validation rate is a coin, and the difference between two iterations is noise. The gate exists so that a wording does not get picked because it was the third one tried.")
	d.rule()
	d.fact("measured", fmt.Sprintf("%d of %d iterations", len(f.iters), iterationsRequired))
	d.fact("train", trainSummary(f))
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

// openIterationRecord is one wording in full: what it was measured at on both sets, what the revision
// before it fixed and broke, and every train row it failed, with what loaded instead. It is where the
// ranking's one-line row and the workbench's failure pane both point.
func (m *Model) openIterationRecord(it iteration) tea.Cmd {
	d := &docWriter{w: max(28, m.width-6)}
	valid := it.valid()
	// The reader's head carries the iteration and the candidate; the body opens with what the two
	// rates mean, so the title is not said twice.
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
	fails := it.Cand.FailedTrain()
	if len(fails) == 0 {
		d.card("the train set")
		d.para("nothing failed — every train row passed this wording.")
	}
	for _, row := range fails {
		d.card(fmt.Sprintf("q%d · %s · fired %d of %d", row.ID, labelWord(row.ShouldTrigger), row.Triggers, row.Runs))
		d.fact("the ask", clipText(row.Query, max(40, m.width-20)))
		if row.Why != "" {
			d.fact("why it is here", clipText(row.Why, max(40, m.width-20)))
		}
		d.fact("what loaded", loadedInstead(row))
		d.fact("grade", verdictOf(row))
	}
	d.action("esc", "back")
	doc := &docState{scope: "iteration", title: fmt.Sprintf("iteration %d · candidate #%d", it.N, it.Cand.ID),
		lines: d.lines}
	// One level back to where the reader was standing: a record opened from the table returns to the
	// table, with the cursor still on the row it describes.
	if m.mode == screenDoc && m.doc != nil && m.doc.scope == "ranking" {
		doc.back = m.doc
	}
	m.doc = doc
	m.backTo = screenFlow
	m.mode = screenDoc
	return nil
}

// askApply is the write into SKILL.md, confirmed: it is the one action in the loop that changes what
// ships, and the ledger records it beside the measurement that earned it.
func (m *Model) askApply(it iteration) {
	validTxt := "not validated"
	if v := it.valid(); v != nil {
		validTxt = pct(v.Passed, v.Judged)
		if !m.flow.complete(v) && m.flow.lab != nil {
			validTxt += fmt.Sprintf(" over %d of %d — a stopped batch", v.Judged, m.flow.lab.ValidationRows)
		}
	}
	title := fmt.Sprintf("apply #%d to %s/SKILL.md?  train %s · validation %s — the file is backed up first",
		it.Cand.ID, m.skill, pct(it.Train.Train.Passed, it.Train.Train.Judged), validTxt)
	m.askNow(title, "apply it", func(m *Model) tea.Cmd {
		msg, err := runner.New(m.store).ApplyDescription(m.skill, it.Cand.ID)
		if err != nil {
			m.setStatus(err.Error(), true)
			return nil
		}
		m.reload()
		if m.mode == screenDoc {
			// The table the reader is standing on just changed under them: the applied row is "in
			// file" now, and a document that still says otherwise is reading a file that moved.
			if m.doc != nil && m.doc.scope == "ranking" {
				m.doc.lines = m.rankingLines()
			}
		}
		m.setStatus(msg+" — h runs the fresh queries once", false)
		return nil
	})
}

// openHoldout is the guide's last move: 5–10 fresh queries, measured once, reported apart from the
// rates the loop tuned. They are not a second validation set.
func (m *Model) openHoldout() tea.Cmd {
	f := m.flow
	if f == nil || f.lab == nil {
		return nil
	}
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
