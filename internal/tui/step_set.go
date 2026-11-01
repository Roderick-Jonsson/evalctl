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

// setRegionName is the split in the register every region uses. The pane on the section screen, the
// workbench's right column and this step are all the same set, so they all call it the same thing —
// a name that changes shape between two screens is a name you have to translate.
func setRegionName(train bool) string {
	if train {
		return "TRAIN SET"
	}
	return "VALIDATION SET"
}

// setRegionWord is the same set in running text: the status row names the region the keys are in,
// and it names it in lower case because it is a sentence and not a heading.
func setRegionWord(train bool) string {
	if train {
		return "train set"
	}
	return "validation set"
}

// setRegionLead is the rule that set works under, one line under its name.
func setRegionLead(train bool) string {
	if train {
		return "the queries the wording may learn from"
	}
	return "the queries that judge the wording, never shape it"
}

func (m *Model) setBody(h int) []string {
	m.syncSetForm()
	f := m.flow
	train := f.sec == secIteration
	ruled := "  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, m.width-4))))
	// The set has the keys — the table above and the fields below are one region, so the head keeps
	// the block when the caret moves into the fields under it.
	lines := []string{
		"",
		regionHead(m.width, setRegionName(train), "", true),
		ruled,
	}
	// The rule closes the head and the sentence under it explains the set, the order the frame itself
	// uses for the bar, its rule and the line beneath: one pattern, every region.
	for _, line := range wrapPlain(setRegionLead(train), max(10, min(96, m.width-4))) {
		lines = append(lines, "  "+dimStyle.Render(line))
	}
	// The footer is bottom-anchored: the counts and the one warning are the screen's standing
	// advice, and they must not scroll away with the table.
	foot := []string{"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, m.width-4))))}
	for _, line := range wrapPlain(m.setFacts(train), max(20, m.width-6)) {
		foot = append(foot, "  "+dimStyle.Render(line))
	}
	if w := m.setWarning(); w != "" {
		foot = append(foot, "  "+badgeWarn.Render("!")+" "+dimStyle.Render(w))
	} else {
		foot = append(foot, "")
	}

	// The table is the upper half and the row's fields are the lower half, always: the row is edited
	// where it is listed. The fields get what they measure, the table keeps the rest.
	form := m.inlineForm()
	formH := 0
	if form != nil {
		formH = min(form.want()+1, max(9, h-len(lines)-len(foot)-3))
	}
	rule := "  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, m.width-4))))
	// One of the rows below the table is that rule.
	room := max(1, h-len(lines)-len(foot)-formH-boolInt(formH > 0))
	if len(f.rows) == 0 {
		lines = append(lines, "",
			"  "+dimStyle.Render("no rows in this split yet"),
			"", "  "+titleStyle.Render("n")+"  "+dimStyle.Render("add the first query"))
		// The first row of an empty split is written in the same fields, `n` puts the caret in them.
		if form != nil {
			lines = append(lines, rule)
			lines = append(lines, form.body(m.width-2, formH)...)
		}
		return fitRows(append(lines, foot...), h)
	}
	f.clampSet(room)
	lines = append(lines, queryHeader(queryCols(m.width)))
	for i := f.top; i < len(f.rows) && len(lines) < 2+room; i++ {
		on := !m.formActive() && i == f.sel
		lines = append(lines, queryRow(f.rows[i], queryCols(m.width), on, on))
	}
	// The table keeps its height so the fields do not jump when a short set is edited — but it never
	// takes a row the fields or the footer need.
	for len(lines) < 2+room && len(lines)+formH+boolInt(formH > 0)+len(foot) <= h {
		lines = append(lines, "")
	}
	if form != nil {
		lines = append(lines, rule)
		lines = append(lines, form.body(m.width-2, formH)...)
	}
	lines = append(lines, foot...)
	return fitRows(lines, h)
}

// clipLines keeps at most n lines: a wrapped paragraph in a pane is a paragraph, not a screen.
func clipLines(lines []string, n int) []string {
	if len(lines) > n {
		return lines[:n]
	}
	return lines
}

// labelCell is a row's label as one coloured word, for the places that print it beside an id.
func labelCell(label string) string {
	if label == "yes" {
		return colYes.Render("should trigger")
	}
	return colNo.Render("should not")
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

// queryHeader is the table's column head. It is quiet whether or not the table has the keys: the
// region's name above it already says where the keystrokes are, and a second accented thing on the
// same table is a second cursor.
func queryHeader(c queryColumns) string {
	style := dimStyle
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
		m.syncSetForm()
	case "G", "end":
		f.sel = max(0, len(f.rows)-1)
		m.syncSetForm()
	case "enter":
		return m.leaveSet()
	case "n":
		m.openForm(nil)
	case "e", "tab":
		return m.focusForm()
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
	// The fields under the table are seeded from the file: a write the file just took re-seeds them,
	// so nothing on screen still says what the row used to be.
	m.form = nil
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

// setPane is the set as one column: its title, the headings and the rows with the last run's verdict
// beside each. It is what the workbench puts in its right-hand column: the set as the run left it,
// reported and not edited — the rows are written on the step screen that owns them, and beside the
// two sections, so there is one editor and it is not this one.
func (m *Model) setPane(w, h int) []string {
	f := m.flow
	const idW, trigW, resW = 4, 7, 6
	queryW := max(12, w-2-3-idW-2-trigW-1-resW-2)
	head := "  " + dimStyle.Render(padCell("ID", idW, true)) + "   " +
		dimStyle.Render(padCell("TRIGGER", trigW, false)) + " " +
		dimStyle.Render(padCell("RESULT", resW, false)) + "  " +
		dimStyle.Render(padCell("QUERY", queryW, false))
	lines := []string{"", regionHead(w, setRegionName(true), pluralOf(len(f.rows), "query", "queries"),
		f.focus == focusSet),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, w-4))), head}

	facts := wrapPlain(m.setFacts(true), max(20, w-6))
	room := max(1, h-len(lines)-len(facts))
	f.clampSet(room)
	for i := f.top; i < len(f.rows) && i < f.top+room; i++ {
		lines = append(lines, m.quadSetRow(i, idW, trigW, resW, queryW))
	}
	for len(lines) < h-len(facts) {
		lines = append(lines, "")
	}
	for i, line := range facts {
		if i == 1 && len(lines) >= h {
			break
		}
		lines = append(lines, "  "+dimStyle.Render(line))
	}
	return fitRows(lines, h)
}

// ---------------------------------------------------------------- writing a row

// openForm opens the query editor inside the set the workbench is showing: the row stays beside its
// neighbours and the failure card that says why it matters stays on screen, which is the difference
// between editing a query and switching away to edit one.
func (m *Model) openForm(e *store.Entry) {
	f := m.flow
	if f == nil {
		return
	}
	w := m.width - 2
	if f.sec == secIteration {
		w = m.workbenchPaneWidth() - 2
	}
	m.openInlineForm(e, f.split(), w)
}

// focusForm puts the caret in the fields the pane is already drawing. The fields are always there;
// this is only the move that says the next keystroke is a character, not a row command.
func (m *Model) focusForm() tea.Cmd {
	if m.inlineForm() == nil {
		return nil
	}
	return m.form.focus()
}

// openPaneForm is the same editor for the set beside the two sections, which is the width of that
// pane and not of the window.
func (m *Model) openPaneForm(sec int, e *store.Entry) {
	w := m.sectionPaneWidth()
	if w == 0 {
		return
	}
	m.openInlineForm(e, splitOf(sec), w-2)
}

// syncSetForm keeps the fields under the table about the row under the cursor. It is not a mode and it
// does not wait for a key: the form is part of the pane, seeded from the selection and re-seeded when
// the selection moves. A row being written new is left alone — it has no row to follow yet.
func (m *Model) syncSetForm() {
	// A screen that is not showing a set has no fields under a table to keep in step.
	if m.mode == screenFlow && m.flow != nil && m.flow.split() == "" {
		m.form = nil
		return
	}
	rows, sel, _, w := m.setView()
	if m.form != nil && !m.form.editing {
		return
	}
	if w == 0 || len(rows) == 0 {
		m.form = nil
		return
	}
	e := rows[min(sel, len(rows)-1)]
	// The form is kept only while it is still about that row at that index: a row deleted above it
	// shifts every index below, and a form holding the old one would write the wrong row.
	// A row deleted above it shifts every index below, and a narrower pane wraps the ask differently:
	// both are reasons to seed the fields again rather than reuse what was drawn for another shape.
	if m.form != nil && m.form.editing && m.form.id == e.ID && m.form.index == e.Index &&
		m.form.width == max(24, w) {
		return
	}
	m.seedForm(&e, e.Split, w)
}

// setView is the list the screen in front of you is showing: its rows, its cursor, its split and the
// width its pane gives the fields. The workbench returns no width: its right column reports the set
// and does not edit it, so there are no fields to seed there.
func (m *Model) setView() (rows []store.Entry, sel int, split string, w int) {
	if m.mode == screenSections {
		sec := sectionRows[m.sections.cursor].sec
		return m.sections.rows, m.sections.sel, splitOf(sec), m.sectionPaneWidth() - 2
	}
	if m.flow == nil {
		return nil, 0, "", 0
	}
	if m.flow.sec == secIteration {
		return m.flow.rows, m.flow.sel, m.flow.split(), 0
	}
	return m.flow.rows, m.flow.sel, m.flow.split(), m.width - 2
}

// seedForm is the one place a form is made: from a row, or empty for a new one.
func (m *Model) seedForm(e *store.Entry, split string, w int) {
	if e != nil && store.KnownSplit(e.Split) {
		split = e.Split
	}
	if !store.KnownSplit(split) {
		split = store.SplitTrain
	}
	fs := &formState{split: split, width: max(24, w), height: m.bodyHeight()}
	m.fillForm(fs, e)
	fs.build()
	m.form = fs
}

// openInlineForm is the editor drawn in the set itself. The split it opens on is the row's own when
// there is a row, and the split on screen when there is not — a new row belongs to the set you were
// looking at, and may be moved afterwards. The mode is the screen that called: a form is drawn inside
// whatever was already there, and never becomes a screen of its own.
func (m *Model) openInlineForm(e *store.Entry, split string, w int) {
	m.seedForm(e, split, w)
	if m.form != nil {
		m.form.focus()
	}
}

// fillForm seeds the fields from the row being edited, or from the next free id for a new one. The
// id is the tool's: ids index the result directories, and the guided flow has no second place to fix
// a collision.
func (m *Model) fillForm(f *formState, e *store.Entry) {
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
}

// inlineForm is the fields a set screen is drawing, or nil. It is not a screen: the screen that owns
// the set keeps its other panes and its own title, and only the set's table makes room.
func (m *Model) inlineForm() *formState {
	if m.form != nil && (m.mode == screenFlow || m.mode == screenSections) {
		return m.form
	}
	return nil
}

// formActive is whether the caret is in those fields, which is the only thing the keys care about.
func (m *Model) formActive() bool {
	f := m.inlineForm()
	return f != nil && f.active
}

// apply writes the draft through the store and re-reads the set, so the table behind the form is the
// file and not the draft.
func (f *formState) apply(m *Model) tea.Cmd {
	split := strings.TrimSpace(f.splitSel)
	if split == "" {
		split = f.split
	}
	if !store.KnownSplit(split) {
		f.errMsg = "split must be " + strings.Join(splitOptions, ", ")
		return nil
	}
	moved := f.editing && split != f.split
	// The select is the field, and the boolean behind it is the draft: a form whose label is changed
	// and then saved must write the label the form showed.
	should := f.label
	switch strings.TrimSpace(f.labelSel) {
	case labelWord(true):
		should = true
	case labelWord(false):
		should = false
	}
	q := model.TriggerQuery{
		ID:            f.id,
		Skill:         m.skill,
		ShouldTrigger: should,
		Split:         split,
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
	m.form = nil
	m.reload()
	m.selectRow(f.id)
	verb := "added"
	if f.editing {
		verb = "saved"
	}
	msg := fmt.Sprintf("q%d %s — %s, %s", f.id, verb, split, labelWord(should))
	if moved {
		// Moving a row between splits is the one edit that changes what the other screen measures,
		// so it says so instead of looking like an ordinary save.
		msg = fmt.Sprintf("q%d moved %s → %s — it has left the split this screen shows", f.id, f.split, split)
	}
	m.setStatus(msg, false)
	return nil
}

// selectRow puts a set's cursor back on a row after a write: the row may have moved down the file
// order, and a cursor that jumped to the top would hide the row just saved. Both sets a form can be
// opened from keep their own cursor, so both are put back.
func (m *Model) selectRow(id int) {
	s := &m.sections
	for i, e := range s.rows {
		if e.ID == id {
			s.sel = i
			break
		}
	}
	s.sel = min(s.sel, max(0, len(s.rows)-1))
	if f := m.flow; f != nil {
		if i := rowIndex(f.rows, id); i >= 0 {
			f.sel = i
		} else {
			f.sel = min(f.sel, max(0, len(f.rows)-1))
		}
	}
	// The fields follow the cursor, and the cursor just moved: seed them again for the row it landed on.
	m.syncSetForm()
}

// rowIndex is the position of a row in a list, or -1.
func rowIndex(rows []store.Entry, id int) int {
	for i, e := range rows {
		if e.ID == id {
			return i
		}
	}
	return -1
}

func labelWord(yes bool) string {
	if yes {
		return "should trigger"
	}
	return "should not trigger"
}
