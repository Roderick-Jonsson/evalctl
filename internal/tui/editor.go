package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"evalctl/internal/model"
	"evalctl/internal/runner"
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

// pageSpec is one page of the form: the name the rail calls it, and the fields huh draws on it. huh
// shows one group at a time and will not say which one it is on, so the fields are kept here as
// well — the focused field (GetFocusedField) is the supported way back to the page, and it works
// because these are the same field values huh itself is holding.
type pageSpec struct {
	name   string
	fields []huh.Field
}

type editorState struct {
	store  *store.Store
	lane   string
	isNew  bool
	target store.RowRef
	d      draft
	// orig is the row as it was opened: the difference between it and d is what esc would throw
	// away, so esc asks before throwing it away.
	orig  draft
	armed bool // esc has been pressed once on an edited form
	form  *huh.Form
	pages []pageSpec
	// pageIdx is the page the last read saw. A completed form has no focused field to ask, and the
	// form is rebuilt under a refused save, so the page has to outlive the form that knew it.
	pageIdx int
	// saveAfter is ctrl+s held across the form: the row goes into memory and then, once it is
	// there, onto disk. A draft that is refused writes nothing.
	saveAfter bool
	// dupe is the near-twin cluster the row carried when it was opened — the trim the list
	// advertises, said where the query can actually be reworded.
	dupe int
	// builtH is the window height the multi-line boxes were sized for: text boxes are sized at
	// build time, so a window that changes height needs them laid out again.
	builtH int
	errMsg string
	note   string // why the last page jump did not go further
	width  int
	height int
}

// dirty is "there is typing here that leaving the form would lose".
func (e *editorState) dirty() bool { return e.d != e.orig }

func (m *Model) openEditor(e store.Entry, isNew bool) tea.Cmd {
	st := &editorState{store: m.store, lane: e.Lane, isNew: isNew, width: m.width, height: m.height,
		target: store.RowRef{Lane: e.Lane, Skill: e.Skill, Index: e.Index}, dupe: e.Dupe}
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
	st.orig = st.d
	m.editor = st
	m.mode = screenEditor
	return st.form.Init() // huh focuses the first field and renders nothing until this runs
}

// briefBox is a short box: a list of paths, or a line of reasoning beside a paragraph, is read in
// three lines — the rows it does not take are rows the box next to it can use.
const briefBox = 3

// build lays the form out for the window in hand. Every value lives in e.d, so a rebuild — a
// resize, a refused save — costs nothing and loses nothing.
//
// The pages are cut by thought, not by field count: identity (where the collisions are), then the
// ask, then the yardstick, then the two lists. A page that carries one box gets the whole body; a
// page with two shares it, and the short one stays short so the paragraph keeps the rows.
func (e *editorState) build() {
	d := &e.d
	e.pageIdx = 0
	tall, shared := e.textLines(1), e.textLines(2)
	if e.lane == store.LaneTrigger {
		e.pages = []pageSpec{
			{name: "id · skill", fields: []huh.Field{
				huh.NewInput().Title("id").Value(&d.id).CharLimit(6).Validate(validateID),
				e.skillSelect(&d.skill),
			}},
			{name: "label · split", fields: []huh.Field{
				huh.NewSelect[string]().Title("should the skill load?").Height(2).
					Options(huh.NewOptions("should trigger", "should not trigger")...).Value(&d.label),
				huh.NewSelect[string]().Title("split").Height(2).
					Options(huh.NewOptions("train", "validation")...).Value(&d.split),
			}},
			{name: "query · why", fields: []huh.Field{
				huh.NewText().Title("query").Value(&d.query).Lines(tall).
					Description("the prompt as a user would type it").
					Validate(validateQuery),
				huh.NewText().Title("why").Value(&d.why).Lines(briefBox).
					Description("the reason this row is in the set — read when it fails"),
			}},
		}
	} else {
		e.pages = []pageSpec{
			{name: "id · skill · slug", fields: []huh.Field{
				huh.NewInput().Title("id").Value(&d.id).CharLimit(6).Validate(validateID),
				e.skillSelect(&d.skill),
				huh.NewInput().Title("slug").Value(&d.slug).
					Description("a-z 0-9 hyphen; names the fixture dir and the results dir").
					Validate(validateSlug),
			}},
			{name: "prompt", fields: []huh.Field{
				huh.NewText().Title("prompt").Value(&d.prompt).Lines(tall).Validate(validatePrompt),
			}},
			{name: "expected", fields: []huh.Field{
				huh.NewText().Title("expected_output").Value(&d.expect).Lines(tall).
					Description("what a correct run produces — the grader's yardstick"),
			}},
			{name: "assertions · files", fields: []huh.Field{
				huh.NewText().Title("assertions").Value(&d.assert).Lines(shared).
					Description("one checkable statement per line — graded PASS/FAIL with evidence; leave empty until you have seen a run"),
				huh.NewText().Title("files").Value(&d.files).Lines(briefBox).
					Description("one staged path per line, relative to the fixture dir; empty stages nothing"),
			}},
		}
	}
	groups := make([]*huh.Group, 0, len(e.pages))
	for _, p := range e.pages {
		groups = append(groups, huh.NewGroup(p.fields...))
	}
	// No WithHeight: huh pads the active group to the form height, which leaves a large empty block
	// under a three-field group. Each group is sized by its fields instead, and the frame fits the
	// result into the window (fitView).
	e.form = huh.NewForm(groups...).
		WithWidth(max(40, e.width-6)).
		// The app owns the key bar. huh's own bar says "↑ up ↓ down / filter shift+tab back enter
		// select" one line above ours, which says most of the same things again.
		WithShowHelp(false).
		WithTheme(huh.ThemeBase())
	e.builtH = e.height
}

func (e *editorState) skillSelect(dst *string) *huh.Select[string] {
	return huh.NewSelect[string]().Title("skill").Height(min(7, max(2, len(e.skillOptions())))).
		Options(huh.NewOptions(e.skillOptions()...)...).Value(dst)
}

// textLines is how tall one multi-line box is on this window: the rows the frame leaves for the
// form, shared out between the boxes on the page. A box shorter than three lines hides the
// paragraph it is holding; past nine a page becomes a scroll for no gain.
func (e *editorState) textLines(boxes int) int {
	rows := 18 // an unsized window has no frame yet: a usable default, not a one-line box
	if e.height > 0 {
		rows = gridBody(e.height)
	}
	return max(3, min(9, (rows-6)/max(1, boxes)))
}

// ---------------------------------------------------------------- pages

// page is the page the cursor is on. huh keeps the group index to itself (its selector is internal),
// so the way back is the focused field: it is one of the fields this file built, and the index is
// whichever page spec holds it.
func (e *editorState) page() int {
	if e.form == nil {
		return e.pageIdx
	}
	f := e.form.GetFocusedField()
	if f == nil {
		return e.pageIdx
	}
	for i := range e.pages {
		for _, pf := range e.pages[i].fields {
			if pf == f {
				e.pageIdx = i
				return i
			}
		}
	}
	return e.pageIdx
}

// blocker is why page i cannot be left, in the words the field would use for itself. It is the same
// rule the field validates and the same rule the store enforces at save time — one list, so a page
// can never be marked finished by a rule the save would then refuse.
func (e *editorState) blocker(i int) string {
	d := e.d
	if e.lane == store.LaneTrigger {
		switch i {
		case 0:
			if err := validateID(d.id); err != nil {
				return err.Error()
			}
			if d.skill == "" {
				return "pick a skill"
			}
		case 1:
			if d.split != "train" && d.split != "validation" {
				return "pick the split"
			}
		case 2:
			if err := validateQuery(d.query); err != nil {
				return err.Error()
			}
		}
		return ""
	}
	switch i {
	case 0:
		if err := validateID(d.id); err != nil {
			return err.Error()
		}
		if d.skill == "" {
			return "pick a skill"
		}
		if err := validateSlug(d.slug); err != nil {
			return err.Error()
		}
	case 1:
		if err := validatePrompt(d.prompt); err != nil {
			return err.Error()
		}
	}
	return ""
}

// step moves the form one page and returns the command that finishes the move. huh advances the
// group index inside NextGroup/PrevGroup and hands back the new page's focus work, so the move is
// already done when this returns — the command must go to bubbletea, not be run here: feeding the
// page's init message back in leaves the group's error state dirty and the next step refuses.
func (e *editorState) step(dir int) (bool, tea.Cmd) {
	before := e.page()
	var cmd tea.Cmd
	if dir > 0 {
		cmd = e.form.NextGroup()
	} else {
		cmd = e.form.PrevGroup()
	}
	return e.page() != before, cmd
}

// gotoPage moves to page n (0-based). Backwards is free — nothing typed blocks the way back.
// Forwards walks one page at a time and stops on the first page that cannot be left, saying what
// that page is missing: a jump that silently does nothing teaches the operator nothing.
func (e *editorState) gotoPage(n int) (tea.Cmd, string) {
	if e.form == nil || n < 0 || n >= len(e.pages) || n == e.page() {
		return nil, ""
	}
	var cmds []tea.Cmd
	if n < e.page() {
		for i := 0; i < len(e.pages) && e.page() > n; i++ {
			moved, cmd := e.step(-1)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			if !moved {
				break
			}
		}
		return tea.Batch(cmds...), ""
	}
	for i := 0; i < len(e.pages) && e.page() < n; i++ {
		cur := e.page()
		if why := e.blocker(cur); why != "" {
			return tea.Batch(cmds...), fmt.Sprintf("page %d of %d will not let you past: %s",
				cur+1, len(e.pages), why)
		}
		moved, cmd := e.step(+1)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		if !moved {
			return tea.Batch(cmds...), fmt.Sprintf("page %d of %d will not move on", cur+1, len(e.pages))
		}
	}
	return tea.Batch(cmds...), ""
}

// rebuildAt re-lays the form and puts the cursor back on page n: a refused save must not cost the
// operator the place they were typing in.
func (e *editorState) rebuildAt(n int) tea.Cmd {
	e.build()
	init := e.form.Init()
	if cmd, _ := e.gotoPage(n); cmd != nil {
		return tea.Batch(init, cmd)
	}
	return init
}

// ---------------------------------------------------------------- live facts

// relPath is a store path as the operator reads it: relative to the tree root.
func (e *editorState) relPath(p string) string {
	return strings.TrimPrefix(p, strings.TrimSuffix(e.store.Root, "/")+"/")
}

// idOwner names the row that already carries this id, under the same rule the store applies at save
// time: a query id is unique across the whole file, a case id only within its skill.
func (e *editorState) idOwner(skill string, id int) string {
	if id <= 0 {
		return ""
	}
	if e.lane == store.LaneTrigger {
		for i, q := range e.store.Trigger.Queries {
			if !e.isNew && i == e.target.Index {
				continue
			}
			if q.ID == id {
				return "query #" + strconv.Itoa(q.ID)
			}
		}
		return ""
	}
	if skill == "" {
		return ""
	}
	sk := e.store.Skill(skill)
	if sk == nil {
		return ""
	}
	for i, c := range sk.Output.Evals {
		if !e.isNew && skill == e.d.origSkill && i == e.target.Index {
			continue
		}
		if c.ID == id {
			return "case " + strconv.Quote(c.Slug)
		}
	}
	return ""
}

// slugOwner names the case that already holds a slug. The store refuses a slug across every skill,
// not just this one, so the warning looks the same width.
func (e *editorState) slugOwner(slug string) string {
	if slug == "" {
		return ""
	}
	for _, sk := range e.store.Skills {
		for i, c := range sk.Output.Evals {
			if !e.isNew && sk.Name == e.d.origSkill && i == e.target.Index {
				continue
			}
			if c.Slug == slug {
				return fmt.Sprintf("%s case #%d", sk.Name, c.ID)
			}
		}
	}
	return ""
}

// nextFree is the id a new row would take in the skill the draft is filed under.
func (e *editorState) nextFree() int {
	if e.lane == store.LaneTrigger {
		return e.store.NextTriggerID()
	}
	return e.store.NextCaseID(e.d.skill)
}

// facts is what is true about the page on screen, read off the draft on every render. That is the
// point: the old form described the fixture dir once, when it was built, and went on describing the
// slug that had since been typed over.
func (e *editorState) facts() string {
	d := e.d
	var warn, dim []string
	add := func(into *[]string, s string) {
		if s != "" {
			*into = append(*into, s)
		}
	}
	if e.lane == store.LaneTrigger {
		switch e.page() {
		case 0:
			id, err := strconv.Atoi(strings.TrimSpace(d.id))
			if err == nil {
				add(&dim, "id "+strconv.Itoa(id))
				if owner := e.idOwner(d.skill, id); owner != "" {
					add(&warn, fmt.Sprintf("id %d is already %s", id, owner))
				} else if e.isNew || id != e.d.origID {
					add(&dim, fmt.Sprintf("#%d would be the next one free", e.nextFree()))
				}
			}
		case 2:
			if e.dupe > 0 {
				add(&warn, fmt.Sprintf("near-twin cluster #%d — u shows them in the list", e.dupe))
			}
		}
		return e.factLine(warn, dim)
	}
	switch e.page() {
	case 0:
		id, err := strconv.Atoi(strings.TrimSpace(d.id))
		if err == nil {
			add(&dim, "id "+strconv.Itoa(id))
			if owner := e.idOwner(d.skill, id); owner != "" {
				add(&warn, fmt.Sprintf("id %d is already %s in %s", id, owner, orDashText(d.skill)))
			} else if e.isNew || id != e.d.origID {
				add(&dim, fmt.Sprintf("#%d is the next free id in %s", e.nextFree(), orDashText(d.skill)))
			}
		}
		if slug := strings.TrimSpace(d.slug); slug != "" && store.SlugPattern.MatchString(slug) {
			if owner := e.slugOwner(slug); owner != "" {
				add(&warn, fmt.Sprintf("slug %q is already %s", slug, owner))
			}
		}
	case 1:
		if e.dupe > 0 {
			add(&warn, fmt.Sprintf("near-twin cluster #%d — u shows them in the list", e.dupe))
		}
	case 2:
		if strings.TrimSpace(d.expect) == "" {
			add(&warn, "expected_output is empty: grading has nothing to check against")
		}
	case 3:
		n := len(splitAssertions(d.assert))
		if n == 0 {
			add(&dim, "no assertions yet — the runner grades nothing here")
		} else {
			add(&dim, plural(n, "assertion"))
		}
		files := splitLines(d.files)
		slug := strings.TrimSpace(d.slug)
		exists := slug != "" && e.store.FixtureExists(d.skill, slug)
		dir := e.relPath(e.store.FixtureDir(d.skill, slug))
		switch {
		case len(files) > 0 && !exists:
			add(&warn, fmt.Sprintf("%s declared but %s does not exist", plural(len(files), "file"), dir))
		case len(files) > 0:
			add(&dim, fmt.Sprintf("%s · %s — %d on disk", plural(len(files), "file"), dir,
				len(e.store.FixtureFiles(d.skill, slug))))
		case exists:
			add(&warn, fmt.Sprintf("%s exists but files is empty — the runner stages the whole dir anyway", dir))
		default:
			add(&dim, "no files staged · no dir yet at "+dir)
		}
	}
	return e.factLine(warn, dim)
}

// factLine puts the warnings in front of the plain facts: a line that has to be cut must cut the
// thing you can do nothing about. Both registers are sentence-safe (no width), which is why they are
// badgeWarn and dimStyle and not the table's column styles.
func (e *editorState) factLine(warn, dim []string) string {
	parts := append(append([]string{}, warn...), dim...)
	if len(parts) == 0 {
		return ""
	}
	out := strings.Join(parts, " · ")
	if len(warn) > 0 {
		return badgeWarn.Render(out)
	}
	return dimStyle.Render(out)
}

// ---------------------------------------------------------------- validators

// The rules live once, and both the field and the page rail ask them: huh validates the field as it
// is left, blocker() answers "may this page be left at all", and the store enforces the same three
// at save time.
func validateID(s string) error {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return fmt.Errorf("id must be a positive integer")
	}
	return nil
}

func validateSlug(s string) error {
	if !store.SlugPattern.MatchString(strings.TrimSpace(s)) {
		return fmt.Errorf("slug must be lowercase a-z, 0-9 and hyphens")
	}
	return nil
}

func validatePrompt(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("a prompt cannot be empty")
	}
	return nil
}

func validateQuery(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("a query cannot be empty")
	}
	return nil
}

// ---------------------------------------------------------------- files

func (e *editorState) fileNote() string {
	if e.lane == store.LaneTrigger {
		return "trigger/trigger-evals.json"
	}
	return e.d.skill + "/evals/evals.json"
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

// resize re-lays the form when the window's body changes size, and always takes the new width. The
// page comes back with it.
func (e *editorState) resize(w, h int) tea.Cmd {
	e.width, e.height = w, h
	if h == e.builtH {
		e.form.WithWidth(max(40, w-6))
		return nil
	}
	return e.rebuildAt(e.page())
}

// ---------------------------------------------------------------- update

func (e *editorState) update(msg tea.Msg) tea.Cmd {
	model, cmd := e.form.Update(msg)
	if f, ok := model.(*huh.Form); ok {
		e.form = f
	}
	return cmd
}

// altPage reads the page-jump binding: alt+1 … alt+9. A bare digit cannot be it — the fields are
// text, and a prompt starting with "3" is not a jump.
func altPage(s string) (int, bool) {
	if !strings.HasPrefix(s, "alt+") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(s, "alt+"))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// ---------------------------------------------------------------- view

func (e *editorState) title() string {
	if e.isNew {
		return "new " + e.lane + " row · " + e.fileNote()
	}
	return fmt.Sprintf("edit %s #%d · %s", e.lane, e.d.origID, e.fileNote())
}

// bar is the editor's top row: what is being edited on the left, the page count at the window's
// edge — the same two ends the list's tab bar uses, so the form reads as the same program.
func (e *editorState) bar() string {
	return endsLine(titleStyle.Render(e.title()),
		statValue.Render(fmt.Sprintf("page %d of %d", e.page()+1, len(e.pages))), e.width)
}

// rail is the pages as chips: the number is the jump key, the name says what is on the page, and a
// page that can be left carries the done mark. It loses its names before it loses its numbers,
// because a rail without numbers cannot be jumped to.
func (e *editorState) rail(room int) string {
	mark := func(i int) string {
		if i == e.page() || e.blocker(i) != "" {
			return ""
		}
		return " " + glyphs.Done
	}
	build := func(names, marks bool) string {
		parts := make([]string, 0, len(e.pages))
		for i, p := range e.pages {
			text := strconv.Itoa(i + 1)
			if names {
				text += " " + p.name
			}
			if marks {
				text += mark(i)
			}
			parts = append(parts, chip(text, i == e.page()))
		}
		return strings.Join(parts, tabSep.Render("  "))
	}
	for _, v := range []struct{ names, marks bool }{{true, true}, {true, false}, {false, true}, {false, false}} {
		if line := build(v.names, v.marks); room <= 0 || lipgloss.Width(line) <= room {
			return line
		}
	}
	return build(false, false)
}

// hints is the editor's key bar. It goes through the same hint table and the same band as every
// other screen, so a narrow window drops bindings by rank instead of cutting the line. The jump key
// outranks a newline: a narrow window that cannot page is a form with pages you cannot reach.
func (e *editorState) hints() []hint {
	return []hint{
		{key: "enter", act: "next", rank: 1,
			help: "the next field; on the last field of the last page it applies the draft"},
		{key: fmt.Sprintf("alt+1–%d", len(e.pages)), act: "page", rank: 2,
			help: "jump to a page — a forward jump stops on an unfinished page and says what it needs"},
		{key: "alt+enter", act: "newline", rank: 3,
			help: "a new line inside prompt, query, assertions or files"},
		{key: "shift+tab", act: "back", rank: 4, help: "the previous field"},
		{key: "ctrl+s", act: "save", rank: 0,
			help: "apply the draft and write both lanes — a refused draft writes nothing"},
		{key: "esc", act: "cancel", rank: 0,
			help: "leave the form; on an edited form the first press says what is at stake"},
		// The key map is not a hint here: its cap sits at the right edge of this very band (alt+h,
		// because ? types a question mark inside a form), and a list that repeats the cap spends a
		// slot on the one key already parked in the corner. The list makes the same trade for ?.
	}
}

// editorCap is the help cap for the editor: the one binding that is not ? here says so itself.
func editorCap() string { return helpChip.Render(" alt+h keys ") }

func (e *editorState) band() string {
	w := max(1, e.width)
	room := max(10, w-lipgloss.Width(editorCap())-3)
	return bandLine(hintLine(e.hints(), room), editorCap(), w)
}

// statusRow is the last row: what just happened on the left, and the draft's own state at the right
// edge — the same two ends the list's status line uses.
func (e *editorState) statusRow() string {
	left := e.facts()
	switch {
	case e.armed:
		left = badgeWarn.Render("esc again discards your edits — nothing is written until ctrl+s")
	case e.errMsg != "":
		left = statusBad.Render("not saved: " + e.errMsg)
	case e.note != "":
		left = badgeWarn.Render(e.note)
	}
	var right []string
	if e.dirty() {
		right = append(right, badgeWarn.Render("unsaved draft"))
	}
	return footRowAt(left, right, max(1, e.width))
}

// formLines is the form's render as rows: huh returns one string, and a frame that counts rows has
// to count them.
func (e *editorState) formLines() []string {
	lines := strings.Split(strings.TrimRight(e.form.View(), "\n"), "\n")
	if e.width > 1 {
		for i, l := range lines {
			if lipgloss.Width(l) > e.width-1 {
				lines[i] = ellipsis(l, e.width-1)
			}
		}
	}
	return lines
}

func (e *editorState) view() string {
	head := []string{
		e.bar(),
		tabRule.Render(strings.Repeat(glyphs.Heavy, max(1, e.width))),
		e.rail(e.width),
	}
	return fitView(head, e.formLines(), []string{e.band(), e.statusRow()}, e.height)
}

// ---------------------------------------------------------------- apply

// apply writes the draft into the in-memory store, but an id that is moving asks first: the number
// is what earlier result dirs and reports cite. Errors keep the form open with the values intact —
// a rejected slug must not cost the user the prompt they just typed.
func (e *editorState) apply(m *Model) tea.Cmd {
	// A case that moves skill takes the target skill's next free id, so a typed id would be dropped
	// on the floor — say that here rather than let the form pretend it worked.
	if id, err := strconv.Atoi(strings.TrimSpace(e.d.id)); err == nil && e.lane == store.LaneOutput &&
		!e.isNew && e.d.skill != e.d.origSkill && id != e.d.origID {
		return e.fail("a move re-ids the case — save the move, then edit the id")
	}
	if ask := e.idAsk(); ask != nil {
		m.openIDConfirm(e, ask)
		return nil
	}
	return e.applyNow(m)
}

// idAsk is the question an id edit raises, or nil when nothing about the number is changing. An edit
// asks about the id it leaves behind; a new row asks about the id it takes, because a gap in the
// file is not a gap on disk — the deleted row's results are still there, waiting for an owner.
func (e *editorState) idAsk() *idAsk {
	id, err := strconv.Atoi(strings.TrimSpace(e.d.id))
	if err != nil || id <= 0 || e.d.skill == "" {
		return nil // applyNow reports these, in the form's own words
	}
	if !e.isNew && id == e.d.origID {
		return nil
	}
	skill, cite := e.d.origSkill, e.d.origID
	if e.isNew {
		skill, cite = e.d.skill, id
	}
	refs := runner.IDReferences(e.store.Root, e.lane, skill, cite)
	if e.isNew && len(refs) == 0 {
		return nil
	}
	return &idAsk{oldID: e.d.origID, newID: id, cite: cite, isNew: e.isNew, refs: refs}
}

// fail keeps the form open with every value intact and says why. A form that had already completed
// applies again on the next keystroke, so a refused draft needs a fresh one — and the page it was
// on, not the first page.
func (e *editorState) fail(why string) tea.Cmd {
	e.errMsg = why
	if e.form != nil && e.form.State == huh.StateCompleted {
		return e.rebuildAt(e.page())
	}
	return nil
}

// done ends a successful apply: the row is in memory, and ctrl+s asked for it on disk too.
func (e *editorState) done(m *Model, msg string) tea.Cmd {
	m.dirty = true
	m.revalidate()
	m.refresh()
	m.mode = screenList
	m.editor = nil
	if e.saveAfter {
		e.saveAfter = false
		m.save()
		return nil
	}
	m.setStatus(msg+" — ctrl+s to write", false)
	return nil
}

// applyNow is apply without the id question: the answer is already in hand.
func (e *editorState) applyNow(m *Model) tea.Cmd {
	e.errMsg = ""
	id, err := strconv.Atoi(strings.TrimSpace(e.d.id))
	if err != nil || id <= 0 {
		return e.fail("id must be a positive integer")
	}
	if e.d.skill == "" {
		return e.fail("pick a skill")
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
			return e.fail(err.Error())
		}
		return e.done(m, fmt.Sprintf("trigger #%d %s", q.ID,
			map[bool]string{true: "added", false: "updated"}[e.isNew]))
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
			return e.done(m, strings.Join(notes, "; "))
		}
	} else {
		var notes []string
		notes, err = m.store.UpdateOutput(e.d.origSkill, e.target.Index, c)
		if err == nil && len(notes) > 0 {
			m.setStatus(strings.Join(notes, "; "), false)
		}
	}
	if err != nil {
		return e.fail(err.Error())
	}
	return e.done(m, fmt.Sprintf("%s case #%d %s (slug %s)", e.d.skill, c.ID,
		map[bool]string{true: "added", false: "updated"}[e.isNew], c.Slug))
}

func (m *Model) updateEditor(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	e := m.editor
	leave := func() (tea.Model, tea.Cmd) {
		m.mode = screenList
		m.editor = nil
		m.setStatus("edit cancelled — nothing written", false)
		return m, nil
	}
	if e == nil {
		m.mode = screenList
		return m, nil
	}
	// ctrl+s is the promise the key bar makes: the draft is applied and, if it holds, written. It is
	// answered before the form's own state, so a completed form saves rather than re-applying.
	if msg.String() == "ctrl+s" {
		e.armed, e.note = false, ""
		e.saveAfter = true
		return m, e.apply(m)
	}
	switch e.form.State {
	case huh.StateCompleted:
		return m, e.apply(m)
	case huh.StateAborted:
		return leave()
	}
	// esc is this app's "one level up", and the form library does not bind it (its own quit is
	// ctrl+c), so the editor answers for it here. Leaving with typing in hand asks once: losing a
	// paragraph of prompt to a stray esc is worse than one extra keystroke.
	if msg.String() == "esc" {
		if !e.armed && e.dirty() {
			e.armed, e.note = true, ""
			return m, nil
		}
		return leave()
	}
	// ? types a question mark inside a form, so the key map is one keystroke further out.
	if msg.String() == "alt+h" {
		e.armed, e.note = false, ""
		m.help = true
		m.helpTop = 0
		return m, nil
	}
	if n, ok := altPage(msg.String()); ok {
		e.armed = false
		if n > len(e.pages) {
			// The rail shows how many pages there are, but a key that does nothing says nothing.
			e.note = plural(len(e.pages), "page") + " in this form"
			return m, nil
		}
		cmd, why := e.gotoPage(n - 1)
		e.note = why
		return m, cmd
	}
	e.armed, e.note = false, ""
	cmd := e.update(msg)
	if m.editor == nil {
		return m, nil
	}
	if e.form.State == huh.StateAborted {
		return leave()
	}
	if e.form.State == huh.StateCompleted {
		return m, e.apply(m)
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

// splitLines is one staged path per line, exactly as the field says. It used to split on commas as
// well, which let the live count and the written list disagree about a path with a comma in its
// name: the count is now the list.
func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if p := strings.TrimSpace(line); p != "" {
			out = append(out, p)
		}
	}
	return out
}
