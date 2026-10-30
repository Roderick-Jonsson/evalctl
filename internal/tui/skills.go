// The skill list is the TUI's front door: the set is worked on one stage skill at a time, so the
// thing you choose first is the skill, not a lane. Everything a skill owns — its output cases,
// the fixtures they stage, the trigger rows that test its description, and its validation
// findings — is on one line, and enter opens only that skill.
package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"evalctl/internal/store"
)

// skillRow is one line of the list: a real skill, or the leading "all skills" aggregate that is
// the way back to the unscoped two-lane view.
type skillRow struct {
	sum store.SkillSummary
	all bool
}

type skillsState struct {
	rows      []skillRow
	cursor    int
	offset    int
	search    textinput.Model
	searching bool
	err       string
}

// openSkills builds the list from the tree as it is in memory (unsaved edits included, because a
// count that lags the editor is a count you cannot trust while trimming).
func (m *Model) openSkills() {
	ti := textinput.New()
	ti.Placeholder = "filter skills"
	ti.CharLimit = 80
	ti.Prompt = "/ "
	st := &skillsState{search: ti}

	total := store.SkillSummary{Skill: ""}
	for _, sum := range m.store.Summaries() {
		st.rows = append(st.rows, skillRow{sum: sum})
		total.Cases += sum.Cases
		total.Assertions += sum.Assertions
		total.Graded += sum.Graded
		total.Fixtures += sum.Fixtures
		total.Files += sum.Files
		total.Queries += sum.Queries
		total.Should += sum.Should
		total.ShouldNot += sum.ShouldNot
		total.Errors += sum.Errors
		total.Warnings += sum.Warnings
		total.OnDisk = true
	}
	if len(st.rows) > 0 {
		st.rows = append([]skillRow{{sum: total, all: true}}, st.rows...)
	}
	m.skills = st
	m.mode = screenSkills
	m.returnTo = screenSkills
	m.help = false
	m.status = ""
	// m.skill is kept on purpose: the list marks the skill you last had open, and re-entering it
	// lands you back on that skill's lane rather than starting from the top.
	if m.skill != "" {
		for i, row := range st.rows {
			if !row.all && row.sum.Skill == m.skill {
				st.cursor = i
			}
		}
	}
	m.clampSkillsScroll()
}

func (s *skillsState) filtered() []skillRow {
	needle := strings.ToLower(strings.TrimSpace(s.search.Value()))
	if needle == "" {
		return s.rows
	}
	var out []skillRow
	for _, r := range s.rows {
		name := r.sum.Skill
		if r.all {
			name = "all skills"
		}
		if strings.Contains(strings.ToLower(name), needle) {
			out = append(out, r)
		}
	}
	return out
}

func (s *skillsState) current(rows []skillRow) (skillRow, bool) {
	if s.cursor < 0 || s.cursor >= len(rows) {
		return skillRow{}, false
	}
	return rows[s.cursor], true
}

func (m *Model) clampSkillsScroll() {
	h := m.skillsHeight()
	if h <= 0 || m.skills == nil {
		return
	}
	if m.skills.cursor < m.skills.offset {
		m.skills.offset = m.skills.cursor
	}
	if m.skills.cursor >= m.skills.offset+h {
		m.skills.offset = m.skills.cursor - h + 1
	}
	if m.skills.offset < 0 {
		m.skills.offset = 0
	}
	if last := len(m.skills.filtered()) - h; m.skills.offset > last && last >= 0 {
		m.skills.offset = last
	}
}

// skillsHeight is the room the rows get, under the same frame as a lane view: header (2), the
// list's own head (1), then the hint and the status on the last two rows of the window.
func (m *Model) skillsHeight() int { return gridBody(m.height) }

func (m *Model) updateSkills(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	st := m.skills
	if st == nil {
		m.mode = screenList
		return m, nil
	}
	if st.searching {
		switch msg.String() {
		case "esc":
			st.searching = false
			st.search.SetValue("")
			st.search.Blur()
			st.cursor, st.offset = 0, 0
			m.clampSkillsScroll()
			return m, nil
		case "enter", "down", "tab":
			st.searching = false
			st.search.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		st.search, cmd = st.search.Update(msg)
		st.cursor, st.offset = 0, 0
		m.clampSkillsScroll()
		return m, cmd
	}

	rows := st.filtered()
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
	case "enter", "right", "l":
		row, ok := st.current(rows)
		if !ok {
			return m, nil
		}
		if row.all {
			m.enterSkill("")
		} else {
			m.enterSkill(row.sum.Skill)
		}
		return m, nil
	case "up", "k":
		if st.cursor > 0 {
			st.cursor--
		}
	case "down", "j":
		if st.cursor < len(rows)-1 {
			st.cursor++
		}
	case "pgup", "ctrl+u":
		st.cursor = max(0, st.cursor-m.skillsHeight()/2)
	case "pgdown", "ctrl+d":
		st.cursor = min(len(rows)-1, st.cursor+m.skillsHeight()/2)
	case "home":
		st.cursor = 0
	case "G", "end":
		st.cursor = max(0, len(rows)-1)
	case "/":
		st.searching = true
		st.search.Focus()
		return m, textinput.Blink
	case "esc":
		if m.help {
			m.help = false
			return m, nil
		}
		if st.search.Value() != "" {
			st.search.SetValue("")
			st.cursor, st.offset = 0, 0
		}
	case "?":
		m.help = !m.help
		return m, nil
	case "ctrl+s":
		m.save()
		msg, isErr := m.status, m.statusErr
		m.openSkills()
		m.setStatus(msg, isErr)
		return m, nil
	case "ctrl+v":
		m.viewer = newIssueViewer(m.issues, m.width)
		m.mode = screenViewer
		return m, nil
	case "L":
		// The skill list is the all-skills scope: the skill it remembers stays marked, but L here
		// means every skill's iterations, not the one you last had open. It opens the same results
		// tab the list has, so there is one results view and one set of keys for it.
		m.openResultsScope("")
		return m, nil
	case "P":
		m.openProfiles()
		return m, nil
	case "R":
		// The run table lives above the list; a run started from the picker is still reachable,
		// but there is nothing to start from here — a run is always scoped to a skill's rows.
		if m.runActive {
			m.mode = screenRunProgress
			m.setStatus("a run is already going — this is its table", false)
			return m, nil
		}
		m.setStatus("open a skill first — a run measures that skill's rows", false)
		return m, nil
	}
	m.clampSkillsScroll()
	return m, nil
}

// enterSkill scopes the list to one skill ("" = every skill, the old two-lane view). A skill
// opens on its output cases: they are the thing a skill is worked on for, and the trigger rows
// that test its description are one tab away.
func (m *Model) enterSkill(name string) {
	m.skill = name
	m.filter.Skill = name
	m.filter.Label, m.filter.Split = "", ""
	m.focusCol, m.sortKeys, m.groupField, m.searchIn = -1, nil, "", ""
	m.search.SetValue("")
	m.search.Placeholder = "search " + searchPlaceholder(name)
	m.cursor, m.offset = 0, 0
	// The tab strip and the body are ONE fact: the strip names a lane, so the body has to be that
	// lane. Deriving the tab from the lane here (never the other way round) is what keeps them
	// from drifting: they did once — the aggregate row forced the cases tab while the lane stayed
	// on trigger, so the strip promised "output cases" over a table of 32 trigger queries and tab
	// did nothing but relabel the strip.
	//
	// A named skill is worked on for its cases. The aggregate keeps the lane the caller asked for,
	// because --lane is the only thing that says what an unscoped view shows.
	if name != "" || m.lane == "" {
		m.lane = store.LaneOutput
	}
	m.tab = tabOfLane(m.lane)
	m.tabCursor, m.tabOffset = [3]int{}, [3]int{}
	m.paneFocus, m.paneTop = false, 0
	m.mode = screenList
	m.returnTo = screenList
	m.refresh()
	// The results tab's count is part of the header, so the iterations behind it are read once on
	// arrival rather than on every repaint.
	m.loadRuns()
	// No prose in the status slot on arrival: the header already names the scope, the lane and
	// the counts. The status line is for what just happened, and it must be empty when nothing
	// has.
	m.setStatus("", false)
}

func searchPlaceholder(skill string) string {
	if skill == "" {
		return "query / prompt / slug / why"
	}
	return "prompt / slug / assertion"
}

// ---------------------------------------------------------------- view

// colCases/colAsr are styled in theme.go.

func (m *Model) skillsView() string {
	if m.help {
		return m.helpView()
	}
	st := m.skills
	rows := st.filtered()

	head := append(m.headerLines(), strings.Repeat(" ", rowGutter)+dimStyle.Render(m.skillHead()))
	var body []string
	if len(rows) == 0 {
		body = append(body, "  "+dimStyle.Render("no skill matches"))
	}
	end := min(len(rows), st.offset+m.skillsHeight())
	for i := st.offset; i < end; i++ {
		line := m.skillLine(rows[i], i == st.cursor)
		if i == st.cursor {
			line = selected(line)
		}
		body = append(body, line)
	}

	// The detail block fills the rows the list does not. This screen is five rows of table in a
	// window four times that tall, and the findings behind "2 warnings" are the only thing on it
	// that is currently behind a number. It sits under the list, not beside it: the emptiness here
	// is vertical, a right-hand pane would be a tall empty column against five short rows, and
	// findings are prose, not a column of values.
	if room := m.skillsHeight() - len(body); room >= 4 {
		body = append(body, m.skillsDetail(rows, room)...)
	}
	return fitView(head, body, []string{m.skillsHint(), m.skillsStatus()}, m.height)
}

// skillsDetail is the selected skill spelled out: the counts its one line compresses, then the
// findings that line collapses into "2 warnings". It is cut to the room it has — the frame is the
// window, the block is content.
func (m *Model) skillsDetail(rows []skillRow, room int) []string {
	row, ok := m.skills.current(rows)
	if !ok {
		return nil
	}
	w := max(20, m.width-1)
	sum := row.sum
	name := sum.Skill
	if row.all {
		name = "all skills"
	}

	parts := []string{plural(sum.Cases, "case"), fmt.Sprintf("%d/%d graded", sum.Graded, sum.Cases),
		plural(sum.Assertions, "assertion"), plural(sum.Fixtures, "fixture dir"),
		plural(sum.Files, "file"), plural(sum.Queries, "trigger row"),
		fmt.Sprintf("%d yes · %d no", sum.Should, sum.ShouldNot)}
	out := []string{"", detailRule(name, w), dimStyle.Render(fitParts(parts, " · ", w))}

	issues := m.issuesFor(row)
	if len(issues) == 0 {
		out = append(out, "", statusOk.Render("clean — nothing to fix here"))
	} else {
		out = append(out, "", dimStyle.Render(fmt.Sprintf("FINDINGS (%d)", len(issues))))
		out = append(out, issueLines(issues, w, true, 5)...)
	}

	if len(out) > room {
		out = out[:room]
		out[room-1] = dimStyle.Render(glyphs.Ellipsis)
	}
	return out
}

// detailRule is the section header over the block: a rule that carries the name it belongs to.
// A rule with nothing on it is the one kind of border this tool does not draw.
func detailRule(name string, w int) string {
	rule := strings.Repeat(glyphs.Rule, 2)
	label := rule + " " + name + " "
	if n := w - len([]rune(label)); n < 0 {
		return dimStyle.Render(fitPlain(label, w))
	} else {
		return dimStyle.Render(rule+" ") + titleStyle.Render(name) + dimStyle.Render(" "+strings.Repeat(glyphs.Rule, n))
	}
}

// issuesFor is a skill's own findings; on the aggregate row it is every finding in the tree, which
// is what "all skills" means.
func (m *Model) issuesFor(row skillRow) []store.Issue {
	if row.all {
		return m.issues
	}
	var out []store.Issue
	for _, is := range m.issues {
		if is.Skill == row.sum.Skill {
			out = append(out, is)
		}
	}
	return out
}

// The skill list's columns, in one place: the head and every row are built from these widths, so
// they cannot drift. Each value is a count you can act on — the old cells packed four numbers into
// one field ("10·47f", "32·17/15") and needed a legend to read.
var (
	skillNameWidth = 22
	skillColWidths = []int{5, 6, 4, 5, 5, 5} // cases, graded, fixtures, files, should, should-not
	skillHeads     = []struct {
		head  string
		right bool
	}{
		{"CASES", true}, {"GRADED", true}, {"FIXT", true}, {"FILES", true}, {"TRIG+", true}, {"TRIG-", true},
	}
	// skillDropOrder is the order the counts are given up when the table is wider than the
	// terminal: file counts first, then fixture dirs, then the should-not and should-trigger split.
	// A row is never clipped — a column that cannot fit is dropped whole, the way the key line
	// drops a binding, rather than pushed past the edge where nobody can read it.
	skillDropOrder = []int{3, 2, 5, 4}
	// skillMinHealth is what HEALTH needs for the worst thing it ever says.
	skillMinHealth = 14 // "no evals.json"
)

// skillCols is the columns that fit at this width, in table order: the counts are given up one at a
// time, in skillDropOrder, until the row fits the window.
func (m *Model) skillCols() []int {
	all := []int{0, 1, 2, 3, 4, 5}
	if m.width <= 0 {
		return all
	}
	for k := 0; k <= len(skillDropOrder); k++ {
		dropped := map[int]bool{}
		for _, d := range skillDropOrder[:k] {
			dropped[d] = true
		}
		need := rowGutter + skillNameWidth + 1 + skillMinHealth
		out := make([]int, 0, len(all))
		for _, i := range all {
			if dropped[i] {
				continue
			}
			need += skillColWidths[i] + 1
			out = append(out, i)
		}
		if need <= m.width-1 || k == len(skillDropOrder) {
			return out
		}
	}
	return all
}

func (m *Model) skillHead() string {
	var b strings.Builder
	b.WriteString(padCell("SKILL", skillNameWidth, false) + " ")
	for _, i := range m.skillCols() {
		b.WriteString(padCell(skillHeads[i].head, skillColWidths[i], skillHeads[i].right) + " ")
	}
	return b.String() + "HEALTH"
}

func (m *Model) skillLine(r skillRow, isCursor bool) string {
	name := r.sum.Skill
	if r.all {
		name = "all skills"
	}
	// One gutter, two marks: the cursor (what enter opens) and the skill you last had open. The
	// band alone is 2.2:1 — visible, but not something to aim at under stress.
	mark := "  "
	switch {
	case isCursor:
		mark = cursorMark.Render(glyphs.Cursor)
	case r.sum.Skill == m.skill && !r.all && m.skill != "":
		mark = selMark.Render(glyphs.Selected)
	}

	graded := "-"
	if r.sum.Cases > 0 {
		graded = fmt.Sprintf("%d/%d", r.sum.Graded, r.sum.Cases)
	}
	cells := []string{
		strconv.Itoa(r.sum.Cases),
		graded,
		strconv.Itoa(r.sum.Fixtures),
		strconv.Itoa(r.sum.Files),
		strconv.Itoa(r.sum.Should),
		strconv.Itoa(r.sum.ShouldNot),
	}
	var b strings.Builder
	b.WriteString(mark)
	b.WriteString(colSkill.Render(padCell(name, skillNameWidth, false)) + " ")
	for _, i := range m.skillCols() {
		v := cells[i]
		if v == "-" {
			b.WriteString(dimStyle.Render(padCell(v, skillColWidths[i], true)) + " ")
			continue
		}
		style := colCases
		if i == 1 && r.sum.Graded < r.sum.Cases {
			// Not yet graded is a gap worth seeing, but it is not a failure: amber, not red.
			style = badgeWarn
		}
		b.WriteString(style.Render(padCell(v, skillColWidths[i], true)) + " ")
	}

	health, style := "ok", statusOk
	switch {
	case r.sum.Errors > 0:
		health, style = plural(r.sum.Errors, "error"), statusBad
	case r.sum.Warnings > 0:
		health, style = plural(r.sum.Warnings, "warning"), badgeWarn
	}
	if !r.all && !r.sum.OnDisk {
		health, style = "no evals.json", statusBad
	}
	b.WriteString(style.Render(health))
	return b.String()
}

// skillsHint keeps the same rules as the lane list: fixed keys first, everything else by rank.
func (m *Model) skillsHint() string {
	st := m.skills
	if st.searching {
		return st.search.View() + dimStyle.Render("   enter apply · esc clear")
	}
	return m.hintLine(m.skillsHints())
}

// skillsHints is the front door's verb list: the footer renders it and ? explains it.
func (m *Model) skillsHints() []hint {
	return []hint{
		{key: "enter", act: "open", rank: 0, help: "open the skill under the cursor (· all skills = every skill)"},
		{key: "j k", act: "move", rank: 1, help: "move"},
		{key: "/", act: "filter", rank: 2, help: "filter the skill names"},
		{key: "L", act: "results", rank: 4, help: "every iteration under the tree — the same results tab a scope has"},
		{key: "P", act: "profiles", rank: 6, help: "the four sandboxes: what exists and what is stale"},
		{key: "ctrl+s", act: "save", rank: 0, help: "write both lanes atomically"},
		{key: "?", act: "keys", rank: 0, help: "this key map"},
		{key: "q", act: "quit", rank: 5, help: "quit — asks once if there is an unsaved edit or a run in flight"},
	}
}

// skillsStatus is the last row of the window, like the lane list's: position and whatever just
// happened, then the two whole-tree states — an unsaved edit and a live run — because this is the
// only screen that shows the whole tree.
func (m *Model) skillsStatus() string {
	status := m.status
	switch {
	case status == "":
		status = dimStyle.Render(m.skillsPosition())
	case m.statusErr:
		status = statusBad.Render(status)
	default:
		status = statusOk.Render(status)
	}
	if m.dirty {
		status += "  " + badgeWarn.Render("unsaved — ctrl+s writes")
	}
	if m.runActive {
		status += "  " + tabActive.Render(glyphs.Running+" run in progress — R opens the table")
	}
	return status
}

func (m *Model) skillsPosition() string {
	if m.skills == nil {
		return "0 / 0"
	}
	n := len(m.skills.filtered())
	if n == 0 {
		return "0 / 0"
	}
	return fmt.Sprintf("%d / %d", m.skills.cursor+1, n)
}
