package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"evalctl/internal/runner"
	"evalctl/internal/stage"
	"evalctl/internal/store"
)

// promoRun is one output iteration as the form sees it: the run's name, where it sits, and the
// case dirs that actually finished. Only what is on disk can be promoted.
type promoRun struct {
	name  string
	path  string
	cases []string
}

// Where a promotion lands: the next stage's fixture (the chain), or this skill's own references/
// (a worked example the agent reads to learn the shape of a run).
const (
	destFixture = iota
	destReferences
)

// promoteState is the chain form: pick a run's output on the left of the arrow, say where it
// should land, and the preview shows exactly what would be written — including what is
// deliberately left behind. The store still owns every rule; this screen only fills a request in.
type promoteState struct {
	runs   []promoRun
	runIx  int
	caseIx int
	armIx  int

	skills  []string
	skillIx int

	id       int
	slug     string
	prompt   string
	expected string
	dryRun   bool

	// the references destination: which workspace, which files, and where each lands
	dest int
	file string
	as   string
	wsIx int

	slugEdited   bool
	promptEdited bool

	cursor  int
	editing bool
	field   textinput.Model
	fieldOn string

	preview    store.Promotion
	reference  store.Reference
	previewErr string
}

type promoRow struct{ label, value string }

// promoNote is colDupe without its fixed width: the table's column style would reflow a sentence
// into four-character fragments inside the form's box.
var promoNote = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))

// openPromote starts the form from where the cursor is: the newest output iteration, the focused
// case inside it if that case ran there, and the stage *after* the one that produced it.
func (m *Model) openPromote() {
	if m.runActive {
		m.setStatus("a run is in flight — promoting now would change what it stages", true)
		return
	}
	if m.lane != store.LaneOutput {
		m.setStatus("promote chains an output case — tab to the output lane first", true)
		return
	}
	p := &promoteState{runs: promoRuns(m.store), skills: m.store.SkillNames()}
	if len(p.runs) == 0 {
		m.setStatus("no output iteration under evals/results — run a stage first (R)", true)
		return
	}
	if len(p.skills) == 0 {
		m.setStatus("no stage skill has an evals/evals.json to promote into", true)
		return
	}
	p.runIx = len(p.runs) - 1
	if e, ok := m.focused(); ok {
		if sk := m.store.Skill(e.Skill); sk != nil && e.Index < len(sk.Output.Evals) {
			if ix := promoIndex(p.runs[p.runIx].cases, sk.Output.Evals[e.Index].Slug); ix >= 0 {
				p.caseIx = ix
			}
		}
	}
	p.armIx = promoIndex(p.arms(), runner.ArmWith)
	if p.armIx < 0 {
		p.armIx = 0
	}
	p.skillIx = promoIndex(p.skills, nextStage(skillOfSlug(m.store, p.caseSlug())))
	if p.skillIx < 0 {
		// The pipeline's next stage is not installed: promote into any other stage rather than
		// defaulting to the one that produced the artifact.
		p.skillIx = 0
		if source := skillOfSlug(m.store, p.caseSlug()); source != "" && len(p.skills) > 1 && p.skills[0] == source {
			p.skillIx = 1
		}
	}
	p.id = m.store.NextCaseID(p.skill())

	ti := textinput.New()
	ti.CharLimit = 500
	p.field = ti

	p.refreshPreview(m.store)
	p.slug = p.suggestSlug(m.store)
	p.prompt = p.suggestPrompt()

	m.promote = p
	m.mode = screenPromote
	m.setStatus("", false)
}

// ---------------------------------------------------------------- data

func promoRuns(s *store.Store) []promoRun {
	var out []promoRun
	for _, info := range runner.New(s).ListRuns() {
		if info.Lane != store.LaneOutput {
			continue
		}
		cases := subdirs(info.Path)
		if len(cases) == 0 {
			continue
		}
		out = append(out, promoRun{name: info.Name, path: info.Path, cases: cases})
	}
	return out
}

func subdirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// skillOfSlug finds the stage case a run's artifact belongs to, so the form can default to the
// next stage in the pipeline (a fixture) or the producing stage (a reference) rather than a guess.
func skillOfSlug(s *store.Store, slug string) string { return s.SkillOfSlug(slug) }

// nextStage is the stage after name in the pipeline; empty input gets the one that consumes
// research, because that is what a promotion is for.
func nextStage(name string) string {
	if name == "" {
		return "learning-path"
	}
	for i, n := range stage.Order {
		if n == name {
			return stage.Order[(i+1)%len(stage.Order)]
		}
	}
	return "learning-path"
}

func (p *promoteState) caseSlug() string {
	if p.runIx < 0 || p.runIx >= len(p.runs) || p.caseIx < 0 || p.caseIx >= len(p.runs[p.runIx].cases) {
		return ""
	}
	return p.runs[p.runIx].cases[p.caseIx]
}

func (p *promoteState) arms() []string {
	if p.caseSlug() == "" {
		return nil
	}
	return subdirs(filepath.Join(p.runs[p.runIx].path, p.caseSlug()))
}

func (p *promoteState) arm() string {
	arms := p.arms()
	if len(arms) == 0 {
		return ""
	}
	if p.armIx < 0 || p.armIx >= len(arms) {
		return arms[0]
	}
	return arms[p.armIx]
}

func (p *promoteState) skill() string {
	if p.skillIx < 0 || p.skillIx >= len(p.skills) {
		return ""
	}
	return p.skills[p.skillIx]
}

func (p *promoteState) sourceDir() string {
	if p.caseSlug() == "" || p.arm() == "" {
		return ""
	}
	return filepath.Join(p.runs[p.runIx].path, p.caseSlug(), p.arm())
}

// refreshPreview re-reads the source. It is read-only, so every arrow key can afford it, and the
// form never claims a copy it has not looked at.
func (p *promoteState) refreshPreview(s *store.Store) {
	p.preview = store.Promotion{}
	p.reference = store.Reference{}
	p.previewErr = ""
	src := p.sourceDir()
	if src == "" {
		p.previewErr = "no arm on disk for this case"
		return
	}
	if p.dest == destReferences {
		ref, err := s.ReferencePreview(store.ReferenceRequest{Skill: p.skill(), Source: src,
			Workspace: p.workspace(), File: p.fileList(), As: p.as})
		if err != nil {
			p.previewErr = err.Error()
			return
		}
		p.reference = ref
		return
	}
	preview, err := store.PromotionPreview(src, p.skill())
	if err != nil {
		p.previewErr = err.Error()
		return
	}
	if len(preview.Files) == 0 {
		p.previewErr = "nothing to promote: no MC-*/ workspace here"
		return
	}
	p.preview = preview
}

func (p *promoteState) suggestPrompt() string {
	ws := "MC-[topic]"
	if len(p.preview.Workspaces) > 0 {
		ws = p.preview.Workspaces[0]
	}
	switch p.skill() {
	case "learning-path":
		return fmt.Sprintf("The research in the %s workspace here is done — build the learning path and the presentation outline from it.", ws)
	case "slide-builder":
		return fmt.Sprintf("The outline in the %s workspace here is ready — build the slides from it.", ws)
	case "av-production":
		return fmt.Sprintf("The scripted slides in the %s workspace here are ready — build their AV files and the manifest.", ws)
	}
	return fmt.Sprintf("Continue the pipeline from the %s workspace here.", ws)
}

// suggestSlug keeps a promotion's naming predictable: the target stage's own prefix, the case id
// it is about to take, and what the source artifact was called. It is a suggestion, not a rule —
// the row is editable.
func (p *promoteState) suggestSlug(s *store.Store) string {
	suffix := promoSlugSuffix(p.caseSlug())
	if suffix == "" {
		return ""
	}
	return slugify(fmt.Sprintf("%s-%d-%s", slugPrefix(s, p.skill()), p.id, suffix))
}

func promoSlugSuffix(slug string) string {
	if i := strings.Index(slug, "-"); i >= 0 {
		slug = slug[i+1:]
	}
	slug = strings.TrimLeftFunc(slug, func(r rune) bool { return r >= '0' && r <= '9' })
	return strings.Trim(slug, "-")
}

// slugPrefix is the prefix a stage's existing cases already use (src-, lp-, sb-, …); a stage with
// no cases yet falls back to the initials of its name.
func slugPrefix(s *store.Store, skill string) string {
	counts := map[string]int{}
	if sk := s.Skill(skill); sk != nil {
		for _, c := range sk.Output.Evals {
			if i := strings.Index(c.Slug, "-"); i > 0 {
				counts[c.Slug[:i]]++
			}
		}
	}
	best, bestN := "", 0
	for prefix, n := range counts {
		if n > bestN || (n == bestN && prefix < best) {
			best, bestN = prefix, n
		}
	}
	if best != "" {
		return best
	}
	var b strings.Builder
	for _, part := range strings.Split(skill, "-") {
		if part != "" {
			b.WriteByte(part[0])
		}
	}
	if b.Len() == 0 {
		return "case"
	}
	return b.String()
}

// slugify keeps a slug inside the runner's rule: [a-z0-9-], no doubled or trailing hyphens.
func slugify(raw string) string {
	raw = strings.ToLower(raw)
	var b strings.Builder
	lastHyphen := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		case !lastHyphen && b.Len() > 0:
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func promoIndex(values []string, want string) int {
	for i, v := range values {
		if v == want {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------- rows / keys

// destName is what the destination row reads.
func (p *promoteState) destName() string {
	if p.dest == destReferences {
		return "this skill's references/"
	}
	return "next stage's fixture"
}

// workspaces lists the MC-* dirs the arm produced; a run normally wrote one, and the form says
// which instead of guessing when it wrote more.
func (p *promoteState) workspaces() []string {
	return store.WorkspaceDirs(p.sourceDir())
}

func (p *promoteState) workspace() string {
	ws := p.workspaces()
	if len(ws) == 0 {
		return ""
	}
	if p.wsIx < 0 || p.wsIx >= len(ws) {
		return ws[0]
	}
	return ws[p.wsIx]
}

// fileList splits the file row back into paths: comma- or space-separated, so one text row can
// still name a set.
func (p *promoteState) fileList() []string {
	return strings.FieldsFunc(p.file, func(r rune) bool { return r == ',' || r == ' ' || r == '	' })
}

func (p *promoteState) rows() []promoRow {
	action := ""
	if p.previewErr != "" {
		action = p.previewErr
	} else if p.dest == destReferences {
		action = fmt.Sprintf("%d file(s) from %s", len(p.reference.Files), orDashText(p.workspace()))
	} else {
		action = fmt.Sprintf("%d file(s) from %s", len(p.preview.Files), strings.Join(p.preview.Workspaces, ", "))
	}
	onOff := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	out := []promoRow{
		{"from run", p.runs[p.runIx].name},
		{"from case", p.caseSlug()},
		{"from arm", orDashText(p.arm())},
		{"destination", p.destName()},
		{"target skill", p.skill()},
	}
	if p.dest == destReferences {
		file := oneLine(p.file)
		if file == "" {
			file = "(the stage's own outputs)"
		}
		as := p.as
		if as == "" {
			as = "(same path as in the workspace)"
		}
		out = append(out,
			promoRow{"workspace", orDashText(p.workspace())},
			promoRow{"file", file},
			promoRow{"as", as})
	} else {
		expected := oneLine(p.expected)
		if expected == "" {
			expected = "(empty — validate will warn)"
		}
		out = append(out,
			promoRow{"slug", p.slug},
			promoRow{"id", strconv.Itoa(p.id)},
			promoRow{"prompt", oneLine(p.prompt)},
			promoRow{"expected", expected})
	}
	return append(out,
		promoRow{"dry run", onOff(p.dryRun)},
		promoRow{"enter to promote", action})
}

func isPromoTextField(label string) bool {
	switch label {
	case "slug", "id", "prompt", "expected", "file", "as":
		return true
	}
	return false
}

func (m *Model) updatePromote(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.promote
	if p == nil {
		m.mode = screenList
		return m, nil
	}
	if p.editing {
		switch msg.String() {
		case "esc":
			p.editing = false
			p.field.Blur()
			return m, nil
		case "enter":
			m.commitPromoField(p.fieldOn, strings.TrimSpace(p.field.Value()))
			p.editing = false
			p.field.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		p.field, cmd = p.field.Update(msg)
		return m, cmd
	}

	rows := p.rows()
	switch msg.String() {
	case "esc", "q":
		m.promote = nil
		m.mode = screenList
		m.setStatus("promotion cancelled — nothing was written", false)
		return m, nil
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
		return m, nil
	case "down", "j":
		if p.cursor < len(rows)-1 {
			p.cursor++
		}
		return m, nil
	case "enter":
		label := rows[p.cursor].label
		if isPromoTextField(label) {
			p.editing = true
			p.fieldOn = label
			p.field.Placeholder = promoPlaceholder(label)
			p.field.SetValue(p.fieldValue(label))
			p.field.SetCursor(len([]rune(p.fieldValue(label))))
			p.field.Focus()
			return m, textinput.Blink
		}
		if label == "enter to promote" {
			return m, m.doPromote()
		}
		return m, nil
	case "left", "h":
		p.adjust(m, -1)
		return m, nil
	case "right", "l", " ":
		p.adjust(m, 1)
		return m, nil
	}
	return m, nil
}

func promoPlaceholder(label string) string {
	switch label {
	case "slug":
		return "fixture dir name"
	case "id":
		return "next free"
	case "prompt":
		return "what you would say to the agent"
	case "expected":
		return "what a good run produces"
	case "file":
		return "path inside the workspace, e.g. PS-<topic>.md or sources/"
	case "as":
		return "name inside references/, e.g. PS-example.md"
	}
	return ""
}

func (p *promoteState) fieldValue(label string) string {
	switch label {
	case "slug":
		return p.slug
	case "id":
		return strconv.Itoa(p.id)
	case "prompt":
		return p.prompt
	case "expected":
		return p.expected
	case "file":
		return p.file
	case "as":
		return p.as
	}
	return ""
}

// commitPromoField writes an edited value back. A slug cleared back to nothing falls back to the
// suggestion, and either way the suggestion stops overwriting what was typed.
func (m *Model) commitPromoField(label, value string) {
	p := m.promote
	if p == nil {
		return
	}
	repreview := false
	switch label {
	case "slug":
		if clean := slugify(value); clean != "" {
			p.slug = clean
			p.slugEdited = true
		} else {
			p.slugEdited = false
			p.slug = p.suggestSlug(m.store)
		}
	case "id":
		if n, err := strconv.Atoi(value); err == nil && n > 0 {
			p.id = n
			if !p.slugEdited {
				p.slug = p.suggestSlug(m.store)
			}
		}
	case "prompt":
		p.prompt = value
		p.promptEdited = value != ""
	case "expected":
		p.expected = value
	case "file":
		p.file = value
		repreview = true
	case "as":
		p.as = value
		repreview = true
	}
	if repreview {
		p.refreshPreview(m.store)
	}
}

// adjust steps a row's value. Changing the source or the destination re-reads the preview and
// re-suggests what the destination wants, unless it was typed by hand.
func (p *promoteState) adjust(m *Model, dir int) {
	label := p.rows()[p.cursor].label
	cycle := func(values []string, ix *int) {
		if len(values) == 0 {
			return
		}
		*ix = (*ix + dir + len(values)) % len(values)
	}
	newSource := false
	switch label {
	case "from run":
		p.runIx = (p.runIx + dir + len(p.runs)) % len(p.runs)
		p.caseIx, p.armIx, p.wsIx = 0, 0, 0
		newSource = true
	case "from case":
		p.caseIx = (p.caseIx + dir + len(p.runs[p.runIx].cases)) % len(p.runs[p.runIx].cases)
		p.armIx, p.wsIx = 0, 0
		newSource = true
	case "from arm":
		ix := p.armIx
		cycle(p.arms(), &ix)
		p.armIx = ix
		p.wsIx = 0
		newSource = true
	case "destination":
		if dir != 0 {
			p.dest = (p.dest + 1) % 2
		}
		p.retarget(m.store)
		newSource = true
	case "target skill":
		p.skillIx = (p.skillIx + dir + len(p.skills)) % len(p.skills)
		if p.dest == destFixture {
			p.id = m.store.NextCaseID(p.skill())
		}
		newSource = true
	case "workspace":
		ix := p.wsIx
		cycle(p.workspaces(), &ix)
		p.wsIx = ix
		newSource = true
	case "id":
		p.id += dir
		if p.id < 1 {
			p.id = 1
		}
	case "dry run":
		if dir != 0 {
			p.dryRun = !p.dryRun
		}
	}
	if newSource {
		p.refreshPreview(m.store)
		if !p.slugEdited && p.dest == destFixture {
			p.slug = p.suggestSlug(m.store)
		}
		if !p.promptEdited && p.dest == destFixture {
			p.prompt = p.suggestPrompt()
		}
	}
	if label == "id" && !p.slugEdited {
		p.slug = p.suggestSlug(m.store)
	}
}

// retarget points the target skill at what the chosen destination wants: the stage after the one
// that produced the artifact for a fixture, the producing stage itself for a reference.
func (p *promoteState) retarget(s *store.Store) {
	source := skillOfSlug(s, p.caseSlug())
	want := nextStage(source)
	if p.dest == destReferences {
		want = source
	}
	if ix := promoIndex(p.skills, want); ix >= 0 {
		p.skillIx = ix
	}
	p.wsIx = 0
}

// doPromote runs the copy. Unlike a row edit it cannot wait for ctrl+s — the fixture bytes land on
// disk the moment the copy happens — so it refuses to run over unsaved edits and writes the case
// file itself.
func (m *Model) doPromote() tea.Cmd {
	p := m.promote
	if p == nil {
		return nil
	}
	if m.dirty {
		m.setStatus("unsaved changes in memory — ctrl+s first: a promotion writes its case file immediately", true)
		return nil
	}
	if p.previewErr != "" {
		m.setStatus("cannot promote: "+p.previewErr, true)
		return nil
	}
	if p.dest == destReferences {
		return m.doPromoteReferences(p)
	}
	if strings.TrimSpace(p.slug) == "" || strings.TrimSpace(p.prompt) == "" {
		m.setStatus("slug and prompt are required — enter on those rows to edit them", true)
		return nil
	}
	req := store.PromoteRequest{Skill: p.skill(), Slug: p.slug, ID: p.id, Prompt: p.prompt,
		Expected: p.expected, Source: p.sourceDir(), Run: p.runs[p.runIx].name,
		Case: p.caseSlug(), Arm: p.arm(), DryRun: p.dryRun}
	res, err := m.store.PromoteOutput(req)
	if err != nil {
		m.setStatus("promote failed: "+err.Error(), true)
		return nil
	}
	if p.dryRun {
		m.setStatus(fmt.Sprintf("dry run: %d file(s) would go into %s — nothing written",
			len(res.Files), res.FixtureDir), false)
		return nil
	}
	if err := m.store.SaveSkill(res.Skill); err != nil {
		m.setStatus("the fixture was copied but the case file did not save: "+err.Error(), true)
		return nil
	}
	sha := res.SHA256
	if len(sha) > 12 {
		sha = sha[:12]
	}
	msg := fmt.Sprintf("promoted %s/%s/%s → %s case #%d (%s) · %d file(s) · fixture %s",
		res.Run, res.Case, res.Arm, res.Skill, res.ID, res.Slug, len(res.Files), sha)
	if len(res.Skipped) > 0 {
		msg += " · left behind " + strings.Join(res.Skipped, ", ")
	}

	m.promote = nil
	m.mode = screenList
	m.lane = store.LaneOutput
	m.revalidate()
	if !m.focusCase(res.Skill, res.Slug) {
		// A filter or search was hiding it: a promotion changes the visible set, so drop them
		// rather than leave the new case invisible.
		m.filter = store.Filter{}
		m.onlyDup = false
		m.search.SetValue("")
		m.refresh()
		m.focusCase(res.Skill, res.Slug)
	}
	m.setStatus(msg, false)
	return nil
}

// doPromoteReferences is the references destination's write: the stage's own artifacts land in
// the skill's references/ dir, where every later run's agent can read them. There is no case to
// append and nothing to focus afterwards, so it reports and returns to the list.
func (m *Model) doPromoteReferences(p *promoteState) tea.Cmd {
	res, err := m.store.PromoteReferences(store.ReferenceRequest{
		Skill: p.skill(), Source: p.sourceDir(), Run: p.runs[p.runIx].name,
		Case: p.caseSlug(), Arm: p.arm(), Workspace: p.workspace(),
		File: p.fileList(), As: p.as, DryRun: p.dryRun})
	if err != nil {
		m.setStatus("promote failed: "+err.Error(), true)
		return nil
	}
	if p.dryRun {
		m.setStatus(fmt.Sprintf("dry run: %d file(s) would land in %s/references/ — nothing written",
			len(res.Files), res.Skill), false)
		return nil
	}
	m.promote = nil
	m.mode = screenList
	msg := fmt.Sprintf("promoted %s/%s/%s → %s/references/ · %d file(s) · name it in %s/SKILL.md or the agent never opens it",
		res.Run, res.Case, res.Arm, res.Skill, len(res.Files), res.Skill)
	if len(res.Overwrote) > 0 {
		msg += " · replaced " + strings.Join(res.Overwrote, ", ")
	}
	m.setStatus(msg, false)
	return nil
}

// promotePromptToTrigger is the other way a prompt travels: the focused output case's prompt
// becomes a trigger query, so a request the case already grades is also measured for selection —
// the description should pick that skill when a user asks exactly that. There is no fixture half
// to land, so it writes the trigger file at once; it refuses while a run is going (a run is
// reading that file) or edits are unsaved (the write would carry them).
func (m *Model) promotePromptToTrigger() {
	if m.runActive {
		m.setStatus("a run is in flight and is reading the trigger set — wait for it", true)
		return
	}
	if m.lane != store.LaneOutput {
		m.setStatus("t promotes an output case's prompt — tab to the output lane first", true)
		return
	}
	if m.dirty {
		m.setStatus("unsaved changes in memory — ctrl+s first: the trigger file is written at once", true)
		return
	}
	e, ok := m.focused()
	if !ok {
		return
	}
	res, err := m.store.PromotePrompt(store.PromotePromptRequest{Skill: e.Skill, Index: e.Index, ShouldTrigger: true})
	if err != nil {
		m.setStatus("promote to trigger: "+err.Error(), true)
		return
	}
	if err := m.store.SaveTrigger(); err != nil {
		m.setStatus("the query was added in memory but the file did not save: "+err.Error(), true)
		return
	}
	m.revalidate()
	m.setStatus(fmt.Sprintf("trigger #%d added (%s, %s, should-trigger) from %s case #%d — tab to the trigger lane",
		res.ID, res.Skill, res.Split, res.SourceSkill, res.SourceID), false)
}

func (m *Model) focusCase(skill, slug string) bool {
	for i, e := range m.rows {
		if e.Lane == store.LaneOutput && e.Skill == skill && e.Slug == slug {
			m.cursor = i
			m.clampScroll()
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- view

// formWidth is the box's inner width, capped so the form reads like a form on a wide terminal.
func (m *Model) formWidth() int { return min(m.width-2, 110) }

// wrapPlain word-wraps to width, so the box never reflows a styled line by itself (it measures
// ANSI-laden text badly).
func wrapPlain(s string, width int) []string {
	if width < 10 {
		width = 10
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			switch {
			case line == "":
				line = word
			case lipgloss.Width(line)+1+lipgloss.Width(word) <= width:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	return out
}

func (m *Model) promoteView() string {
	p := m.promote
	if p == nil {
		return ""
	}
	// Every line is fitted to the box before it is styled: lipgloss re-wraps a line that is too
	// wide, and once it does, the lines after it land at odd offsets.
	inner := m.formWidth() - 2
	var lines []string
	add := func(style lipgloss.Style, text string) {
		lines = append(lines, style.Render(fitPlain(text, inner)))
	}
	add(titleStyle, "promote · a run's output becomes the next stage's fixture, or a worked example")
	lines = append(lines, "")
	if p.caseSlug() != "" {
		to := p.skill()
		if p.dest == destReferences {
			to += "/references/"
		}
		add(dimStyle, fmt.Sprintf("  %s/%s/%s  →  %s", p.runs[p.runIx].name, p.caseSlug(),
			orDashText(p.arm()), to))
		lines = append(lines, "")
	}
	for i, row := range p.rows() {
		mark := "  "
		if i == p.cursor {
			mark = styleOn.Render("▸ ")
		}
		value := row.value
		if p.editing && i == p.cursor {
			value = p.field.View()
		}
		value = fitPlain(value, inner-20)
		if i == p.cursor && !p.editing {
			value = styleOn.Render(value)
		}
		lines = append(lines, mark+fmt.Sprintf("%-17s", row.label)+" "+value)
	}
	lines = append(lines, "")

	switch {
	case p.previewErr != "":
		add(statusBad, "  "+p.previewErr)
	case p.dest == destReferences && len(p.reference.Files) > 0:
		add(dimStyle, "  will add to "+p.skill()+"/references/")
		for i, f := range p.reference.Files {
			if i == 6 {
				add(dimStyle, fmt.Sprintf("               … %d more", len(p.reference.Files)-6))
				break
			}
			add(dimStyle, "               "+fitPlain(p.reference.Targets[i]+"  ←  "+f, inner-2))
		}
		if len(p.reference.Skipped) > 0 {
			for i, line := range wrapPlain("not taken      "+strings.Join(p.reference.Skipped, ", "), inner-2) {
				pad := "  "
				if i > 0 {
					pad = strings.Repeat(" ", 17)
				}
				add(dimStyle, pad+line)
			}
		}
	case len(p.preview.Files) > 0:
		add(dimStyle, "  will stage")
		for i, f := range p.preview.Files {
			if i == 6 {
				add(dimStyle, fmt.Sprintf("               … %d more", len(p.preview.Files)-6))
				break
			}
			add(dimStyle, "               "+f)
		}
		if len(p.preview.Skipped) > 0 {
			for i, line := range wrapPlain("left behind    "+strings.Join(p.preview.Skipped, ", "), inner-2) {
				pad := "  "
				if i > 0 {
					pad = strings.Repeat(" ", 17)
				}
				add(promoNote, pad+line)
			}
			add(dimStyle, "                 the target stage writes these itself")
		}
	}
	if p.dest == destReferences && len(p.reference.Overwrote) > 0 {
		add(statusBad, "  already in references/: "+strings.Join(p.reference.Overwrote, ", "))
		add(dimStyle, "                 pick another 'as', or use the CLI with --force to replace it")
	}
	if p.dest == destFixture && p.slug != "" && m.store.FixtureExists(p.skill(), p.slug) {
		add(statusBad, "  fixture already exists — a promotion never overwrites one; pick another slug")
	}
	// The full prompt is shown under the cursor only: it is long, and showing it twice on one
	// screen is noise. Editing shows it in the input instead.
	if strings.TrimSpace(p.prompt) != "" && !p.editing && p.rows()[p.cursor].label == "prompt" {
		lines = append(lines, "")
		for i, line := range wrapPlain("prompt   "+oneLine(p.prompt), inner-13) {
			pad := "  "
			if i > 0 {
				pad = strings.Repeat(" ", 13)
			}
			add(dimStyle, pad+line)
		}
	}
	lines = append(lines, "")
	if p.editing {
		add(dimStyle, "  type the value, enter keeps it, esc drops it")
	} else {
		add(dimStyle, "  j k move · ← → change · enter edit / promote · esc cancel")
	}
	lines = append(lines, "")
	if p.dryRun {
		add(promoNote, "  dry run: reports what it would stage and writes nothing")
	}
	return box.Width(m.formWidth()).Render(strings.Join(lines, "\n"))
}

// fitPlain truncates a plain line to width so the box never re-wraps it.
func fitPlain(s string, width int) string {
	if width < 4 || lipgloss.Width(s) <= width {
		return s
	}
	r := []rune(s)
	if len(r) > width-1 {
		return string(r[:width-1]) + "…"
	}
	return s
}
