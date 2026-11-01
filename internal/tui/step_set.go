package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"evalctl/internal/model"
	"evalctl/internal/store"
)

// Steps 1 and 2: the query set.
//
// The two splits are the same screen with different words, because they are the same list with a
// different job — the train rows are what a revision may learn from, the validation rows are what
// judges it. Editing happens here and nowhere else: the guided flow is the whole interface, so a
// query is added, relabelled and deleted inside the step that owns it.
//
// The screen also carries the rule the guide states and a tool should not leave to memory: both
// labels in both splits, and roughly 60/40 between them. It is a warning line, not a blocker — a
// set of nine positives and one negative is a bad set, not an invalid one.

func (m *Model) setBody(h int) []string {
	f := m.flow
	train := f.sec == secIteration
	title := "train set — the queries the wording may learn from"
	if !train {
		title = "validation set — the queries that judge the wording, never shape it"
	}
	lines := []string{
		"",
		"  " + titleStyle.Render(title),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, m.width-4)))),
	}
	// The facts block is bottom-anchored: three rows of it are the screen's standing advice, and
	// they must not scroll away with the table.
	const facts = 3
	room := max(1, h-2-facts)
	if len(f.rows) == 0 {
		lines = append(lines, "",
			"  "+dimStyle.Render("no rows in this split yet"),
			"", "  "+titleStyle.Render("n")+"  "+dimStyle.Render("add the first query"))
		return fitRows(lines, h)
	}
	f.clampSet(room)
	lines = append(lines, queryHeader(queryCols(m.width), true))
	for i := f.top; i < len(f.rows) && len(lines) < 2+room; i++ {
		lines = append(lines, queryRow(f.rows[i], queryCols(m.width), true, i == f.sel))
	}
	for len(lines) < 2+room {
		lines = append(lines, "")
	}
	lines = append(lines, "  "+dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, m.width-4)))))
	lines = append(lines, "  "+dimStyle.Render(m.setFacts(train)))
	if w := m.setWarning(); w != "" {
		lines = append(lines, "  "+badgeWarn.Render("!")+" "+dimStyle.Render(w))
	} else {
		lines = append(lines, "")
	}
	return fitRows(lines, h)
}

// queryCols is the width each column takes in a table of w cells: the id and the label are fixed,
// the query takes what is left, and the why gets a third of the space until the table is too narrow
// to carry it at all — a why squeezed to four cells is a column of ellipses, so it goes instead.
//
// Every table that lists queries asks this one function, so the pane on the section screen and the
// step screen's own set cannot drift apart in their columns.
type queryColumns struct{ id, trig, query, why int }

func queryCols(w int) queryColumns {
	const id, trig = 4, 7
	rest := w - 2 - id - 2 - trig - 2 // the cursor's two cells and the gaps between columns
	why := min(30, max(12, rest/3))
	if query := max(12, rest-2-why); query >= 20 {
		return queryColumns{id: id, trig: trig, query: query, why: why}
	}
	return queryColumns{id: id, trig: trig, query: max(12, rest)}
}

func queryHeader(c queryColumns, focused bool) string {
	style := dimStyle
	if focused {
		style = headerFocus
	}
	line := "  " + style.Render(fitTo("ID", c.id)) + "  " + style.Render(fitTo("TRIGGER", c.trig)) +
		"  " + style.Render(fitTo("QUERY", c.query))
	if c.why > 0 {
		line += "  " + style.Render("WHY")
	}
	return line
}

// queryRow is one query: the id, whether it should trigger, the text, and the reason it is in the
// set — which is the field the ladder reads back to you when the row fails.
func queryRow(e store.Entry, c queryColumns, cursor, sel bool) string {
	mark := "  "
	if cursor {
		mark = cursorMark.Render(glyphs.Cursor)
	}
	label := colYes.Render(fitTo("yes", c.trig))
	if e.Label == "no" {
		label = colNo.Render(fitTo("no", c.trig))
	}
	line := mark + colID.Render(fitTo(fmt.Sprint(e.ID), c.id)) + "  " + label + "  " +
		colText.Render(fitTo(oneLine(e.Text), c.query))
	if c.why > 0 {
		why := e.Why
		if why == "" {
			why = "—"
		}
		line += "  " + colSplit.Render(fitTo(oneLine(why), c.why))
	}
	if sel {
		line = selected(line)
	}
	return line
}

// setFacts is the count line: what the split holds and how it is balanced, with the guide's own
// ratio beside the real one so the comparison needs no memory.
func (m *Model) setFacts(train bool) string {
	if m.flow == nil {
		return ""
	}
	return m.splitFacts(m.flow.rows, train)
}

// splitFacts is that line for any list of rows: the step screens pass their own split, the section
// screen's pane passes the split the cursor is standing on, and both get the same arithmetic.
func (m *Model) splitFacts(rows []store.Entry, train bool) string {
	yes, no := 0, 0
	for _, e := range rows {
		if e.Label == "yes" {
			yes++
		} else {
			no++
		}
	}
	parts := []string{
		plural(len(rows), "row"),
		fmt.Sprintf("%d should trigger", yes),
		fmt.Sprintf("%d should not", no),
	}
	if m.flow != nil && m.flow.lab != nil {
		if total := m.flow.lab.TrainRows + m.flow.lab.ValidationRows; total > 0 {
			share := m.flow.lab.ValidationRows
			if train {
				share = m.flow.lab.TrainRows
			}
			parts = append(parts, fmt.Sprintf("%d%% of the set (the guide asks ~60/40)",
				(share*100+total/2)/total))
		}
	}
	return strings.Join(parts, " · ")
}

// setWarning is the one thing about this split worth saying without being asked: a label the split
// does not carry, a duplicate, or a row with no reason on it.
func (m *Model) setWarning() string {
	if m.flow == nil {
		return ""
	}
	return m.splitWarning(m.flow.rows)
}

func (m *Model) splitWarning(rows []store.Entry) string {
	if len(rows) == 0 {
		return ""
	}
	yes, no := 0, 0
	noWhy, dupes := 0, 0
	seen := map[string]bool{}
	for _, e := range rows {
		if e.Label == "yes" {
			yes++
		} else {
			no++
		}
		if strings.TrimSpace(e.Why) == "" {
			noWhy++
		}
		t := strings.ToLower(oneLine(e.Text))
		if seen[t] {
			dupes++
		}
		seen[t] = true
	}
	var out []string
	if yes == 0 || no == 0 {
		out = append(out, fmt.Sprintf("this split carries only one label — a revision cannot learn from it"))
	}
	if noWhy > 0 {
		out = append(out, fmt.Sprintf("%s without a why — the ladder reads that line when the row fails", plural(noWhy, "row")))
	}
	if dupes > 0 {
		out = append(out, fmt.Sprintf("%s read the same as another row", plural(dupes, "row")))
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, " · ")
}

// clampWindow keeps a table's cursor inside its rows and its rows inside the window. Two screens now
// scroll a table of queries, and a scroll position that works on one and not the other is a bug.
func clampWindow(sel, top *int, n, room int) {
	if *sel < *top {
		*top = *sel
	}
	if *sel >= *top+room {
		*top = *sel - room + 1
	}
	if *top < 0 {
		*top = 0
	}
	if *sel >= n {
		*sel = max(0, n-1)
	}
}

func (f *flowState) clampSet(room int) { clampWindow(&f.sel, &f.top, len(f.rows), room) }

func (m *Model) updateSet(msg tea.KeyMsg) tea.Cmd {
	f := m.flow
	switch msg.String() {
	case "j", "down":
		f.sel = min(f.sel+1, max(0, len(f.rows)-1))
		m.reload()
	case "k", "up":
		f.sel = max(0, f.sel-1)
		m.reload()
	case "g", "home":
		f.sel = 0
	case "G", "end":
		f.sel = max(0, len(f.rows)-1)
	case "enter":
		return m.leaveSet()
	case "n":
		m.openForm(nil)
	case "e":
		if len(f.rows) == 0 {
			return nil
		}
		e := f.rows[f.sel]
		m.openForm(&e)
	case " ", "space":
		if len(f.rows) == 0 {
			return nil
		}
		return m.toggleLabel()
	case "d":
		if len(f.rows) == 0 {
			return nil
		}
		m.askDeleteRow()
	}
	return nil
}

// leaveSet is the forward move inside a section: a set with no rows measures nothing, so the step
// after it is refused rather than opened onto an empty run.
func (m *Model) leaveSet() tea.Cmd {
	f := m.flow
	if len(f.rows) == 0 {
		m.setStatus("this split is empty — add a query with n", true)
		return nil
	}
	return m.jumpStep(2)
}

// toggleLabel flips the label of the row under the step screen's cursor.
func (m *Model) toggleLabel() tea.Cmd {
	f := m.flow
	if f == nil || len(f.rows) == 0 {
		return nil
	}
	return m.toggleRow(f.rows[f.sel])
}

// toggleRow flips a row's label and writes the file at once: the set is what the next run reads from
// disk, so a change that only lives in memory would measure something nobody wrote. Both tables of
// queries — the step screen's and the section screen's pane — come through here.
func (m *Model) toggleRow(e store.Entry) tea.Cmd {
	qs := m.store.Queries()
	if e.Index < 0 || e.Index >= len(qs) {
		return nil
	}
	q := qs[e.Index]
	q.ShouldTrigger = !q.ShouldTrigger
	if err := m.store.UpdateTrigger(e.Index, q); err != nil {
		m.setStatus(err.Error(), true)
		return nil
	}
	if err := m.store.SaveTrigger(); err != nil {
		m.setStatus(err.Error(), true)
		return nil
	}
	label := "should not trigger"
	if q.ShouldTrigger {
		label = "should trigger"
	}
	m.reload()
	m.setStatus(fmt.Sprintf("q%d is now %s", q.ID, label), false)
	return nil
}

// askDeleteRow confirms a delete from the step screen's own set.
func (m *Model) askDeleteRow() {
	f := m.flow
	if f == nil || len(f.rows) == 0 {
		return
	}
	m.askDeleteEntry(f.rows[f.sel], f.sectionName(), screenFlow, func(m *Model) {
		f.sel = min(f.sel, max(0, len(f.rows)-1))
	})
}

// askDeleteEntry confirms a delete in the same dialog every other destructive step uses. The set is
// the instrument the whole loop depends on, so a keystroke never removes a row silently. after runs
// once the file is written and re-read, for the caller that keeps a cursor of its own.
func (m *Model) askDeleteEntry(e store.Entry, scope string, back screen, after func(*Model)) {
	m.ask = &askState{
		scope: scope,
		back:  back,
		title: fmt.Sprintf("delete q%d?", e.ID),
		lines: []string{
			oneLine(e.Text),
			"",
			dimStyle.Render("the row leaves " + m.store.TriggerPathFor() + "; the file is backed up first"),
		},
		yes: "delete it",
		then: func(m *Model) tea.Cmd {
			if _, err := m.store.DeleteRows([]store.RowRef{{Lane: store.LaneTrigger, Skill: m.skill, Index: e.Index}}, false); err != nil {
				m.setStatus(err.Error(), true)
				return nil
			}
			if err := m.store.SaveTrigger(); err != nil {
				m.setStatus(err.Error(), true)
				return nil
			}
			m.reload()
			if after != nil {
				after(m)
			}
			m.setStatus(fmt.Sprintf("q%d deleted", e.ID), false)
			return nil
		},
	}
	m.mode = screenAsk
}

// ---------------------------------------------------------------- writing a row

// openForm opens the query form on a row of the step screen's split. A new row takes the next free id
// without asking: ids are the tool's business, not the operator's, and the guided flow has no second
// place to fix a collision.
func (m *Model) openForm(e *store.Entry) {
	split := store.SplitTrain
	if m.flow != nil && m.flow.split() == store.SplitValidation {
		split = store.SplitValidation
	}
	m.openFormIn(split, e, screenFlow)
}

// openFormIn is the same form for any split and any screen: the section screen's pane writes a row of
// the split it is showing and hands the keys back to itself.
func (m *Model) openFormIn(split string, e *store.Entry, back screen) {
	f := &formState{split: split, width: m.width, height: m.bodyHeight(), back: back}
	if e != nil {
		f.editing = true
		f.index = e.Index
		f.id = e.ID
		f.label = e.Label == "yes"
		f.query = e.Text
		f.why = e.Why
	} else {
		f.id = m.store.NextTriggerID()
		f.label = true
	}
	f.hint = "a query is a prompt a real user would type — the wording is what the router matches on"
	if f.editing {
		f.hint = "the why is read back to you when this row fails"
	}
	f.build()
	m.form = f
	m.mode = screenForm
}

// apply writes the draft through the store and re-reads the set, so the table behind the form is the
// file and not the draft.
func (f *formState) apply(m *Model) tea.Cmd {
	q := model.TriggerQuery{
		ID:            f.id,
		Skill:         m.skill,
		ShouldTrigger: f.label,
		Split:         f.split,
		Query:         strings.TrimSpace(f.query),
		Why:           strings.TrimSpace(f.why),
	}
	var err error
	if f.editing {
		qs := m.store.Queries()
		if f.index >= 0 && f.index < len(qs) {
			// The stored record keeps whatever keys this tool does not know about; the form only
			// owns the five it draws.
			merged := qs[f.index]
			merged.ShouldTrigger, merged.Split, merged.Query, merged.Why = q.ShouldTrigger, q.Split, q.Query, q.Why
			q = merged
		}
		err = m.store.UpdateTrigger(f.index, q)
	} else {
		err = m.store.AddTrigger(q)
	}
	if err != nil {
		f.errMsg = err.Error()
		return nil
	}
	if err = m.store.SaveTrigger(); err != nil {
		f.errMsg = err.Error()
		return nil
	}
	m.form, m.mode = nil, f.target()
	m.reload()
	verb := "added"
	if f.editing {
		verb = "saved"
	}
	m.setStatus(fmt.Sprintf("q%d %s — %s/%s", f.id, verb, f.split, labelWord(f.label)), false)
	return nil
}

func labelWord(yes bool) string {
	if yes {
		return "should trigger"
	}
	return "should not trigger"
}
