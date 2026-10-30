package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"

	"evalctl/internal/markdown"
	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// ---------------------------------------------------------------- delete confirm

type confirmState struct {
	rows        []store.Entry
	withFixture bool
	hasOutput   bool
}

func (m *Model) openConfirm(rows []store.Entry) {
	c := &confirmState{rows: rows}
	for _, e := range rows {
		if e.Lane == store.LaneOutput {
			c.hasOutput = true
		}
	}
	m.confirm = c
	m.mode = screenConfirm
}

// updateConfirm is the one irreversible action in the tool, so it follows the convention every Unix
// hand already has: y confirms, everything else — including a bare Enter — cancels. The Enter key
// is what a tired operator presses without reading, and it must never be the destructive one.
func (m *Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := m.confirm
	if c == nil {
		m.mode = screenList
		return m, nil
	}
	switch msg.String() {
	case "y":
		refs := make([]store.RowRef, 0, len(c.rows))
		touched := map[string]bool{}
		for _, e := range c.rows {
			refs = append(refs, store.RowRef{Lane: e.Lane, Skill: e.Skill, Index: e.Index})
			if e.Lane == store.LaneTrigger {
				touched[store.LaneTrigger] = true
			} else {
				touched[e.Skill] = true
			}
		}
		changes, err := m.store.DeleteRows(refs, c.withFixture)
		if err != nil {
			m.mode = screenList
			m.confirm = nil
			m.setStatus("delete failed: "+err.Error(), true)
			return m, nil
		}
		m.chosen = map[string]bool{}
		m.dirty = true
		m.mode = screenList
		m.confirm = nil
		m.revalidate()
		m.refresh()
		msgText := fmt.Sprintf("deleted %d row(s) in memory", len(c.rows))
		if c.withFixture {
			msgText += fmt.Sprintf(" + fixture dirs (%d change(s))", len(changes))
		}
		m.setStatus(msgText+" — ctrl+s to write", false)
		return m, nil
	case "f":
		if c.hasOutput {
			c.withFixture = !c.withFixture
		}
		return m, nil
	case "esc", "enter", "n", "q":
		m.mode = screenList
		m.confirm = nil
		m.setStatus("delete cancelled — nothing was written", false)
		return m, nil
	}
	// Any other key is ignored rather than read as consent or as a cancel: a mistyped key must not
	// dismiss the one dialog that stands between the operator and a deleted row.
	return m, nil
}

// view is the delete dialog. It prints WHAT is going, by id and slug, because "delete 1 row?" over a
// cursor you cannot see is not enough to say yes to — and it spells the default out.
func (c *confirmState) view(width, height int) string {
	var b strings.Builder
	title := fmt.Sprintf("delete %d rows", len(c.rows))
	if len(c.rows) == 1 {
		title = "delete 1 row"
	}
	b.WriteString(statusBad.Render(title))
	b.WriteString("\n\n")
	limit := min(len(c.rows), max(4, height-8))
	for i := 0; i < limit; i++ {
		e := c.rows[i]
		name := fmt.Sprintf("#%-3d %-20s ", e.ID, e.Slug)
		if e.Lane == store.LaneTrigger {
			name = fmt.Sprintf("#%-3d %-3s %-10s ", e.ID, e.Label, e.Split)
		}
		head := "  " + name
		inner := min(width-2, 110)
		b.WriteString(head + clipLine(e.Text, max(10, inner-2-lipgloss.Width(head))))
		b.WriteString("\n")
	}
	if len(c.rows) > limit {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  … %d more", len(c.rows)-limit)))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if c.hasOutput {
		box := "[ ]"
		if c.withFixture {
			box = "[x]"
		}
		b.WriteString(fmt.Sprintf("  fixtures   %s delete the dirs too     f toggles\n", box))
	}
	b.WriteString("\n")
	b.WriteString("  " + statusBad.Render("y") + " confirm    " +
		dimStyle.Render("enter / esc / n cancel    (ctrl+s writes it; nothing is on disk yet)") + "\n")
	return b.String()
}

func clipLine(s string, n int) string {
	s = oneLine(s)
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// ---------------------------------------------------------------- viewer

type viewerState struct {
	kind   string // "profiles" enables the u = ensure action, "activity" is a live tail
	title  string
	lines  []string
	top    int
	path   string // activity.log behind a live view; re-read on every tick
	follow bool   // pinned to the newest line

	// A markdown document (a report) is kept as its source and laid out for the pane's width, so
	// a resize re-wraps it instead of scrolling a layout made for another window.
	md      string
	mdWidth int

	// run is the iteration a report came from, so the document can switch to that run's activity
	// log: one object, two views. It is nil for every other document.
	run *runner.RunInfo
}

// newMarkdownViewer shows a markdown document — a report, a note — rendered rather than raw.
func newMarkdownViewer(title, text string, width int) *viewerState {
	v := &viewerState{title: title, md: text}
	v.renderAt(width)
	return v
}

// renderAt lays the markdown out for a pane width. Lines already rendered at this width are
// reused: the pane is painted on every keystroke, and re-rendering a report for a resize only
// is what keeps that cheap.
func (v *viewerState) renderAt(width int) {
	if v.md == "" {
		return
	}
	if v.mdWidth == width && v.lines != nil {
		return
	}
	v.lines = markdown.Lines(v.md, max(markdown.MinWidth, width))
	v.mdWidth = width
}

// newIssueViewer lists what validation found, in the same scrollable pane as a row.
func newIssueViewer(issues []store.Issue, width int) *viewerState {
	v := &viewerState{title: fmt.Sprintf("validation · %d finding(s)", len(issues))}
	if len(issues) == 0 {
		v.lines = []string{"  " + statusOk.Render("clean: both lanes match the runner's rules")}
		return v
	}
	for _, i := range issues {
		sev := statusBad.Render("error")
		if i.Severity != store.SeverityError {
			sev = colDupe.Render("warn ")
		}
		where := i.Lane
		if i.Skill != "" {
			where += "/" + i.Skill
		}
		if i.ID != 0 {
			where += fmt.Sprintf(" #%d", i.ID)
		}
		v.lines = append(v.lines, fmt.Sprintf("  %s %-26s %s", sev, where, i.Msg))
	}
	return v
}

// docLine is a line of a record before it is fitted and styled: the styling is applied after the
// cut, because a cut through an escape sequence is a broken screen.
type docLine struct {
	text  string
	style lipgloss.Style
}

// recordLines is one row as a document: every field of the record, laid out for a width. The pane
// and the v view both draw from this, so there is exactly one answer to "what is in this row" —
// the pane is the same document at 50 cells, and v is it at the width of the window.
func recordLines(s *store.Store, e store.Entry, w int) []string {
	var doc []docLine
	section := func(label string, body ...docLine) {
		if len(body) == 0 {
			return
		}
		doc = append(doc, docLine{"", dimStyle}, docLine{label, dimStyle})
		doc = append(doc, body...)
	}
	text := func(label, body string) {
		var lines []docLine
		for _, l := range wrapPlain(body, w) {
			lines = append(lines, docLine{l, lipgloss.NewStyle()})
		}
		section(label, lines...)
	}
	paths := func(label string, list []string) {
		var lines []docLine
		for _, p := range list {
			lines = append(lines, docLine{p, lipgloss.NewStyle()})
		}
		section(label, lines...)
	}
	numbered := func(label string, items []string) {
		var lines []docLine
		for i, a := range items {
			head := fmt.Sprintf("%d. ", i+1)
			for j, l := range wrapPlain(a, w-len(head)) {
				if j == 0 {
					lines = append(lines, docLine{head + l, lipgloss.NewStyle()})
					continue
				}
				lines = append(lines, docLine{strings.Repeat(" ", len(head)) + l, lipgloss.NewStyle()})
			}
		}
		section(label, lines...)
	}

	if e.Lane == store.LaneTrigger {
		q := s.Trigger.Queries[e.Index]
		text("QUERY", q.Query)
		text("WHY", q.Why)
		if len(q.Extra) > 0 {
			text("EXTRA KEYS · kept on save", strings.Join(keysOf(q.Extra), ", "))
		}
	} else {
		sk := s.Skill(e.Skill)
		c := sk.Output.Evals[e.Index]
		text("PROMPT", c.Prompt)
		// An empty expected_output is the thing validation warns about, so it is said out loud
		// rather than skipped the way an empty field would be: a missing section reads as "no
		// such field".
		if strings.TrimSpace(c.ExpectedOutput) == "" {
			section("EXPECTED OUTPUT", docLine{"(empty — grading has nothing to check against)", badgeWarn})
		} else {
			text("EXPECTED OUTPUT", c.ExpectedOutput)
		}
		if len(c.Assertions) > 0 {
			numbered(fmt.Sprintf("ASSERTIONS (%d)", len(c.Assertions)), c.Assertions)
		} else {
			section("ASSERTIONS", docLine{"(none yet — add them once a run has shown what good looks like)", dimStyle})
		}
		if len(c.Files) > 0 {
			paths("FILES · declared", c.Files)
		}
		// The dir is shown relative to the tree the header already names: an absolute path here is
		// half the window's width of the same prefix on every case.
		rel := strings.TrimPrefix(s.FixtureDir(sk.Name, c.Slug), strings.TrimSuffix(s.Root, "/")+"/")
		if files := s.FixtureFiles(sk.Name, c.Slug); files != nil {
			section("FIXTURE", append([]docLine{{rel, dimStyle}}, pathLines(files)...)...)
		} else {
			section("FIXTURE", docLine{rel + " (absent)", dimStyle})
		}
		if len(c.Extra) > 0 {
			text("EXTRA KEYS · kept on save", strings.Join(keysOf(c.Extra), ", "))
		}
	}

	out := make([]string, 0, len(doc))
	for _, d := range doc {
		fitted := d.text
		switch {
		case lipgloss.Width(fitted) <= w:
		case strings.Contains(fitted, "/"):
			fitted = fitPath(fitted, w)
		default:
			fitted = fitPlain(fitted, w)
		}
		out = append(out, d.style.Render(fitted))
	}
	return out
}

func pathLines(list []string) []docLine {
	out := make([]docLine, 0, len(list))
	for _, p := range list {
		out = append(out, docLine{p, lipgloss.NewStyle()})
	}
	return out
}

func newViewer(s *store.Store, e store.Entry, width, height int) *viewerState {
	v := &viewerState{}
	if e.Lane == store.LaneTrigger {
		q := s.Trigger.Queries[e.Index]
		state := "should not trigger"
		if q.ShouldTrigger {
			state = "should trigger"
		}
		v.title = fmt.Sprintf("trigger #%d · %s · %s · %s", q.ID, q.Skill, state, q.Split)
	} else {
		c := s.Skill(e.Skill).Output.Evals[e.Index]
		v.title = fmt.Sprintf("%s case #%d · %s", e.Skill, c.ID, c.Slug)
	}
	lines := recordLines(s, e, max(30, width-6))
	if len(lines) > 0 && lines[0] == "" {
		lines = lines[1:] // the document's first section needs no gap above it
	}
	v.lines = lines
	return v
}

func keysOf(m map[string]json.RawMessage) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func wrapText(s string, width int) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	words := strings.Fields(s)
	line := ""
	for _, w := range words {
		if line == "" {
			line = w
			continue
		}
		if len([]rune(line))+1+len([]rune(w)) > width {
			out = append(out, line)
			line = w
			continue
		}
		line += " " + w
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

func (m *Model) updateViewer(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	v := m.viewer
	if v == nil {
		m.mode = m.back()
		return m, nil
	}
	h := max(4, m.height-6)
	switch msg.String() {
	case "a":
		// A report and its activity log are two views of one run; read the other one from here.
		if v.run != nil {
			m.openRunActivity(*v.run)
		}
	case "esc", "q", "v":
		// A live activity tail belongs to the run table it came from.
		if v.kind == "activity" && m.runActive {
			m.mode = screenRunProgress
		} else {
			m.mode = m.back()
		}
		m.viewer = nil
	case "down", "j":
		if v.top < len(v.lines)-h {
			v.top++
		}
		v.follow = v.top >= len(v.lines)-h
	case "up", "k":
		if v.top > 0 {
			v.top--
		}
		v.follow = false
	case "pgdown", "ctrl+d", " ":
		v.top = min(len(v.lines)-h, v.top+h/2)
		v.follow = v.top >= len(v.lines)-h
	case "pgup", "ctrl+u":
		v.top = max(0, v.top-h/2)
		v.follow = false
	case "g":
		v.top = 0
		v.follow = false
	case "G":
		v.top = max(0, len(v.lines)-h)
		v.follow = true
	case "u":
		if v.kind == "profiles" {
			message := m.ensureProfiles()
			m.openProfiles()
			m.setStatus(message, strings.Contains(message, "failed"))
		}
	}
	return m, nil
}

func (v *viewerState) view(width, height int) string {
	// Four rows of chrome — a title and a gap above, a gap and the foot below — and the body gets the
	// rest. Reserving six for the four this spends left even a full document two rows short of the
	// bottom, so the foot floated mid-screen on the screen read longest.
	h := max(1, height-4)
	v.renderAt(width)
	// A followed log grows under the reader: anchor on the newest line instead of a fixed offset.
	start := v.top
	if v.follow {
		start = len(v.lines)
	}
	if start > len(v.lines)-h {
		start = len(v.lines) - h
	}
	if start < 0 {
		start = 0
	}
	end := min(len(v.lines), start+h)
	// Rows, not a string: the body is the document's window, and any shortfall is padding that
	// fitView owns.
	body := make([]string, 0, h)
	if len(v.lines) == 0 {
		body = append(body, dimStyle.Render("(empty)"))
	}
	body = append(body, v.lines[start:end]...)
	foot := dimStyle.Render(fmt.Sprintf("  %d/%d · j k scroll · esc close", min(end, len(v.lines)), len(v.lines)))
	// Same grid as every other screen: title, gap, body, gap, foot — exactly the window's height.
	return fitView([]string{titleStyle.Render(v.title), ""}, body, []string{"", foot}, height)
}

// refresh re-reads a live activity log. The file is the source of truth: the pane mirrors it.
func (v *viewerState) refresh() {
	if v.path == "" {
		return
	}
	b, err := os.ReadFile(v.path)
	if err != nil {
		return
	}
	v.lines = textLines(string(b))
}
