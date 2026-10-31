package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
)

// One query, three fields.
//
// A query is the ask, who it asks for, and the reason it is in the set — the third field is the one
// this tool reads back to you when the row fails, which is why it is a field and not a comment. The
// id and the split are not editable here: the section's first step assigns both, so a form filled in there
// cannot quietly write a validation row.
//
// It is a huh form and not a hand-rolled editor because huh already owns the caret, the
// multi-line text area and the select, and a second implementation of those is a second set of bugs
// in the one screen a person types into.

type formState struct {
	form    *huh.Form
	editing bool
	index   int // store index of the row being edited
	id      int
	split   string

	label    bool
	labelSel string
	query    string
	why      string
	errMsg   string
	hint     string

	width, height int
}

func (f *formState) build() {
	f.labelSel = labelWord(f.label)
	f.form = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("should the skill load?").
				Height(2).
				Options(huh.NewOptions("should trigger", "should not trigger")...).
				Value(&f.labelSel),
			huh.NewText().
				Title("query").
				Value(&f.query).
				Lines(f.textLines()).
				Description("the prompt as a user would type it — not a summary of the task").
				Validate(validateQuery),
			huh.NewText().
				Title("why").
				Value(&f.why).
				Lines(3).
				Description("the reason this row is in the set — read back when the row fails"),
		),
	).
		WithWidth(max(40, f.width-6)).
		// The app owns the key bar; huh's own would say most of it again one line above ours.
		WithShowHelp(false).
		WithKeyMap(formKeys).
		WithTheme(huh.ThemeBase())
}

func (f *formState) textLines() int {
	return min(8, max(3, f.height/4))
}

func (f *formState) body(h int) []string {
	title := fmt.Sprintf("new query · q%d · %s", f.id, f.split)
	if f.editing {
		title = fmt.Sprintf("q%d · %s", f.id, f.split)
	}
	lines := []string{
		"",
		"  " + titleStyle.Render(title),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, f.width-4)))),
	}
	view := strings.Split(strings.TrimRight(f.form.View(), "\n"), "\n")
	for _, line := range view {
		lines = append(lines, "  "+line)
	}
	if f.errMsg != "" {
		lines = append(lines, "", "  "+statusBad.Render("! "+f.errMsg))
	}
	return fitRows(lines, h)
}

// facts is the form's own read-out on the status row: which file the save lands in.
func (f *formState) facts() string {
	verb := "adds"
	if f.editing {
		verb = "rewrites"
	}
	return verb + " one row of trigger/trigger-evals.json · backed up first"
}

func (f *formState) hints() []hint {
	return []hint{
		{"tab", "next field"},
		{"ctrl+s", "save"},
		{"esc", "discard"},
	}
}

func (f *formState) update(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+s":
		return f.apply(m)
	case "esc":
		m.form, m.mode = nil, screenFlow
		m.setStatus("discarded — nothing was written", false)
		return nil
	}
	mm, cmd := f.form.Update(msg)
	if next, ok := mm.(*huh.Form); ok {
		f.form = next
	}
	switch f.form.State {
	case huh.StateCompleted:
		return f.apply(m)
	case huh.StateAborted:
		m.form, m.mode = nil, screenFlow
		return nil
	}
	return cmd
}

func (f *formState) updateMsg(msg tea.Msg) tea.Cmd {
	if f.form == nil {
		return nil
	}
	mm, cmd := f.form.Update(msg)
	if next, ok := mm.(*huh.Form); ok {
		f.form = next
	}
	return cmd
}

func (f *formState) resize(w, h int) tea.Cmd {
	f.width, f.height = w, h
	f.build()
	return f.form.Init()
}

func validateQuery(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("a query is required — the router has nothing to match otherwise")
	}
	return nil
}

// keyMap is the binding this app has to offer huh: the app owns esc and ctrl+s, so the form must not
// take them for itself.
var formKeys = func() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c"))
	return km
}()
