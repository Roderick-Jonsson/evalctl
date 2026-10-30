package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"

	"evalctl/internal/markdown"
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

func newViewer(s *store.Store, e store.Entry, width, height int) *viewerState {
	v := &viewerState{}
	wrap := max(30, width-6)
	add := func(label, text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		v.lines = append(v.lines, dimStyle.Render(label))
		for _, para := range strings.Split(strings.TrimSpace(text), "\n") {
			for _, line := range wrapText(para, wrap) {
				v.lines = append(v.lines, "  "+line)
			}
		}
		v.lines = append(v.lines, "")
	}

	if e.Lane == store.LaneTrigger {
		q := s.Trigger.Queries[e.Index]
		state := "should not trigger"
		if q.ShouldTrigger {
			state = "should trigger"
		}
		v.title = fmt.Sprintf("trigger #%d · %s · %s · %s", q.ID, q.Skill, state, q.Split)
		add("query", q.Query)
		add("why", q.Why)
		if len(q.Extra) > 0 {
			add("extra keys (preserved on save)", strings.Join(keysOf(q.Extra), ", "))
		}
		return v
	}

	sk := s.Skill(e.Skill)
	c := sk.Output.Evals[e.Index]
	v.title = fmt.Sprintf("%s case #%d · %s", sk.Name, c.ID, c.Slug)
	add("prompt", c.Prompt)
	add("expected_output", c.ExpectedOutput)
	if len(c.Assertions) > 0 {
		var b strings.Builder
		for i, a := range c.Assertions {
			b.WriteString(fmt.Sprintf("%d. %s", i+1, a))
			if i < len(c.Assertions)-1 {
				b.WriteString("\n")
			}
		}
		add(fmt.Sprintf("assertions (%d)", len(c.Assertions)), b.String())
	} else {
		add("assertions", "(none yet — add them once a run has shown what good looks like)")
	}
	if len(c.Files) > 0 {
		add("files", strings.Join(c.Files, "\n"))
	} else {
		add("files", "(none — the run root starts empty)")
	}
	dir := s.FixtureDir(sk.Name, c.Slug)
	if files := s.FixtureFiles(sk.Name, c.Slug); files != nil {
		add("fixture dir "+dir, strings.Join(files, "\n"))
	} else {
		add("fixture dir", dir+"\n(absent)")
	}
	if len(c.Extra) > 0 {
		add("extra keys (preserved on save)", strings.Join(keysOf(c.Extra), ", "))
	}
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
	h := max(4, height-6)
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
	body := strings.Join(v.lines[start:end], "\n")
	if len(v.lines) == 0 {
		body = dimStyle.Render("(empty)")
	}
	foot := dimStyle.Render(fmt.Sprintf("  %d/%d · j k scroll · esc close", min(end, len(v.lines)), len(v.lines)))
	return titleStyle.Render(v.title) + "\n\n" + body + "\n\n" + foot
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
