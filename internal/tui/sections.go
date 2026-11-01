package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"evalctl/internal/store"
)

// The section screen: iteration or generalization, and the set that section works.
//
// The two halves of the loop use their two sets for opposite things — one is what a revision may
// learn from, the other is what must never shape one — so they are chosen here, explicitly, and each
// opens on its own steps. The choice is on the left and the one under the cursor is on the right,
// the same shape as the door and the skill chooser.
//
// The right half is the split that choice works, not a paragraph about it: the train set beside
// iteration and the validation set beside generalization, with the rows editable in place. The
// moment before entering the workbench is exactly when someone wants to look at the set and fix a
// label or a why, so the pane is a small table with the step screen's own keys — a mini workbench —
// and enter on either side goes deeper into the real one.

// sectionFocus is which of the screen's two things has the keys: the two halves of the loop on the
// left, or the split's rows on the right.
const (
	sectionFocusList = iota
	sectionFocusSet
)

type sectionState struct {
	cursor, top int // the list of sections
	focus       int
	// rows is the split the cursored section works — train for iteration, validation for
	// generalization — read from the file, so the pane shows what a run would read.
	rows []store.Entry
	sel  int
	rtop int
}

var sectionRows = []struct {
	sec   int
	title string
}{
	{secIteration, "iteration"},
	{secGeneralization, "generalization"},
}

func (m *Model) sectionsBody(h int) []string {
	paneW := m.sectionPaneWidth()
	if paneW == 0 {
		return m.sectionsNarrow(h)
	}
	return m.splitBody(h, m.sectionListWidth(), paneW, m.sectionsList(), m.sectionSet(paneW, h))
}

// sectionListWidth is the list's column. The two halves of the loop are short words, so it is a
// third of the window and no wider; the pane gets the rest.
func (m *Model) sectionListWidth() int { return min(34, max(24, m.width/3)) }

// sectionPaneWidth is the width the set beside the list gets, and 0 when the window cannot hold one:
// the list and the pane are two thirds of a screen each, and a table squeezed under 38 columns is a
// column of ellipses with a cursor in it.
func (m *Model) sectionPaneWidth() int {
	if paneW := min(96, m.width-m.sectionListWidth()-3); paneW >= 38 {
		return paneW
	}
	return 0
}

// sectionListName is what the list calls itself, in the title row where every other region names
// itself. The pane beside it says "train set"; without a name of its own the list looked like the
// screen's background with rows on it.
const sectionListName = "SECTIONS"

// sectionsList is the two halves of the loop, each with the state of the set it works. The cursor
// mark is drawn only while this list has the keys: a mark in a list nobody is driving is a second
// cursor on the screen, and the one question this screen has to answer is where the keys are.
func (m *Model) sectionsList() []string {
	w := m.sectionListWidth()
	ruled := "  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, w-4)))
	lines := []string{"", regionHead(w, sectionListName, "", m.sections.focus == sectionFocusList), ruled}
	for i, row := range sectionRows {
		mark := "  "
		if i == m.sections.cursor && m.sections.focus == sectionFocusList {
			mark = cursorMark.Render(glyphs.Cursor)
		}
		state := dimStyle.Render(m.sectionTag(row.sec))
		line := mark + titleStyle.Render(row.title) + "  " + state
		// The band and the mark are the same thing seen twice — this is the row the keys are on —
		// so it is drawn only while this list has them. A banded row in a list nobody is driving is
		// exactly the second selection this screen used to have.
		if i == m.sections.cursor && m.sections.focus == sectionFocusList {
			line = selected(line)
		}
		lines = append(lines, line, "")
	}
	return lines
}

// sectionTag is the short state beside a section in the list: two or three words, because the pane
// is where the sentence lives.
func (m *Model) sectionTag(sec int) string {
	f := m.flow
	if f == nil {
		return ""
	}
	if sec == secIteration {
		return fmt.Sprintf("%d of %d", len(f.iters), iterationsRequired)
	}
	switch {
	case f.lab == nil || f.lab.ValidationRows == 0:
		return "no queries"
	case f.lab.Applied != nil && allValidated(f.lab):
		return "applied"
	case allValidated(f.lab):
		return "measured"
	case !f.gateOpen():
		return "gated"
	}
	return "to validate"
}

// sectionLead is that state as a sentence, for the pane's head row.
func (m *Model) sectionLead(sec int) string {
	f := m.flow
	if f == nil {
		return ""
	}
	if sec == secIteration {
		return fmt.Sprintf("%d of %d iterations measured", len(f.iters), iterationsRequired)
	}
	switch {
	case f.lab == nil || f.lab.ValidationRows == 0:
		return "no validation queries yet"
	case f.lab.Applied != nil && allValidated(f.lab):
		return fmt.Sprintf("#%d applied · every measured wording validated", f.lab.Applied.Candidate)
	case allValidated(f.lab):
		return "every measured wording validated"
	case !f.gateOpen():
		return fmt.Sprintf("gated — %s still to measure", plural(max(0, iterationsRequired-len(f.iters)), "iteration"))
	}
	return "ready to validate the measured wordings"
}

// sectionSet is the pane: the cursored section's split as a table, with the step screen's columns and
// its keys. It is the mini workbench — enough to manage the set before going in, and not the place
// the run happens. `e` and `n` open the same editor as the step screens, under this table, so a label
// or a why is fixed without leaving the two halves of the loop.
func (m *Model) sectionSet(w, h int) []string {
	m.syncSetForm()
	s := &m.sections
	sec := sectionRows[s.cursor].sec
	train := sec == secIteration
	// The caret in the fields under the table is the pane losing the keys, not a second focus: the
	// set's mark goes with them.
	focused := s.focus == sectionFocusSet && !m.formActive()
	name := setRegionName(train)
	// The pane's name is the thing that must survive a narrow pane: the sentence beside it goes
	// first, because "VALIDATIO…" is a region that cannot be named.
	head := regionHead(w, name, "", focused)
	lead := dimStyle.Render(m.sectionLead(sec)) + " "
	if lipgloss.Width(head)+lipgloss.Width(lead) > w {
		lead = ""
	}
	lines := []string{
		"",
		endsLine(head, lead, w),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, w-4))),
	}

	// The counts and the one warning about this split sit at the bottom, where the step screen puts
	// them too: the rule the guide states is worth more than a row of the table.
	facts := wrapPlain(m.splitFacts(s.rows, train), max(20, w-6))
	if len(facts) > 2 {
		facts = facts[:2]
	}
	foot := []string{"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, w-4)))}
	for _, line := range facts {
		foot = append(foot, "  "+dimStyle.Render(line))
	}
	if warn := m.splitWarning(s.rows); warn != "" {
		foot = append(foot, "  "+badgeWarn.Render("!")+" "+dimStyle.Render(ellipsis(warn, max(10, w-4))))
	} else {
		foot = append(foot, "")
	}

	// The row under the cursor is drawn in full under the table whether or not the editor is open: the
	// fields are part of the set's pane, and `e` only puts the caret in them.
	formH := 0
	if form := m.inlineForm(); form != nil {
		formH = min(form.want()+1, max(9, h-len(lines)-len(foot)-3))
	}
	// One of the rows below the table is the rule that separates it from the fields.
	room := max(1, h-len(lines)-len(foot)-formH-boolInt(formH > 0))

	if len(s.rows) == 0 {
		lines = append(lines, "", "  "+dimStyle.Render("no rows in this split yet"),
			"", "  "+titleStyle.Render("n")+"  "+dimStyle.Render("add the first query"))
		if form := m.inlineForm(); form != nil {
			lines = append(lines, "  "+dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, w-4))))
			lines = append(lines, form.body(w-2, formH)...)
		}
		return fitRows(append(lines, foot...), h)
	}

	cols := queryCols(w)
	lines = append(lines, queryHeader(cols))
	clampWindow(&s.sel, &s.rtop, len(s.rows), room)
	for i := s.rtop; i < len(s.rows) && i < s.rtop+room; i++ {
		lines = append(lines, queryRow(s.rows[i], cols, focused && i == s.sel, focused && i == s.sel))
	}
	// The table keeps its height so the fields do not jump when a short set is edited — but it never
	// takes a row the block or the footer needs.
	for len(lines) < 3+room && len(lines)+formH+boolInt(formH > 0)+len(foot) <= h {
		lines = append(lines, "")
	}
	if form := m.inlineForm(); form != nil {
		lines = append(lines, "  "+dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, w-4))))
		lines = append(lines, form.body(w-2, formH)...)
	}
	for len(lines) < h-len(foot) {
		lines = append(lines, "")
	}
	return fitRows(append(lines, foot...), h)
}

// sectionsNarrow is the section screen without a pane: each row carries its own tag, and the one
// under the cursor carries a sentence. A terminal that cannot hold a table beside a list gets the
// list and the numbers, and the set is managed one screen in.
func (m *Model) sectionsNarrow(h int) []string {
	lines := []string{""}
	for i, row := range sectionRows {
		mark := "  "
		if i == m.sections.cursor {
			mark = cursorMark.Render(glyphs.Cursor)
		}
		lines = append(lines, mark+titleStyle.Render(row.title)+"  "+dimStyle.Render(m.sectionTag(row.sec)))
		if i == m.sections.cursor {
			blurb := "the train set, the run, the failures, the revision"
			if row.sec == secGeneralization {
				blurb = "the validation set, one run per wording, the ranking"
			}
			for _, line := range wrapPlain(blurb, max(20, m.width-8)) {
				lines = append(lines, "    "+dimStyle.Render(line))
			}
			if n := len(m.sections.rows); n > 0 {
				lines = append(lines, "    "+dimStyle.Render(plural(n, "row")+" in this split"))
			}
		}
		lines = append(lines, "")
	}
	return fitRows(lines, h)
}

// ---------------------------------------------------------------- keys

func (m *Model) updateSections(msg tea.KeyMsg) tea.Cmd {
	if msg.String() == "tab" {
		if m.sectionPaneWidth() == 0 {
			m.setStatus("no room beside the list for the set — widen the window, or manage it one screen in", true)
			return nil
		}
		m.sections.focus = 1 - m.sections.focus
		// The pane's fields are seeded when it takes the keys, so the caret-less view is the file's.
		m.syncSetForm()
		return nil
	}
	if m.sections.focus == sectionFocusSet && m.sectionPaneWidth() > 0 {
		return m.updateSectionSet(msg)
	}
	return m.updateSectionList(msg)
}

func (m *Model) updateSectionList(msg tea.KeyMsg) tea.Cmd {
	s := &m.sections
	switch msg.String() {
	case "j", "down":
		s.cursor = min(s.cursor+1, len(sectionRows)-1)
		m.loadSectionRows()
	case "k", "up":
		s.cursor = max(0, s.cursor-1)
		m.loadSectionRows()
	case "g", "home":
		s.cursor = 0
		m.loadSectionRows()
	case "G", "end":
		s.cursor = len(sectionRows) - 1
		m.loadSectionRows()
	case "enter":
		return m.openSectionStep(sectionRows[s.cursor].sec, 0)
	}
	return nil
}

// updateSectionSet is the pane's mini workbench. The keys are the step screen's own — add, edit,
// relabel, delete — because it is the same set, and a second vocabulary for one list is a second
// thing to learn.
func (m *Model) updateSectionSet(msg tea.KeyMsg) tea.Cmd {
	s := &m.sections
	sec := sectionRows[s.cursor].sec
	switch msg.String() {
	case "j", "down":
		s.sel = min(s.sel+1, max(0, len(s.rows)-1))
		m.syncSetForm()
	case "k", "up":
		s.sel = max(0, s.sel-1)
		m.syncSetForm()
	case "g", "home":
		s.sel = 0
		m.syncSetForm()
	case "G", "end":
		s.sel = max(0, len(s.rows)-1)
		m.syncSetForm()
	case "n":
		m.openPaneForm(sec, nil)
	case "e", "tab":
		return m.focusForm()
	case "shift+tab":
		m.sections.focus = sectionFocusList
	case " ", "space":
		if len(s.rows) > 0 {
			return m.toggleRow(s.rows[s.sel])
		}
	case "d":
		if len(s.rows) > 0 {
			m.askDeleteEntry(s.rows[s.sel], sectionRows[s.cursor].title, screenSections, nil)
		}
	case "enter":
		// The pane's forward move: the workbench, opened on the set it is showing.
		return m.openSectionStep(sec, 1)
	}
	return nil
}

// loadSectionRows reads the split the cursored section works, so the pane always draws the file a run
// would read rather than a copy of it.
func (m *Model) loadSectionRows() {
	s := &m.sections
	if m.skill == "" {
		s.rows, s.sel, s.rtop = nil, 0, 0
		return
	}
	s.rows = m.store.Rows(store.LaneTrigger, store.Filter{Skill: m.skill, Split: splitOf(sectionRows[s.cursor].sec)})
	m.syncSetForm()
	if s.sel >= len(s.rows) {
		s.sel = max(0, len(s.rows)-1)
	}
	if s.sel < 0 {
		s.sel = 0
	}
}

// openSectionStep enters one half of the loop: at the step the disk says the work is at (step 0), or
// at a named step. The gate is not enforced here — generalization's first step is its query set, and
// a set can be written at any time; it is the run that waits.
func (m *Model) openSectionStep(sec, step int) tea.Cmd {
	f := m.flow
	if f == nil {
		return nil
	}
	f.sec = sec
	m.mode = screenFlow
	if step == 0 {
		f.resumeIn(m)
		return nil
	}
	f.step = step
	f.enterStep(m)
	return nil
}
