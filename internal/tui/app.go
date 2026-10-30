// Package tui is the interactive face of evalctl: a filterable, multi-select table over both
// eval lanes with in-place editing, plus the run form, the live run table and the results
// browser. It shares store and runner with the CLI, so the rules about ids, slugs and fixtures
// are enforced in exactly one place.
package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
	screenRuns
	screenPromote
)

type Model struct {
	store *store.Store
	lane  string
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
	m := &Model{store: s, lane: lane, ver: version.Resolve().Short(), chosen: map[string]bool{},
		search: ti, focusCol: -1, mode: screenList}
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

func (m *Model) listHeight() int {
	// header (3 lines) + column head (1) + footer (3)
	return m.height - 8
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
		case screenRuns:
			return m.updateRuns(msg)
		case screenPromote:
			return m.updatePromote(msg)
		}
		return m.updateList(msg)
	case runEventMsg:
		m.applyEvent(msg.event)
		return m, waitForEvent(m.runEvents)
	case runDoneMsg:
		m.runActive = false
		if m.progress != nil {
			if m.progress.err == nil && m.progress.summary != "" {
				m.setStatus(m.progress.summary, false)
			}
		}
		m.revalidate()
		m.refresh()
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

	// Number keys focus a column; the header lights up and o / g / / then act on that column.
	if n, ok := columnDigit(msg.String()); ok {
		m.focusColumn(n)
		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c":
		if m.runActive && msg.String() != "ctrl+c" && !m.quitArmed {
			m.quitArmed = true
			m.setStatus("a run is in progress — q again cancels it and quits, R opens the table", true)
			return m, nil
		}
		if m.dirty && msg.String() != "ctrl+c" && !m.quitArmed {
			m.quitArmed = true
			m.setStatus("unsaved changes in memory — ctrl+s saves, q again discards them", true)
			return m, nil
		}
		if m.progress != nil && m.progress.cancel != nil {
			m.progress.cancel()
		}
		return m, tea.Quit
	case "R":
		if m.runActive {
			m.mode = screenRunProgress
			m.setStatus("a run is already going — this is its table", false)
			return m, nil
		}
		m.openLauncher()
		return m, nil
	case "L":
		m.openRuns()
		return m, nil
	case "P":
		m.openProfiles()
		return m, nil
	case "c":
		m.openPromote()
		return m, nil
	case "t":
		m.promotePromptToTrigger()
		return m, nil
	case "ctrl+s":
		m.save()
		return m, nil
	case "esc":
		if m.focusCol >= 0 {
			m.focusCol = -1
			m.setStatus("column focus cleared", false)
			return m, nil
		}
		if m.help {
			m.help = false
			return m, nil
		}
		if len(m.chosen) > 0 {
			m.chosen = map[string]bool{}
			m.setStatus("selection cleared", false)
			return m, nil
		}
		m.openSkills()
		return m, nil
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
		if m.focusCol >= 0 {
			m.toggleGroup()
			return m, nil
		}
		m.cursor = 0
		m.clampScroll()
		return m, nil
	case "?":
		m.help = !m.help
		return m, nil
	case "ctrl+v":
		m.viewer = newIssueViewer(m.issues, m.width)
		m.mode = screenViewer
		return m, nil
	case "tab":
		if m.lane == store.LaneTrigger {
			m.lane = store.LaneOutput
			m.filter.Label, m.filter.Split = "", ""
		} else {
			m.lane = store.LaneTrigger
		}
		m.focusCol = -1
		m.dropLaneColumns()
		m.cursor, m.offset = 0, 0
		m.refresh()
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
	case screenRuns:
		if m.runsList != nil {
			return m.runsView()
		}
	case screenPromote:
		if m.promote != nil {
			return m.promoteView()
		}
	}
	return m.listView()
}

func (m *Model) listView() string {
	if m.help {
		return m.helpView()
	}
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n")
	b.WriteString(m.columnHead())
	b.WriteString("\n")

	h := m.listHeight()
	if h < 3 {
		h = 3
	}
	items := m.items()
	if len(m.rows) == 0 {
		b.WriteString(dimStyle.Render("  no rows match" + m.filterHint()))
		b.WriteString("\n")
	}
	end := min(len(items), m.offset+h)
	for i := m.offset; i < end; i++ {
		it := items[i]
		if it.row < 0 {
			b.WriteString(m.groupLine(it))
			b.WriteString("\n")
			continue
		}
		line := m.rowLine(it.row, m.rows[it.row])
		if it.row == m.cursor {
			line = selected(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString(m.footer())
	return b.String()
}

// groupLine is a section header: what the section is, how many rows are in it, and a rule that
// reaches the edge so a scrolled list still shows where one section ends.
func (m *Model) groupLine(it listItem) string {
	lead := "  ──"
	label := fmt.Sprintf(" %s · %d ", it.label, it.count)
	rule := strings.Repeat("─", max(0, m.width-lipgloss.Width(lead)-lipgloss.Width(label)-1))
	return dimStyle.Render(lead) + styleGroup.Render(label) + dimStyle.Render(rule)
}

// header is the frame the list is read in: which build, which skill, and the two lanes that
// skill owns with their counts — the switch you make is a tab, not a mode you have to discover.
func (m *Model) header() string {
	scope := dimStyle.Render("· all skills")
	if m.skill != "" {
		scope = titleStyle.Render("◆ " + m.skill)
	}
	line1 := strings.Join([]string{
		titleStyle.Render("evalctl " + m.ver),
		scope,
		m.laneTabs(),
	}, "  ")

	errs, warns := m.counts()
	badge := statusOk.Render("clean")
	if errs > 0 {
		badge = statusBad.Render(fmt.Sprintf("@ %d error(s)", errs))
	} else if warns > 0 {
		badge = badgeWarn.Render(fmt.Sprintf("! %d warning(s)", warns))
	}
	shown := fmt.Sprintf("%d of %d", len(m.rows), len(m.store.Rows(m.lane, store.Filter{Skill: m.skill})))
	line2 := dimStyle.Render(m.store.Root) + "  " + shown + "  " + badge
	if n := len(m.chosen); n > 0 {
		line2 += " · " + styleOn.Render(fmt.Sprintf("%d selected", n)) + m.selectionBreakdown()
	}
	if m.dirty {
		line2 += " · " + badgeWarn.Render("unsaved")
	}
	if m.runActive {
		line2 += " · " + badgeWarn.Render("⟳ run in progress")
	}
	if fh := m.filterHint(); fh != "" {
		line2 += "  " + fh
	}
	return line1 + "\n" + line2
}

// laneTabs draws both lanes of the current scope; the active one is bracketed and bright, the
// other still carries its count so the number of trigger rows behind it is never a surprise.
func (m *Model) laneTabs() string {
	var parts []string
	for _, lane := range []string{store.LaneOutput, store.LaneTrigger} {
		label := "trigger queries"
		if lane == store.LaneOutput {
			label = "output cases"
		}
		n := len(m.store.Rows(lane, store.Filter{Skill: m.skill}))
		text := fmt.Sprintf("%s %d", label, n)
		if lane == m.lane {
			parts = append(parts, tabActive.Render("["+text+"]"))
			continue
		}
		parts = append(parts, tabIdle.Render(" "+text+" "))
	}
	return strings.Join(parts, " ")
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
// flexible text column) and how one cell is styled. The name is the store's column name, so the
// number-key focus, sort, group and search reach the same field the table draws.
type column struct {
	name  string
	head  string
	width int
	cell  func(store.Entry) string
}

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
	id := func(e store.Entry) string { return colID.Render(fmt.Sprintf("%-4d", e.ID)) }
	skill := func(e store.Entry) string { return colSkill.Render(e.Skill) }
	dupe := func(e store.Entry) string {
		v := "-"
		if e.Dupe != 0 {
			v = "#" + strconv.Itoa(e.Dupe)
		}
		return colDupe.Render(fmt.Sprintf("%-4s", v))
	}
	text := func(e store.Entry) string { return oneLine(e.Text) }
	if lane == store.LaneOutput {
		return []column{
			{name: "id", head: "ID", width: 4, cell: id},
			{name: "skill", head: "SKILL", width: 20, cell: skill},
			{name: "slug", head: "SLUG", width: 24, cell: func(e store.Entry) string { return colSlug.Render(e.Slug) }},
			{name: "assert", head: "ASR", width: 4, cell: func(e store.Entry) string {
				if e.Assert == 0 {
					return colNone.Render("-   ")
				}
				return colAsr.Render(fmt.Sprintf("%-4d", e.Assert))
			}},
			{name: "dupe", head: "DUP", width: 4, cell: dupe},
			{name: "text", head: "PROMPT", width: 0, cell: text},
		}
	}
	return []column{
		{name: "id", head: "ID", width: 4, cell: id},
		{name: "skill", head: "SKILL", width: 20, cell: skill},
		{name: "label", head: "TRIG", width: 4, cell: func(e store.Entry) string {
			if e.Label == "no" {
				return colNo.Render("no")
			}
			return colYes.Render("yes")
		}},
		{name: "split", head: "SPLIT", width: 11, cell: func(e store.Entry) string { return colSplit.Render(e.Split) }},
		{name: "dupe", head: "DUP", width: 4, cell: dupe},
		{name: "text", head: "QUERY", width: 0, cell: text},
	}
}

// headCell pads a header to its column width. Padding (never a marker) is what keeps the header
// in line with the rows: the focus, sort and group state is carried by the style and by the
// filter line, both of which cost no characters.
func headCell(label string, width int) string {
	w := lipgloss.Width(label)
	if width <= 0 || w >= width {
		return label
	}
	return label + strings.Repeat(" ", width-w)
}

func (m *Model) columnHead() string {
	var b strings.Builder
	b.WriteString("     ") // the cursor mark and the checkbox gutter, exactly as a row
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
		b.WriteString(style.Render(headCell(c.head, c.width)))
		b.WriteString(" ")
	}
	return b.String()
}

func (m *Model) sortedBy(name string) bool {
	for _, k := range m.sortKeys {
		if k.Field == name {
			return true
		}
	}
	return false
}

func (m *Model) rowLine(i int, e store.Entry) string {
	mark := "  "
	if i == m.cursor {
		mark = styleOn.Render("▸ ")
	} else if m.chosen[m.key(e)] {
		mark = styleOn.Render("▌ ")
	}
	box := styleOff.Render("[ ]")
	if m.chosen[m.key(e)] {
		box = styleOn.Render("[x]")
	}
	cols := m.columns()
	fixed := 6 // mark, box, the space after the box
	for _, c := range cols {
		if c.width > 0 {
			fixed += c.width + 1
		}
	}
	textWidth := m.width - fixed - 1
	if textWidth < 10 {
		textWidth = 10
	}
	var b strings.Builder
	b.WriteString(mark)
	b.WriteString(box)
	b.WriteString(" ")
	for _, c := range cols {
		if c.width == 0 {
			b.WriteString(ellipsis(c.cell(e), textWidth))
			continue
		}
		b.WriteString(c.cell(e))
		b.WriteString(" ")
	}
	return b.String()
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

func (m *Model) footer() string {
	var hints string
	if m.searching {
		hints = m.search.View() + dimStyle.Render("   enter apply · esc clear")
	} else if name := m.focusedColumnName(); name != "" {
		hints = dimStyle.Render(fmt.Sprintf(
			"col %s ▸ o sort · g group · / search this column · 1–%d another · 0 / esc clear",
			name, len(m.columns())))
	} else if m.width < 150 {
		hints = dimStyle.Render("space select · e edit · d delete · / search · tab lane · v view · R run · c chain · t →trigger · L results · ctrl+s save · ? help · esc skills")
	} else {
		scope := "f skill"
		if m.skill != "" {
			scope = "esc skills"
		}
		hints = dimStyle.Render("space select · e edit · n new · d delete · / search · 1–6 column (o sort · g group) · tab lane · u dupes · s split · l label · v view · R run · c chain · t →trigger · L results · P profiles · ctrl+s save · ? help · q quit · " + scope)
	}
	status := m.status
	if status == "" {
		status = dimStyle.Render(m.position())
	} else if m.statusErr {
		status = statusBad.Render(status)
	} else {
		status = statusOk.Render(status)
	}
	return hints + "\n" + status + "\n"
}

func (m *Model) position() string {
	if len(m.rows) == 0 {
		return "0 / 0"
	}
	return fmt.Sprintf("%d / %d", m.cursor+1, len(m.rows))
}

func (m *Model) helpView() string {
	lines := []string{
		titleStyle.Render("evalctl — keys"),
		"",
		titleStyle.Render("  the skill list (where it opens)"),
		"  j k ↑ ↓      move           enter / →  open a skill",
		"  /            filter the skill names      esc  clear the filter",
		"  · all skills  the first row: every skill, both lanes, as before",
		"  L / P        results browser / profiles, from here too",
		"",
		titleStyle.Render("  inside a skill"),
		"  tab          its output cases ⇄ its trigger queries (the header carries both counts)",
		"  esc          back to the skill list (after it has cleared a column focus or a selection)",
		"  space        select row (and advance)      a  select all visible      A / esc  clear",
		"  e enter      edit the focused row           n  new row in this lane, skill prefilled",
		"  v            full text: prompt, expected_output, files, assertions",
		"  d x          delete selection (or the focused row); asks first",
		"  /            search prompt / slug / assertion      esc clears it",
		"  u            show only near-duplicate rows — the trim shortlist",
		"  s / l        cycle split / label (trigger lane)",
		"  ctrl+s       write both lanes (atomic; backup in .evalctl/backups/)",
		"",
		dimStyle.Render("  everything else below works the same in a skill and in · all skills"),
		"",
		"  j k ↑ ↓      move           g G  top / bottom      ctrl+d ctrl+u  half page",
		titleStyle.Render("  columns"),
		"  1 … 6        focus a column of this lane (again, or 0 / esc, drops it)",
		"  o            sort by the focused column: ascending → descending → file order",
		"  g            group by the focused column: a section header with a count each",
		"  /            with a column focused, search only that column",
		dimStyle.Render("  sorting and grouping are the view only — the row key never changes, so a"),
		dimStyle.Render("  selection, a delete or a run acts on the same rows in any order."),
		"",
		titleStyle.Render("  running the set"),
		"  R            run what is selected (or the whole lane of this skill) — form, then enter",
		"  c            chain: promote a run's output into the next stage's fixture (output lane)",
		"  t            a case's prompt becomes a trigger query — does that request pick the skill?",
		"  L            results: every iteration, per skill workspace, enter opens its report.md rendered",
		"  P            profiles: check the four sandboxes, u runs ensure on what fails",
		"  esc          in a run table: back to the list, the run keeps going; x cancels it",
		"  v / a        agent activity: v in the run table (live), a in the results browser",
		"",
		"  q            quit (warns once if unsaved)",
		"",
		dimStyle.Render("  ids are never renumbered on delete: result dirs are keyed q<id>-<skill>,"),
		dimStyle.Render("  so a gap is honest and a renumber is a separate, deliberate act (evalctl renumber)."),
		"",
		dimStyle.Render("  ctrl+v shows the validation issues · ? or esc closes this"),
	}
	return box.Width(min(m.width-2, 96)).Render(strings.Join(lines, "\n"))
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

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
