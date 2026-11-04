package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"evalctl/internal/runner"
	"evalctl/internal/store"
	"evalctl/internal/theme"
	"evalctl/internal/version"
)

// The TUI is one guided path, not a browser.
//
// It exists to walk the agentskills.io description loop: define the train set, define the validation
// set, iterate (run, read the failures, revise, repeat), then generalize and compare what each
// iteration scored on the held-back split. Every screen therefore belongs to a step of that loop,
// and the moves are the ones the loop has — forward with enter, back with esc, and the one verb the
// current step is asking for. What used to be spread over four tabs and a skill list is now the rail
// at the top of every step screen, which is also the resume point.
//
// One screen has two things with keys rather than one: the section screen shows the two halves of the
// loop and, beside them, the split the chosen half works. There the keys belong to whichever half of
// that screen had them last — tab says which — because a set you cannot look at before entering the
// workbench is a set you enter the workbench to look at.
//
// What the loop measures comes from internal/runner (the trigger lane) and what it writes comes from
// internal/store (the ledger, SKILL.md, the query set), so the TUI holds no state of its own that a
// file does not already carry: relaunching the binary reopens the step the disk says you are on.

type screen int

const (
	screenLane     screen = iota // the front door: the two optimizations
	screenSkill                  // which skill, with where each one is in the loop
	screenSections               // iteration or generalization
	screenFlow                   // the section's steps, one filling the body
	screenDraft                  // the description being revised
	screenAsk                    // a yes/no question
	screenDoc                    // a scrollable document (failures, a report, a log)
	screenRunLive                // the run, while it runs
	screenQuality                // output lane: setup, dojo and quality, as three sections on one rail
	screenHelp
)

// Model is the whole program state that is not on disk.
type Model struct {
	store *store.Store
	ver   string
	// built is the build's clock, local, when the binary was stamped: the corner of the header says
	// which binary is on screen, so a stale `bin/evalctl` is one glance and not a bug hunt.
	built string
	skill string // the skill being optimized ("" on the front door)
	// lane is which optimization the app is inside — the door's choice, kept for the whole
	// session so a skill opens the loop it was chosen under.
	lane string

	width, height int
	mode          screen
	status        string
	statusErr     bool
	quitArmed     bool

	lanes  laneState
	skills skillState
	// stamp is the tree as the store was last read from. Every screen here is a drawing of the
	// disk, so the app asks once a second whether the disk is still the one it drew and re-reads
	// when it is not — otherwise a skill added in another window stays invisible until a restart,
	// which reads as the tool having lost it.
	stamp    string
	sections sectionState
	flow     *flowState
	quality  *qualityState
	form     *formState
	// assertEdit is the output lane's assertion strip: the case's grading list, open under the
	// workbench's two columns. Nil when the list is not being written.
	assertEdit *assertState
	ask        *askState
	doc        *docState
	run        *runState
	help       int

	// backTo is where a screen opened off a step returns to, so a form or a document always hands
	// the flow back the way it left it.
	backTo screen
	// runEvents is the channel the running trigger lane emits into; it is kept on the model so the
	// wait command can re-arm itself for every frame.
	runEvents chan runner.Event
}

// Run starts the TUI and returns the process exit code. With no skill it opens on the front door —
// which optimization — because that choice comes before any particular skill. --skill goes straight
// into that skill's loop, which is what a person working on one skill wants from a shell alias.
func Run(s *store.Store, skill, lane, glyphProfile string) int {
	// The palette and the glyph profile are process state (the style table was built at startup),
	// so both are resolved here too: a caller that resolved them after import is drawn with what
	// it asked for.
	applyStyles(theme.Current())
	g, err := Glyphs(glyphProfile, os.Getenv("LANG"), os.Getenv("LC_CTYPE"), os.Getenv("LC_ALL"))
	if err != nil {
		fmt.Println("evalctl tui:", err)
		return 2
	}
	setName(g)
	m := newModel(s)
	if lane == store.LaneOutput {
		m.lanes.cursor = 1
	}
	if lane == store.LaneOutput {
		m.lane = store.LaneOutput
	}
	if skill != "" {
		if m.lane == store.LaneOutput {
			m.openQualitySkill(skill)
		} else {
			m.openSkill(skill)
		}
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Println("evalctl tui:", err)
		return 1
	}
	return 0
}

func newModel(s *store.Store) *Model {
	info := version.Resolve()
	m := &Model{store: s, ver: info.Short(), built: info.Clock(), mode: screenLane, lane: store.LaneTrigger}
	// The stamp the store was opened at: a screen drawn from it is current until the tree says
	// otherwise. A tree that cannot be stamped reads as changed, and the first tick re-reads it.
	m.stamp, _ = store.TreeStamp(s.Root)
	return m
}

// Init arms the tree poll. It runs for the whole session, on every screen: what the screens show is
// the disk, and the disk changes without asking this window.
func (m *Model) Init() tea.Cmd { return watchEvery() }

// reload re-reads what the loop knows: the skill's ledger and its measurements, and the rows of the
// split the current step is about. It is called after every write and every finished run, so no
// screen shows a number that came from before the change.
func (m *Model) reload() {
	if m.lane == store.LaneOutput {
		m.reloadQuality()
		return
	}
	if m.skill == "" || m.flow == nil {
		return
	}
	lab, err := runner.New(m.store).DescriptionLab(m.skill)
	if err != nil {
		m.setStatus(err.Error(), true)
		return
	}
	m.flow.lab = lab
	m.flow.iters = iterationsOf(lab)
	if s := m.flow.split(); s != "" {
		m.flow.rows = m.store.Rows(store.LaneTrigger, store.Filter{Skill: m.skill, Split: s})
	}
	// The section screen keeps a table of the split its cursor stands on: a write from that pane has
	// to show up there the same way a write from a step shows up in the step's own table.
	if m.mode == screenSections {
		m.loadSectionRows()
	}
	// The workbench's cursor stands on a failure: its pane is about one failing row, and a cursor
	// resting on a passing row leaves the pane describing something the cursor is not on.
	if m.mode == screenFlow && m.flow.sec == secIteration {
		m.flow.snapWorkbench()
	}
	// The fields under a set's table follow its cursor. Every write and every re-read comes through
	// here, so they are seeded once, here, rather than on the frame that happens to draw them.
	m.syncSetForm()
}

func (m *Model) setStatus(msg string, isErr bool) {
	m.status, m.statusErr = msg, isErr
}

func (m *Model) bodyHeight() int { return max(1, m.height-len(m.headLines())-2) }

// ---------------------------------------------------------------- update

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Every screen measures itself against the window on the way out, and a form measures itself
		// against its pane on every draw — so a resize is one assignment and nothing else.
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		return m.key(msg)
	case runEventMsg:
		m.applyEvent(msg.event)
		return m, waitForEvent(m.runEvents)
	case runDoneMsg:
		return m, m.finishRun()
	case tickMsg:
		if m.mode == screenRunLive {
			return m, tickEvery()
		}
		return m, nil
	case watchMsg:
		m.syncTree()
		return m, watchEvery()
	case editorDoneMsg:
		// The editor exited; whatever it did to the file is now the disk. The tree is re-read and the
		// screens are put back on it, so a revision written in $EDITOR is on the pane the moment the
		// terminal comes back.
		m.stamp = ""
		m.syncTree()
		m.reload()
		if msg.err != nil {
			m.setStatus(msg.scope+" editor exited with "+msg.err.Error(), true)
			return m, nil
		}
		m.setStatus(msg.scope+" re-read — the screens are the file's again", false)
		return m, nil
	}
	return m, m.passToForm(msg)
}

// watchMsg is the tree poll firing: one stat walk of the tree, once a second.
type watchMsg struct{}

func watchEvery() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return watchMsg{} })
}

// syncTree is the app's answer to "is what I am showing still the disk?" It costs one stat walk and
// re-reads nothing while the stamp holds — which is almost every second.
func (m *Model) syncTree() {
	stamp, err := store.TreeStamp(m.store.Root)
	if err != nil {
		m.noteStatus("cannot read the tree: "+err.Error(), true)
		return
	}
	if stamp == m.stamp {
		return
	}
	// Two states are not a reading of the disk and must not be swapped under: a run in flight, whose
	// runner is still writing into the store it started with, and a form or a question, which holds
	// the store index of the row it is about to write. The stamp is deliberately not recorded, so
	// the tick after the screen gives the keys back does the re-read.
	// Only a field with the caret holds the re-read back: its draft is the one thing on screen the
	// store cannot replace. Fields without it are seeded from the file and can be seeded again.
	if m.runActive() || m.mode == screenAsk || m.formActive() || m.assertEditActive() || m.noteActive() {
		return
	}
	if m.reopen() {
		m.stamp = stamp
	}
}

// reopen re-reads the tree and puts the current screen on the new reading. Everything a screen shows
// is either derived from the model's store or re-read from a file by reload, so the swap is the whole
// of it — but the cursors are the model's, and they are kept where they still mean something: the
// skill under the cursor by name, not by row, because a skill that sorts above it moves it.
//
// It reports whether the tree was read: a tree caught mid-write keeps the reading that works, and
// the stamp is left alone so the next tick tries again.
func (m *Model) reopen() bool {
	s, err := store.Open(m.store.Root)
	if err != nil {
		m.noteStatus("the tree changed and did not read back: "+err.Error(), true)
		return false
	}
	m.store = s

	// The skill being worked on may have left the tree. Its loop has no screens without its files,
	// so the app goes back to the chooser rather than drawing a flow whose set is gone.
	if m.skill != "" && m.store.Skill(m.skill) == nil {
		gone := m.skill
		m.skill, m.flow = "", nil
		m.sections, m.skills = sectionState{}, skillState{}
		if m.mode != screenLane {
			m.mode = screenSkill
			m.loadSkills()
		}
		m.noteStatus(gone+" is no longer in the tree — the chooser has been re-read", true)
		return true
	}
	switch m.mode {
	case screenSkill:
		sel := ""
		if m.skills.cursor >= 0 && m.skills.cursor < len(m.skills.rows) {
			sel = m.skills.rows[m.skills.cursor].name
		}
		top := m.skills.top
		m.loadSkills()
		m.skills.top = top
		for i, r := range m.skills.rows {
			if r.name == sel {
				m.skills.cursor = i
				return true
			}
		}
	default:
		// reload re-reads the ledger and the split the screen is about, including the section
		// screen's pane, so one call covers every screen that shows a set.
		m.reload()
	}
	return true
}

// noteStatus is setStatus for a message the app may repeat: a tree caught mid-write would otherwise
// rewrite the same line every second and overwrite whatever the person was just told.
func (m *Model) noteStatus(msg string, isErr bool) {
	if m.status == msg && m.statusErr == isErr {
		return
	}
	m.setStatus(msg, isErr)
}

func (m *Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	m.status = ""
	// A form owns its keystrokes: every character is a character, and the frame keys would eat the
	// ones the field needs. It is not a screen — the set it belongs to stays drawn around it, always —
	// so this is only about the caret: with the keys in the table, j and k still move.
	if m.assertEditActive() {
		return m, m.assertEdit.update(m, msg)
	}
	if m.noteActive() {
		return m, m.quality.note.update(m, msg)
	}
	if m.formActive() {
		return m, m.form.update(m, msg)
	}
	// The description field owns its keystrokes: q, ? and the digits are characters first, and a
	// "q" that quits while someone is writing a sentence is a bug, not a shortcut.
	if m.typing() {
		switch key {
		case "ctrl+c":
			return m, tea.Quit
		}
		return m, m.typingKey(msg)
	}
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "?":
		if m.mode == screenHelp {
			m.mode = m.backTo
			return m, nil
		}
		m.backTo = m.mode
		m.mode, m.help = screenHelp, 0
		return m, nil
	case "q":
		if m.mode == screenHelp {
			m.mode = m.backTo
			return m, nil
		}
		return m, m.quit()
	case "esc":
		return m, m.escUp()
	}
	switch m.mode {
	case screenLane:
		return m, m.updateLane(msg)
	case screenSkill:
		return m, m.updateSkills(msg)
	case screenSections:
		return m, m.updateSections(msg)
	case screenFlow:
		return m, m.updateFlow(msg)
	case screenQuality:
		return m, m.updateQuality(msg)
	case screenAsk:
		return m, m.ask.update(m, msg)
	case screenDoc:
		return m, m.doc.update(m, msg)
	case screenRunLive:
		return m, m.updateRunLive(msg)
	case screenHelp:
		return m, m.updateHelp(msg)
	}
	return m, nil
}

// passToForm hands a non-key message to the form that asked for it. huh moves its caret by returning
// a command — `tab` in a text field comes back as a nextFieldMsg — so an editor that never receives
// these messages is an editor where tab does nothing. The full-body form and the one drawn in a set
// both need this; only the inline one is not a screen of its own.
func (m *Model) passToForm(msg tea.Msg) tea.Cmd {
	if m.assertEditActive() {
		return m.assertEdit.updateMsg(msg)
	}
	if m.noteActive() {
		return m.quality.note.updateMsg(msg)
	}
	if !m.formActive() {
		return nil
	}
	return m.form.updateMsg(msg)
}

// escUp is one level up, and it does exactly one thing per screen: the footer names the level so
// the key is never a guess.
func (m *Model) escUp() tea.Cmd {
	switch m.mode {
	case screenSkill:
		m.skill, m.flow = "", nil
		m.mode = screenLane
	case screenFlow:
		m.mode = screenSections
	case screenQuality:
		// The lane's own levels: a section back to setup, then the skills. The rail is one level of
		// this screen, and esc has to walk it the way it walks a step's sections.
		if q := m.quality; q != nil && q.sec != qSetup {
			m.setQualitySection(qSetup)
			m.setStatus("setup — the declaration", false)
			return nil
		}
		m.skill = ""
		m.quality = nil
		m.mode = screenSkill
	case screenSections:
		// The run's block is inside the pane, so esc leaves it before it leaves the screen: one
		// level up, every time, is the only promise the key has to keep.
		if m.sections.runFocus {
			m.sections.runFocus = false
			m.setStatus("the run's settings are the set's — the table has the keys again", false)
			return nil
		}
		m.skill, m.flow = "", nil
		m.mode = screenSkill
	case screenAsk:
		m.ask, m.mode = nil, screenFlow
	case screenDoc:
		m.doc, m.mode = nil, m.backTo
	case screenHelp:
		m.mode = m.backTo
	case screenRunLive:
		// A run in flight is the one screen esc cannot leave: a half-read table of a run that is
		// still spending money is worse than the few seconds of waiting.
		if m.runActive() {
			m.setStatus("the run is going — x cancels, esc cannot", false)
		}
	}
	return nil
}

// quit asks once when there is something to lose: a run in flight.
func (m *Model) quit() tea.Cmd {
	if m.runActive() && !m.quitArmed {
		m.quitArmed = true
		m.setStatus("a run is going — q again cancels it and quits", true)
		return nil
	}
	return tea.Quit
}

func (m *Model) runActive() bool {
	return m.run != nil && m.run.live != nil && !m.run.live.done
}

// ---------------------------------------------------------------- view

func (m *Model) View() string {
	if m.width == 0 {
		// Before the first WindowSizeMsg there is no frame to draw into. Bubble Tea asks once
		// immediately, so this is one keystroke long and never on screen.
		return "evalctl"
	}
	head := m.headLines()
	body := m.bodyLines(m.bodyHeight())
	foot := []string{m.keyBand(), m.statusRow()}
	out := fitView(head, body, foot, m.height)
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		lines[i] = fitWidth(line, m.width)
	}
	return strings.Join(lines, "\n")
}

func (m *Model) headLines() []string {
	// The tool's name and the build sit together at the right edge: what you are looking at is read
	// from the left margin inward, and which evalctl drew it is one look at the corner.
	brand := brandName.Render(" evalctl") + " " + brandVer.Render(m.ver)
	if m.built != "" {
		brand += brandVer.Render(" · " + m.built)
	}
	bar := endsLine(m.barLeft(), brand, m.width)
	rule := tabRule.Render(strings.Repeat(glyphs.Heavy, max(1, m.width)))
	head := []string{bar, rule}
	// The context row is the rail on a step screen and a line of prose elsewhere — and nowhere at
	// all on the two screens that are only a list. A screen that has nothing to put there does not
	// get the row: an empty band under the rule is space the body can use.
	if ctx := m.contextLine(m.width); ctx != "" {
		head = append(head, ellipsis(ctx, m.width))
	}
	return head
}

// pathSegments is where you are, as a path: the optimization, the skill, and then the step, the
// form, the document or the run that is open. The bar draws it with `>` between the segments and
// nothing else is allowed on that row, because a path with facts tacked onto its end stops reading
// as a path — it reads as one more run-on line of separated words.
func (m *Model) pathSegments() []string {
	switch m.mode {
	case screenLane:
		return []string{"what to optimize"}
	case screenSkill:
		if m.lane == store.LaneOutput {
			return []string{"output optimization", "choose a skill"}
		}
		return []string{"trigger optimization", "choose a skill"}
	}
	segs := []string{"trigger optimization"}
	if m.lane == store.LaneOutput {
		segs = []string{"output optimization"}
	}
	if m.skill != "" {
		segs = append(segs, m.skill)
	}
	switch m.mode {
	case screenFlow:
		if m.flow != nil {
			segs = append(segs, m.flow.sectionName())
			// A section that is one screen has no step to name: the path stops at the section.
			if m.flow.stepCount() > 1 {
				segs = append(segs, m.flow.stepName())
			}
		}
	case screenQuality:
		segs = append(segs, m.quality.sectionName())
	case screenDraft:
		segs = append(segs, "revise")
	case screenDoc:
		if m.doc != nil {
			segs = append(segs, m.doc.scope)
		}
	case screenRunLive:
		if m.run != nil {
			segs = append(segs, m.run.scope)
		}
	case screenAsk:
		if m.ask != nil {
			segs = append(segs, m.ask.scope)
		}
	}
	return segs
}

// barLeft draws the path: what you came through in the quiet register, what you are looking at in the
// accent. `>` is the separator because it is one ASCII cell that says "and then" in every font, and
// because a middot between three words is a sentence, not a path.
func (m *Model) barLeft() string {
	segs := m.pathSegments()
	var b strings.Builder
	b.WriteString(" ")
	for i, seg := range segs {
		if i > 0 {
			b.WriteString(dimStyle.Render(" > "))
		}
		switch i {
		case len(segs) - 1:
			b.WriteString(tabScope.Render(seg))
		case 0:
			b.WriteString(dimStyle.Render(seg))
		default:
			b.WriteString(colSkill.Render(seg))
		}
	}
	return b.String()
}

// contextLine is the second row under the bar. On a step screen it is the rail — which step you are
// in, what is done, what is locked — and that rail is the only navigation this app has.
func (m *Model) contextLine(w int) string {
	// On a step screen the rail is the left half and the loop's numbers are the right half: the
	// steps say where you are, the numbers say how far in. They are two registers of one row, and
	// neither belongs on the path above.
	if m.mode == screenFlow && m.flow != nil {
		// The ranking is one table: its head says what ranks it and the line under it says what it
		// still owes, so a rail above would be the flow restated at the top of a screen that is all
		// table. It takes the row the way the workbench does.
		if m.flow.sec == secGeneralization && m.flow.step == 2 {
			return ""
		}
		rail := m.flow.rail(m, w)
		// A one-screen section has no chips to draw, so its rail row is already the numbers — or
		// empty, where the workbench itself is drawing them.
		if m.flow.stepCount() == 1 {
			return rail
		}
		facts := m.flow.stateLine()
		// The numbers are the secondary register: when the row cannot hold both with a gutter between
		// them, they go — a rail cut in half loses the step you are in, and the body repeats them.
		if facts != "" && lipgloss.Width(rail)+lipgloss.Width(facts)+4 <= w {
			return endsLine(rail, statValue.Render(facts)+" ", w)
		}
		return rail
	}
	// The output lane's three sections are its rail, in the same row the trigger lane's steps use:
	// the sections say where you are, the facts on the right say how far the loop has got.
	if m.mode == screenQuality {
		return m.qualityRail(w)
	}
	switch m.mode {
	case screenLane:
		return ""
	case screenSkill:
		return ""
	case screenRunLive:
		if m.run != nil {
			return dimStyle.Render("  " + m.run.hint)
		}
	case screenSections, screenDoc, screenAsk:
		return ""
	}
	return ""
}

func (m *Model) bodyLines(h int) []string {
	switch m.mode {
	case screenLane:
		return m.laneBody(h)
	case screenSkill:
		return m.skillBody(h)
	case screenSections:
		return m.sectionsBody(h)
	case screenFlow:
		if m.flow != nil {
			return m.flow.body(m, h)
		}
	case screenQuality:
		return m.qualityBody(h)
	case screenAsk:
		if m.ask != nil {
			return m.ask.body(h)
		}
	case screenDoc:
		if m.doc != nil {
			m.doc.md(m.width)
			return m.doc.body(h)
		}
	case screenRunLive:
		if m.run != nil {
			return m.run.liveBody(m, h)
		}
	case screenHelp:
		return m.helpBody(h)
	}
	return nil
}

// ---------------------------------------------------------------- footer

type hint struct{ key, act string }

// hintLine is the key band: what your keys are, then what they do. The keys are the bright register
// and the actions the soft one, so the line is scanned for the key you are looking for.
func hintLine(hints []hint) string {
	var parts []string
	for _, h := range hints {
		parts = append(parts, footKey.Render(h.key)+" "+footAct.Render(h.act))
	}
	return strings.Join(parts, footAct.Render(" · "))
}

func (m *Model) keyBand() string {
	line := hintLine(m.hints())
	room := m.width - lipgloss.Width(helpChip.Render(" ? keys ")) - 1
	if lipgloss.Width(line) > room {
		// Cut the hints, never the cap: the way to the rest of the keys is the one thing on this
		// band that has to survive a narrow window.
		line = ellipsis(line, max(0, room))
	}
	return banded(footSeq, endsLine(line, helpChip.Render(" ? keys "), m.width), m.width)
}

func (m *Model) hints() []hint {
	switch m.mode {
	case screenLane:
		return []hint{{"enter", "open"}, {"q", "quit"}}
	case screenSkill:
		return []hint{{"j k", "move"}, {"enter", "open the loop"}, {"esc", "lanes"}}
	case screenSections:
		if m.formActive() {
			return m.form.hints()
		}
		if m.sections.focus == sectionFocusSet && m.sectionPaneWidth() > 0 {
			// The run's block has the keys: two settings, and the keys a setting has.
			if m.sections.runFocus {
				return []hint{{"← →", "change"}, {"tab", "next knob"}, {"space", "on·off"}, {"enter", "the set"}}
			}
			// The pane has the keys: the set's own vocabulary, one line short of the step screen's —
			// and tab, which walks into the run's block below the table.
			return []hint{{"j k", "rows"}, {"tab", "the run"}, {"n e d", "new·edit·delete"},
				{"space", "trigger"}, {"enter", "the workbench"}, {"shift+tab", "sections"}}
		}
		if m.sectionPaneWidth() == 0 {
			if m.sections.runFocus {
				return []hint{{"← →", "change"}, {"tab", "next knob"}, {"space", "on·off"}, {"esc", "the set"}}
			}
			return []hint{{"j k", "move"}, {"tab", "the run"}, {"enter", "open"}, {"esc", "skills"}}
		}
		return []hint{{"j k", "move"}, {"tab", "the set"}, {"enter", "open"}, {"esc", "skills"}}
	case screenFlow:
		if m.flow != nil {
			return m.flow.hints(m)
		}
	case screenQuality:
		return qualityHints(m)
	case screenAsk:
		if m.ask != nil {
			return m.ask.hints()
		}
	case screenDoc:
		return []hint{{"j k", "scroll"}, {"g G", "ends"}, {"esc", "back"}}
	case screenRunLive:
		if m.run != nil {
			return m.run.liveHints()
		}
	case screenHelp:
		return []hint{{"j k", "scroll"}, {"esc", "back"}}
	}
	return nil
}

// statusRow is the last row of the window, always: what just happened on the left, where you are on
// the right. It is the one row that must not move, so every screen draws it the same way.
func (m *Model) statusRow() string {
	left := m.status
	style := statusOk
	if m.statusErr {
		style = statusBad
	}
	if left == "" {
		left = m.stateText()
	}
	return endsLine(" "+style.Render(left), m.hereLine(), m.width)
}

// hereLine is the right edge of the status row: the region the keystrokes are going into, and where
// the cursor stands inside it. The frame says the same thing in colour and a glyph, and this says it
// in words — which is the answer that survives a palette a terminal cannot draw, a screenshot, and
// two regions that both look plausible at a glance.
func (m *Model) hereLine() string {
	region, pos := m.region(), m.position()
	switch {
	case region == "" && pos == "":
		return ""
	case region == "":
		return statValue.Render(pos) + " "
	case pos == "":
		return regionLabel.Render(region) + " "
	}
	return regionLabel.Render(region) + statValue.Render(" · "+pos) + " "
}

// region is the area the keys are going into, in the words the frame names it with. Screens with one
// area return "": naming a region there would say the obvious and cost the position its room.
func (m *Model) region() string {
	switch m.mode {
	case screenSections:
		if m.formActive() {
			return "editor"
		}
		if m.sections.runFocus {
			return "the run"
		}
		if m.sections.focus == sectionFocusList || m.sectionPaneWidth() == 0 {
			return "sections"
		}
		return setRegionWord(sectionRows[m.sections.cursor].sec == secIteration)
	case screenFlow:
		if m.formActive() {
			return "editor"
		}
		if m.flow == nil {
			return ""
		}
		if m.flow.sec == secIteration {
			switch m.flow.focus {
			case focusWording:
				return "description"
			case focusNote:
				return "note"
			}
			return setRegionWord(true)
		}
		if m.flow.step == 1 {
			return setRegionWord(false)
		}
	case screenQuality:
		if m.assertEditActive() || m.noteActive() {
			return "editor"
		}
		if q := m.quality; q != nil {
			switch q.sec {
			case qDojo:
				return "failures"
			case qQuality:
				return "iterations"
			}
			return "cases"
		}
	}
	return ""
}

// stateText is what the status line says when nothing has just happened: the frame's own fact about
// where you are, which is cheaper to read than the rail.
func (m *Model) stateText() string {
	switch m.mode {
	case screenLane:
		// Nothing has happened yet: the status row is for what just happened, and the door has not
		// done anything. The pane beside the list is where the explanation lives.
		return ""
	case screenSkill:
		if m.lane == store.LaneOutput {
			return plural(len(m.skillNames()), "skill") + " with cases to grade"
		}
		return plural(len(m.skillNames()), "skill") + " with a description to tune"
	case screenSections:
		if m.formActive() {
			return m.form.facts()
		}
		// The rule the section works under, where the pane used to spend a paragraph on it. What is
		// on screen is the set; what belongs on the status row is what that set is for.
		if sectionRows[m.sections.cursor].sec == secGeneralization {
			return "these rows judge the wording; they are never used to write one"
		}
		return "the wording may learn from these rows — nothing else shapes a revision"
	case screenFlow:
		if m.formActive() {
			return m.form.facts()
		}
		if m.flow != nil {
			return m.flow.stateText()
		}
	case screenRunLive:
		if m.run != nil && m.run.live != nil {
			return m.run.live.stateText()
		}
	case screenQuality:
		if q := m.quality; q != nil && q.lab != nil {
			if q.lab.Next.Line != "" {
				return q.lab.Next.Line
			}
			return fmt.Sprintf("%s · %s · %s", plural(q.lab.Cases, "case"),
				plural(q.lab.Assertions, "assertion"), plural(len(q.lab.Iterations), "iteration"))
		}
	}
	return ""
}

func (m *Model) position() string {
	switch m.mode {
	case screenFlow:
		if m.formActive() {
			return fmt.Sprintf("q%d", m.form.id)
		}
		f := m.flow
		if f == nil {
			return ""
		}
		// The area names the region (see region); this says where the cursor stands inside it.
		if f.stepCount() > 1 {
			return fmt.Sprintf("step %d of %d", f.step, f.stepCount())
		}
		var parts []string
		if f.focus == focusSet && len(f.rows) > 0 {
			// The set is walked by its failures, so this counts failures: "row 3 of 3" would promise
			// two rows the cursor will not go to.
			if idxs := f.failIdxs(); len(idxs) > 0 {
				n := 1
				for _, i := range idxs {
					if i < f.sel {
						n++
					}
				}
				parts = append(parts, fmt.Sprintf("failure %d of %d", n, len(idxs)))
			} else {
				parts = append(parts, "nothing failed")
			}
		}
		if f.draftChanged() {
			parts = append(parts, "unsaved")
		}
		return strings.Join(parts, " · ")
	case screenSections:
		if m.formActive() {
			return fmt.Sprintf("q%d", m.form.id)
		}
		if m.sections.runFocus {
			// The block names the setting the arrows will change: the status row's corner is where
			// that fact belongs, not the knob's own line.
			if m.sections.runSel == knobDry {
				return "dry run"
			}
			return "runs per query"
		}
		if m.sections.focus == sectionFocusSet && m.sectionPaneWidth() > 0 {
			if len(m.sections.rows) == 0 {
				return "no rows"
			}
			return fmt.Sprintf("row %d of %d", m.sections.sel+1, len(m.sections.rows))
		}
		return fmt.Sprintf("%d / 2", m.sections.cursor+1)
	case screenSkill:
		return fmt.Sprintf("%d / %d", m.skills.cursor+1, max(1, len(m.skillNames())))
	case screenQuality:
		if q := m.quality; q != nil {
			switch q.sec {
			case qDojo:
				if len(q.fails) > 0 {
					return fmt.Sprintf("failure %d of %d", q.fsel+1, len(q.fails))
				}
				return "nothing failed"
			case qQuality:
				if q.lab != nil && len(q.lab.Iterations) > 0 {
					return fmt.Sprintf("%d / %d", q.rsel+1, len(q.lab.Iterations))
				}
			default:
				if len(q.cases) > 0 {
					return fmt.Sprintf("case %d of %d", q.sel+1, len(q.cases))
				}
			}
		}
	case screenLane:
		return fmt.Sprintf("%d / 2", m.lanes.cursor+1)
	case screenDoc:
		if m.doc != nil {
			return m.doc.position()
		}
	}
	return ""
}

// ---------------------------------------------------------------- help

func (m *Model) helpBody(h int) []string {
	lines := m.helpLines()
	m.help = min(max(0, m.help), max(0, len(lines)-h))
	return fitRows(lines[m.help:], h)
}

func (m *Model) updateHelp(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "j", "down":
		m.help++
	case "k", "up":
		m.help = max(0, m.help-1)
	case "g", "home":
		m.help = 0
	case "G", "end":
		m.help = max(0, len(m.helpLines())-m.bodyHeight())
	}
	return nil
}

// helpLines is one key map for the whole app, grouped by the step it belongs to. There is no
// per-screen key list, because a key map that changes shape is a key map you stop trusting.
func (m *Model) helpLines() []string {
	head := func(s string) []string { return []string{"", titleStyle.Render(s)} }
	dim := func(s string) []string { return []string{"  " + dimStyle.Render(s)} }
	var out []string
	add := func(groups ...[]string) {
		for _, g := range groups {
			out = append(out, g...)
		}
	}
	add(
		head("the loop"),
		[]string{
			"  enter      the next step — the one thing the screen is asking for",
			"  esc        one level back; the footer names the level",
			"  ?          this map · q quits",
		},
		head("steps 1 and 2 — the query set"),
		[]string{
			"  n          new query in this split",
			"  e          edit the row under the cursor, in the set",
			"  d          delete the row under the cursor (asks first)",
			"  space      flip should-trigger: the row asks the router yes or no",
			"  j k g G    move; first and last row",
		},
		head("the editor, under a set's table"),
		[]string{
			"  query      the ask, as a user would type it — the field the router matches on",
			"  why        the reason the row is in the set; read back when it fails",
			"  split      train guides a revision, validation judges it, holdout is measured once",
			"  tab        the next field · ctrl+s saves · esc discards",
		},
		head("the two sections"),
		[]string{
			"  j k        iteration or generalization, on the skill's section screen",
			"  tab        the keys: the two sections, then the set beside them, then the run",
			"  n e d      new, edit, delete a query in that set",
			"  space      flip should-trigger: the row asks the router yes or no",
			"  enter      open the workbench — at the step the disk is at, or at the set",
			"  esc        one level back: a step to its section, a section to the skills",
		},
		head("the run's block, under the set"),
		[]string{
			"  tab        into the block from the table, then from one knob to the other",
			"  ← →        change the knob under the cursor: runs per query, or the dry run",
			"  space      flip the dry run — it prints the commands and writes nothing",
			"  enter      back to the table; the block writes its file as it is changed",
			"  —          runs per query is the set's own number, in trigger/trigger-evals.json",
		},
		head("iteration — the workbench"),
		[]string{
			"  tab        the keys: the set, the description, the note — the description is what types",
			"  j k g G    walk the failures; the passing rows are drawn, not walked",
			"  enter      the loop's next thing: save the wording you typed, then measure it",
			"  R          run the train set again against the same wording",
			"  f          the failures as full cards",
			"  —          the set is written beside the two sections; the workbench only reads it",
		},
		head("generalization — validate, rank, apply"),
		[]string{
			"  v          run the validation set for every wording that has no rate yet",
			"  j k g G    walk the table; it opens on the wording the rates recommend",
			"  enter / a  apply the wording under the cursor to SKILL.md",
			"  f          one iteration in full: what it failed and what changed",
			"  h          run the fresh queries (holdout) once, after applying",
			"  ctrl+g     validate before five iterations, with the warning",
		},
		head("a run"),
		[]string{
			"  enter      starts it — the workbench goes straight to the live log, no screen between",
			"  R          run the train set again against the same wording",
			"  x          cancel the run; finished queries are kept",
			"  v          the arm's activity log; esc back",
			"  —          runs per query and dry run are set beside the two sections, on the set's pane",
		},
		head("output optimization — the loop"),
		[]string{
			"  R          run the next iteration: every case, both arms, in a fresh temp root",
			"  g          grade the iteration on screen: one judge call per arm, PASS/FAIL with evidence",
			"  j k        walk the cases; the pane is the evidence behind the row",
			"  e          write the case's assertions under the pane: one per line, ctrl+s writes, esc discards",
			"  tab        the ranking: every iteration's two rates, and the best complete one",
			"  enter      the iteration's report.md · f its failed assertions as one list",
			"  esc        back to the skills (from the ranking, first back to the loop)",
		},
		dim("every write backs the old file up under .evalctl/backups/"),
	)
	return out
}
