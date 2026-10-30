// The skill list is the TUI's front door: the set is worked on one stage skill at a time, so the
// thing you choose first is the skill, not a lane. Everything a skill owns — its output cases,
// the fixtures they stage, the trigger rows that test its description, and its validation
// findings — is on one line, and enter opens only that skill.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

// skillsHeight is the room the rows get: header (2), the list's own head and rule (2), footer (2).
func (m *Model) skillsHeight() int { return m.height - 7 }

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
		m.openRuns()
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
	m.mode = screenList
	m.returnTo = screenList
	if name != "" {
		m.lane = store.LaneOutput
	}
	m.refresh()
	if name == "" {
		m.setStatus("every skill — tab switches lane, enter a skill to scope down", false)
		return
	}
	sum, _ := m.store.SummaryOf(name)
	m.setStatus(fmt.Sprintf("%s — %d case(s), %d trigger row(s); tab switches lane, esc comes back here",
		name, sum.Cases, sum.Queries), false)
}

func searchPlaceholder(skill string) string {
	if skill == "" {
		return "query / prompt / slug / why"
	}
	return "prompt / slug / assertion"
}

// ---------------------------------------------------------------- view

var (
	colCases = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	colAsr   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	colNone  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func (m *Model) skillsView() string {
	if m.help {
		return m.helpView()
	}
	st := m.skills
	rows := st.filtered()
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n")
	b.WriteString("   " + dimStyle.Render(fmt.Sprintf("%-24s %6s %7s %9s %11s  %s",
		"SKILL", "CASES", "ASR", "FIXTURE", "TRIGGER", "HEALTH")))
	b.WriteString("\n")

	h := m.skillsHeight()
	if h < 3 {
		h = 3
	}
	if len(rows) == 0 {
		b.WriteString(dimStyle.Render("  no skill matches"))
		b.WriteString("\n")
	}
	end := min(len(rows), st.offset+h)
	for i := st.offset; i < end; i++ {
		line := m.skillLine(rows[i])
		if i == st.cursor {
			line = styleCur.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString(m.skillsFooter())
	return b.String()
}

func (m *Model) skillLine(r skillRow) string {
	name := r.sum.Skill
	if r.all {
		name = "· all skills"
	}
	nameCell := colSkill.Render(fmt.Sprintf("%-24s", truncateRunes(name, 24)))

	cases := colCases.Render(fmt.Sprintf("%6s", fmt.Sprintf("%d", r.sum.Cases)))

	asr := colNone.Render(fmt.Sprintf("%7s", "-"))
	if r.sum.Graded > 0 {
		asr = colAsr.Render(fmt.Sprintf("%7s", fmt.Sprintf("%d/%d", r.sum.Graded, r.sum.Cases)))
	} else if r.sum.Cases > 0 {
		asr = dimStyle.Render(fmt.Sprintf("%7s", fmt.Sprintf("0/%d", r.sum.Cases)))
	}

	fix := dimStyle.Render(fmt.Sprintf("%9s", "—"))
	if r.sum.Fixtures > 0 {
		fix = dimStyle.Render(fmt.Sprintf("%9s", fmt.Sprintf("%d·%df", r.sum.Fixtures, r.sum.Files)))
	}

	trig := dimStyle.Render(fmt.Sprintf("%11s", "—"))
	if r.sum.Queries > 0 {
		trig = fmt.Sprintf("%11s", fmt.Sprintf("%d·%d/%d", r.sum.Queries, r.sum.Should, r.sum.ShouldNot))
	}

	health := statusOk.Render(r.sum.Health())
	if r.sum.Errors > 0 {
		health = statusBad.Render(r.sum.Health())
	} else if r.sum.Warnings > 0 {
		health = badgeWarn.Render(r.sum.Health())
	}
	if !r.all && !r.sum.OnDisk {
		health = statusBad.Render("no evals.json")
	}

	mark := "  "
	if r.sum.Skill == m.skill && !r.all && m.skill != "" {
		mark = styleOn.Render("› ")
	}
	return mark + "\u00a0" + nameCell + " " + cases + " " + asr + " " + fix + " " + trig + "  " + health
}

func (m *Model) skillsFooter() string {
	st := m.skills
	var hints string
	switch {
	case st.searching:
		hints = st.search.View() + dimStyle.Render("   enter apply · esc clear")
	case m.width < 110:
		hints = dimStyle.Render("enter open · j/k move · / filter · ? help · ctrl+s save · q quit")
	default:
		hints = dimStyle.Render("enter open a skill · j/k move · / filter · ? help · ctrl+s save · L results · P profiles · q quit")
	}
	status := m.status
	switch {
	case status == "":
		status = dimStyle.Render(m.skillsPosition())
	case m.statusErr:
		status = statusBad.Render(status)
	default:
		status = statusOk.Render(status)
	}
	// The list is also where an unsaved edit and a live run have to stay visible: both are states
	// of the whole tree, and this is the only screen that shows all of it.
	if m.dirty {
		status += "  " + badgeWarn.Render("unsaved — ctrl+s writes")
	}
	if m.runActive {
		status += "  " + badgeWarn.Render("⟳ run in progress — R opens the table")
	}
	return hints + "\n" + status + "\n"
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

// truncateRunes cuts a cell on rune boundaries; the skill names here are directories, so they
// are plain but never guaranteed to be short.
func truncateRunes(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	return string(r[:width-1]) + "…"
}
