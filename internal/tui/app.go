package tui

import (
	"fmt"
	"os"
	"strings"

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
// and the only moves are the ones the loop has — forward with enter, back with esc, and the one verb
// the current step is asking for. There is no table to sort, no column to focus and no second menu:
// what used to be spread over four tabs and a skill list is now the rail at the top of every step
// screen, which is also the resume point.
//
// What the loop measures comes from internal/runner (the trigger lane) and what it writes comes from
// internal/store (the ledger, SKILL.md, the query set), so the TUI holds no state of its own that a
// file does not already carry: relaunching the binary reopens the step the disk says you are on.

type screen int

const (
	screenLane    screen = iota // the front door: the two optimizations
	screenSkill                 // which skill, with where each one is in the loop
	screenFlow                  // the loop's four steps, one filling the body
	screenForm                  // a query's own fields
	screenDraft                 // the description being revised
	screenAsk                   // a yes/no question
	screenDoc                   // a scrollable document (failures, a report, a log)
	screenRunAsk                // what a run will do, before it does it
	screenRunLive               // the run, while it runs
	screenHelp
)

// Model is the whole program state that is not on disk.
type Model struct {
	store *store.Store
	ver   string
	skill string // the skill being optimized ("" on the front door)

	width, height int
	mode          screen
	status        string
	statusErr     bool
	quitArmed     bool

	lanes  laneState
	skills skillState
	flow   *flowState
	form   *formState
	draft  *draftState
	ask    *askState
	doc    *docState
	run    *runState
	help   int

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
	if skill != "" {
		m.openSkill(skill)
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Println("evalctl tui:", err)
		return 1
	}
	return 0
}

func newModel(s *store.Store) *Model {
	return &Model{store: s, ver: version.Resolve().Short(), mode: screenLane}
}

func (m *Model) Init() tea.Cmd { return nil }

// reload re-reads what the loop knows: the skill's ledger and its measurements, and the rows of the
// split the current step is about. It is called after every write and every finished run, so no
// screen shows a number that came from before the change.
func (m *Model) reload() {
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
}

func (m *Model) setStatus(msg string, isErr bool) {
	m.status, m.statusErr = msg, isErr
}

func (m *Model) bodyHeight() int { return max(1, m.height-len(m.headLines())-2) }

// ---------------------------------------------------------------- update

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, m.resize()
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
	}
	return m, m.passToForm(msg)
}

func (m *Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	m.status = ""
	// A form owns its keystrokes: every character is a character, and the frame keys would eat the
	// ones the field needs.
	if m.mode == screenForm && m.form != nil {
		return m, m.form.update(m, msg)
	}
	if m.mode == screenDraft && m.draft != nil {
		return m, m.draft.update(m, msg)
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
	case screenFlow:
		return m, m.updateFlow(msg)
	case screenAsk:
		return m, m.ask.update(m, msg)
	case screenDoc:
		return m, m.doc.update(m, msg)
	case screenRunAsk:
		return m, m.updateRunAsk(msg)
	case screenRunLive:
		return m, m.updateRunLive(msg)
	case screenHelp:
		return m, m.updateHelp(msg)
	}
	return m, nil
}

func (m *Model) passToForm(msg tea.Msg) tea.Cmd {
	if m.mode == screenForm && m.form != nil {
		return m.form.updateMsg(msg)
	}
	return nil
}

// escUp is one level up, and it does exactly one thing per screen: the footer names the level so
// the key is never a guess.
func (m *Model) escUp() tea.Cmd {
	switch m.mode {
	case screenSkill:
		m.skill, m.flow = "", nil
		m.mode = screenLane
	case screenFlow:
		m.skill, m.flow = "", nil
		m.mode = screenSkill
	case screenForm:
		m.form, m.mode = nil, screenFlow
	case screenDraft:
		m.draft, m.mode = nil, screenFlow
	case screenAsk:
		m.ask, m.mode = nil, screenFlow
	case screenDoc:
		m.doc, m.mode = nil, m.backTo
	case screenRunAsk:
		m.run, m.mode = nil, screenFlow
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

func (m *Model) resize() tea.Cmd {
	if m.form != nil {
		return m.form.resize(m.width, m.bodyHeight())
	}
	return nil
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
	bar := endsLine(m.barLeft(), brandName.Render(" evalctl")+" "+brandVer.Render(m.ver), m.width)
	rule := tabRule.Render(strings.Repeat(glyphs.Heavy, max(1, m.width)))
	// The context row is prose of wildly different lengths — the rail on a step screen, a sentence
	// on the door — so it is cut here, once, rather than trusted per screen.
	return []string{bar, rule, ellipsis(m.contextLine(m.width), m.width)}
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
		return []string{"trigger optimization", "choose a skill"}
	}
	segs := []string{"trigger optimization"}
	if m.skill != "" {
		segs = append(segs, m.skill)
	}
	switch m.mode {
	case screenFlow:
		if m.flow != nil {
			segs = append(segs, stepNames[m.flow.step])
		}
	case screenForm:
		segs = append(segs, "query")
	case screenDraft:
		segs = append(segs, "revise")
	case screenDoc:
		if m.doc != nil {
			segs = append(segs, m.doc.scope)
		}
	case screenRunAsk, screenRunLive:
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
		rail := m.flow.rail(w)
		facts := m.flow.stateLine()
		// The numbers are the secondary register: when the row cannot hold both with a gutter between
		// them, they go — a rail cut in half loses the step you are in, and the body repeats them.
		if facts != "" && lipgloss.Width(rail)+lipgloss.Width(facts)+4 <= w {
			return endsLine(rail, statValue.Render(facts)+" ", w)
		}
		return rail
	}
	switch m.mode {
	case screenLane:
		return dimStyle.Render("  output is the other loop, and it is not built yet — the trigger loop comes first")
	case screenSkill:
		return dimStyle.Render("  the description loop is per skill — pick the one whose wording is being tuned")
	case screenForm:
		if m.form != nil {
			return dimStyle.Render("  " + m.form.hint)
		}
	case screenDraft:
		return dimStyle.Render("  the wording is the only thing the router sees before it decides whether to load the skill")
	case screenRunAsk, screenRunLive:
		if m.run != nil {
			return dimStyle.Render("  " + m.run.hint)
		}
	case screenDoc, screenAsk:
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
	case screenFlow:
		if m.flow != nil {
			return m.flow.body(m, h)
		}
	case screenForm:
		if m.form != nil {
			return m.form.body(h)
		}
	case screenDraft:
		if m.draft != nil {
			return m.draft.body(m, h)
		}
	case screenAsk:
		if m.ask != nil {
			return m.ask.body(h)
		}
	case screenDoc:
		if m.doc != nil {
			return m.doc.body(h)
		}
	case screenRunAsk:
		if m.run != nil {
			return m.run.askBody(m, h)
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
	case screenFlow:
		if m.flow != nil {
			return m.flow.hints(m)
		}
	case screenForm:
		if m.form != nil {
			return m.form.hints()
		}
	case screenDraft:
		if m.draft != nil {
			return m.draft.hints()
		}
	case screenAsk:
		if m.ask != nil {
			return m.ask.hints()
		}
	case screenDoc:
		return []hint{{"j k", "scroll"}, {"g G", "ends"}, {"esc", "back"}}
	case screenRunAsk:
		if m.run != nil {
			return m.run.askHints()
		}
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
	return endsLine(" "+style.Render(left), statValue.Render(m.position())+" ", m.width)
}

// stateText is what the status line says when nothing has just happened: the frame's own fact about
// where you are, which is cheaper to read than the rail.
func (m *Model) stateText() string {
	switch m.mode {
	case screenLane:
		return "the trigger loop is the one that is built"
	case screenSkill:
		return plural(len(m.skillNames()), "skill") + " with a description to tune"
	case screenFlow:
		if m.flow != nil {
			return m.flow.stateText()
		}
	case screenRunLive:
		if m.run != nil && m.run.live != nil {
			return m.run.live.stateText()
		}
	case screenRunAsk:
		return "nothing has been run yet"
	case screenForm:
		if m.form != nil {
			return m.form.facts()
		}
	case screenDraft:
		if m.draft != nil {
			return m.draft.stateText()
		}
	}
	return ""
}

func (m *Model) position() string {
	switch m.mode {
	case screenFlow:
		if m.flow != nil {
			return fmt.Sprintf("step %d of 4", m.flow.step)
		}
	case screenSkill:
		return fmt.Sprintf("%d / %d", m.skills.cursor+1, max(1, len(m.skillNames())))
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
			"  e          edit the row under the cursor",
			"  d          delete the row under the cursor (asks first)",
			"  space      flip the label: should trigger / should not",
			"  j k g G    move; first and last row",
		},
		head("step 3 — the iteration"),
		[]string{
			"  j k        pick a sub-step of the loop",
			"  enter      do it: run the train set, read the failures, or revise",
			"  ctrl+g     generalize before five iterations, with the warning",
		},
		head("step 4 — generalization"),
		[]string{
			"  enter      run the validation set for every measured iteration",
			"  a          apply the iteration under the cursor to SKILL.md",
			"  h          run the fresh queries (holdout) once, after applying",
		},
		head("a run"),
		[]string{
			"  ← →        runs per query, on the run screen",
			"  d          dry run: print the commands, write nothing",
			"  x          cancel the run; finished queries are kept",
			"  v          the arm's activity log; esc back",
		},
		dim("every write backs the old file up under .evalctl/backups/"),
	)
	return out
}
