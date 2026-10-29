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
)

type screen int

const (
	screenList screen = iota
	screenEditor
	screenConfirm
	screenViewer
	screenRunLauncher
	screenRunProgress
	screenRuns
)

type Model struct {
	store *store.Store
	lane  string

	rows    []store.Entry
	cursor  int
	offset  int
	chosen  map[string]bool // key -> selected
	filter  store.Filter
	onlyDup bool

	search    textinput.Model
	searching bool

	width, height int
	mode          screen
	dirty         bool
	status        string
	statusErr     bool

	editor    *editorState
	confirm   *confirmState
	viewer    *viewerState
	launcher  *launcherState
	progress  *progressState
	runsList  *runsState
	runActive bool
	runEvents chan runner.Event
	help      bool
	quit      bool
	quitArmed bool

	issues []store.Issue
}

// Run starts the TUI and returns the process exit code.
func Run(s *store.Store, lane string) int {
	m := newModel(s, lane)
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
	m := &Model{store: s, lane: lane, chosen: map[string]bool{}, search: ti}
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
	m.rows = m.store.Rows(m.lane, m.visibleFilter())
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

func (m *Model) visibleFilter() store.Filter {
	f := m.filter
	f.Search = m.search.Value()
	f.Dupes = m.onlyDup
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

func (m *Model) clampScroll() {
	h := m.listHeight()
	if h <= 0 {
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *Model) listHeight() int {
	// header (3 lines) + column head (1) + footer (3)
	return m.height - 8
}

func (m *Model) setStatus(msg string, isErr bool) {
	m.status = msg
	m.statusErr = isErr
}

func (m *Model) revalidate() { m.issues = m.store.Validate() }

func (m *Model) counts() (errs, warns int) {
	for _, i := range m.issues {
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
	case "ctrl+s":
		m.save()
		return m, nil
	case "esc":
		if m.help {
			m.help = false
			return m, nil
		}
		if len(m.chosen) > 0 {
			m.chosen = map[string]bool{}
			m.setStatus("selection cleared", false)
		}
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
	case "g", "home":
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
		m.cycleSkill(1)
		return m, nil
	case "F":
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

// ---------------------------------------------------------------- view

var (
	colSkill   = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Width(20)
	colID      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	colSlug    = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Width(24)
	colYes     = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Width(4)
	colNo      = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Width(4)
	colSplit   = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Width(11)
	colDupe    = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Width(4)
	styleOn    = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	styleOff   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleCur   = lipgloss.NewStyle().Background(lipgloss.Color("237"))
	box        = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("237"))
	statusOk   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	statusBad  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	titleStyle = lipgloss.NewStyle().Bold(true)
	dimStyle   = lipgloss.NewStyle().Faint(true)
	// activityTool colours the tool-call lines of a live activity log.
	activityTool = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

func (m *Model) View() string {
	if m.width == 0 {
		return "loading…"
	}
	switch m.mode {
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
	end := min(len(m.rows), m.offset+h)
	if len(m.rows) == 0 {
		b.WriteString(dimStyle.Render("  no rows match" + m.filterHint()))
		b.WriteString("\n")
	}
	for i := m.offset; i < end; i++ {
		line := m.rowLine(i, m.rows[i])
		if i == m.cursor {
			line = styleCur.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString(m.footer())
	return b.String()
}

func (m *Model) header() string {
	errs, warns := m.counts()
	badge := statusOk.Render("clean")
	if errs > 0 {
		badge = statusBad.Render(fmt.Sprintf("@ %d error(s)", errs))
	} else if warns > 0 {
		badge = colDupe.Render(fmt.Sprintf("! %d warning(s)", warns))
	}
	chips := []string{
		titleStyle.Render("evalctl"),
		dimStyle.Render(m.store.Root),
	}
	laneLabel := "trigger queries"
	if m.lane == store.LaneOutput {
		laneLabel = "output cases"
	}
	shown := len(m.rows)
	total := m.laneCount()
	count := fmt.Sprintf("%s · %d/%d", titleStyle.Render(laneLabel), shown, total)
	if n := len(m.chosen); n > 0 {
		count += " · " + styleOn.Render(fmt.Sprintf("%d selected", n)) + m.selectionBreakdown()
	}
	if m.dirty {
		count += " · " + colDupe.Render("unsaved")
	}
	if m.runActive {
		count += " · " + colDupe.Render("⟳ run in progress")
	}
	line1 := strings.Join(chips, "  ")
	line2 := count + "  " + badge + "  " + m.filterHint() + "  " + dimStyle.Render("tab lane")
	return line1 + "\n" + line2
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
	if m.filter.Skill != "" {
		parts = append(parts, "skill="+m.filter.Skill)
	}
	if m.filter.Label != "" {
		parts = append(parts, "label="+m.filter.Label)
	}
	if m.filter.Split != "" {
		parts = append(parts, "split="+m.filter.Split)
	}
	if q := m.search.Value(); q != "" {
		parts = append(parts, "search="+strconv.Quote(q))
	}
	if m.onlyDup {
		parts = append(parts, "dupes only")
	}
	if len(parts) == 0 {
		return ""
	}
	return dimStyle.Render("[" + strings.Join(parts, " ") + "]")
}

func (m *Model) columnHead() string {
	head := dimStyle.Render(fmt.Sprintf("     %-4s %-20s", "ID", "SKILL"))
	if m.lane == store.LaneTrigger {
		head = dimStyle.Render(fmt.Sprintf("     %-4s %-20s %-4s %-11s %-4s", "ID", "SKILL", "TRIG", "SPLIT", "DUP"))
	} else {
		head = dimStyle.Render(fmt.Sprintf("     %-4s %-20s %-24s %-4s", "ID", "SKILL", "SLUG", "DUP"))
	}
	return head + dimStyle.Render("TEXT / PROMPT")
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
	dupe := " "
	if e.Dupe != 0 {
		dupe = fmt.Sprintf("#%d", e.Dupe)
	}
	idCell := colID.Render(fmt.Sprintf("%-4d", e.ID))
	var head string
	if m.lane == store.LaneTrigger {
		label := colYes.Render("yes")
		if e.Label == "no" {
			label = colNo.Render("no")
		}
		head = fmt.Sprintf("%s%s %s %s %s %s %s ", mark, box, idCell, colSkill.Render(e.Skill),
			label, colSplit.Render(e.Split), colDupe.Render(fmt.Sprintf("%-4s", dupe)))
	} else {
		head = fmt.Sprintf("%s%s %s %s %s %s ", mark, box, idCell, colSkill.Render(e.Skill),
			colSlug.Render(e.Slug), colDupe.Render(fmt.Sprintf("%-4s", dupe)))
	}
	width := m.width - lipgloss.Width(head) - 1
	if width < 10 {
		width = 10
	}
	text := oneLine(e.Text)
	if lipgloss.Width(text) > width {
		r := []rune(text)
		if len(r) > width-1 {
			text = string(r[:width-1]) + "…"
		}
	}
	return head + text
}

func (m *Model) footer() string {
	var hints string
	if m.searching {
		hints = m.search.View() + dimStyle.Render("   enter apply · esc clear")
	} else if m.width < 150 {
		hints = dimStyle.Render("space select · e edit · d delete · / search · u dupes · R run · L results · ctrl+s save · ? help")
	} else {
		hints = dimStyle.Render("space select · e edit · n new · d delete · / search · f skill · u dupes · s split · l label · v view · R run · L results · P profiles · ctrl+s save · ? help · q quit")
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
		"  tab          switch lane (trigger queries / output cases)",
		"  j k ↑ ↓      move           g G  top / bottom      ctrl+d ctrl+u  half page",
		"  space        select row (and advance)      a  select all visible      A / esc  clear",
		"  /            search text, slug, why, skill      esc clears it",
		"  f / F        cycle skill filter forward / back",
		"  s            cycle split (train / validation / any)      l  cycle label",
		"  u            show only near-duplicate rows — the trim shortlist",
		"  e enter      edit the focused row           n  new row in this lane",
		"  d x          delete selection (or the focused row); asks first",
		"  v            full text of the focused row",
		"  ctrl+s       write both lanes (atomic; backup in .evalctl/backups/)",
		"",
		titleStyle.Render("  running the set"),
		"  R            run what is selected (or the focused row, or the lane) — form, then enter",
		"  L            results: every iteration under evals/results, enter opens its report.md",
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
