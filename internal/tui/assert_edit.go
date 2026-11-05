package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"evalctl/internal/model"
)

// The case's assertion list, written where it is read.
//
// Setup is the section that shows what a case declares, so it is the section the list is edited from:
// `e` turns the assertion block into one field, in place — one assertion per line, the way `evalctl
// edit --assertions` takes them. It is not a screen because the record above the list has to stay on
// screen while the sentence it is about is typed, and it is not a strip under the columns any more
// because the list is drawn in the pane and typing it somewhere else made the operator hunt for the
// line they were editing.
//
// It is a huh form for the same reason the query fields are: huh already owns the caret, the
// multi-line area and the scrolling, and a second implementation of those is a second set of bugs in
// the one place a person types.

type assertState struct {
	form   *huh.Form
	active bool
	slug   string
	id     int
	text   string
	path   string // <skill>/evals/evals.json, as the head and the status row name it
	errMsg string
	width  int
	// want is the field's height, fixed when the editor opens: a list that grows the strip while the
	// operator types would move the line under their cursor with every enter.
	want int
}

func (a *assertState) build() {
	lines := model.SplitAssertions(a.text)
	a.want = min(8, max(3, len(lines)+1))
	a.form = huh.NewForm(
		huh.NewGroup(
			huh.NewText().
				Value(&a.text).
				Lines(a.want),
		),
	).
		// The app owns the key bar and ctrl+s; huh's own would say most of it again a line above.
		WithShowHelp(false).
		WithKeyMap(assertKeys).
		WithTheme(huh.ThemeBase())
	a.form.Init()
}

// assertKeys is huh's keymap with the list's one field in mind. Enter is a line in this field: the
// list is lines, and a form that submitted on the first enter would save half an edit. Tab has
// nowhere to go — there is one field — and the app owns ctrl+s and esc, so huh is given neither.
var assertKeys = func() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c"))
	km.Text.NewLine = key.NewBinding(key.WithKeys("enter", "ctrl+j"))
	km.Text.Next = key.NewBinding(key.WithDisabled())
	km.Text.Submit = key.NewBinding(key.WithDisabled())
	return km
}()

// pane draws the list as the setup pane's own block: the field takes the place the assertion list
// had, under the record it belongs to. It is the same field the strip was — one assertion per line,
// ctrl+s writes — and the reason it moved is that the pane is where the list is read, so it is where
// the list is typed.
func (a *assertState) pane(w, h int) []string {
	a.width = w
	a.form.WithWidth(max(20, w-4))
	a.form.WithHeight(a.want)
	lines := []string{"", regionHead(w, "ASSERTIONS", a.path, true)}
	for _, line := range strings.Split(strings.TrimRight(a.form.View(), "\n"), "\n") {
		lines = append(lines, "  "+line)
	}
	if a.errMsg != "" {
		lines = append(lines, "  "+statusBad.Render("! "+a.errMsg))
	}
	return fitRows(lines, h)
}

func (a *assertState) hints() []hint {
	return []hint{{"enter", "a line"}, {"ctrl+s", "write"}, {"esc", "discard"}}
}

func (a *assertState) update(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+s":
		return a.apply(m)
	case "esc":
		m.assertEdit = nil
		m.setStatus("the list is the file's again — nothing was written", false)
		return nil
	}
	mm, cmd := a.form.Update(msg)
	if next, ok := mm.(*huh.Form); ok {
		a.form = next
	}
	return cmd
}

func (a *assertState) updateMsg(msg tea.Msg) tea.Cmd {
	if a.form == nil {
		return nil
	}
	mm, cmd := a.form.Update(msg)
	if next, ok := mm.(*huh.Form); ok {
		a.form = next
	}
	return cmd
}

// apply writes the list through the store, which backs the file up first. An empty list is not an
// empty array: the case is written as one with no assertions yet, the shape the format gives a case
// whose assertions are still unknown.
func (a *assertState) apply(m *Model) tea.Cmd {
	sk := m.store.Skill(m.skill)
	idx := -1
	if sk != nil {
		for i, c := range sk.Output.Evals {
			if c.Slug == a.slug {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		a.errMsg = a.slug + " is no longer in " + a.path + " — esc leaves the editor"
		return nil
	}
	cse := sk.Output.Evals[idx]
	cse.Assertions = model.SplitAssertions(a.text)
	if _, err := m.store.UpdateOutput(m.skill, idx, cse); err != nil {
		a.errMsg = err.Error()
		return nil
	}
	if err := m.store.SaveSkill(m.skill); err != nil {
		a.errMsg = "could not write " + a.path + ": " + err.Error()
		return nil
	}
	m.assertEdit = nil
	m.reloadQuality()
	switch n := len(cse.Assertions); n {
	case 0:
		m.setStatus(a.slug+" declares no assertions now — nothing for the judge to grade", false)
	case 1:
		m.setStatus("wrote 1 assertion · the grades on screen were read against the old list — g re-grades", false)
	default:
		m.setStatus(fmt.Sprintf("wrote %d assertions · the grades on screen were read against the old list — g re-grades", n), false)
	}
	return nil
}

// openAssertEditor opens the list of the case under the cursor. It reads the list from the file
// rather than from the verdicts: an assertion written since the last grade is one the judge has
// never seen, and it still belongs in the field.
func (m *Model) openAssertEditor() tea.Cmd {
	q := m.quality
	if q == nil || q.caseAt() == nil {
		m.setStatus("no case on screen — R runs the first iteration, and the list is written after it", true)
		return nil
	}
	c := q.caseAt()
	a := &assertState{
		slug:  c.Slug,
		id:    c.ID,
		text:  strings.Join(m.caseAssertions(c.Slug), "\n"),
		path:  m.skill + "/evals/evals.json",
		width: m.width,
	}
	a.build()
	a.active = true
	m.assertEdit = a
	if len(model.SplitAssertions(a.text)) == 0 {
		m.setStatus(c.Slug+" declares no assertion yet — one per line, ctrl+s writes them", false)
	} else {
		m.setStatus(fmt.Sprintf("%s · one assertion per line · ctrl+s writes %s", c.Slug, a.path), false)
	}
	return a.form.Init()
}

// caseAssertions is the declared list as the file has it.
func (m *Model) caseAssertions(slug string) []string {
	sk := m.store.Skill(m.skill)
	if sk == nil {
		return nil
	}
	for _, c := range sk.Output.Evals {
		if c.Slug == slug {
			return append([]string(nil), c.Assertions...)
		}
	}
	return nil
}

func (m *Model) assertEditActive() bool { return m.assertEdit != nil && m.assertEdit.active }
