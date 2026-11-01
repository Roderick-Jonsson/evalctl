package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"evalctl/internal/runner"
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
	// runFocus is whether the run's block has the keys — it is the pane's second region, under the
	// table — and runSel is the knob it is standing on. The block is drawn always; this is only
	// which keys the next keystroke belongs to.
	runFocus bool
	runSel   int
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
	// No blank row above the head: the pane beside it starts with its run block on the first row, and
	// the two columns read as one screen only if their heads are on the same row.
	lines := []string{regionHead(w, sectionListName, "", m.sections.focus == sectionFocusList), ruled}
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

// sectionSet is the pane: the run's block, then the cursored section's split as a table, then the row
// under the cursor's fields — the order the work is read in. The run's settings come first because
// they apply to every row below them, the table is the set, and the fields under it are the row being
// changed. It is the mini workbench — enough to manage the set before going in, and not the place the
// run happens. `e` and `n` open the same editor as the step screens, under this table, so a label or a
// why is fixed without leaving the two halves of the loop.
func (m *Model) sectionSet(w, h int) []string {
	m.syncSetForm()
	s := &m.sections
	sec := sectionRows[s.cursor].sec
	train := sec == secIteration
	// The caret in the fields under the table does not make them a second region: the table and the
	// fields are one pane, and the pane has the keys either way. The cursor arrow belongs to the
	// table inside it, so it goes when the caret is down in the fields — one mark at a time.
	//
	// The run's block is the pane's second region: it is drawn here always, because the knobs are
	// what a run costs and that is decided before going in, and it takes the accent only while it
	// has the keys — the table is quiet then, so the screen still names exactly one region.
	runKeys := s.focus == sectionFocusSet && s.runFocus
	focused := s.focus == sectionFocusSet && !s.runFocus
	atTable := focused && !m.formActive()
	name := setRegionName(train)
	// The pane's name is the thing that must survive a narrow pane: the sentence beside it goes
	// first, because "VALIDATIO…" is a region that cannot be named.
	head := regionHead(w, name, "", focused)
	lead := dimStyle.Render(m.sectionLead(sec)) + " "
	if lipgloss.Width(head)+lipgloss.Width(lead) > w {
		lead = ""
	}
	// The block leads: it is the pane's first row because it governs every row under it. Then the
	// set's name and rule, then the table, then the fields of the row under the cursor. No blank row
	// above it — the rule under the bar is already the pane's top edge, and a short window cannot pay
	// for an empty row.
	lines := m.runBlock(w, runKeys)
	lines = append(lines,
		"  "+dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, w-4))),
		endsLine(head, lead, w),
		"  "+dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, w-4))),
	)

	// The counts and the one warning about this split sit at the bottom, under everything they are
	// about — where the step screen puts them too: the rule the guide states is worth more than a row
	// of the table.
	facts := wrapPlain(m.splitFacts(s.rows, train), max(20, w-6))
	if len(facts) > 2 {
		facts = facts[:2]
	}
	foot := facts2dim(facts)
	if warn := m.splitWarning(s.rows); warn != "" {
		foot = append(foot, "  "+badgeWarn.Render("!")+" "+dimStyle.Render(ellipsis(warn, max(10, w-4))))
	} else {
		foot = append(foot, "")
	}

	// The row under the cursor is drawn in full under the table whether or not the editor is open: the
	// fields are part of the set's pane, and `e` only puts the caret in them. The fields take what is
	// left after the block and the footer, never the other way round: the run's knobs are the thing a
	// short window must still show, because it is where a run is configured.
	formH := 0
	if form := m.inlineForm(); form != nil {
		formH = min(form.want()+1, max(0, h-len(lines)-len(foot)-3))
	}
	// One of the rows below the table is the rule that separates it from the fields.
	room := max(1, h-len(lines)-len(foot)-formH-boolInt(formH > 0))

	if len(s.rows) == 0 {
		lines = append(lines, queryHeader(queryCols(w)),
			"  "+dimStyle.Render("no rows in this split yet"),
			"", "  "+titleStyle.Render("n")+"  "+dimStyle.Render("add the first query"))
		if form := m.inlineForm(); form != nil && formH > 0 {
			lines = append(lines, "  "+dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, w-4))))
			lines = append(lines, form.body(w-2, formH)...)
		}
		return fitRows(append(lines, foot...), h)
	}

	cols := queryCols(w)
	lines = append(lines, queryHeader(cols))
	clampWindow(&s.sel, &s.rtop, len(s.rows), room)
	for i := s.rtop; i < len(s.rows) && i < s.rtop+room; i++ {
		lines = append(lines, queryRow(s.rows[i], cols, atTable && i == s.sel, atTable && i == s.sel))
	}
	// The table keeps its height so the fields do not jump when a short set is edited — but it never
	// takes a row the fields or the footer needs.
	for len(lines) < 6+room && len(lines)+formH+boolInt(formH > 0)+len(foot) <= h {
		lines = append(lines, "")
	}
	if form := m.inlineForm(); form != nil && formH > 0 {
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
// list and the numbers, and the set is managed one screen in — but the run's block is drawn here
// too, because a window too narrow for a pane is not a reason to lose the only place a run is
// configured.
func (m *Model) sectionsNarrow(h int) []string {
	lines := []string{" "}
	for i, row := range sectionRows {
		mark := "  "
		if i == m.sections.cursor && !m.sections.runFocus {
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
	block := append([]string{"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, m.width-4)))},
		m.runBlock(max(20, m.width-2), m.sections.runFocus)...)
	for len(lines) < h-len(block) {
		lines = append(lines, "")
	}
	return fitRows(append(lines, block...), h)
}

// facts2dim is a wrapped fact line as the pane's own footer rows.
func facts2dim(facts []string) []string {
	out := make([]string, 0, len(facts))
	for _, line := range facts {
		out = append(out, "  "+dimStyle.Render(line))
	}
	return out
}

// runBlock is the run's settings, as a block with a name and two knobs — the same shape as every
// other region on the screen, and the same shape as the fields under the table: a label, and the
// value it holds. The knobs are drawn whether or not they have the keys, because what a run costs
// is decided before it is started and a setting you cannot see is a setting you forget; the name is
// accented only while the block has the keys, and then the table above is quiet.
func (m *Model) runBlock(w int, keys bool) []string {
	f := m.flow
	if f == nil {
		return nil
	}
	runs := runner.DefaultTriggerRuns
	if f.lab != nil && f.lab.Runs > 0 {
		runs = f.lab.Runs
	}
	right := ""
	if rows := len(m.sections.rows); rows > 0 {
		right = plural(rows*runs, "agent call")
	}
	// The block's rows are indented by their own marks — the region mark, the cursor mark — so the
	// caller adds no prefix: the name lands in the same column as every other region's name.
	head := regionHead(w, runBlockName, right, keys)
	knobLine := m.knobCell("runs per query", fmt.Sprint(runs), keys && m.sections.runSel == knobRuns) +
		dimStyle.Render("   ") +
		m.knobCell("dry run", m.dryWord(), keys && m.sections.runSel == knobDry)
	if lipgloss.Width(knobLine) > w-2 {
		// A pane too narrow for both knobs keeps the first one and the name: one number in full
		// beats two in ellipses.
		knobLine = m.knobCell("runs per query", fmt.Sprint(runs), keys && m.sections.runSel == knobRuns)
	}
	return []string{ellipsis(head, max(10, w)), ellipsis(knobLine, max(10, w))}
}

// runBlockName is the block's name in the register every region uses.
const runBlockName = "THE RUN"

// The block's two knobs, in the order a run reads them.
const (
	knobRuns = iota
	knobDry
	knobCount
)

// knobCell is one setting: its label, and its value between the brackets that say the value steps.
// The mark and the accent belong to the knob under the cursor and to no other, so the block names
// one setting at a time the way the screen names one region at a time.
func (m *Model) knobCell(label, value string, focused bool) string {
	mark, labelStyle, valueStyle := "  ", dimStyle, titleStyle
	if focused {
		mark, labelStyle, valueStyle = cursorMark.Render(glyphs.Cursor), regionOn, regionOn
	}
	if value == dryOnWord() {
		valueStyle = badgeWarn
	}
	return mark + labelStyle.Render(label) + " " +
		dimStyle.Render(glyphs.KnobL+" ") + valueStyle.Render(value) + dimStyle.Render(" "+glyphs.KnobR)
}

// dryWord is the dry run as the knob says it.
func (m *Model) dryWord() string {
	if m.flow != nil && m.flow.dry {
		return dryOnWord()
	}
	return "off"
}

func dryOnWord() string { return "on" }

// runKnob keys: tab and shift+tab walk the two settings, the arrows change the one under the cursor,
// space flips a boolean, and esc or enter hands the keys back to the table. Nothing here writes a
// query — the block is the run's, and it writes the set's own numbers.
func (m *Model) updateRunKnobs(msg tea.KeyMsg) tea.Cmd {
	s := &m.sections
	switch msg.String() {
	case "j", "down", "tab", "right", "l", "+":
		if msg.String() == "right" || msg.String() == "l" || msg.String() == "+" {
			return m.stepKnob(1)
		}
		s.runSel = (s.runSel + 1) % knobCount
	case "k", "up", "shift+tab", "left", "h", "-":
		if msg.String() == "left" || msg.String() == "h" || msg.String() == "-" {
			return m.stepKnob(-1)
		}
		s.runSel = (s.runSel + knobCount - 1) % knobCount
	case " ":
		if s.runSel == knobDry {
			return m.toggleDryRun()
		}
		m.setStatus("runs per query steps with ← →", false)
	case "enter":
		s.runFocus = false
		m.setStatus("the run's settings are the set's — the table has the keys again", false)
	}
	return nil
}

// stepKnob changes the setting under the cursor: the runs per query count steps, and the dry run —
// which has two states — flips whichever arrow is used.
func (m *Model) stepKnob(delta int) tea.Cmd {
	m.sections.runSel = min(max(m.sections.runSel, 0), knobCount-1)
	if m.sections.runSel == knobDry {
		return m.toggleDryRun()
	}
	return m.adjustRuns(delta)
}

// adjustRuns changes how many times each query is measured. The number is the set's, so it is written
// into the file and every run — this session's, or the CLI's tomorrow — reads the same count.
func (m *Model) adjustRuns(delta int) tea.Cmd {
	f := m.flow
	if f == nil || f.lab == nil {
		return nil
	}
	runs := min(max(f.lab.Runs+delta, 1), 9)
	if runs == f.lab.Runs {
		return nil
	}
	if err := m.store.SetTriggerRuns(runs); err != nil {
		m.setStatus(err.Error(), true)
		return nil
	}
	m.reload()
	// Three is the guide's floor: one run cannot tell a flaky miss from a real one. Saying so once,
	// where the number was just changed, is worth more than a check nobody opens.
	if runs < 3 {
		m.setStatus(fmt.Sprintf("%d run per query — three is the floor that tells a flaky miss from a real one", runs), false)
		return nil
	}
	m.setStatus(fmt.Sprintf("%d runs per query", runs), false)
	return nil
}

// toggleDryRun is the run's session-setting: the next run prints the commands it would issue and
// writes nothing. It is not in the file because it is not a property of the set — it is what the next
// run is asked to be.
func (m *Model) toggleDryRun() tea.Cmd {
	f := m.flow
	if f == nil {
		return nil
	}
	f.dry = !f.dry
	if f.dry {
		m.setStatus("dry run — the next run prints the commands it would issue and writes nothing", false)
		return nil
	}
	m.setStatus("dry run off — the next run spends", false)
	return nil
}

// ---------------------------------------------------------------- keys

func (m *Model) updateSections(msg tea.KeyMsg) tea.Cmd {
	if m.sectionPaneWidth() == 0 {
		// No pane, but the run's block is still on screen under the list: tab is the way into it, and
		// the set itself stays one screen in.
		if msg.String() == "tab" {
			m.sections.runFocus = !m.sections.runFocus
			m.sections.runSel = knobRuns
			return nil
		}
		if m.sections.runFocus {
			return m.updateRunKnobs(msg)
		}
		return m.updateSectionList(msg)
	}
	// Tab in the pane is not the same move as tab on the list: the list has two places to be, and
	// the pane has the set, its fields and the run's block under them, so tab walks inward there
	// and shift+tab walks back out.
	if m.sections.focus == sectionFocusSet {
		if m.sections.runFocus {
			return m.updateRunKnobs(msg)
		}
		return m.updateSectionSet(msg)
	}
	if msg.String() == "tab" {
		m.sections.focus = sectionFocusSet
		m.sections.runFocus = false
		// The pane's fields are seeded when it takes the keys, so the caret-less view is the file's.
		m.syncSetForm()
		return nil
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
// thing to learn. `tab` is the exception and it is the pane's own move: it walks into the run's block
// below the table, the way tab walks into a field, because the block is where a run is configured and
// the table is where its rows are.
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
	case "e":
		return m.focusForm()
	case "tab":
		s.runFocus, s.runSel = true, knobRuns
	case "shift+tab":
		s.focus = sectionFocusList
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
