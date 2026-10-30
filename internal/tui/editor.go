package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"evalctl/internal/model"
	"evalctl/internal/store"
)

// draft is the editor's form state, kept as strings so a half-typed id never crashes anything.
type draft struct {
	id     string
	skill  string
	label  string
	split  string
	query  string
	why    string
	slug   string
	prompt string
	expect string
	files  string
	assert string // one assertion per line — the grading list

	origID    int
	origSkill string
	origSlug  string
}

type editorState struct {
	store  *store.Store
	lane   string
	isNew  bool
	target store.RowRef
	d      draft
	form   *huh.Form
	errMsg string
	width  int
	height int
}

func (m *Model) openEditor(e store.Entry, isNew bool) tea.Cmd {
	st := &editorState{store: m.store, lane: e.Lane, isNew: isNew, width: m.width, height: m.height,
		target: store.RowRef{Lane: e.Lane, Skill: e.Skill, Index: e.Index}}
	if isNew {
		if e.Lane == store.LaneTrigger {
			st.d.id = strconv.Itoa(m.store.NextTriggerID())
			st.d.label = "should trigger"
			st.d.split = "train"
			if m.filter.Skill != "" {
				st.d.skill = m.filter.Skill
			}
		} else {
			skill := m.filter.Skill
			if skill == "" && len(m.store.Skills) > 0 {
				skill = m.store.Skills[len(m.store.Skills)-1].Name
			}
			st.d.id = strconv.Itoa(m.store.NextCaseID(skill))
			st.d.skill = skill
		}
	} else if e.Lane == store.LaneTrigger {
		q := m.store.Trigger.Queries[e.Index]
		st.d = draft{id: strconv.Itoa(q.ID), skill: q.Skill, split: q.Split, query: q.Query, why: q.Why,
			origID: q.ID, origSkill: q.Skill}
		if q.ShouldTrigger {
			st.d.label = "should trigger"
		} else {
			st.d.label = "should not trigger"
		}
	} else {
		sk := m.store.Skill(e.Skill)
		c := sk.Output.Evals[e.Index]
		st.d = draft{id: strconv.Itoa(c.ID), skill: sk.Name, slug: c.Slug, prompt: c.Prompt,
			expect: c.ExpectedOutput, files: strings.Join(c.Files, "\n"),
			assert: strings.Join(c.Assertions, "\n"),
			origID: c.ID, origSkill: sk.Name, origSlug: c.Slug}
	}
	st.build()
	m.editor = st
	m.mode = screenEditor
	return st.form.Init() // huh focuses the first field and renders nothing until this runs
}

func (e *editorState) build() {
	d := &e.d
	var groups []*huh.Group
	if e.lane == store.LaneTrigger {
		groups = append(groups,
			huh.NewGroup(
				huh.NewInput().Title("id").Value(&d.id).CharLimit(6).
					Validate(func(s string) error {
						n, err := strconv.Atoi(strings.TrimSpace(s))
						if err != nil || n <= 0 {
							return fmt.Errorf("id must be a positive integer")
						}
						return nil
					}),
				huh.NewSelect[string]().Title("skill").Height(min(7, max(2, len(e.skillOptions())))).
					Options(huh.NewOptions(e.skillOptions()...)...).Value(&d.skill),
				huh.NewSelect[string]().Title("should the skill load?").Height(2).
					Options(huh.NewOptions("should trigger", "should not trigger")...).Value(&d.label),
				huh.NewSelect[string]().Title("split").Height(2).
					Options(huh.NewOptions("train", "validation")...).Value(&d.split),
			),
			huh.NewGroup(
				huh.NewText().Title("query").Value(&d.query).Lines(5).
					Description("the prompt as a user would type it").
					Validate(func(s string) error {
						if strings.TrimSpace(s) == "" {
							return fmt.Errorf("a query cannot be empty")
						}
						return nil
					}),
				huh.NewInput().Title("why").Value(&d.why).
					Description("the reason this row is in the set — read when it fails"),
			),
		)
	} else {
		groups = append(groups,
			huh.NewGroup(
				huh.NewInput().Title("id").Value(&d.id).CharLimit(6).
					Validate(func(s string) error {
						n, err := strconv.Atoi(strings.TrimSpace(s))
						if err != nil || n <= 0 {
							return fmt.Errorf("id must be a positive integer")
						}
						return nil
					}),
				huh.NewSelect[string]().Title("skill").Height(min(7, max(2, len(e.skillOptions())))).
					Options(huh.NewOptions(e.skillOptions()...)...).Value(&d.skill),
				huh.NewInput().Title("slug").Value(&d.slug).
					Description("a-z 0-9 hyphen; names the fixture dir and the results dir").
					Validate(func(s string) error {
						if !store.SlugPattern.MatchString(strings.TrimSpace(s)) {
							return fmt.Errorf("slug must be lowercase a-z, 0-9 and hyphens")
						}
						return nil
					}),
			),
			huh.NewGroup(
				huh.NewText().Title("prompt").Value(&d.prompt).Lines(5).
					Validate(func(s string) error {
						if strings.TrimSpace(s) == "" {
							return fmt.Errorf("a prompt cannot be empty")
						}
						return nil
					}),
				huh.NewText().Title("expected_output").Value(&d.expect).Lines(5).
					Description("what a correct run produces — the grader's yardstick"),
			),
			huh.NewGroup(
				huh.NewText().Title("assertions").Value(&d.assert).Lines(5).
					Description("one checkable statement per line — graded PASS/FAIL with evidence; leave empty until you have seen a run"),
			),
			huh.NewGroup(
				huh.NewText().Title("files").Value(&d.files).Lines(3).
					Description("one staged path per line, relative to the fixture dir; empty stages nothing"),
				huh.NewNote().Title("fixture").Description(e.fixtureNote()),
			),
		)
	}
	title := "new " + e.lane + " row"
	if !e.isNew {
		title = fmt.Sprintf("edit %s #%d", e.lane, e.d.origID)
	}
	if e.errMsg != "" {
		title += "\n" + statusBad.Render("not saved: "+e.errMsg)
	}
	// No WithHeight: huh pads the active group to the form height, which leaves a large empty
	// block under a three-field group. Each group is sized by its fields instead.
	form := huh.NewForm(groups...).
		WithWidth(max(40, e.width-6)).
		WithShowHelp(true).
		WithTheme(huh.ThemeBase())
	_ = title
	e.form = form
}

func (e *editorState) fileNote() string {
	if e.lane == store.LaneTrigger {
		return "evals/trigger-evals.json"
	}
	return e.d.skill + "/evals/evals.json"
}

func (e *editorState) fixtureNote() string {
	if e.d.slug == "" {
		return "no slug yet"
	}
	dir := e.store.FixtureDir(e.d.skill, e.d.slug)
	files := e.store.FixtureFiles(e.d.skill, e.d.slug)
	if files == nil {
		return dir + "\n(does not exist — a case with files will fail validation)"
	}
	return fmt.Sprintf("%s\n%d file(s): %s", dir, len(files), strings.Join(files, ", "))
}

func (e *editorState) skillOptions() []string {
	names := e.store.KnownSkills()
	if len(names) == 0 {
		names = e.store.SkillNames()
	}
	if e.d.skill != "" {
		found := false
		for _, n := range names {
			if n == e.d.skill {
				found = true
			}
		}
		if !found {
			names = append(names, e.d.skill)
		}
	}
	return names
}

func (e *editorState) resize(w, h int) {
	e.width, e.height = w, h
	e.form.WithWidth(max(40, w-6))
}

func (e *editorState) update(msg tea.Msg) tea.Cmd {
	model, cmd := e.form.Update(msg)
	if f, ok := model.(*huh.Form); ok {
		e.form = f
	}
	return cmd
}

func (e *editorState) view(width int) string {
	title := "new " + e.lane + " row"
	if !e.isNew {
		title = fmt.Sprintf("edit %s #%d — %s", e.lane, e.d.origID, e.fileNote())
	}
	head := titleStyle.Render(title)
	if e.lane == store.LaneOutput {
		head += "\n" + dimStyle.Render(e.fixtureNote())
	}
	if e.errMsg != "" {
		head += "\n" + statusBad.Render("not saved: "+e.errMsg)
	}
	body := e.form.View()
	foot := dimStyle.Render("enter next / submit · alt+enter newline · shift+tab back · esc cancel · ctrl+s saves")
	return head + "\n\n" + body + "\n" + foot
}

// apply writes the draft into the in-memory store. Errors keep the form open with the values
// intact — a rejected slug must not cost the user the prompt they just typed.
func (e *editorState) apply(m *Model) {
	id, err := strconv.Atoi(strings.TrimSpace(e.d.id))
	if err != nil || id <= 0 {
		e.errMsg = "id must be a positive integer"
		e.build()
		return
	}
	if e.d.skill == "" {
		e.errMsg = "pick a skill"
		e.build()
		return
	}

	if e.lane == store.LaneTrigger {
		q := model.TriggerQuery{ID: id, Skill: e.d.skill, Split: e.d.split,
			Query: strings.TrimSpace(e.d.query), Why: strings.TrimSpace(e.d.why),
			ShouldTrigger: e.d.label == "should trigger"}
		if !e.isNew {
			q = m.store.Trigger.Queries[e.target.Index]
			q.ID, q.Skill, q.Split = id, e.d.skill, e.d.split
			q.Query, q.Why = strings.TrimSpace(e.d.query), strings.TrimSpace(e.d.why)
			q.ShouldTrigger = e.d.label == "should trigger"
		}
		if e.isNew {
			err = m.store.AddTrigger(q)
		} else {
			err = m.store.UpdateTrigger(e.target.Index, q)
		}
		if err != nil {
			e.errMsg = err.Error()
			e.build()
			return
		}
		m.dirty = true
		m.revalidate()
		m.refresh()
		m.mode = screenList
		m.setStatus(fmt.Sprintf("trigger #%d %s — ctrl+s to write", q.ID, map[bool]string{true: "added", false: "updated"}[e.isNew]), false)
		return
	}

	c := model.OutputCase{ID: id, Slug: strings.TrimSpace(e.d.slug), Prompt: strings.TrimSpace(e.d.prompt),
		ExpectedOutput: strings.TrimSpace(e.d.expect), Files: splitLines(e.d.files),
		Assertions: splitAssertions(e.d.assert)}
	if !e.isNew {
		sk := m.store.Skill(e.d.origSkill)
		c = sk.Output.Evals[e.target.Index]
		c.ID, c.Slug = id, strings.TrimSpace(e.d.slug)
		c.Prompt, c.ExpectedOutput = strings.TrimSpace(e.d.prompt), strings.TrimSpace(e.d.expect)
		c.Files = splitLines(e.d.files)
		c.Assertions = splitAssertions(e.d.assert)
	}
	if e.isNew {
		err = m.store.AddOutput(e.d.skill, c)
	} else if e.d.skill != e.d.origSkill {
		var notes []string
		notes, err = m.store.MoveOutput(e.d.origSkill, e.target.Index, e.d.skill)
		if err == nil {
			m.dirty = true
			m.revalidate()
			m.refresh()
			m.mode = screenList
			m.setStatus(strings.Join(notes, "; ")+" — ctrl+s to write", false)
			return
		}
	} else {
		var notes []string
		notes, err = m.store.UpdateOutput(e.d.origSkill, e.target.Index, c)
		if err == nil && len(notes) > 0 {
			m.setStatus(strings.Join(notes, "; "), false)
		}
	}
	if err != nil {
		e.errMsg = err.Error()
		e.build()
		return
	}
	m.dirty = true
	m.revalidate()
	m.refresh()
	m.mode = screenList
	m.setStatus(fmt.Sprintf("%s case #%d %s (slug %s) — ctrl+s to write", e.d.skill, c.ID,
		map[bool]string{true: "added", false: "updated"}[e.isNew], c.Slug), false)
}

func (m *Model) updateEditor(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.editor == nil {
		m.mode = screenList
		return m, nil
	}
	switch m.editor.form.State {
	case huh.StateCompleted:
		m.editor.apply(m)
		return m, nil
	case huh.StateAborted:
		m.mode = screenList
		m.setStatus("edit cancelled — nothing written", false)
		m.editor = nil
		return m, nil
	}
	cmd := m.editor.update(msg)
	if m.editor.form.State == huh.StateAborted {
		m.mode = screenList
		m.setStatus("edit cancelled — nothing written", false)
		m.editor = nil
		return m, nil
	}
	if m.editor.form.State == huh.StateCompleted {
		m.editor.apply(m)
	}
	return m, cmd
}

// splitAssertions keeps an assertion on one line: unlike files, a comma is content here
// ("the chart shows March, July and November"), so only line breaks separate entries.
func splitAssertions(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if p := strings.TrimSpace(line); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ',' }) {
		if p := strings.TrimSpace(f); p != "" {
			out = append(out, p)
		}
	}
	return out
}

var _ = lipgloss.NewStyle
