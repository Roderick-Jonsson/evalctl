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
	train := f.step == StepTrain
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
	head := "  " + headerFocus.Render(fitTo("ID", 4)) + "  " + headerFocus.Render(fitTo("LABEL", 5)) + "  " +
		headerFocus.Render(fitTo("QUERY", max(20, m.width-4-4-5-6-2-30))) + "  " +
		headerFocus.Render("WHY")
	lines = append(lines, head)
	for i := f.top; i < len(f.rows) && len(lines) < 2+room; i++ {
		lines = append(lines, m.setRow(i, m.width))
	}
	for len(lines) < 2+room {
		lines = append(lines, "")
	}
	lines = append(lines, "  "+dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, m.width-4)))))
	lines = append(lines, "  "+dimStyle.Render(m.setFacts(train)))
	if w := m.setWarning(train); w != "" {
		lines = append(lines, "  "+badgeWarn.Render("!")+" "+dimStyle.Render(w))
	} else {
		lines = append(lines, "")
	}
	return fitRows(lines, h)
}

// setRow is one query: the id, the label it must not lie about, the text, and the reason it is in
// the set — which is the field step 3 reads back to you when the row fails.
func (m *Model) setRow(i, w int) string {
	f := m.flow
	e := f.rows[i]
	mark := "  "
	if i == f.sel {
		mark = cursorMark.Render(glyphs.Cursor)
	}
	label := colYes.Render(fitTo("yes", 5))
	if e.Label == "no" {
		label = colNo.Render(fitTo("no", 5))
	}
	queryW := max(20, w-4-4-5-6-2-30)
	why := e.Why
	if why == "" {
		why = "—"
	}
	line := mark + colID.Render(fitTo(fmt.Sprint(e.ID), 4)) + "  " + label + "  " +
		colText.Render(fitTo(oneLine(e.Text), queryW)) + "  " +
		colSplit.Render(fitTo(oneLine(why), 30))
	if i == f.sel {
		line = selected(line)
	}
	return line
}

// setFacts is the count line: what the split holds and how it is balanced, with the guide's own
// ratio beside the real one so the comparison needs no memory.
func (m *Model) setFacts(train bool) string {
	f := m.flow
	yes, no := 0, 0
	for _, e := range f.rows {
		if e.Label == "yes" {
			yes++
		} else {
			no++
		}
	}
	parts := []string{
		plural(len(f.rows), "row"),
		fmt.Sprintf("%d should trigger", yes),
		fmt.Sprintf("%d should not", no),
	}
	if f.lab != nil {
		if total := f.lab.TrainRows + f.lab.ValidationRows; total > 0 {
			share := f.lab.ValidationRows
			if train {
				share = f.lab.TrainRows
			}
			parts = append(parts, fmt.Sprintf("%d%% of the set (the guide asks ~60/40)",
				(share*100+total/2)/total))
		}
	}
	return strings.Join(parts, " · ")
}

// setWarning is the one thing about this split worth saying without being asked: a label the split
// does not carry, a duplicate, or a row with no reason on it.
func (m *Model) setWarning(train bool) string {
	f := m.flow
	if len(f.rows) == 0 {
		return ""
	}
	yes, no := 0, 0
	noWhy, dupes := 0, 0
	seen := map[string]bool{}
	for _, e := range f.rows {
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
		out = append(out, fmt.Sprintf("%s without a why — step 3 reads that line when the row fails", plural(noWhy, "row")))
	}
	if dupes > 0 {
		out = append(out, fmt.Sprintf("%s read the same as another row", plural(dupes, "row")))
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, " · ")
}

func (f *flowState) clampSet(room int) {
	if f.sel < f.top {
		f.top = f.sel
	}
	if f.sel >= f.top+room {
		f.top = f.sel - room + 1
	}
	if f.top < 0 {
		f.top = 0
	}
	if f.sel >= len(f.rows) {
		f.sel = max(0, len(f.rows)-1)
	}
}

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
		m.openForm(nil, f.step)
	case "e":
		if len(f.rows) == 0 {
			return nil
		}
		e := f.rows[f.sel]
		m.openForm(&e, f.step)
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

// leaveSet is the forward move: the train set has to hold rows before the validation set is worth
// defining, and the validation set has to hold rows before anything is measured against it.
func (m *Model) leaveSet() tea.Cmd {
	f := m.flow
	if len(f.rows) == 0 {
		m.setStatus("this split is empty — add a query with n", true)
		return nil
	}
	if f.step == StepTrain {
		return m.gotoStep(StepValidation)
	}
	return m.gotoStep(StepIterate)
}

// toggleLabel flips the row's label and writes the file at once: the set is what the next run reads
// from disk, so a change that only lives in memory would measure something nobody wrote.
func (m *Model) toggleLabel() tea.Cmd {
	f := m.flow
	e := f.rows[f.sel]
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

// askDeleteRow confirms a delete in the same dialog every other destructive step uses. The set is
// the instrument the whole loop depends on, so a keystroke never removes a row silently.
func (m *Model) askDeleteRow() {
	f := m.flow
	e := f.rows[f.sel]
	m.ask = &askState{
		scope: stepNames[f.step],
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
			f.sel = min(f.sel, max(0, len(f.rows)-1))
			m.setStatus(fmt.Sprintf("q%d deleted", e.ID), false)
			return nil
		},
	}
	m.mode = screenAsk
}

// ---------------------------------------------------------------- writing a row

// openForm opens the query form on a row of the current split. A new row takes the next free id
// without asking: ids are the tool's business, not the operator's, and the guided flow has no
// second place to fix a collision.
func (m *Model) openForm(e *store.Entry, step int) {
	split := store.SplitTrain
	if step == StepValidation {
		split = store.SplitValidation
	}
	f := &formState{split: split, width: m.width, height: m.bodyHeight()}
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
		f.hint = "the why is read back to you in step 3 when this row fails"
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
	m.form, m.mode = nil, screenFlow
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
