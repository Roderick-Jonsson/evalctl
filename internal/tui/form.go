package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	xansi "github.com/charmbracelet/x/ansi"

	"evalctl/internal/store"
)

// One query, four fields, drawn under the table the row came from.
//
// A query is the ask, who it asks for, the reason it is in the set, and which split it belongs to —
// the third field is the one this tool reads back to you when the row fails, which is why it is a
// field and not a comment, and the fourth is the one that moves a row between the set a revision may
// learn from and the set that judges it. The id is not editable here: ids are the tool's business,
// they index the result directories, and the guided flow has no second place to fix a collision.
//
// There is no editor screen. Every set — the workbench's train table, the validation set, the split
// beside the two sections — opens the same form in its own pane, under its own rows, so the row being
// changed is still read against its neighbours and the failure that says why it matters is still on
// screen. That is why the width and the height here come from the pane and not from the window.
//
// It is a huh form and not a hand-rolled editor because huh already owns the caret, the multi-line
// text area and the select, and a second implementation of those is a second set of bugs in the one
// screen a person types into.

type formState struct {
	form    *huh.Form
	editing bool
	// active is whether the caret is in these fields. The form is always drawn under the table —
	// it is the pane's lower half, not a screen and not a mode — so the only thing focus decides is
	// which set of keys the next keystroke belongs to.
	active   bool
	index    int // store index of the row being edited
	id       int
	split    string
	splitSel string

	label    bool
	labelSel string
	query    string
	why      string
	errMsg   string

	width, height int
}

// splitOptions is every split a row may live in, in the order the tool talks about them: the two the
// loop works, then the fresh queries that are measured once and never steered by.
var splitOptions = []string{store.SplitTrain, store.SplitValidation, store.SplitHoldout}

func (f *formState) build() {
	f.labelSel = labelWord(f.label)
	if !store.KnownSplit(f.split) {
		f.split = store.SplitTrain
	}
	f.splitSel = f.split
	f.form = huh.NewForm(
		huh.NewGroup(
			huh.NewText().
				Title("query").
				Value(&f.query).
				Lines(f.textLines()).
				Validate(validateQuery),
			huh.NewText().
				Title("why").
				Value(&f.why).
				Lines(f.whyLines()),
			// Value before Options, always: huh positions a select's cursor from the bound value when
			// the options are set, so the other order opens every select on its first entry and shows
			// a row's label or split as the wrong one.
			huh.NewSelect[string]().
				Title("should the skill load?").
				Height(f.optLines(2)).
				Value(&f.labelSel).
				Options(huh.NewOptions(labelWord(true), labelWord(false))...),
			huh.NewSelect[string]().
				Title("split").
				Height(f.optLines(len(splitOptions))).
				Value(&f.splitSel).
				Options(huh.NewOptions(splitOptions...)...),
		),
	).
		WithWidth(f.formWidth()).
		// The app owns the key bar; huh's own would say most of it again one line above ours.
		WithShowHelp(false).
		WithKeyMap(formKeys).
		WithTheme(f.theme())
	if f.height > 0 {
		// A pane is shorter than a body, so the group scrolls inside the height it is given rather
		// than being cut off at the bottom by the frame.
		f.form.WithHeight(max(4, f.height))
	}
	// huh paints nothing until it has been updated once: the group renders its fields into a viewport
	// during Update, so a form opened and drawn in the same frame would be a blank pane until the
	// first keystroke. Priming it once means the fields are on screen the moment it opens.
	f.form.Init()
	_, _ = f.form.Update(nil)
}

// theme is huh's own theme, with one difference: when the caret is in the table, the fields that are
// always drawn under it are drawn quietly. huh has no way to blur a form — a field is either the
// focused one or not — so the unfocused look is the focused styles replaced by the blurred ones.
func (f *formState) theme() *huh.Theme {
	th := huh.ThemeBase()
	if !f.active {
		th.Focused = th.Blurred
	}
	return th
}

func (f *formState) formWidth() int { return max(20, f.width-4) }

// optLines is the height of a select: the title and the option under the cursor. The rest of the list
// is one arrow key away, and a pane cannot pay for the whole list twice.
func (f *formState) optLines(options int) int { return max(1, min(options, 2)) }

// textLines is how tall the ask's field is: as tall as the ask needs, so a prompt is read whole and
// not scrolled to its last line, but never so tall that the fields below it fall out of the pane.
func (f *formState) textLines() int {
	need := 2
	if w := max(16, f.formWidth()-4); w > 0 {
		need = len(wrapPlain(oneLine(f.query), w))
	}
	return max(2, min(need, min(6, max(2, f.height/5))))
}

// whyLines is the reason's field, which is one sentence: the rows belong to the ask.
func (f *formState) whyLines() int { return 2 }

// title is the form's head inside a set: the pane above it already says which split is on screen, so
// it names the row and nothing else — the split is a field two lines down.
func (f *formState) title() string {
	switch {
	case f.active && f.editing:
		return fmt.Sprintf("edit q%d", f.id)
	case f.active:
		return fmt.Sprintf("new query · q%d", f.id)
	case f.editing:
		return fmt.Sprintf("q%d", f.id)
	}
	return fmt.Sprintf("new query · q%d", f.id)
}

// want is how many rows the fields need at this width, measured rather than guessed. The frame
// allocates exactly this many, because huh's group paints a viewport and does not scroll it to the
// field under the cursor: a field cut off at the bottom is a field that cannot be reached. The
// viewport pads its view to its own height, so the padding is stripped before the last real row is
// found — otherwise a generous measurement is a measurement of the probe height.
func (f *formState) want() int {
	f.form.WithWidth(f.formWidth())
	f.form.WithHeight(200)
	lines := strings.Split(f.form.View(), "\n")
	last := len(lines) - 1
	for last > 0 && strings.TrimSpace(xansi.Strip(lines[last])) == "" {
		last--
	}
	return last + 1
}

// body is the form drawn in a pane: one head line, then the fields, fitted to the height the set gives
// it. The pane keeps its own title (the set's), so this one carries only what the pane cannot say —
// which row, and whether the last save failed.
func (f *formState) body(w, h int) []string {
	f.width = w
	f.form.WithWidth(f.formWidth())
	// The pane decides the height, every draw: a taller window is more of the form in view, not a
	// different form.
	f.form.WithHeight(max(6, h))
	// The fields are the lower half of the set's pane, not a region beside it: the pane's own head
	// is the one that carries the block when the keys are here, so this line names the row and
	// nothing else. The caret, the "edit q3" wording and the status row's corner are what say the
	// next keystroke is a character rather than a row command.
	lines := []string{"  " + titleStyle.Render(f.title())}
	for _, line := range strings.Split(strings.TrimRight(f.form.View(), "\n"), "\n") {
		lines = append(lines, "  "+line)
	}
	if f.errMsg != "" {
		lines = append(lines, "  "+statusBad.Render("! "+f.errMsg))
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

// focus puts the caret in the fields: the first field takes it, the way a form you tab into does. The
// form is rebuilt rather than re-focused, because the only thing that changes about it is its theme.
func (f *formState) focus() tea.Cmd {
	f.active = true
	f.build()
	return f.form.Init()
}

// unfocus hands the keys back to the table and draws the fields quietly again.
func (f *formState) unfocus() {
	f.active = false
	f.build()
}

// leave gives the keys back to the table and puts the fields back to the row's own values: leaving the
// editor is not a way to write, and a field left half-changed would otherwise be applied to whatever
// row the cursor moved to.
func (f *formState) leave(m *Model) tea.Cmd {
	f.active = false
	m.setStatus("unchanged — the row still holds what the file says", false)
	return nil
}

func (f *formState) update(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+s":
		return f.apply(m)
	case "esc":
		f.unfocus()
		m.setStatus("the fields are the row's again — nothing was written", false)
		return nil
	}
	mm, cmd := f.form.Update(msg)
	if next, ok := mm.(*huh.Form); ok {
		f.form = next
	}
	if f.form.State == huh.StateCompleted {
		return f.apply(m)
	}
	if f.form.State == huh.StateAborted {
		f.unfocus()
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
