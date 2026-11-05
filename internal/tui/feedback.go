package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"evalctl/internal/runner"
)

// The human's half of the loop, written where it is read.
//
// feedback.json has been on disk since the first iteration — one string per case that ran — and the
// dojo's pane has quoted it since the pane existed. Nothing wrote it, so the loop's middle was a
// sentence about a file only a text editor could fill. This is the field: `f` in the dojo opens the
// feedback of the case under the cursor, and ctrl+s writes it into that iteration's feedback.json,
// backed up first.
//
// It is called feedback and not a note because it has a shape the guide gives it: it names what is
// wrong with the OUTPUT ("the chart is missing axis labels" is actionable; "looks bad" is not), it is
// written per case, and an empty string is a verdict of its own — the output looked fine, and that
// case passed the human review. What the iterations are improved from is the cases carrying a
// complaint.
//
// It is a strip under the dojo's two columns and not a screen: the output, the failed assertion and
// the evidence are what the feedback is written ABOUT, and feedback typed on a screen that has pushed
// them off the window is feedback about a memory.

type feedbackState struct {
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

// noteKeys is huh's keymap for a field of sentences: enter is a line, tab has nowhere to go (there is
// one field), and the app owns ctrl+s and esc, so huh is given neither.
var noteKeys = func() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c"))
	km.Text.NewLine = key.NewBinding(key.WithKeys("enter", "ctrl+j"))
	km.Text.Next = key.NewBinding(key.WithDisabled())
	km.Text.Submit = key.NewBinding(key.WithDisabled())
	return km
}()

func (fb *feedbackState) build() {
	fb.want = min(6, max(3, len(wrapPlain(fb.text, 60))+1))
	fb.form = huh.NewForm(
		huh.NewGroup(
			huh.NewText().
				Value(&fb.text).
				Lines(fb.want),
		),
	).
		WithShowHelp(false).
		WithKeyMap(noteKeys).
		WithTheme(huh.ThemeBase())
	fb.form.Init()
}

// body draws the strip: its head names the case and the file the feedback lands in, then the field.
func (fb *feedbackState) body(w, h int) []string {
	fb.form.WithWidth(max(20, w-4))
	fb.form.WithHeight(fb.want)
	lines := []string{
		"",
		regionHead(w, "FEEDBACK · "+fb.slug, fb.path, true),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, w-4)))),
	}
	for _, line := range strings.Split(strings.TrimRight(fb.form.View(), "\n"), "\n") {
		lines = append(lines, "  "+line)
	}
	if fb.errMsg != "" {
		lines = append(lines, "  "+statusBad.Render("! "+fb.errMsg))
	}
	return fitRows(lines, h)
}

func (fb *feedbackState) hints() []hint {
	return []hint{{"enter", "a line"}, {"ctrl+s", "write"}, {"esc", "discard"}}
}

func (fb *feedbackState) update(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+s":
		return fb.apply(m)
	case "esc":
		m.quality.feedback = nil
		m.setStatus("the feedback is the file's again — nothing was written", false)
		return nil
	}
	mm, cmd := fb.form.Update(msg)
	if next, ok := mm.(*huh.Form); ok {
		fb.form = next
	}
	return cmd
}

func (fb *feedbackState) updateMsg(msg tea.Msg) tea.Cmd {
	if fb.form == nil {
		return nil
	}
	mm, cmd := fb.form.Update(msg)
	if next, ok := mm.(*huh.Form); ok {
		fb.form = next
	}
	return cmd
}

// apply writes the note through the runner, which backs the iteration's feedback.json up first. An
// empty note is a write that clears the note: the scaffold's empty string is what "no note yet" looks
// like on disk, so the two are the same state.
func (fb *feedbackState) apply(m *Model) tea.Cmd {
	if err := runner.New(m.store).WriteFeedback(m.skill, fb.iter, fb.slug, fb.text); err != nil {
		fb.errMsg = err.Error()
		return nil
	}
	m.quality.feedback = nil
	m.reloadQuality()
	if strings.TrimSpace(fb.text) == "" {
		m.setStatus("cleared the feedback for "+fb.slug+" · the old file is in .evalctl/backups/", false)
		return nil
	}
	m.setStatus(fmt.Sprintf("wrote the feedback for %s · iteration %d · %s", fb.slug, fb.iter, fb.path), false)
	return nil
}

// openFeedbackEditor opens the feedback of the case under the cursor. Feedback is written against the
// CASE and never against a verdict: an assertion can pass and the work still be wrong, which is the
// one thing the judge cannot tell the loop and the human can.
func (m *Model) openFeedbackEditor() tea.Cmd {
	q := m.quality
	c := q.caseAt()
	if c == nil {
		m.setStatus("no case under the cursor — feedback is written against one case", true)
		return nil
	}
	fb := &feedbackState{slug: c.Slug, iter: q.iter, text: c.Feedback,
		path: fmt.Sprintf("%s-workspace/iteration-%d/feedback.json", m.skill, q.iter)}
	fb.build()
	q.feedback = fb
	if strings.TrimSpace(c.Feedback) == "" {
		m.setStatus(c.Slug+" has no feedback yet — ctrl+s writes this one into the iteration", false)
	} else {
		m.setStatus("the feedback on disk is in the field — ctrl+s writes it back", false)
	}
	return fb.form.Init()
}

// feedbackActive is whether the feedback field has the caret; the app's key router asks before the
// dojo's keys do, so a `j` in the field is a letter.
func (m *Model) feedbackActive() bool { return m.quality != nil && m.quality.feedback != nil }
