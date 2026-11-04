package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"evalctl/internal/runner"
)

// The human's half of the loop, written where it is read.
//
// feedback.json has been on disk since the first iteration — one empty string per case that ran —
// and the dojo's pane has quoted it since the pane existed. Nothing wrote it, so the loop's middle
// was a sentence about a file only a text editor could fill. This is the field: `n` in the dojo opens
// the note of the case under the failure cursor, and ctrl+s writes it into that iteration's
// feedback.json, backed up first.
//
// It is a strip under the dojo's two columns and not a screen: the failed assertion and its evidence
// are what a note is written ABOUT, and a note typed on a screen that has pushed them off the window
// is a note about a memory.

type noteState struct {
	form   *huh.Form
	slug   string
	iter   int
	text   string
	path   string // <skill>-workspace/iteration-N/feedback.json, as the head names it
	errMsg string
	// want is the field's height, fixed when it opens: a list that grows the strip while the
	// operator types would move the line under their cursor with every enter.
	want int
}

func (n *noteState) build() {
	n.want = min(6, max(3, len(wrapPlain(n.text, 60))+1))
	n.form = huh.NewForm(
		huh.NewGroup(
			huh.NewText().
				Value(&n.text).
				Lines(n.want),
		),
	).
		WithShowHelp(false).
		WithKeyMap(assertKeys).
		WithTheme(huh.ThemeBase())
	n.form.Init()
}

// body draws the strip: its head names the case and the file the note lands in, then the field.
func (n *noteState) body(w, h int) []string {
	n.form.WithWidth(max(20, w-4))
	n.form.WithHeight(n.want)
	lines := []string{
		"",
		regionHead(w, "NOTE · "+n.slug, n.path, true),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, w-4)))),
	}
	for _, line := range strings.Split(strings.TrimRight(n.form.View(), "\n"), "\n") {
		lines = append(lines, "  "+line)
	}
	if n.errMsg != "" {
		lines = append(lines, "  "+statusBad.Render("! "+n.errMsg))
	}
	return fitRows(lines, h)
}

func (n *noteState) hints() []hint {
	return []hint{{"enter", "a line"}, {"ctrl+s", "write"}, {"esc", "discard"}}
}

func (n *noteState) update(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+s":
		return n.apply(m)
	case "esc":
		m.quality.note = nil
		m.setStatus("the note is the file's again — nothing was written", false)
		return nil
	}
	mm, cmd := n.form.Update(msg)
	if next, ok := mm.(*huh.Form); ok {
		n.form = next
	}
	return cmd
}

func (n *noteState) updateMsg(msg tea.Msg) tea.Cmd {
	if n.form == nil {
		return nil
	}
	mm, cmd := n.form.Update(msg)
	if next, ok := mm.(*huh.Form); ok {
		n.form = next
	}
	return cmd
}

// apply writes the note through the runner, which backs the iteration's feedback.json up first. An
// empty note is a write that clears the note: the scaffold's empty string is what "no note yet" looks
// like on disk, so the two are the same state.
func (n *noteState) apply(m *Model) tea.Cmd {
	if err := runner.New(m.store).WriteFeedback(m.skill, n.iter, n.slug, n.text); err != nil {
		n.errMsg = err.Error()
		return nil
	}
	m.quality.note = nil
	m.reloadQuality()
	if strings.TrimSpace(n.text) == "" {
		m.setStatus("cleared the note for "+n.slug+" · the old file is in .evalctl/backups/", false)
		return nil
	}
	m.setStatus(fmt.Sprintf("wrote the note for %s · iteration %d · %s", n.slug, n.iter, n.path), false)
	return nil
}

// openNoteEditor opens the note of the failure under the cursor's case.
func (m *Model) openNoteEditor() tea.Cmd {
	q := m.quality
	f := q.failAt()
	if f == nil {
		m.setStatus("no failure under the cursor — a note is written against one case's evidence", true)
		return nil
	}
	c := q.caseOf(f.Case)
	text := ""
	if c != nil {
		text = c.Feedback
	}
	n := &noteState{slug: f.Case, iter: q.iter, text: text,
		path: fmt.Sprintf("%s-workspace/iteration-%d/feedback.json", m.skill, q.iter)}
	n.build()
	q.note = n
	if strings.TrimSpace(text) == "" {
		m.setStatus(f.Case+" has no note yet — ctrl+s writes this one into the iteration", false)
	} else {
		m.setStatus("the note on disk is in the field — ctrl+s writes it back", false)
	}
	return n.form.Init()
}

// noteActive is whether the note field has the caret; the app's key router asks before the dojo's
// keys do, so a `j` in a note is a letter.
func (m *Model) noteActive() bool { return m.quality != nil && m.quality.note != nil }
