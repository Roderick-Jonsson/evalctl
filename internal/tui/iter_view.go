package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The iteration workbench.
//
// Iteration is one activity with four things in it, and three of them are not steps: the set is what
// you edit, the run is an action, the result is what the run returned, and the revision is an edit.
// Splitting them into rungs made a screen you walk through to do one thing, and hid the set behind a
// step you had already finished. So they are one screen:
//
//	TRAIN SET (left)              the queries, edited in place
//	THE WORDING (right)           the candidate under test, and whether SKILL.md holds it
//	THE LAST RUN (right)          what the train set said about that wording
//	FAILURES (right)              the rows that failed, and which way the wording is wrong
//
// One key does the loop's next thing — run while the wording is unmeasured, revise once it has been
// measured — and `R` runs again on purpose. Everything else on the screen is a fact you need in order
// to decide what to type into the description.

func (m *Model) iterBody(h int) []string {
	// The set needs a readable measure; below that it takes the whole window and the pane's facts
	// move under the table, because a query table squeezed into half a screen is a table you scan.
	listW := min(74, max(40, m.width*55/100))
	paneW := min(96, m.width-listW-3)
	if paneW < 40 {
		return m.iterStacked(h)
	}
	left := m.iterSet(listW, h)
	right := m.iterPane(paneW, h)
	return m.splitBody(h, listW, paneW, left, right)
}

// iterSet is the train set: the same table the set step used, cut to the column this screen has.
func (m *Model) iterSet(w, h int) []string {
	f := m.flow
	const idW, labelW = 4, 5
	withWhy := w >= 64
	whyW := 0
	if withWhy {
		whyW = 18
	}
	queryW := max(16, w-2-3-idW-2-labelW-2-whyW)
	head := "  " + dimStyle.Render(padCell("ID", idW, true)) + "   " +
		dimStyle.Render(padCell("LABEL", labelW, false)) + "  " +
		dimStyle.Render(padCell("QUERY", queryW, false))
	if withWhy {
		head += "  " + dimStyle.Render("WHY")
	}
	lines := []string{"", "  " + dimStyle.Render("TRAIN SET") + "  " +
		dimStyle.Render(pluralOf(len(f.rows), "query", "queries")), head}

	// The facts under the table: the split's balance, then the one warning worth making. Both wrap,
	// because a count line cut in half is a count you cannot read.
	var facts []string
	for _, line := range wrapPlain(m.setFacts(true), max(20, w-4)) {
		facts = append(facts, "  "+dimStyle.Render(line))
	}
	if warn := m.setWarning(true); warn != "" {
		for _, line := range wrapPlain(warn, max(20, w-8)) {
			facts = append(facts, "  "+badgeWarn.Render("!")+" "+dimStyle.Render(line))
		}
	}
	room := max(1, h-len(lines)-len(facts))
	f.clampSet(room)
	for i := f.top; i < len(f.rows) && i < f.top+room; i++ {
		lines = append(lines, m.iterSetRow(i, idW, labelW, queryW, whyW))
	}
	for len(lines) < 2+room {
		lines = append(lines, "")
	}
	return fitRows(append(lines, facts...), h)
}

func (m *Model) iterSetRow(i, idW, labelW, queryW, whyW int) string {
	f := m.flow
	e := f.rows[i]
	mark := "  "
	if i == f.sel {
		mark = cursorMark.Render(glyphs.Cursor)
	}
	label := colYes.Render(padCell("yes", labelW, false))
	if e.Label == "no" {
		label = colNo.Render(padCell("no", labelW, false))
	}
	line := mark + colID.Render(padCell(fmt.Sprint(e.ID), idW, true)) + "   " + label + "  " +
		colText.Render(padCell(oneLine(e.Text), queryW, false))
	if whyW > 0 {
		why := e.Why
		if why == "" {
			why = "—"
		}
		line += "  " + colSplit.Render(padCell(oneLine(why), whyW, false))
	}
	if i == f.sel {
		line = selected(line)
	}
	return line
}

// iterPane is the other three: the wording under test, what the last run said about it, and the rows
// that failed. The action line is what `enter` does right now, and it says which one it is.
func (m *Model) iterPane(w, h int) []string {
	f := m.flow
	d := &docWriter{w: w, label: 11}
	var blocks [][]string
	from := len(d.lines)
	block := func() {
		blocks = append(blocks, append([]string(nil), d.lines[from:]...))
		from = len(d.lines)
	}

	pend, it := f.pending(), f.latest()
	cand := pend
	if cand == nil && it != nil {
		cand = &it.Cand
	}
	d.title("THE WORDING")
	if cand == nil {
		d.lead("no candidate yet — the loop adopts SKILL.md's wording on the first open")
	} else {
		head := fmt.Sprintf("#%d · %s", cand.ID, plural(len([]rune(strings.TrimSpace(cand.Text))), "character"))
		if cand.InFile {
			head += " · in SKILL.md"
		}
		d.lead(head)
	}
	d.rule()
	if cand != nil {
		for _, line := range wrapPlain(oneLine(cand.Text), max(20, d.body())) {
			d.lines = append(d.lines, "  "+line)
		}
	}
	block()

	d.card("THE LAST RUN")
	switch {
	case pend != nil:
		d.fact("candidate", fmt.Sprintf("#%d has not been run yet", pend.ID))
	case it != nil:
		d.fact("iteration", fmt.Sprintf("%d · trigger-%d · %s", it.N, it.Train.Iteration, shortWhen(it.Train.Started)))
		d.fact("train", fmt.Sprintf("%d of %d pass (%s)", it.Train.Train.Passed, it.Train.Train.Judged,
			pct(it.Train.Train.Passed, it.Train.Train.Judged)))
		if len(it.Train.Fixed) > 0 || len(it.Train.Broke) > 0 {
			d.fact("against parent", fmt.Sprintf("fixed %s · broke %s", idListOf(it.Train.Fixed), idListOf(it.Train.Broke)))
		}
	default:
		d.fact("run", "nothing measured yet")
	}
	block()

	// Failures are the screen's payload: the reason the next revision exists.
	if it != nil && pend == nil {
		fails := it.Cand.FailedTrain()
		d.card("FAILURES")
		if len(fails) == 0 {
			d.fact("none", "this wording passes the train set")
		}
		for i, row := range fails {
			if i == 3 {
				d.fact("more", fmt.Sprintf("%s — f opens them all", plural(len(fails)-3, "row")))
				break
			}
			d.fact(fmt.Sprintf("q%d", row.ID), fmt.Sprintf("%s · %s", labelWord(row.ShouldTrigger), verdictOf(row)))
			if q := strings.TrimSpace(row.Query); q != "" {
				d.note(clipText(q, max(20, d.body()-6)))
			}
		}
		block()
	}

	// The action: run while the wording is unmeasured, revise once it has been measured.
	actDoc := &docWriter{w: w}
	if pend != nil || it == nil {
		actDoc.action("enter", fmt.Sprintf("run the train set — %d queries × %d runs = %d calls",
			len(f.rows), f.lab.Runs, len(f.rows)*f.lab.Runs))
	} else {
		actDoc.action("enter", fmt.Sprintf("revise #%d into #%d", it.Cand.ID, it.Cand.ID+1))
	}
	room := h - len(actDoc.lines)
	var out []string
	for _, b := range blocks {
		if len(out)+len(b) > room {
			break
		}
		out = append(out, b...)
	}
	return fitRows(append(out, actDoc.lines...), h)
}

// iterStacked is the workbench without a pane: the set, then the facts that matter underneath it.
func (m *Model) iterStacked(h int) []string {
	f := m.flow
	lines := m.iterSet(m.width, h-5)
	pend, it := f.pending(), f.latest()
	lines = append(lines, "")
	switch {
	case pend != nil || it == nil:
		lines = append(lines, "  "+dimStyle.Render("nothing measured yet — enter runs the train set"))
	case len(it.Cand.FailedTrain()) == 0:
		lines = append(lines, "  "+styleOn.Render(glyphs.Done+" train "+pct(it.Train.Train.Passed, it.Train.Train.Judged)+" — nothing failing"))
	default:
		lines = append(lines, "  "+dimStyle.Render(fmt.Sprintf("train %s · %s — enter revises #%d",
			pct(it.Train.Train.Passed, it.Train.Train.Judged), idList(it.Cand.FailedTrain()), it.Cand.ID)))
	}
	return fitRows(lines, h)
}

func (m *Model) updateIter(msg tea.KeyMsg) tea.Cmd {
	f := m.flow
	switch msg.String() {
	case "j", "down":
		f.sel = min(f.sel+1, max(0, len(f.rows)-1))
	case "k", "up":
		f.sel = max(0, f.sel-1)
	case "g", "home":
		f.sel = 0
	case "G", "end":
		f.sel = max(0, len(f.rows)-1)
	case "n":
		m.openForm(nil)
	case "e":
		if len(f.rows) > 0 {
			row := f.rows[f.sel]
			m.openForm(&row)
		}
	case " ", "space":
		if len(f.rows) > 0 {
			return m.toggleLabel()
		}
	case "d":
		if len(f.rows) > 0 {
			m.askDeleteRow()
		}
	case "R":
		return m.openRunAsk()
	case "f":
		if it := f.latest(); it != nil && f.pending() == nil && len(it.Cand.FailedTrain()) > 0 {
			return m.openFailures(it)
		}
	case "enter":
		// The one key that moves the loop: run the unmeasured wording, revise the measured one.
		if f.pending() != nil || f.latest() == nil {
			return m.openRunAsk()
		}
		return m.openDraft()
	}
	return nil
}
