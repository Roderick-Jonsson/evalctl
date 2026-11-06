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
	screenPick        screen = iota // the work list: every lane that has anything to do
	screenLane                      // retired: the front door — the two optimizations
	screenSkill                     // which skill, with where each one is in the loop
	screenSections                  // iteration or generalization
	screenFlow                      // the section's steps, one filling the body
	screenDraft                     // the description being revised
	screenDoc                       // a scrollable document (failures, a report, a log)
	screenRunLive                   // the run, while it runs
	screenQualityMenu               // output lane: the skill's three views, chosen on one screen
	screenQuality                   // output lane: initialization, iteration or testing quality
	screenRevision                  // the agent's proposal for SKILL.md, as a diff against the file
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

	// pick is the work list: one row per lane of work, which is the app's front door now.
	pick pickState
	// stamp is the tree as the store was last read from. Every screen here is a drawing of the
	// disk, so the app asks once a second whether the disk is still the one it drew and re-reads
	// when it is not — otherwise a skill added in another window stays invisible until a restart,
	// which reads as the tool having lost it.
	stamp   string
	flow    *flowState
	quality *qualityState
	form    *formState
	// revision is the proposal `p` asks for: the agent's whole SKILL.md, the text it was written
	// against, and the diff between them.
	revision *revisionState
	// assertEdit is the output lane's assertion strip: the case's grading list, open under the
	// workbench's two columns. Nil when the list is not being written.
	assertEdit *assertState
	// caseNew is a case being made: two fields, drawn where the record of a case is drawn. Nil when
	// nothing is being added.
	caseNew *caseNewState
	ask     *askState
	doc     *docState
	run     *runState
	help    int

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
		m.lane = store.LaneOutput
	}
	if skill != "" {
		m.openWork(pickRow{skill: skill, lane: laneOrDefault(lane)})
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
	m := &Model{store: s, ver: info.Short(), built: info.Clock(), mode: screenPick, lane: store.LaneTrigger}
	// The stamp the store was opened at: a screen drawn from it is current until the tree says
	// otherwise. A tree that cannot be stamped reads as changed, and the first tick re-reads it.
	m.stamp, _ = store.TreeStamp(s.Root)

	m.loadPick()
	return m
}

// Init arms the tree poll. It runs for the whole session, on every screen: what the screens show is
// the disk, and the disk changes without asking this window.
func (m *Model) Init() tea.Cmd { return watchEvery() }

// reload re-reads what the loop knows: the skill's ledger and its measurements, and the rows of the
// split the current step is about. It is called after every write and every finished run, so no
// screen shows a number that came from before the change.
func (m *Model) reload() {
	// The list is only the list while nothing is open: a screen that has just named a skill is
	// already inside one, and reload's job there is that screen's reading.
	if m.mode == screenPick && m.skill == "" {
		m.loadPick()
		return
	}
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
		if msg.scope == "revision" {
			m.reloadProposal()
		}
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
	if m.runActive() || m.ask != nil || m.formActive() || m.assertEditActive() || m.caseNewActive() || m.feedbackActive() {
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
	// so the app goes back to the list rather than drawing a flow whose set is gone.
	if m.skill != "" && m.store.Skill(m.skill) == nil {
		gone := m.skill
		m.skill, m.flow = "", nil
		m.quality = nil
		m.mode = screenPick
		m.loadPick()
		m.noteStatus(gone+" is no longer in the tree — the list has been re-read", true)
		return true
	}
	m.reload()
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
	if m.caseNewActive() {
		return m, m.caseNew.update(m, msg)
	}
	if m.feedbackActive() {
		return m, m.quality.feedback.update(m, msg)
	}
	if m.formActive() {
		return m, m.form.update(m, msg)
	}
	if m.ask != nil {
		return m, m.ask.update(m, msg)
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
	case screenPick:
		return m, m.updatePick(msg)
	case screenFlow:
		return m, m.updateFlow(msg)
	case screenQuality:
		return m, m.updateQuality(msg)
	case screenRevision:
		return m, m.updateRevision(msg)
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
	if m.caseNewActive() {
		return m.caseNew.updateMsg(msg)
	}
	if m.feedbackActive() {
		return m.quality.feedback.updateMsg(msg)
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
	case screenFlow:
		// The workbench is a surface, not a step: esc leaves the work for the list it came from.
		// A wording that was being typed is the one thing on that screen the file does not hold, so
		// leaving it takes two escs — the first says what the second would cost.
		if m.flow != nil && m.flow.draftChanged() && !m.flow.discardArmed {
			m.flow.discardArmed = true
			m.setStatus("a sentence is being written — ctrl+s saves it, esc again leaves it behind", true)
			return nil
		}
		m.skill, m.flow = "", nil
		m.mode = screenPick
		m.loadPick()
	case screenQuality:
		m.quality = nil
		m.skill = ""
		m.mode = screenPick
		m.loadPick()
	case screenRevision:
		m.revision = nil
		m.setQualitySection(qDojo)
		m.mode = screenQuality
		m.setStatus("the proposal is still on disk — p opens it again, and SKILL.md is untouched", false)
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

// pathSegments is where you are, as a path. There are three surfaces now, so the path is short by
// construction: the list is one word, and everything else is the row it opened — the skill, the
// lane, and the document or the run on top of it. "Where am I" is a line you can read at a glance,
// which is the whole point of having no menus.
func (m *Model) pathSegments() []string {
	if m.mode == screenPick || m.mode == screenLane {
		return []string{"the work"}
	}
	if m.mode == screenSkill {
		return []string{"choose a skill"}
	}
	var segs []string
	if m.skill != "" {
		segs = append(segs, m.skill)
	}
	if m.lane == store.LaneOutput {
		segs = append(segs, "output")
	} else {
		segs = append(segs, "description")
	}
	switch m.mode {
	case screenQuality:
		// The lane has one work surface; its three sections are states of it, and a state that is
		// not the default one says its name on the path rather than leaving it to the region head.
		if m.quality != nil && m.quality.sec != qDojo {
			segs = append(segs, m.quality.sectionName())
		}
	case screenFlow:
		// Same rule on the other lane: the split is a toggle, so the path says when it is the half
		// the loop may not learn from.
		if m.flow != nil && m.flow.sec == secGeneralization {
			segs = append(segs, "validation")
		}
	case screenDoc:
		if m.doc != nil {
			segs = append(segs, m.doc.scope)
		}
	case screenRunLive:
		if m.run != nil {
			segs = append(segs, m.run.scope)
		}
	case screenRevision:
		segs = append(segs, "revision")
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
	// The work surface carries no rail: there is nothing to walk between. What the row holds is the
	// split the table is showing and the rule that set works under — the two facts a revision must
	// not get wrong — and, on the other lane, the loop's numbers.
	if m.mode == screenFlow && m.flow != nil {
		// Two words and a count: the rule the set works under is the status row's sentence, and a
		// row that says it twice costs the loop's counter its place on narrow windows.
		lead := setRegionWord(m.flow.sec == secIteration) + " · " + plural(len(m.flow.rows), "row")
		if facts := m.flow.stateLine(); facts != "" && lipgloss.Width(lead)+lipgloss.Width(facts)+6 <= w {
			return endsLine("  "+dimStyle.Render(lead), statValue.Render(facts)+" ", w)
		}
		return ellipsis("  "+dimStyle.Render(lead), w)
	}
	// The output lane's three views are reached from the menu and nowhere else, so a view carries no
	// rail of them: its path already ends in its own name, and the menu above is where the switch is.
	// What stays is the one row no view counts for itself — the loop's numbers.
	if m.mode == screenQuality {
		return m.qualityFactsLine(w)
	}
	switch m.mode {
	case screenRunLive:
		if m.run != nil {
			return dimStyle.Render("  " + m.run.hint)
		}
	case screenDoc:
		return ""
	}
	return ""
}

func (m *Model) bodyLines(h int) []string {
	switch m.mode {
	case screenPick:
		return m.pickBody(h)
	case screenFlow:
		if m.flow != nil {
			return m.flow.body(m, h)
		}
	case screenQuality:
		return m.qualityBody(h)
	case screenDoc:
		if m.doc != nil {
			// Three cells short of the frame: the document is drawn two cells in from the edge by its
			// own indent, and glamour keeps a margin of its own. Rendered at the full width, the last
			// two characters of every full line were cut by the frame — a report whose numbers run off
			// the edge is a report whose numbers cannot be read.
			m.doc.md(max(10, m.width-3))
			return m.doc.body(h)
		}
	case screenRunLive:
		if m.run != nil {
			switch m.run.kind {
			case runQuality, runGrade:
				return m.outputRunBody(h)
			case runRevise:
				return m.revisionRunBody(h)
			}
			return m.run.liveBody(m, h)
		}
	case screenRevision:
		return m.revisionBody(h)
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
	if m.ask != nil {
		return banded(footSeq, endsLine(hintLine(m.ask.hints()), helpChip.Render(" ? keys "), m.width), m.width)
	}
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
	case screenPick:
		return []hint{{"j k", "move"}, {"enter", "the work"}, {"q", "quit"}}
	case screenFlow:
		if m.flow != nil {
			return m.flow.hints(m)
		}
	case screenQuality:
		return qualityHints(m)
	case screenRevision:
		return []hint{{"j k space", "walk · tick a hunk"}, {"n N", "next change"}, {"a", "apply"},
			{"e", "edit"}, {"esc", "discard"}}
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
	// A question takes the row: it is the one thing on screen that wants an answer, and putting it
	// anywhere but where the keys are named would be a second place to look.
	if m.ask != nil {
		return m.ask.line(m.width)
	}
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
	case screenPick:
		return "the work"
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
		if m.assertEditActive() || m.caseNewActive() || m.feedbackActive() {
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
	case screenPick:
		if len(m.pick.rows) == 0 {
			return ""
		}
		return fmt.Sprintf("%s to work · the row under the cursor says what the disk is waiting for",
			plural(len(m.pick.rows), "lane"))
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
	case screenQualityMenu, screenQuality:
		// The menu's own line is where the loop stands: the lab's `next` is the one sentence that
		// says what the disk is waiting for, and the menu is the screen that offers it.
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
	case screenPick:
		return fmt.Sprintf("%d / %d", m.pick.cursor+1, max(1, len(m.pick.rows)))
	case screenFlow:
		if m.formActive() {
			return fmt.Sprintf("q%d", m.form.id)
		}
		f := m.flow
		if f == nil {
			return ""
		}
		// The area names the region (see region); this says where the cursor stands inside it.
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
	case screenQuality:
		// The corner names the case the caret is in, not the row the cursor stands on: while a case
		// is being written, the cursor's row is not what the keys are touching.
		if m.caseNewActive() {
			return m.caseNew.slug
		}
		if q := m.quality; q != nil {
			switch q.sec {
			case qDojo:
				if len(q.cases) > 0 {
					return fmt.Sprintf("case %d of %d", q.sel+1, len(q.cases))
				}
				return "nothing to work"
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
		head("output optimization — three views, chosen on one screen"),
		[]string{
			"  enter      the skill, then the view — the cursor is on the one its disk is asking for",
			"  1 2 3      initialization, iteration, testing quality — on the menu, and nowhere else",
			"  esc        one level back: a view to the menu, the menu to the skills",
			"  —          a view has no key to another view; its name is the path's last segment",
			"  —          the loop is closed until a graded run exists, and only initialization opens then",
			"  S e        the cases in evals.json · a case's assertions, one bullet each, in the pane",
			"  R          run the next iteration: every case, both arms, then the judge — one spend",
			"  g          grade an iteration with not graded (--no-grade, an older build); on a graded one",
			"             it re-grades after an assertion edit, and asks first",
			"  f          the review: what the output got wrong, in your words — empty means it looked fine",
			"  —          a run grades itself; the loop opens on the grades, never on the outputs",
			"  j k        walk the cases; tab moves the keys into the assertion table, j k then walk its rows",
			"  e n d      the case's assertions, one bullet each · a new case · delete the case under the cursor",
			"  r R        run the case under the cursor, both arms and judged · run every case (a run of one",
			"             case is a probe: it writes an iteration and never ranks)",
			"  f E        the review of the case, from any row · SKILL.md, which is what a revision changes",
			"  v enter    the two arms' outputs · the arm's activity log",
			"  —          the review is per case, not per assertion: an assertion is a sentence about the output",
			"  p          ask the revising agent for a new SKILL.md — the diff lands on its own screen,",
			"             one hunk per change: space ticks a hunk off, n jumps to the next, a writes",
			"  [ ]        the iteration the iteration view is reading",
			"  enter      the iteration on this case · its evidence from the testing table",
		},
		dim("every write backs the old file up under .evalctl/backups/"),
	)
	return out
}
