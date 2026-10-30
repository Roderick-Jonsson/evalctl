// Package tui is the interactive face of evalctl: a filterable, multi-select table over both
// eval lanes with in-place editing, plus the run form, the live run table and the results
// browser. It shares store and runner with the CLI, so the rules about ids, slugs and fixtures
// are enforced in exactly one place.
package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"evalctl/internal/format"
	"evalctl/internal/runner"
	"evalctl/internal/store"
	"evalctl/internal/theme"
	"evalctl/internal/version"
)

type screen int

const (
	screenSkills screen = iota // the front door: one line per stage skill
	screenList
	screenEditor
	screenConfirm
	screenViewer
	screenRunLauncher
	screenRunProgress
	screenPromote
)

// viewTab is which view of the current scope is on screen. A scope is a skill (or every skill);
// its tabs are its two eval lanes and the results of the runs measured against them. A tab is a
// view and not a mode: the frame, the header, the status line and tab / shift+tab are the same
// everywhere, and only the body and the verbs in the footer change.
type viewTab int

const (
	tabCases   viewTab = iota // output cases — what a skill is worked on for
	tabQueries                // trigger queries — what tests its description
	tabResults                // what the runs against them came back with
)

// lane is the eval lane a tab edits; the results tab edits nothing.
func (t viewTab) lane() string {
	switch t {
	case tabCases:
		return store.LaneOutput
	case tabQueries:
		return store.LaneTrigger
	}
	return ""
}

func (t viewTab) tabCount() int { return 3 }

// tabOfLane is the tab that edits a lane, so a model built with a lane and a view built from a lane
// can never disagree about where you are.
func tabOfLane(lane string) viewTab {
	if lane == store.LaneTrigger {
		return tabQueries
	}
	return tabCases
}

func (t viewTab) String() string {
	switch t {
	case tabCases:
		return "cases"
	case tabQueries:
		return "queries"
	}
	return "results"
}

type Model struct {
	store *store.Store
	lane  string // the lane the last list tab edited; kept across a visit to results
	tab   viewTab
	// tabCursor/tabOffset are each tab's own place. Coming back to a tab puts the cursor where you
	// left it: a tab that forgets is a tab you stop switching to.
	tabCursor [3]int
	tabOffset [3]int
	// skill scopes the list to one stage skill ("" = every skill). It is the unit the set is
	// worked on, so it is a field of the model and not just a filter value: the header, the
	// counts, the search placeholder and the way back all key off it.
	skill string
	ver   string // the build in hand, so the header never leaves you guessing

	rows    []store.Entry
	cursor  int
	offset  int
	chosen  map[string]bool // key -> selected
	filter  store.Filter
	onlyDup bool

	// Column functions: which column has focus (the number keys), what the list is sorted and
	// grouped by, and which column a search is scoped to. All presentation — nothing here
	// changes a file, and the row key survives every reorder.
	focusCol   int
	sortKeys   []store.Sort
	groupField string
	searchIn   string

	search    textinput.Model
	searching bool

	width, height int
	mode          screen
	dirty         bool
	status        string
	statusErr     bool

	skills *skillsState
	// returnTo is where a browser screen goes back to: the skill list when that is where you
	// opened it from, the scoped list otherwise.
	returnTo  screen
	editor    *editorState
	confirm   *confirmState
	viewer    *viewerState
	launcher  *launcherState
	progress  *progressState
	runsList  *runsState
	promote   *promoteState
	runActive bool
	runEvents chan runner.Event
	help      bool
	helpTop   int // first key-map line on screen
	quit      bool
	quitArmed bool

	issues []store.Issue
}

// Run starts the TUI and returns the process exit code. With no skill it opens on the skill
// list — the choice that comes before any lane — and lane only decides what the unscoped view
// shows once you pick "all skills".
func Run(s *store.Store, skill, lane string) int {
	// The palette is process state (the style table was built at startup); honour it here too so
	// a caller that resolved a theme after import — the CLI does — is drawn with it.
	applyStyles(theme.Current())
	m := newModel(s, lane)
	if skill != "" {
		m.enterSkill(skill)
		m.setTab(tabOfLane(lane)) // --lane is a request: honour it over the tab a scope starts on
	} else {
		m.openSkills()
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Println("evalctl tui:", err)
		return 1
	}
	return 0
}

func newModel(s *store.Store, lane string) *Model {
	ti := textinput.New()
	ti.Placeholder = "search query / prompt / slug / why"
	ti.CharLimit = 200
	ti.Prompt = "/ "
	// screenSkills is the zero value, so a model built for a lane says so explicitly: nothing but
	// openSkills puts a skill list on screen.
	m := &Model{store: s, lane: lane, tab: tabOfLane(lane), ver: version.Resolve().Short(),
		chosen: map[string]bool{}, search: ti, focusCol: -1, mode: screenList}
	m.refresh()
	m.issues = s.Validate()
	return m
}

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) refresh() {
	prev := ""
	if e, ok := m.focused(); ok {
		prev = m.key(e)
	}
	m.rows = m.store.RowsOrdered(m.lane, m.visibleFilter(), m.order())
	if prev != "" {
		for i, e := range m.rows {
			if m.key(e) == prev {
				m.cursor = i
				break
			}
		}
	}
	if m.cursor >= len(m.rows) {
		m.cursor = max(0, len(m.rows)-1)
	}
	m.clampScroll()
}

// order is the presentation order the list is drawn in: sort keys first, then the group column.
func (m *Model) order() store.Order {
	return store.Order{Keys: m.sortKeys, Group: m.groupField}
}

func (m *Model) visibleFilter() store.Filter {
	f := m.filter
	f.Search = m.search.Value()
	f.Dupes = m.onlyDup
	f.In = m.searchIn
	return f
}

func (m *Model) key(e store.Entry) string {
	return fmt.Sprintf("%s|%s|%d", e.Lane, e.Skill, e.Index)
}

func (m *Model) focused() (store.Entry, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return store.Entry{}, false
	}
	return m.rows[m.cursor], true
}

// targets is what a delete acts on: the explicit selection, or the focused row when nothing
// is selected — the "trim one at a time" path and the "trim fifty" path are the same key.
func (m *Model) targets() []store.Entry {
	var out []store.Entry
	for _, e := range m.rows {
		if m.chosen[m.key(e)] {
			out = append(out, e)
		}
	}
	if len(out) > 0 {
		return out
	}
	if e, ok := m.focused(); ok {
		return []store.Entry{e}
	}
	return nil
}

// selectedAcrossLanes rebuilds refs for every chosen row, including rows hidden by a filter
// (a selection survives filtering, so a delete never silently misses something you picked).
func (m *Model) selectedAcrossLanes() []store.Entry {
	var out []store.Entry
	if len(m.chosen) == 0 {
		return nil
	}
	for _, e := range m.store.Rows(store.LaneTrigger, store.Filter{}) {
		if m.chosen[m.key(e)] {
			out = append(out, e)
		}
	}
	for _, e := range m.store.Rows(store.LaneOutput, store.Filter{}) {
		if m.chosen[m.key(e)] {
			out = append(out, e)
		}
	}
	return out
}

// listItem is one line of the list: a row, or a group header when the list is grouped.
type listItem struct {
	row   int    // index into m.rows; -1 for a group header
	label string // the header's text
	count int    // rows in that group
}

// items is the list as drawn. Grouping only inserts header lines — the rows keep their order and
// the cursor stays an index into m.rows, so navigation and selection are group-blind.
func (m *Model) items() []listItem {
	if m.groupField == "" {
		out := make([]listItem, len(m.rows))
		for i := range m.rows {
			out[i] = listItem{row: i}
		}
		return out
	}
	var out []listItem
	for _, g := range store.GroupIndices(m.rows, m.groupField) {
		out = append(out, listItem{row: -1, label: store.GroupLabel(m.groupField, g.Key), count: len(g.Rows)})
		for _, r := range g.Rows {
			out = append(out, listItem{row: r})
		}
	}
	return out
}

// itemOf is where the focused row sits in the drawn list (headers counted, since scrolling is).
func (m *Model) itemOf(row int) int {
	for i, it := range m.items() {
		if it.row == row {
			return i
		}
	}
	return -1
}

func (m *Model) clampScroll() {
	h := m.listHeight()
	if h <= 0 {
		return
	}
	at := m.itemOf(m.cursor)
	if at < 0 {
		return
	}
	if at < m.offset {
		m.offset = at
	}
	if at >= m.offset+h {
		m.offset = at - h + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
	if last := len(m.items()) - h; m.offset > last && last >= 0 {
		m.offset = last
	}
}

// listHeight is the rows between the column head and the two footer lines. Every list screen is
// drawn top-anchored and then padded out to the terminal, so what is on screen never decides where
// the status line sits: header (2) + column head (1) + body + hint (1) + status (1) = the height.
func (m *Model) listHeight() int {
	return max(1, m.height-5)
}

// The width the table asks for, in the order it asks for it: the columns it has, then a readable
// measure of the text column, then — only while width is still left over after both — a pane that
// shows the cursor row in full. There is no breakpoint constant: the threshold falls out of the
// lane's own columns, so a six-column lane grows its pane later than a five-column one instead of
// clipping a column to make room.
const (
	textMax = 96 // past this a query is scanned by moving the eye, not read as a line
	textMin = 80 // below this the text column stops being worth the width it holds
	paneMin = 28
	paneMax = 72
	// paneSep is the divider cell. It is drawn only where a pane stands, so a border never appears
	// that is not holding two things apart.
	paneSep = " │ "
)

// frame is one list screen's width budget.
type frame struct {
	table int // the table column, gutter included
	text  int // the flexible text column inside it
	pane  int // the detail pane right of the divider; 0 = no pane
}

func (m *Model) frame() frame {
	fixed := rowGutter
	for _, c := range m.columns() {
		if c.width > 0 {
			fixed += c.width + 1
		}
	}
	avail := m.width - 1 // the last cell is left empty: a line that fills the row exactly wraps
	if avail < 20 {
		return frame{table: max(avail, 10), text: 10}
	}
	sep := len([]rune(paneSep))
	if budget := avail - fixed - sep; budget >= textMin+paneMin {
		pane := max(paneMin, min(paneMax, budget-textMax))
		return frame{table: avail - pane - sep, text: budget - pane, pane: pane}
	}
	text := max(10, avail-fixed)
	return frame{table: fixed + text, text: text}
}

// back is the screen a browser closes to. Everything that is not opened from the skill list
// returns to the list, which is the default.
func (m *Model) back() screen {
	if m.returnTo == screenSkills {
		return screenSkills
	}
	return screenList
}

func (m *Model) setStatus(msg string, isErr bool) {
	m.status = msg
	m.statusErr = isErr
}

func (m *Model) revalidate() { m.issues = m.store.Validate() }

// counts is the badge for what is on screen: scoped to a skill it counts only that skill's
// findings, because "2 warnings" over another stage is noise in a view about this one.
func (m *Model) counts() (errs, warns int) {
	for _, i := range m.issues {
		if m.skill != "" && i.Skill != m.skill {
			continue
		}
		if i.Severity == store.SeverityError {
			errs++
		} else {
			warns++
		}
	}
	return
}

func (m *Model) save() {
	if !m.dirty {
		m.setStatus("nothing to save", false)
		return
	}
	if err := m.store.SaveAll(); err != nil {
		m.setStatus("save failed: "+err.Error(), true)
		return
	}
	m.dirty = false
	m.revalidate()
	errs, _ := m.counts()
	msg := "saved — atomic write, previous bytes in .evalctl/backups/"
	if errs > 0 {
		m.setStatus(fmt.Sprintf("%s · %d validation error(s), see ctrl+v", msg, errs), true)
		return
	}
	m.setStatus(msg, false)
}

func (m *Model) laneCount() int { return len(m.store.Rows(m.lane, store.Filter{})) }

// ---------------------------------------------------------------- update

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.editor != nil {
			m.editor.resize(msg.Width, msg.Height)
		}
		m.clampScroll()
		return m, nil
	case tea.KeyMsg:
		// A fast paste can arrive as one KeyMsg carrying several runes; treat each as its own
		// keystroke so a dropped key never silently changes what a selection means.
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && m.mode == screenList && !m.searching {
			var cmds []tea.Cmd
			for _, r := range msg.Runes {
				next, cmd := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
				m = next.(*Model)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			return m, tea.Batch(cmds...)
		}
		// The key map is a scrollable screen of its own: while it is open it owns the keyboard,
		// so a stray letter cannot edit a row underneath it.
		if m.help {
			switch msg.String() {
			case "?", "esc", "q":
				m.help = false
				return m, nil
			}
			m.scrollHelp(msg.String())
			return m, nil
		}
		switch m.mode {
		case screenSkills:
			return m.updateSkills(msg)
		case screenEditor:
			return m.updateEditor(msg)
		case screenConfirm:
			return m.updateConfirm(msg)
		case screenViewer:
			return m.updateViewer(msg)
		case screenRunLauncher:
			return m.updateLauncher(msg)
		case screenRunProgress:
			return m.updateProgress(msg)
		case screenPromote:
			return m.updatePromote(msg)
		}
		return m.updateList(msg)
	case runEventMsg:
		m.applyEvent(msg.event)
		return m, waitForEvent(m.runEvents)
	case promotePreviewMsg:
		// A scan result only lands if the form has not moved on since it was scheduled. The
		// suggestions that depend on what was found are refreshed here, once.
		p := m.promote
		if p == nil || msg.gen != p.gen {
			return m, nil
		}
		p.preview, p.reference, p.previewErr = msg.preview, msg.reference, msg.err
		p.scanned, p.scanning = msg.gen, false
		if !p.promptEdited && p.dest == destFixture {
			p.prompt = p.suggestPrompt()
		}
		return m, nil
	case runDoneMsg:
		m.runActive = false
		if m.progress != nil {
			if m.progress.err == nil && m.progress.summary != "" {
				m.setStatus(m.progress.summary, false)
			}
		}
		m.revalidate()
		m.refresh()
		// The run just wrote an iteration: the results tab's count and its rows are stale, and the
		// marker on the tab has to come off.
		m.loadRuns()
		return m, nil
	case tickMsg:
		if m.runActive {
			// A followed activity log is re-read every tick: the pane shows the arm as it runs.
			if m.mode == screenViewer && m.viewer != nil && m.viewer.path != "" {
				m.viewer.refresh()
			}
			return m, tickEvery()
		}
		return m, nil
	}
	if m.mode == screenEditor && m.editor != nil {
		cmd := m.editor.update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.searching {
		switch msg.String() {
		case "esc":
			m.searching = false
			m.search.SetValue("")
			m.search.Blur()
			m.searchIn = ""
			m.refresh()
			return m, nil
		case "enter", "down", "tab":
			m.searching = false
			m.search.Blur()
			m.refresh()
			return m, nil
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		m.refresh()
		return m, cmd
	}

	// Number keys focus a column; the header lights up and o / g / / then act on that column. The
	// results tab has no columns of rows to focus, so a digit there says so rather than doing
	// nothing.
	if n, ok := columnDigit(msg.String()); ok {
		if m.tab == tabResults {
			m.setStatus("results has no columns to focus — tab to a lane", true)
			return m, nil
		}
		m.focusColumn(n)
		return m, nil
	}

	clearStatusOnMove(m, msg)

	// The keys that mean the same thing on every view of a scope come first: the way out, the way
	// around, save, help, findings, profiles. What is left is the view's own verbs.
	if model, cmd, handled := m.globalKey(msg); handled {
		return model, cmd
	}

	// The results tab is a view of the same scope with its own body and its own verbs; it shares the
	// frame, the global keys and the tab walk, and nothing else.
	if m.tab == tabResults {
		return m.updateResultsTab(msg)
	}

	switch msg.String() {
	case "R":
		if m.runActive {
			m.mode = screenRunProgress
			m.setStatus("a run is already going — this is its table", false)
			return m, nil
		}
		// A run reads the files on disk. With edits still in memory it would grade a set the
		// operator is not looking at, and report a number that belongs to a different tree.
		if m.dirty {
			m.setStatus("unsaved changes — a run grades the files on disk: ctrl+s writes first, q twice discards", true)
			return m, nil
		}
		m.openLauncher()
		return m, nil
	case "c":
		return m, m.openPromote()
	case "t":
		m.promotePromptToTrigger()
		return m, nil
	case "esc":
		return m.escUp()
	case "0":
		if m.focusCol >= 0 {
			m.focusCol = -1
			m.setStatus("column focus cleared", false)
		}
		return m, nil
	case "o":
		m.cycleSort()
		return m, nil
	case "g":
		// g groups and nothing else: the first row is home's, so a key here means one thing.
		if m.focusCol < 0 {
			m.setStatus(fmt.Sprintf("focus a column first (1–%d), then g groups by it — home is the first row",
				len(m.columns())), true)
			return m, nil
		}
		m.toggleGroup()
		return m, nil
	case "tab":
		m.cycleTab(1)
		return m, nil
	case "shift+tab":
		m.cycleTab(-1)
		return m, nil
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		m.clampScroll()
		return m, nil
	case "down", "j":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
		m.clampScroll()
		return m, nil
	case "pgup", "ctrl+u":
		m.cursor = max(0, m.cursor-m.listHeight()/2)
		m.clampScroll()
		return m, nil
	case "pgdown", "ctrl+d":
		m.cursor = min(len(m.rows)-1, m.cursor+m.listHeight()/2)
		m.clampScroll()
		return m, nil
	case "home":
		m.cursor = 0
		m.clampScroll()
		return m, nil
	case "G", "end":
		m.cursor = max(0, len(m.rows)-1)
		m.clampScroll()
		return m, nil
	case " ":
		if e, ok := m.focused(); ok {
			k := m.key(e)
			if m.chosen[k] {
				delete(m.chosen, k)
			} else {
				m.chosen[k] = true
			}
		}
		if m.cursor < len(m.rows)-1 {
			m.cursor++
			m.clampScroll()
		}
		return m, nil
	case "a":
		for _, e := range m.rows {
			m.chosen[m.key(e)] = true
		}
		m.setStatus(fmt.Sprintf("selected all %d visible row(s)", len(m.rows)), false)
		return m, nil
	case "A":
		m.chosen = map[string]bool{}
		m.setStatus("selection cleared", false)
		return m, nil
	case "u":
		m.onlyDup = !m.onlyDup
		m.refresh()
		return m, nil
	case "f":
		if m.skill != "" {
			m.setStatus("scoped to "+m.skill+" — esc goes back to the skill list", false)
			return m, nil
		}
		m.cycleSkill(1)
		return m, nil
	case "F":
		if m.skill != "" {
			m.setStatus("scoped to "+m.skill+" — esc goes back to the skill list", false)
			return m, nil
		}
		m.cycleSkill(-1)
		return m, nil
	case "s":
		if m.lane == store.LaneTrigger {
			m.filter.Split = nextIn([]string{"", "train", "validation"}, m.filter.Split)
			m.refresh()
		}
		return m, nil
	case "l":
		if m.lane == store.LaneTrigger {
			m.filter.Label = nextIn([]string{"", "yes", "no"}, m.filter.Label)
			m.refresh()
		}
		return m, nil
	case "/":
		// With a column focused the search only reads that column — "find the slug that says
		// dense" without matching a prompt that happens to contain the word.
		m.searchIn = m.focusedColumnName()
		if m.searchIn != "" {
			m.search.Placeholder = "search " + m.searchIn + " only"
		} else {
			m.search.Placeholder = "search query / prompt / slug / why"
		}
		m.searching = true
		m.search.Focus()
		return m, textinput.Blink
	case "e", "enter":
		e, ok := m.focused()
		if !ok {
			return m, nil
		}
		return m, m.openEditor(e, false)
	case "n":
		return m, m.openEditor(store.Entry{Lane: m.lane}, true)
	case "d", "x":
		sel := m.targets()
		if len(sel) == 0 {
			m.setStatus("nothing selected", false)
			return m, nil
		}
		m.openConfirm(sel)
		return m, nil
	case "v":
		e, ok := m.focused()
		if !ok {
			return m, nil
		}
		m.viewer = newViewer(m.store, e, m.width, m.height)
		m.mode = screenViewer
		return m, nil
	}
	return m, nil
}

// ---------------------------------------------------------------- keys

// globalKey is the part of the key map that means the same thing on every view of a scope: the way
// out, the way around the tabs, save, help, the findings list, profiles. It is asked before a view's
// own verbs, so no view can shadow them by accident.
func (m *Model) globalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, m.quitCmd(msg.String()), true
	case "ctrl+s":
		m.save()
		return m, nil, true
	case "?":
		m.help = !m.help
		m.helpTop = 0
		return m, nil, true
	case "ctrl+v":
		m.viewer = newIssueViewer(m.issues, m.width)
		m.mode = screenViewer
		return m, nil, true
	case "P":
		m.openProfiles()
		return m, nil, true
	case "tab":
		m.cycleTab(1)
		return m, nil, true
	case "shift+tab":
		m.cycleTab(-1)
		return m, nil, true
	}
	return m, nil, false
}

// quitCmd is q: it quits, unless quitting would throw away an unsaved edit or walk away from a run
// that is still going — then the first press only says what the second one will do.
func (m *Model) quitCmd(key string) tea.Cmd {
	if m.runActive && key != "ctrl+c" && !m.quitArmed {
		m.quitArmed = true
		m.setStatus("a run is in progress — q again cancels it and quits, R opens the table", true)
		return nil
	}
	if m.dirty && key != "ctrl+c" && !m.quitArmed {
		m.quitArmed = true
		m.setStatus("unsaved changes in memory — ctrl+s saves, q again discards them", true)
		return nil
	}
	if m.progress != nil && m.progress.cancel != nil {
		m.progress.cancel()
	}
	return tea.Quit
}

// escLevel is the level esc goes up to from here. The footer renders the same answer the handler
// acts on (escLabel), so a hint can never promise something the key does not do.
type escLevel int

const (
	escToSkills escLevel = iota
	escClearSelection
	escClearColumn
)

func (m *Model) escLevel() escLevel {
	if m.tab == tabResults {
		return escToSkills
	}
	switch {
	case m.focusCol >= 0:
		return escClearColumn
	case len(m.chosen) > 0:
		return escClearSelection
	}
	return escToSkills
}

// escLabel is what esc will do, in the words the footer shows.
func (m *Model) escLabel() string {
	switch m.escLevel() {
	case escClearColumn:
		return "clear column"
	case escClearSelection:
		return "clear selection"
	}
	return "skills"
}

// escUp does exactly one level of what escLabel said, never two.
func (m *Model) escUp() (tea.Model, tea.Cmd) {
	switch m.escLevel() {
	case escClearColumn:
		m.focusCol = -1
		m.setStatus("column focus cleared", false)
	case escClearSelection:
		m.chosen = map[string]bool{}
		m.setStatus("selection cleared", false)
	default:
		m.openSkills()
	}
	return m, nil
}

// clearStatusOnMove lets the status line decay: it reports what just happened, so the next thing
// that happens replaces it. A status that outlives its action reads as if it were still current.
func clearStatusOnMove(m *Model, msg tea.KeyMsg) {
	switch msg.String() {
	case "j", "k", "up", "down", "pgup", "pgdown", "ctrl+u", "ctrl+d",
		"home", "end", "g", "G", "tab", "shift+tab", "enter", " ":
		m.status = ""
	}
}

func (m *Model) cycleSkill(dir int) {
	names := append([]string{""}, m.store.KnownSkills()...)
	cur := 0
	for i, n := range names {
		if n == m.filter.Skill {
			cur = i
			break
		}
	}
	cur = (cur + dir + len(names)) % len(names)
	m.filter.Skill = names[cur]
	m.cursor, m.offset = 0, 0
	m.refresh()
}

func nextIn(values []string, cur string) string {
	for i, v := range values {
		if v == cur {
			return values[(i+1)%len(values)]
		}
	}
	return values[0]
}

// ---------------------------------------------------------------- column functions

func columnDigit(s string) (int, bool) {
	if len(s) != 1 || s[0] < '1' || s[0] > '9' {
		return 0, false
	}
	return int(s[0] - '0'), true
}

// focusColumn moves the focus to column n (1-based); the same key again drops it. A focused
// column is what o, g and / act on, so nothing else on the keyboard changes meaning.
func (m *Model) focusColumn(n int) {
	cols := m.columns()
	if n > len(cols) {
		m.setStatus(fmt.Sprintf("this lane has %d columns", len(cols)), true)
		return
	}
	if m.focusCol == n-1 {
		m.focusCol = -1
		m.setStatus("column focus cleared", false)
		return
	}
	m.focusCol = n - 1
	m.setStatus(fmt.Sprintf("column %s — o sort · g group · / search it · esc clear", cols[n-1].name), false)
}

func (m *Model) focusedColumnName() string {
	cols := m.columns()
	if m.focusCol < 0 || m.focusCol >= len(cols) {
		return ""
	}
	return cols[m.focusCol].name
}

// cycleSort walks the focused column: ascending, descending, then back to file order.
func (m *Model) cycleSort() {
	name := m.focusedColumnName()
	if name == "" {
		m.setStatus(fmt.Sprintf("focus a column first (1–%d), then o sorts by it", len(m.columns())), true)
		return
	}
	switch {
	case len(m.sortKeys) == 1 && m.sortKeys[0].Field == name && !m.sortKeys[0].Desc:
		m.sortKeys = []store.Sort{{Field: name, Desc: true}}
	case len(m.sortKeys) == 1 && m.sortKeys[0].Field == name && m.sortKeys[0].Desc:
		m.sortKeys = nil
	default:
		m.sortKeys = []store.Sort{{Field: name}}
	}
	m.refresh()
	if len(m.sortKeys) == 0 {
		m.setStatus("sorted by file order", false)
		return
	}
	dir := "asc"
	if m.sortKeys[0].Desc {
		dir = "desc"
	}
	m.setStatus("sorted by "+name+" "+dir, false)
}

// toggleGroup draws the list as sections of one column's values, a count on each header.
func (m *Model) toggleGroup() {
	name := m.focusedColumnName()
	if name == "" {
		return
	}
	if m.groupField == name {
		m.groupField = ""
		m.setStatus("ungrouped", false)
	} else {
		m.groupField = name
		m.setStatus("grouped by "+name, false)
	}
	m.offset = 0
	m.refresh()
}

// dropLaneColumns forgets sort and group columns the other lane does not have (split, label),
// so a lane switch cannot leave the list ordered by a column it cannot draw.
func (m *Model) dropLaneColumns() {
	var keep []store.Sort
	for _, k := range m.sortKeys {
		if store.ColumnExists(m.lane, k.Field) {
			keep = append(keep, k)
		}
	}
	m.sortKeys = keep
	if m.groupField != "" && !store.ColumnExists(m.lane, m.groupField) {
		m.groupField = ""
	}
	if m.searchIn != "" && !store.ColumnExists(m.lane, m.searchIn) {
		m.searchIn = ""
	}
}

// ---------------------------------------------------------------- view

// The style table lives in theme.go: every name used below is a role in the active palette.

func (m *Model) View() string {
	if m.width == 0 {
		return "loading…"
	}
	switch m.mode {
	case screenSkills:
		if m.skills != nil {
			return m.skillsView()
		}
	case screenEditor:
		if m.editor != nil {
			return m.editor.view(m.width)
		}
	case screenConfirm:
		if m.confirm != nil {
			return m.confirm.view(m.width, m.height)
		}
	case screenViewer:
		if m.viewer != nil {
			return m.viewer.view(m.width, m.height)
		}
	case screenRunLauncher:
		if m.launcher != nil {
			return m.launcherView()
		}
	case screenRunProgress:
		if m.progress != nil {
			return m.progressView()
		}
	case screenPromote:
		if m.promote != nil {
			return m.promoteView()
		}
	}
	return m.listView()
}

// headerLines is the header as the rows it draws. The frame is counted in rows, and a two-line
// string is one list element: every screen that pads to the terminal has to count these, not the
// string, or the last row falls off the bottom.
func (m *Model) headerLines() []string {
	return strings.Split(m.header(), "\n")
}

func (m *Model) listView() string {
	if m.help {
		return m.helpView()
	}
	lines := make([]string, 0, m.height)
	lines = append(lines, m.headerLines()...)
	if m.tab == tabResults {
		f := m.resultsFrame()
		lines = append(lines, m.resultsHead(f))
		lines = append(lines, m.resultsBody(f, m.listHeight())...)
	} else {
		f := m.frame()
		lines = append(lines, m.columnHead(f))
		lines = append(lines, m.bodyLines(f, m.listHeight())...)
	}
	lines = append(lines, m.hintFooter(), m.statusFooter())
	return strings.Join(lines, "\n")
}

// openResultsScope puts the current scope's results on screen. From the front door that is every
// skill's results (the front door's scope is ""), which is the same view the list has — the tab is
// the entry point, so there is no second screen with its own title, keys and way back.
func (m *Model) openResultsScope(skill string) {
	m.enterSkill(skill)
	m.setTab(tabResults)
}

// setTab moves to another view of the same scope. Each tab keeps its own place, and only a tab that
// edits a lane touches that lane's facets: the two lanes carry different ones, so leaving a lane
// drops the facet that does not exist on the other.
func (m *Model) setTab(t viewTab) {
	if t == m.tab {
		return
	}
	m.tabCursor[m.tab], m.tabOffset[m.tab] = m.cursor, m.offset
	m.tab = t
	m.focusCol = -1
	m.status = ""
	if lane := t.lane(); lane != "" {
		if lane != m.lane {
			m.filter.Label, m.filter.Split = "", ""
			m.lane = lane
			// The facets of the lane you left do not exist here: dropping them after the switch is
			// what keeps a sort or a grouping from pointing at a column that is not on screen.
			m.dropLaneColumns()
		}
		m.lane = lane
		m.refresh()
		m.cursor, m.offset = m.tabCursor[t], m.tabOffset[t]
		m.clampScroll()
		return
	}
	m.loadRuns()
	if m.runsList != nil {
		m.runsList.cursor = min(m.tabCursor[tabResults], max(0, len(m.runsList.infos)-1))
	}
}

// cycleTab walks the scope's views. tab and shift+tab are the same walk in both directions, so the
// third view is one keystroke away from the second and one more from the first.
func (m *Model) cycleTab(dir int) {
	n := int(tabCases.tabCount())
	m.setTab(viewTab((int(m.tab) + dir + n) % n))
}

// bodyLines is the table column and the pane, joined, exactly h rows long. A row that does not
// exist is padding, not a shorter view: the frame must not grow and shrink as the list is filtered,
// or the footer walks up and down the screen while you type in it.
func (m *Model) bodyLines(f frame, h int) []string {
	left := make([]string, 0, h)
	items := m.items()
	if len(m.rows) == 0 {
		left = append(left, "  "+dimStyle.Render("no rows match"+m.filterHint()))
	}
	end := min(len(items), m.offset+h)
	for i := m.offset; i < end; i++ {
		it := items[i]
		var line string
		switch {
		case it.row < 0:
			line = m.groupLine(it)
		default:
			line = m.rowLine(f, it.row, m.rows[it.row])
			if f.pane > 0 {
				// Pad before the band is painted: selected() re-applies a background after every
				// cell's reset, so padding added afterwards would be the one part of the row the
				// cursor does not cover.
				line = padCell(line, f.table, false)
			}
			if it.row == m.cursor {
				line = selected(line)
			}
		}
		if f.pane > 0 {
			line = padCell(line, f.table, false)
		}
		left = append(left, line)
	}

	pane := m.paneColumn(f, h)
	out := make([]string, 0, h)
	for i := 0; i < h; i++ {
		var line string
		if i < len(left) {
			line = left[i]
		}
		if f.pane > 0 {
			if i >= len(left) {
				line = strings.Repeat(" ", f.table)
			}
			line += dimStyle.Render(paneSep) + pane[i]
		}
		out = append(out, line)
	}
	return out
}

// paneColumn is the cursor row, read properly: what the row is, then the text that the table has to
// cut, then the two things the table has no column for at all — why the row exists, and what
// validation says about it. It is h lines of exactly the pane's width, or nothing when there is no
// pane. Scrolling never touches it: it follows the cursor, which is the thing you moved.
func (m *Model) paneColumn(f frame, h int) []string {
	out := make([]string, h)
	if f.pane == 0 {
		return out
	}
	e, ok := m.focused()
	if !ok {
		return out
	}
	w := f.pane
	var lines []string
	// The pane's title sits in the column head above (see columnHead), so the body opens with the
	// rule under it — the same two-row header the table has: names, then a line, then values.
	lines = append(lines, dimStyle.Render(strings.Repeat("─", w)))

	// The facts the row is filed under, on one line: a label and a split, or a slug and its
	// assertions. Colour is the table's own — amber for a dupe, because a near-twin is a warning.
	switch e.Lane {
	case store.LaneTrigger:
		facts := colYes.Render(e.Label)
		if e.Label == "no" {
			facts = colNo.Render("no")
		}
		lines = append(lines, facts+dimStyle.Render(" · "+e.Split))
		if e.Dupe > 0 {
			lines = append(lines, colDupe.Render(fmt.Sprintf("near-twin cluster #%d", e.Dupe)))
		}
	default:
		slug := e.Slug
		if slug == "" {
			slug = "no slug"
		}
		lines = append(lines, colSlug.Render(fitPlain(slug, w-12))+dimStyle.Render(fmt.Sprintf(" · %d assertions", len(e.Assertions))))
	}

	// The record itself — text, expected output, assertions, files, the fixture dir, preserved
	// keys — is the same document v opens at full width. The pane is that document read through a
	// narrow window, so nothing here is a summary of something v would show.
	rec := recordLines(m.store, e, w)
	if len(rec) > 0 && rec[0] == "" {
		rec = rec[1:]
	}
	lines = append(lines, rec...)

	if issues := m.rowIssues(e); len(issues) > 0 {
		lines = append(lines, "")
		lines = append(lines, issueLines(issues, w, false, 3)...)
	}

	// The pane is a window, not a document: what does not fit is named, with the key that opens
	// the whole of it, rather than pushing the footer off the screen.
	if len(lines) > h {
		hidden := len(lines) - h + 1
		lines = lines[:h]
		lines[h-1] = dimStyle.Render(fmt.Sprintf("… %d more lines · v opens it whole", hidden))
	}
	for i := range out {
		if i < len(lines) {
			out[i] = lines[i]
		}
	}
	return out
}

// rowIssues are the validation findings that belong to one row. Severity is not filtered: a row
// carries whatever was said about it, in the colour it was said in.
func (m *Model) rowIssues(e store.Entry) []store.Issue {
	var out []store.Issue
	for _, is := range m.issues {
		if is.Lane != e.Lane || is.Skill != e.Skill {
			continue
		}
		if is.ID != e.ID && (is.Slug == "" || is.Slug != e.Slug) {
			continue
		}
		out = append(out, is)
	}
	return out
}

// issueLines renders findings as a block: the place the finding is about (when the pane does not
// already say it), then the message wrapped, in the colour of its severity. Both panes go through
// this, so a finding reads the same wherever it is met. The cap is the pane's, not the list's —
// ctrl+v is the findings list.
func issueLines(issues []store.Issue, w int, withWhere bool, limit int) []string {
	var out []string
	for i, is := range issues {
		if i == limit {
			out = append(out, badgeWarn.Render(fmt.Sprintf("… %d more · ctrl+v has all of them", len(issues)-limit)))
			break
		}
		style := badgeWarn
		if is.Severity == store.SeverityError {
			style = statusBad
		}
		if withWhere {
			where := is.Lane
			if is.ID != 0 {
				where += fmt.Sprintf(" #%d", is.ID)
			}
			if is.Slug != "" {
				where += " " + is.Slug
			}
			out = append(out, style.Render(fitPlain("! "+where, w)))
		}
		for j, l := range wrapPlain(is.Msg, w-2) {
			body := "  " + l
			if j == 0 && !withWhere {
				body = "! " + l
			}
			out = append(out, style.Render(fitPlain(body, w)))
		}
	}
	return out
}

// groupLine is a section header: a short rule, then what the section is and how many rows are in
// it. It stops where the text stops — a rule stretched to the terminal edge is a border, and a
// border that carries nothing costs the reader a scan line per section.
func (m *Model) groupLine(it listItem) string {
	label := fmt.Sprintf(" %s · %d ", it.label, it.count)
	return dimStyle.Render("──") + styleGroup.Render(label)
}

// header is the frame the list is read in: three lines — identity, state, and the column head. It
// carries what changes (scope, lane, counts, health) and nothing that does not: the tree's full
// path lives in `?`, because a 60-character invariant repeated on every repaint is the least
// informative thing a screen can spend its top row on.
func (m *Model) header() string {
	line1 := titleStyle.Render("evalctl " + m.ver)
	if m.mode == screenSkills {
		// The skill list is the front door. The lane tabs belong to a lane view; drawing them
		// here advertises a switch that does not apply to what is on screen.
		cases, queries := 0, 0
		for _, r := range m.skills.rows {
			if r.all {
				cases, queries = r.sum.Cases, r.sum.Queries
			}
		}
		line1 += "  " + dimStyle.Render("all skills") +
			"  " + fmt.Sprintf("%d cases · %d queries", cases, queries)
	} else {
		scope := dimStyle.Render("all skills")
		if m.skill != "" {
			scope = titleStyle.Render(m.skill)
		}
		line1 += "  " + scope + "  " + m.tabStrip()
	}

	errs, warns := m.counts()
	badge := statusOk.Render("clean")
	if errs > 0 {
		badge = statusBad.Render(plural(errs, "error"))
	} else if warns > 0 {
		badge = badgeWarn.Render(plural(warns, "warning"))
	}
	var line2 string
	if m.mode == screenSkills {
		line2 = dimStyle.Render(filepath.Base(m.store.Root)) + "  " +
			fmt.Sprintf("%d skill(s)", len(m.skills.rows)-1)
	} else {
		// What is on screen decides what "2 of 32" counts: rows in a lane, iterations on the results
		// tab. A count of something you are not looking at is worse than no count.
		shown := fmt.Sprintf("%d of %d", len(m.rows), len(m.store.Rows(m.lane, store.Filter{Skill: m.skill})))
		if m.tab == tabResults {
			n := 0
			if m.runsList != nil {
				n = len(m.runsList.infos)
			}
			shown = plural(n, "iteration")
		}
		line2 = dimStyle.Render(filepath.Base(m.store.Root)) + "  " + shown
	}
	line2 += "  " + badge
	if n := len(m.chosen); n > 0 {
		line2 += "  " + selMark.Render(fmt.Sprintf("%d selected", n)) + m.selectionBreakdown()
	}
	if m.dirty {
		line2 += "  " + badgeWarn.Render("unsaved")
	}
	if m.runActive {
		line2 += "  " + tabActive.Render("⟳ run in progress")
	}
	if fh := m.filterHint(); fh != "" {
		line2 += "  " + fh
	}
	return line1 + "\n" + line2
}

// tabStrip draws the scope's three views; the active one is bracketed and bright, the others still
// carry their counts, so what is behind a tab is never a surprise. tab and shift+tab walk it. On a
// narrow terminal the labels shorten rather than the line clipping: a header that runs off the edge
// loses its counts, which is the only part of it that changes.
func (m *Model) tabStrip() string {
	var parts []string
	compact := m.width > 0 && m.width < 110
	for _, t := range []viewTab{tabCases, tabQueries, tabResults} {
		label, n := "results", m.resultsCount()
		if t == tabCases {
			label, n = "output cases", len(m.store.Rows(store.LaneOutput, store.Filter{Skill: m.skill}))
		}
		if t == tabQueries {
			label, n = "trigger queries", len(m.store.Rows(store.LaneTrigger, store.Filter{Skill: m.skill}))
		}
		if compact {
			label = map[viewTab]string{tabCases: "out", tabQueries: "trig", tabResults: "res"}[t]
		}
		text := label
		if n >= 0 {
			text = fmt.Sprintf("%s %d", label, n)
		}
		// A run in flight belongs to the results tab: the marker says which view has news coming.
		if t == tabResults && m.runActive {
			text += " ⟳"
		}
		if t == m.tab {
			parts = append(parts, tabActive.Render("["+text+"]"))
			continue
		}
		parts = append(parts, tabIdle.Render(" "+text+" "))
	}
	return strings.Join(parts, " ")
}

// resultsCount is how many iterations the scope has, or -1 when the list has not been read for this
// scope yet: an unknown count is drawn as no count, never as zero.
func (m *Model) resultsCount() int {
	if m.runsList == nil || m.runsList.skill != m.skill {
		return -1
	}
	return len(m.runsList.infos)
}

// selectionBreakdown names where the selection sits, so a trim across four skills is not a
// blind count.
func (m *Model) selectionBreakdown() string {
	if len(m.chosen) == 0 {
		return ""
	}
	counts := map[string]int{}
	for _, e := range m.selectedAcrossLanes() {
		counts[e.Skill]++
	}
	var names []string
	for name := range counts {
		names = append(names, name)
	}
	sortStrings(names)
	var parts []string
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s %d", name, counts[name]))
	}
	return dimStyle.Render(" (" + strings.Join(parts, " · ") + ")")
}

func (m *Model) filterHint() string {
	var parts []string
	if m.filter.Skill != "" && m.skill == "" {
		parts = append(parts, "skill="+m.filter.Skill)
	}
	if m.filter.Label != "" {
		parts = append(parts, "label="+m.filter.Label)
	}
	if m.filter.Split != "" {
		parts = append(parts, "split="+m.filter.Split)
	}
	if q := m.search.Value(); q != "" {
		scope := ""
		if m.searchIn != "" {
			scope = " in " + m.searchIn
		}
		parts = append(parts, "search"+scope+"="+strconv.Quote(q))
	}
	if len(m.sortKeys) > 0 {
		var keys []string
		for _, k := range m.sortKeys {
			arrow := "↑"
			if k.Desc {
				arrow = "↓"
			}
			keys = append(keys, k.Field+arrow)
		}
		parts = append(parts, "sort="+strings.Join(keys, ","))
	}
	if m.groupField != "" {
		parts = append(parts, "group="+m.groupField)
	}
	if m.onlyDup {
		parts = append(parts, "dupes only")
	}
	if len(parts) == 0 {
		return ""
	}
	return dimStyle.Render("[" + strings.Join(parts, " ") + "]")
}

// column is one table column: the store field it shows, its header, its fixed width (0 = the
// flexible text column), how one cell is coloured and whether the column is numeric. The name is
// the store's column name, so the number-key focus, sort, group and search reach the same field
// the table draws.
type column struct {
	name  string
	head  string
	width int
	right bool // numeric: right-aligned, so 1 and 21 end on the same edge
	style lipgloss.Style
	cell  func(store.Entry) string // raw text: padding and colour belong to the table
}

// rowGutter is the cursor/selection mark and the space after it, and it is the ONLY definition of
// the table's left edge. The header and every row are padded from it; duplicating the number (a
// header written as five spaces against a six-cell row) is what silently shifts every column by
// one, which is exactly what this table used to do.
const rowGutter = 2

// columns is the table's column list for what is on screen. Scoped to one skill the SKILL column
// would repeat the header on every row, so it is dropped — and the number keys renumber with it,
// which is why they are read off this list rather than a constant.
func (m *Model) columns() []column {
	cols := laneColumns(m.lane)
	if m.skill == "" {
		return cols
	}
	out := make([]column, 0, len(cols))
	for _, c := range cols {
		if c.name == "skill" {
			continue
		}
		out = append(out, c)
	}
	return out
}

func laneColumns(lane string) []column {
	id := func(e store.Entry) string { return strconv.Itoa(e.ID) }
	dupe := func(e store.Entry) string {
		if e.Dupe == 0 {
			return "-"
		}
		return "#" + strconv.Itoa(e.Dupe)
	}
	text := func(e store.Entry) string { return oneLine(e.Text) }
	if lane == store.LaneOutput {
		return []column{
			{name: "id", head: "ID", width: 4, right: true, style: colID, cell: id},
			{name: "skill", head: "SKILL", width: 20, style: colSkill, cell: func(e store.Entry) string { return e.Skill }},
			{name: "slug", head: "SLUG", width: 24, style: colSlug, cell: func(e store.Entry) string { return e.Slug }},
			{name: "assert", head: "ASR", width: 4, right: true, style: colAsr, cell: func(e store.Entry) string {
				if e.Assert == 0 {
					return "-"
				}
				return strconv.Itoa(e.Assert)
			}},
			{name: "dupe", head: "DUP", width: 4, right: true, style: colDupe, cell: dupe},
			{name: "text", head: "PROMPT", width: 0, style: colNone, cell: text},
		}
	}
	return []column{
		{name: "id", head: "ID", width: 4, right: true, style: colID, cell: id},
		{name: "skill", head: "SKILL", width: 20, style: colSkill, cell: func(e store.Entry) string { return e.Skill }},
		{name: "label", head: "TRIG", width: 4, style: colYes, cell: func(e store.Entry) string {
			// yes/no is a property of the row, not a verdict: a should-not-trigger row is a
			// legitimate test. Colour them alike and let the word carry the meaning, so green
			// stays "passed" and red stays "failed".
			if e.Label == "no" {
				return "no"
			}
			return "yes"
		}},
		{name: "split", head: "SPLIT", width: 11, style: colSplit, cell: func(e store.Entry) string { return e.Split }},
		{name: "dupe", head: "DUP", width: 4, right: true, style: colDupe, cell: dupe},
		{name: "text", head: "QUERY", width: 0, style: colNone, cell: text},
	}
}

// padCell fits a value to its column: cut on a rune boundary if it is long, padded to the column's
// edge if it is short. Numeric columns pad on the left so their digits line up. A width of 0 marks
// the flexible text column, which is fitted by the caller instead.
func padCell(s string, width int, right bool) string {
	if width <= 0 {
		return s
	}
	s = ellipsis(s, width)
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	pad := strings.Repeat(" ", width-w)
	if right {
		return pad + s
	}
	return s + pad
}

// headCell pads a header to its column width and alignment. Padding (never a marker) is what keeps
// the header in line with the rows: the focus, sort and group state is carried by the style and by
// the filter line, both of which cost no characters.
func headCell(label string, width int, right bool) string {
	return padCell(label, width, right)
}

// cellText is one value, padded and coloured. A dash is a placeholder and is always faint:
// "no assertion recorded" must not read as loudly as a count.
func cellText(c column, e store.Entry) string {
	raw := c.cell(e)
	if raw == "" || raw == "-" {
		return dimStyle.Render(padCell("-", c.width, c.right))
	}
	return c.style.Render(padCell(raw, c.width, c.right))
}

func (m *Model) columnHead(f frame) string {
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", rowGutter))
	cols := m.columns()
	for i, c := range cols {
		style := dimStyle
		switch {
		case i == m.focusCol:
			style = headerFocus
		case c.name == m.groupField:
			style = headerGroup
		case m.sortedBy(c.name):
			style = headerSort
		}
		b.WriteString(style.Render(headCell(c.head, c.width, c.right)))
		b.WriteString(" ")
	}
	if f.pane == 0 {
		return b.String()
	}
	// The divider's own cell carries the pane's title, so the column that separates two things also
	// says what the right-hand one is.
	head := padCell(b.String(), f.table, false)
	e, ok := m.focused()
	if !ok {
		return head + dimStyle.Render(paneSep) + padCell("", f.pane, false)
	}
	title := fmt.Sprintf("ROW %d · %s", e.ID, e.Skill)
	return head + dimStyle.Render(paneSep) +
		dimStyle.Render(padCell(ellipsis(title, f.pane), f.pane, false))
}

func (m *Model) sortedBy(name string) bool {
	for _, k := range m.sortKeys {
		if k.Field == name {
			return true
		}
	}
	return false
}

// rowLine is one row: a two-cell gutter (cursor, or selected, or neither), then the columns. There
// is no checkbox column — a table whose every row starts with an empty box spends six characters a
// line saying "not selected", and the box only ever carries information on the rows you did select.
func (m *Model) rowLine(f frame, i int, e store.Entry) string {
	mark := "  "
	switch {
	case i == m.cursor:
		mark = cursorMark.Render("▸ ")
	case m.chosen[m.key(e)]:
		mark = selMark.Render("▪ ")
	}
	cols := m.columns()
	var b strings.Builder
	b.WriteString(mark)
	for _, c := range cols {
		if c.width == 0 {
			b.WriteString(ellipsis(c.cell(e), f.text))
			continue
		}
		b.WriteString(cellText(c, e))
		b.WriteString(" ")
	}
	return b.String()
}

// fitPath fits a path into a width from the left when it cannot fit whole: the leaf is what you are
// reading for, and a right-hand ellipsis throws exactly that away.
func fitPath(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	if w < 4 || len(r) <= w-1 {
		return ellipsis(s, w)
	}
	return "…" + string(r[len(r)-(w-1):])
}

// fitParts joins what fits: parts are ordered most-informative first, and the tail is dropped until
// the line fits rather than the line being cut in the middle of a count.
func fitParts(parts []string, sep string, width int) string {
	for len(parts) > 1 && lipgloss.Width(strings.Join(parts, sep)) > width {
		parts = parts[:len(parts)-1]
	}
	return fitPlain(strings.Join(parts, sep), width)
}

// ellipsis cuts a cell to the room it has, on rune boundaries, so a long prompt never pushes a
// column off the screen.
func ellipsis(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	r := []rune(s)
	if width <= 1 {
		return string(r[:min(width, len(r))])
	}
	if len(r) > width-1 {
		r = r[:width-1]
	}
	return string(r) + "…"
}

// hint is one binding in the footer: the key, what it does, and how droppable it is. A hint line
// cut at the screen edge is how a footer silently deletes the save key, so a narrow terminal drops
// bindings by RANK, never by column.
type hint struct {
	key, act string
	rank     int    // higher = dropped sooner; 0 is never dropped
	help     string // the long form, used by ? — the footer keeps the short one
}

// long is what ? prints for a binding: the footer has to fit on one line, the key map does not.
func (h hint) long() string {
	if h.help != "" {
		return h.help
	}
	return h.act
}

// hintLine renders the bindings that fit. Ranks 0 (save, keys) are unconditional: whatever else
// goes, the operator can always find out how to write and how to ask for help.
func (m *Model) hintLine(hints []hint) string {
	render := func(hs []hint) string {
		parts := make([]string, 0, len(hs))
		for _, h := range hs {
			parts = append(parts, h.key+" "+h.act)
		}
		return strings.Join(parts, " · ")
	}
	live := append([]hint(nil), hints...)
	for {
		line := render(live)
		if m.width <= 0 || lipgloss.Width(line) <= m.width-1 {
			return dimStyle.Render(line)
		}
		worst := -1
		for i, h := range live {
			if h.rank == 0 {
				continue
			}
			if worst < 0 || h.rank >= live[worst].rank {
				worst = i
			}
		}
		if worst < 0 {
			return dimStyle.Render(ellipsis(line, m.width-1))
		}
		live = append(live[:worst], live[worst+1:]...)
	}
}

// tabHints is the verb list for the tab on screen. The footer renders it and ? explains it, both
// from here: a key map that is written twice is a key map that will disagree with itself.
func (m *Model) tabHints() []hint {
	switch m.tab {
	case tabResults:
		return m.resultsHints()
	case tabQueries:
		return append(m.laneHints(), hint{key: "u", act: "dupes", rank: 14, help: "only near-duplicate rows — the trim shortlist"},
			hint{key: "s", act: "split", rank: 15, help: "cycle the split filter (train / validation)"},
			hint{key: "l", act: "label", rank: 15, help: "cycle the label filter (should trigger / should not)"})
	}
	return append(m.laneHints(), hint{key: "u", act: "dupes", rank: 14, help: "only near-duplicate rows — the trim shortlist"})
}

// listHints is ordered by how often a hand reaches for it, and ranked by how little is lost when it
// has to go.
func (m *Model) laneHints() []hint {
	return []hint{
		{key: "space", act: "select", rank: 1, help: "select the row and move down — the trim gesture"},
		{key: "e", act: "edit", rank: 2, help: "edit the row under the cursor"},
		{key: "d", act: "delete", rank: 5, help: "delete the selection, or the row under the cursor — asks first"},
		{key: "/", act: "search", rank: 4, help: "search this lane; with a column focused, only that column"},
		{key: "tab", act: "next tab", rank: 6, help: "walk the scope's views: cases → queries → results"},
		{key: "R", act: "run", rank: 3, help: "run what is selected, or the whole lane of this scope"},
		{key: "n", act: "new", rank: 12, help: "new row in this tab's lane"},
		{key: "v", act: "whole", rank: 10, help: "the row in full at the width of the window"},
		{key: "1–6", act: "column", rank: 9, help: "focus a column — o sorts by it, g groups by it, / searches it"},
		{key: "c", act: "chain", rank: 11, help: "promote: a run's output becomes the next stage's fixture"},
		{key: "t", act: "→trigger", rank: 11, help: "this case's prompt becomes a trigger query"},
		{key: "P", act: "profiles", rank: 13, help: "the four sandboxes: what exists and what is stale"},
		{key: "ctrl+s", act: "save", rank: 0, help: "write both lanes atomically — previous bytes in .evalctl/backups/"},
		{key: "?", act: "keys", rank: 0, help: "this key map"},
		{key: "q", act: "quit", rank: 10, help: "quit — asks once if there is an unsaved edit or a run in flight"},
		// esc keeps rank 0: it is the way out, and it names the level it will actually take.
		{key: "esc", act: m.escLabel(), rank: 0, help: "one level up — the footer always names the level"},
	}
}

// hintFooter is the binding line: the one that says what the keys in your hand do. It spans the
// whole frame, pane included — it is chrome for the screen, not for the table. Each tab advertises
// only its own verbs: a read-only view that lists write keys is a view that lies.
func (m *Model) hintFooter() string {
	switch {
	case m.searching:
		return m.search.View() + dimStyle.Render("   enter apply · esc clear")
	case m.focusedColumnName() != "":
		// The focused-column line replaces the verb list, but esc still leaves this state, so it
		// is named here too: a footer may shorten, never lie by omission.
		return dimStyle.Render(fmt.Sprintf(
			"col %s · o sort · g group · / search it · esc clears", m.focusedColumnName()))
	}
	return m.hintLine(m.tabHints())
}

// statusFooter is the last row of the terminal: position, then whatever just happened. It is here,
// and not under the last row of the list, because a status that moves is a status you have to hunt
// for.
func (m *Model) statusFooter() string {
	status := m.status
	switch {
	case status == "":
		status = dimStyle.Render(m.position())
	case m.statusErr:
		status = statusBad.Render(status)
	default:
		status = statusOk.Render(status)
	}
	return status
}

func (m *Model) position() string {
	if m.tab == tabResults {
		n := 0
		at := 0
		if m.runsList != nil {
			n = len(m.runsList.infos)
			at = m.runsList.cursor + 1
		}
		if n == 0 {
			return "0 / 0"
		}
		return fmt.Sprintf("%d / %d", at, n)
	}
	if len(m.rows) == 0 {
		return "0 / 0"
	}
	return fmt.Sprintf("%d / %d", m.cursor+1, len(m.rows))
}

// helpLines is the whole key map, one binding per line. It is a list and not a painted box: the box
// was 35 rows tall, so on a 30-row terminal its first five lines — the skill-list section, the one
// an operator opens help to find — could not be read, and nothing could scroll them back.
func (m *Model) helpLines() []string {
	row := func(key, act string) string {
		return "  " + dimStyle.Render(fmt.Sprintf("%-14s", key)) + act
	}
	head := func(s string) string { return titleStyle.Render(s) }
	lines := []string{
		titleStyle.Render("evalctl "+m.ver) + "  " + dimStyle.Render(m.store.Root),
		"",
		head("EVERYWHERE"),
		row("tab ⇧tab", "walk the scope's views — cases · queries · results"),
		row("esc", "one level up: "+m.escLabel()),
		row("?", "this key map"),
		row("ctrl+v", "the validation findings, for both lanes of this scope"),
		row("ctrl+s", "write both lanes atomically (previous bytes in .evalctl/backups/)"),
		row("q", "quit — asks once if there is an unsaved edit or a run in flight"),
		"",
		head("FRONT DOOR"),
	}
	for _, h := range m.skillsHints() {
		lines = append(lines, row(h.key, h.long()))
	}
	// The tab sections come from the same tables the footers render, so the map cannot describe a
	// key the footer does not offer (or offer one it does not describe).
	for _, t := range []viewTab{tabCases, tabQueries, tabResults} {
		saved := m.tab
		m.tab = t
		hints := m.tabHints()
		m.tab = saved
		lines = append(lines, "", head(strings.ToUpper(t.String())+" TAB"))
		for _, h := range hints {
			lines = append(lines, row(h.key, h.long()))
		}
		if t == tabResults {
			lines = append(lines, row("(none)", "results are read-only: change the set, then run it again"))
			continue
		}
		lines = append(lines,
			"",
			head("MOVING"),
			row("j k ↑ ↓", "move"),
			row("g G", "first / last row"),
			row("ctrl+d ctrl+u", "half page"),
			"",
			head("COLUMNS"),
			row("1 … 6", "focus a column (the same digit, or 0, drops it)"),
			row("o", "sort by the focused column: ascending → descending → file order"),
			row("g", "group by the focused column — a section header with a count"),
			row("/", "with a column focused, search only that column"),
		)
	}
	lines = append(lines,
		"",
		head("FORMS"),
		row("enter", "next field · in a confirmation, yes"),
		row("alt+enter", "a newline inside a multi-line field"),
		row("shift+tab", "previous field"),
		row("ctrl+s", "apply the edit (the editor) · write (the list)"),
		row("esc", "cancel — on an edited form, twice: the first press says what is at stake"),
		row("a", "inside a run's report: its activity log"),
		row("x", "in the run table: cancel the run"),
		"",
		dimStyle.Render("  ids are never renumbered on delete: result dirs are keyed q<id>-<skill>,"),
		dimStyle.Render("  so a gap is honest and a renumber is a separate act (evalctl renumber)."),
	)
	// Fit every line to the screen: a help line that wraps costs two rows and breaks the column.
	for i, l := range lines {
		if m.width > 2 && lipgloss.Width(l) > m.width-1 {
			lines[i] = ellipsis(l, m.width-1)
		}
	}
	return lines
}

func (m *Model) helpView() string {
	lines := m.helpLines()
	h := m.height - 1 // the last row is the scroll/close hint
	if h < 3 {
		h = 3
	}
	if maxTop := len(lines) - h; m.helpTop > maxTop {
		m.helpTop = max(0, maxTop)
	}
	if m.helpTop < 0 {
		m.helpTop = 0
	}
	end := min(len(lines), m.helpTop+h)
	var b strings.Builder
	for _, l := range lines[m.helpTop:end] {
		b.WriteString(l + "\n")
	}
	pos := fmt.Sprintf("%d–%d / %d", m.helpTop+1, end, len(lines))
	b.WriteString(dimStyle.Render("j k scroll · ? or esc closes") + "  " + dimStyle.Render(pos))
	return b.String()
}

// scrollHelp moves the key map. g/G jump, j/k by a line, ctrl+d/ctrl+u by a screen — the same keys
// the list uses, because help is a view of a long thing like any other.
func (m *Model) scrollHelp(key string) {
	h := max(3, m.height-1)
	switch key {
	case "j", "down":
		m.helpTop += 1
	case "k", "up":
		m.helpTop -= 1
	case "ctrl+d", "pgdown":
		m.helpTop += h
	case "ctrl+u", "pgup":
		m.helpTop -= h
	case "g", "home":
		m.helpTop = 0
	case "G", "end":
		m.helpTop = len(m.helpLines())
	}
	if maxTop := len(m.helpLines()) - h; m.helpTop > maxTop {
		m.helpTop = max(0, maxTop)
	}
	if m.helpTop < 0 {
		m.helpTop = 0
	}
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

func plural(n int, noun string) string { return format.Plural(n, noun) }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func sortStrings(in []string) { sort.Strings(in) }
