package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"evalctl/internal/model"
	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// Making and unmaking a case, from the view that declares the set.
//
// The pair is the sections pane's own vocabulary — `n` new, `d` delete — because it is the same set
// in a different shape, and a second word for adding a row is a second thing to learn.
//
// `n` asks for the two fields a case cannot be written without — the prompt and the expected output —
// in the pane, the way the assertion list is typed in the pane. The row is written when ctrl+s says
// so, because the next run reads the set from the file, and a case that lived only in memory would be
// a case nobody measures. The slug and the id are the tool's business, as they are in the query form:
// the generated `new-case-N` is the fixture directory's name and the iteration directory's name, and
// the file `S` opens can rename it if it should say something else.
//
// `d` asks first, in the dialog every other destructive step uses, and it says whether a fixture
// directory goes with the case — a case is its files as much as its lines, and a delete that left one
// behind would be half a delete.

// caseNewState is one case being written: three fields, no screen. It is drawn where the record of a
// case is drawn, so the set it is about stays on screen beside it.
type caseNewState struct {
	form     *huh.Form
	id       int
	slug     string
	prompt   string
	expected string
	// taken is every slug in the tree, so a duplicate is answered in the field rather than by the
	// store after the write: a slug is the fixture dir's name and the iteration dir's name, and it is
	// unique across the tree, not only within this file.
	taken  map[string]bool
	errMsg string
	want   int
}

// caseNewKeys is huh's keymap with prose in mind: enter is a line in the prompt — a form that submitted
// on the first enter would save half of it — tab walks to the next field, and the write is the app's
// ctrl+s, so huh is given no submit of its own, in a text field or in the slug's one-line input.
var caseNewKeys = func() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c"))
	km.Text.NewLine = key.NewBinding(key.WithKeys("enter", "ctrl+j"))
	km.Text.Next = key.NewBinding(key.WithKeys("tab"))
	km.Text.Prev = key.NewBinding(key.WithKeys("shift+tab"))
	km.Text.Submit = key.NewBinding(key.WithDisabled())
	km.Input.Next = key.NewBinding(key.WithKeys("tab"))
	km.Input.Prev = key.NewBinding(key.WithKeys("shift+tab"))
	km.Input.Submit = key.NewBinding(key.WithDisabled())
	return km
}()

func (c *caseNewState) build() {
	c.want = 12
	c.form = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("slug").
				Value(&c.slug).
				Validate(c.checkSlug),
			huh.NewText().
				Title("prompt").
				Value(&c.prompt).
				Lines(3),
			huh.NewText().
				Title("expected_output").
				Value(&c.expected).
				Lines(3),
		),
	).
		// The app owns the key bar and ctrl+s; huh's own would say most of it again a line above.
		WithShowHelp(false).
		WithKeyMap(caseNewKeys).
		WithTheme(huh.ThemeBase())
	c.form.Init()
}

// checkSlug is the store's own rule, answered as the name is typed: the pattern the file accepts, and
// the one thing the pattern cannot know — whether another case in the tree already owns the name.
func (c *caseNewState) checkSlug(s string) error {
	s = strings.TrimSpace(s)
	if !store.SlugPattern.MatchString(s) {
		return fmt.Errorf("lowercase a-z, 0-9 and hyphens, e.g. roll-one-d8")
	}
	if c.taken[s] {
		return fmt.Errorf("another case already owns %q", s)
	}
	return nil
}

// pane draws the two fields where the case record goes: a new case is a record being written, not a
// screen on top of the set.
func (c *caseNewState) pane(w, h int) []string {
	c.form.WithWidth(max(20, w-4))
	c.form.WithHeight(c.want)
	lines := []string{"", regionHead(w, "NEW CASE", fmt.Sprintf("#%d %s", c.id, c.slug), true), laneRule(w)}
	for _, line := range strings.Split(strings.TrimRight(c.form.View(), "\n"), "\n") {
		lines = append(lines, "  "+line)
	}
	if c.errMsg != "" {
		lines = append(lines, "  "+statusBad.Render("! "+c.errMsg))
	}
	return fitRows(lines, h)
}

func (c *caseNewState) hints() []hint {
	return []hint{{"enter", "a line"}, {"tab", "the next field"}, {"ctrl+s", "write"}, {"esc", "discard"}}
}

// caseNewActive is whether a case is being written: the caret is in these fields, so the view's own
// keys are not.
func (m *Model) caseNewActive() bool { return m.caseNew != nil }

func (c *caseNewState) update(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+s":
		return c.apply(m)
	case "esc":
		m.caseNew = nil
		m.setStatus("the new case was not written", false)
		return nil
	}
	// huh moves its caret by returning a command — tab comes back as a next-field message — so the
	// command has to reach the runtime, or tab is a key that draws but does not move.
	return c.updateForm(msg)
}

func (c *caseNewState) updateMsg(msg tea.Msg) tea.Cmd {
	if c.form == nil {
		return nil
	}
	_, cmd := c.form.Update(msg)
	return cmd
}

// updateForm hands one keystroke to huh and keeps the form it returns: huh's fields are values, and a
// form that is not taken back is a form that ignores the next character.
func (c *caseNewState) updateForm(msg tea.Msg) tea.Cmd {
	if c.form == nil {
		return nil
	}
	mm, cmd := c.form.Update(msg)
	if next, ok := mm.(*huh.Form); ok {
		c.form = next
	}
	return cmd
}

// apply writes the case through the store, which backs the file up first. A case with no prompt is
// refused here as well as by the store: it cannot be run, and the pane would have nothing to declare.
func (c *caseNewState) apply(m *Model) tea.Cmd {
	sk := m.store.Skill(m.skill)
	if sk == nil {
		c.errMsg = "unknown skill " + m.skill
		return nil
	}
	if strings.TrimSpace(c.prompt) == "" {
		c.errMsg = "a case with no prompt cannot be run — the prompt is the case"
		return nil
	}
	slug := strings.TrimSpace(c.slug)
	if !store.SlugPattern.MatchString(slug) {
		c.errMsg = "the slug must be lowercase a-z, 0-9 and hyphens"
		return nil
	}
	cse := model.OutputCase{
		ID:             c.id,
		Slug:           slug,
		Prompt:         strings.TrimSpace(c.prompt),
		ExpectedOutput: strings.TrimSpace(c.expected),
		Files:          []string{},
	}
	if err := m.store.AddOutput(m.skill, cse); err != nil {
		c.errMsg = err.Error()
		return nil
	}
	if err := m.store.SaveSkill(m.skill); err != nil {
		// The row is in memory and not on disk: dropping it again keeps the screen and the file
		// saying the same thing.
		m.store.DeleteRows([]store.RowRef{{Lane: store.LaneOutput, Skill: m.skill, Index: len(sk.Output.Evals) - 1}}, false)
		c.errMsg = "could not write " + sk.FilePath + ": " + err.Error()
		return nil
	}
	m.caseNew = nil
	m.reloadQuality()
	for i, one := range m.quality.cases {
		if one.Slug == cse.Slug {
			m.quality.sel = i
		}
	}
	m.setStatus(fmt.Sprintf("case #%d %s added — e its assertions, S the file", cse.ID, cse.Slug), false)
	return nil
}

// openCaseForm is `n`: the three fields, with the id and a slug prefilled but writable — the name is
// the operator's to choose, and the generated one is only the next free `new-case-N`.
func (m *Model) openCaseForm() tea.Cmd {
	if m.store.Skill(m.skill) == nil {
		m.setStatus("unknown skill "+m.skill, true)
		return nil
	}
	taken := m.caseSlugs()
	c := &caseNewState{id: m.store.NextCaseID(m.skill), slug: freeSlug(taken), taken: taken}
	c.build()
	m.caseNew = c
	m.setStatus("a new case — tab walks the fields, enter is a line, ctrl+s writes it", false)
	return c.form.Init()
}

// caseSlugs is every slug in the tree: a case's fixture directory and its iteration directory are
// named from its slug, so uniqueness is the tree's rule and not one file's.
func (m *Model) caseSlugs() map[string]bool {
	taken := map[string]bool{}
	for _, sk := range m.store.Skills {
		for _, c := range sk.Output.Evals {
			taken[c.Slug] = true
		}
	}
	return taken
}

// freeSlug is the next `new-case-N` no case already owns: a name to start from, not a name the form
// insists on.
func freeSlug(taken map[string]bool) string {
	for n := 1; ; n++ {
		slug := fmt.Sprintf("new-case-%d", n)
		if !taken[slug] {
			return slug
		}
	}
}

// setupAction is the view's one errand for the case under the cursor, in the order a case is worked
// on: the prompt, the first run, the assertions written from what that run wrote, the grade, then the
// human review. The ladder is per case and not per set: five cases at five different steps are five
// different next moves, and the pane only ever shows one of them.
func (m *Model) setupAction(c runner.QualityCase) (key, text string) {
	switch {
	case strings.TrimSpace(c.Prompt) == "":
		return "S", "its prompt and expected_output — a case with no prompt cannot be run"
	case !c.Ran && c.Assertions == 0:
		return "r", "run it once — the output is what its assertions are written from"
	case c.Assertions == 0:
		return "e", "the assertions — v reads what the arms wrote"
	case !c.Graded():
		if c.Ran {
			return "g", "grade it — the judge reads the arms against the sentences"
		}
		return "r", "run it — the arms are what the judge reads"
	}
	return "f", "the human review · e the assertions · enter the iteration"
}

// openCaseOutputs is `v` in the initialization view: what the two arms actually wrote for the case
// under the cursor. It is the look the first assertions are written from — a sentence about an output
// cannot be written before the output has been read — and it reads the arm's own response, whether or
// not a judge has been over it.
func (m *Model) openCaseOutputs() {
	q := m.quality
	c := q.caseAt()
	if c == nil {
		m.setStatus("no case on screen — nothing to read", true)
		return
	}
	if c.With.Response == "" && c.Without.Response == "" {
		m.setStatus(c.Slug+" has written nothing yet — r runs it, and its output is what the assertions are written from", true)
		return
	}
	// The document's lines are drawn two cells in from the frame, so its own width is the frame's less
	// the indent: a head padded to the full frame, or a prompt written out unwrapped, ran its last two
	// characters under the edge.
	inner := max(20, m.width-2)
	wrap := max(20, m.width-6)
	lines := []string{
		"",
		"  " + titleStyle.Render(c.Slug) + "  " + dimStyle.Render(fmt.Sprintf("#%d · iteration %d", c.ID, q.iter)),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, min(96, m.width-4))),
		"",
	}
	for _, f := range []struct{ label, text string }{
		{"prompt", c.Prompt},
		{"expected_output", c.ExpectedOutput},
	} {
		for i, line := range wrapPlain(oneLine(f.text), max(20, inner-2-16)) {
			if i == 0 {
				lines = append(lines, "  "+regionLabel.Render(padCell(f.label, 16, false))+"  "+line)
				continue
			}
			lines = append(lines, "  "+strings.Repeat(" ", 18)+line)
		}
	}
	for _, arm := range runner.AllArms {
		a := c.With
		if arm == runner.ArmWithout {
			a = c.Without
		}
		lines = append(lines, "", "  "+regionHead(inner, strings.ToUpper(armWord(arm))+" SKILL", armNote(a), false),
			"  "+dimStyle.Render(strings.Repeat(glyphs.Rule, min(96, m.width-4))), "")
		if strings.TrimSpace(a.Response) == "" {
			lines = append(lines, "  "+dimStyle.Render("this arm wrote no response — v opens the activity log from the dojo"))
			continue
		}
		for _, line := range wrapPlain(a.Response, wrap) {
			lines = append(lines, "  "+line)
		}
	}
	m.doc = &docState{scope: "outputs", title: "outputs · " + c.Slug, lines: lines}
	m.backTo = screenQuality
	m.mode = screenDoc
}

// armNote is the arm's own numbers, on the head of its output: what it cost and whether a judge read
// it, because the output is read beside the price it took to write.
func armNote(a runner.QualityArm) string {
	if a.DurationMS == 0 && a.Tokens == 0 {
		return ""
	}
	note := fmt.Sprintf("%.1fs · %d tokens", float64(a.DurationMS)/1000, a.Tokens)
	if a.Graded() {
		note += fmt.Sprintf(" · %d of %d passed", a.Passed, a.Judged)
	}
	return note
}

// askDeleteCase is `d`: the case under the cursor, removed behind a confirmation that names the file
// it leaves and the fixture directory that goes with it.
func (m *Model) askDeleteCase() {
	q := m.quality
	c := q.caseAt()
	if c == nil {
		m.setStatus("no case on screen — n makes the first one", true)
		return
	}
	slug, id := c.Slug, c.ID
	path := m.skill + "/evals/evals.json"
	lines := []string{fmt.Sprintf("#%d %s", id, slug)}
	if p := oneLine(c.Prompt); strings.TrimSpace(p) != "" {
		lines = append(lines, p)
	}
	lines = append(lines, "", dimStyle.Render("the case leaves "+path+"; the file is backed up first"))
	if m.store.FixtureExists(m.skill, slug) {
		lines = append(lines, dimStyle.Render("its fixture dir goes with it: "+m.store.FixtureDir(m.skill, slug)))
	} else {
		lines = append(lines, dimStyle.Render("it stages no fixture — nothing else on disk is touched"))
	}
	m.askNow(fmt.Sprintf("delete case #%d %s?", id, slug), "delete it", func(m *Model) tea.Cmd {
		// The row is found by slug and not by the cursor: the cursor is the screen's, and the
		// dialog may have outlived a re-read of the file behind it.
		index := -1
		if sk := m.store.Skill(m.skill); sk != nil {
			for i, cse := range sk.Output.Evals {
				if cse.Slug == slug {
					index = i
					break
				}
			}
		}
		if index < 0 {
			m.setStatus(slug+" is no longer in "+path+" — nothing was deleted", true)
			return nil
		}
		if _, err := m.store.DeleteRows([]store.RowRef{{Lane: store.LaneOutput, Skill: m.skill, Index: index}}, true); err != nil {
			m.setStatus(err.Error(), true)
			return nil
		}
		if err := m.store.SaveSkill(m.skill); err != nil {
			m.setStatus("could not write "+path+": "+err.Error(), true)
			return nil
		}
		m.reloadQuality()
		m.setStatus(fmt.Sprintf("case #%d %s deleted", id, slug), false)
		return nil
	})
}
