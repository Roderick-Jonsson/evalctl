package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// ---------------------------------------------------------------- fixture

const skillMD = `---
name: slide-builder
description: Use this skill when an outline has to become slides.
---

# slide-builder

Turns an outline into a deck.
`

const triggerSet = `{
  "runs": 3,
  "threshold": 0.5,
  "queries": [
    {"id": 1, "skill": "slide-builder", "should_trigger": true,  "split": "train",      "why": "core ask",   "query": "build me slides from this outline"},
    {"id": 2, "skill": "slide-builder", "should_trigger": false, "split": "train",      "why": "near miss",  "query": "research the operator pattern for me"},
    {"id": 3, "skill": "slide-builder", "should_trigger": true,  "split": "validation", "why": "held back",  "query": "turn PO-RevB into a deck"},
    {"id": 4, "skill": "slide-builder", "should_trigger": false, "split": "validation", "why": "held back",  "query": "what is a controller loop"}
  ]
}`

// fixture builds a tree with one skill, its description and a four-row trigger set, and returns a
// sized model — sized because every screen here is a window and a zero-height model renders no frame.
func fixture(t *testing.T) (*Model, string, *store.Store) {
	t.Helper()
	root := t.TempDir()
	// No run ever reaches an agent from a test: the binary the runner would exec does not exist, so a
	// keystroke that starts one fails at once instead of spending a session's worth of tokens — and
	// the profile it would have provisioned is provisioned under the fixture, never in ~/.hermes.
	t.Setenv("HERMES_BIN", filepath.Join(root, "no-such-hermes"))
	t.Setenv("HERMES_ROOT", filepath.Join(root, "hermes"))
	t.Setenv("EVAL_MASTERCLASS_ROOT", filepath.Join(root, "masterclasses"))
	write(t, filepath.Join(root, "trigger", "trigger-evals.json"), triggerSet)
	write(t, filepath.Join(root, "slide-builder", "evals", "evals.json"), `{"skill_name":"slide-builder","evals":[]}`)
	write(t, filepath.Join(root, "slide-builder", "SKILL.md"), skillMD)
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(s)
	m.width, m.height = 110, 32
	return m, root, s
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// stripMarks removes the SGR sequences a styled line carries, so a test can assert on the text.
func stripMarks(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			out.WriteByte(s[i])
			continue
		}
		// Skip to the sequence's final byte; the loop's own step then lands on the first character
		// of the text, which used to be thrown away with it.
		for i < len(s) && s[i] != 'm' {
			i++
		}
	}
	return out.String()
}

// enterSection opens one half of the loop the way a person does: from the section screen.
// enterSection puts the model on the work surface with the split asked for. The sections screen is
// gone: a test that wants the validation half asks the toggle for it.
func enterSection(m *Model, sec int) {
	if m.flow == nil {
		m.openWork(pickRow{skill: m.skill, lane: store.LaneTrigger})
	}
	m.mode = screenFlow
	m.flow.sec, m.flow.step = sec, 1
	m.reload()
	// The frame is what seeds the wording's field in the app; a test that presses a key without
	// drawing one seeds it the same way.
	m.ensureDraft()
}

// triggerFile is the trigger set as the file holds it, for the tests that pin "a read writes nothing".
func triggerFile(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "trigger", "trigger-evals.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func press(m *Model, key string) tea.Cmd {
	var msg tea.KeyMsg
	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "space":
		msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	case "ctrl+g":
		msg = tea.KeyMsg{Type: tea.KeyCtrlG}
	case "ctrl+s":
		msg = tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+d":
		msg = tea.KeyMsg{Type: tea.KeyCtrlD}
	case "left":
		msg = tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		msg = tea.KeyMsg{Type: tea.KeyRight}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "home":
		msg = tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		msg = tea.KeyMsg{Type: tea.KeyEnd}
	case "backspace":
		msg = tea.KeyMsg{Type: tea.KeyBackspace}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	_, cmd := m.Update(msg)
	return cmd
}

// row is one scored query: hit says the run's verdict was the one the label asked for.
func row(id int, split string, should, hit bool) runner.ScoreRow {
	passed := hit
	rate := 0.0
	if hit {
		rate = 1
	}
	triggers := 0
	if hit {
		triggers = 3
	}
	return runner.ScoreRow{ID: id, Skill: "slide-builder", Split: split, ShouldTrigger: should,
		Runs: 3, ValidRuns: 3, Triggers: triggers, Rate: &rate, Passed: &passed}
}

// iterationDir fabricates one finished trigger iteration — the description it ran and its score —
// so the loop can be tested without spending agent time.
func iterationDir(t *testing.T, root, description string, iteration int, rows []runner.ScoreRow) {
	t.Helper()
	dir := filepath.Join(root, "trigger-workspace", fmt.Sprintf("trigger-%d", iteration))
	meta := fmt.Sprintf(`{"iteration":%d,"lane":"trigger","started":"2026-10-30T10:0%d:00+0100","runs":3,"threshold":0.5,
  "provenance":{"descriptions":{%q:{"chars":%d,"description":%q}}}}`,
		iteration, iteration%10, "slide-builder", len([]rune(description)), description)
	write(t, filepath.Join(dir, "meta.json"), meta)
	score := runner.TriggerScore{Threshold: 0.5, Runs: 3, Rows: rows,
		Skills: map[string]runner.SkillScore{}, Splits: map[string]runner.Tally{}, Overall: runner.Tally{}}
	blob, err := json.Marshal(map[string]any{"score": score})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "trigger.json"), string(blob))
}

// measured builds a model whose skill has one measured iteration: candidate #1, measured on train.
func measured(t *testing.T, rows []runner.ScoreRow) (*Model, string) {
	t.Helper()
	m, root, s := fixture(t)
	desc := s.SkillDescription("slide-builder")
	iterationDir(t, root, desc, 1, rows)
	m.openSkill("slide-builder")
	return m, root
}

// ---------------------------------------------------------------- the door

// TestTheListOffersBothLanes: the door and the two choosers are one list, and a skill worked in both
// lanes is two rows on it — the lane is a badge, not a screen you pass through.
func TestTheListOffersBothLanes(t *testing.T) {
	m, _ := qualityFixture(t)
	joined := stripMarks(strings.Join(m.pickBody(m.bodyHeight()), "\n"))
	if !strings.Contains(joined, "description") || !strings.Contains(joined, "output") {
		t.Fatalf("the list must carry both lanes:\n%s", joined)
	}
	// The output row opens the work surface, with no menu in between.
	idx := -1
	for i, r := range m.pick.rows {
		if r.lane == store.LaneOutput {
			idx = i
		}
	}
	m.pick.cursor = idx
	press(m, "enter")
	if m.mode != screenQuality || m.lane != store.LaneOutput {
		t.Fatalf("the output row opened mode %v lane %q", m.mode, m.lane)
	}
	press(m, "esc")
	if m.mode != screenPick {
		t.Fatalf("esc = mode %v, want the list", m.mode)
	}
}

func TestTheListOpensTheDescriptionLane(t *testing.T) {
	m, _, _ := fixture(t)
	if m.mode != screenPick {
		t.Fatalf("mode = %v, want the work list", m.mode)
	}
	// One skill is two rows: a description to tune and a set to declare, both startable from nothing.
	if len(m.pick.rows) != 2 {
		t.Fatalf("rows = %d, want the two lanes", len(m.pick.rows))
	}
	idx := -1
	for i, r := range m.pick.rows {
		if r.lane == store.LaneTrigger {
			idx = i
			if !strings.Contains(r.state, "measured") {
				t.Fatalf("a skill with no measurements must say where the loop stands: %q", r.state)
			}
		}
	}
	m.pick.cursor = idx
	press(m, "enter")
	if m.mode != screenFlow || m.skill != "slide-builder" {
		t.Fatalf("mode = %v skill = %q, want the workbench", m.mode, m.skill)
	}
}

// ---------------------------------------------------------------- the work surface

// TestTheSplitIsAToggleOnTheSameScreen: iteration and generalization are two readings of one table,
// so the key swaps the rows under the same workbench rather than opening a screen of its own.
func TestTheSplitIsAToggleOnTheSameScreen(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, false),
		row(3, "validation", true, true),
	})
	if m.flow.rows[0].Split != store.SplitTrain {
		t.Fatalf("the workbench opened on %q, want the train set", m.flow.rows[0].Split)
	}
	press(m, "s")
	if m.flow.sec != secGeneralization || m.flow.rows[0].Split != store.SplitValidation {
		t.Fatalf("s = section %d rows %q, want the validation half", m.flow.sec, m.flow.rows[0].Split)
	}
	if !strings.Contains(stripMarks(m.barLeft()), "validation") {
		t.Fatalf("the path does not say which half: %q", stripMarks(m.barLeft()))
	}
	// The blocks are still the workbench's: the set, the wording, the wordings measured.
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	for _, want := range []string{"VALIDATION SET", "THE DESCRIPTION", "ITERATIONS"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the split changed the screen and lost %q:\n%s", want, body)
		}
	}
	press(m, "s")
	if m.flow.sec != secIteration {
		t.Fatalf("s twice = section %d, want the train half back", m.flow.sec)
	}
}

// TestTheRunsKnobIsAKeyOnTheWork: the section pane's two knobs are the work surface's keys now —
// runs per query is written into the file, dry run is the session's.
func TestTheRunsKnobIsAKeyOnTheWork(t *testing.T) {
	m, root := measured(t, []runner.ScoreRow{row(1, "train", true, true)})
	before := m.flow.lab.Runs
	press(m, "+")
	if m.flow.lab.Runs != before+1 {
		t.Fatalf("+ gave %d runs, want %d", m.flow.lab.Runs, before+1)
	}
	if !strings.Contains(triggerFile(t, root), fmt.Sprintf("\"runs\": %d", before+1)) {
		t.Fatalf("the runs knob did not write the file:\n%s", triggerFile(t, root))
	}
	press(m, "-")
	if m.flow.lab.Runs != before {
		t.Fatalf("- gave %d runs, want %d", m.flow.lab.Runs, before)
	}
	press(m, "x")
	if !m.flow.dry {
		t.Fatal("x must arm a dry run")
	}
	press(m, "x")
	if m.flow.dry {
		t.Fatal("x twice must disarm it")
	}
}

// TestSpaceFlipsTheLabelAndWritesTheFile: the row under the cursor is edited in the block it is read
// in, and the write is the file's.
func TestSpaceFlipsTheLabelAndWritesTheFile(t *testing.T) {
	m, root := measured(t, []runner.ScoreRow{row(1, "train", true, true)})
	before := triggerFile(t, root)
	press(m, "space")
	after := triggerFile(t, root)
	if before == after {
		t.Fatal("space wrote nothing")
	}
	if !strings.Contains(after, "\"should_trigger\": false") && !strings.Contains(after, "\"should_trigger\": true") {
		t.Fatalf("the write is not the label:\n%s", after)
	}
	if !strings.Contains(stripMarks(m.status), "q1") {
		t.Fatalf("status = %q, want the row named", stripMarks(m.status))
	}
}

// TestTheGateHoldsUntilFiveWordings: the ranking is a read of the wordings measured, and ctrl+g is the
// one key that bypasses the count — saying so out loud.
func TestTheGateHoldsUntilFiveWordings(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{row(1, "train", true, true)})
	if m.flow.gateOpen() {
		t.Fatal("one measured wording must not open the ranking")
	}
	press(m, "ctrl+g")
	if !m.flow.forced {
		t.Fatal("ctrl+g must record that the gate was forced")
	}
	if !strings.Contains(stripMarks(m.status), "unproven") {
		t.Fatalf("status = %q, want the gate's cost named", stripMarks(m.status))
	}
}

// TestApplyWritesTheWinnerIntoSkillMD: the pick is `a` on the work surface, and it writes SKILL.md.
func TestApplyWritesTheWinnerIntoSkillMD(t *testing.T) {
	m, root := measured(t, []runner.ScoreRow{row(1, "train", true, true),
		row(3, "validation", true, true), row(4, "validation", false, true)})
	want := m.store.SkillDescription("slide-builder")
	press(m, "a")
	if m.ask == nil {
		t.Fatalf("a must ask before writing: %q", stripMarks(m.status))
	}
	press(m, "y")
	got, err := os.ReadFile(filepath.Join(root, "slide-builder", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), want) {
		t.Fatalf("SKILL.md does not carry the wording:\n%s", got)
	}
}

// TestEveryScreenFitsTheWindow is the one property no screen may break: the view is the height of
// the terminal, whatever it holds, and the status line is its last row.
func TestEveryScreenFitsTheWindow(t *testing.T) {
	m, root, s := fixture(t)
	// A measured wording, so the ranking is drawn with a row in it and the line that offers the
	// rates it is missing — the two places this screen can overrun its width.
	iterationDir(t, root, s.SkillDescription("slide-builder"), 1, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, false),
	})
	screens := []struct {
		name string
		open func()
	}{
		{"the work list", func() { m.mode = screenPick; m.loadPick() }},
		{"the workbench", func() {
			m.openWork(pickRow{skill: "slide-builder", lane: store.LaneTrigger})
			m.flow.sec, m.flow.step = secIteration, 1
			m.reload()
		}},
		{"the validation half", func() { m.flow.sec = secGeneralization; m.reload() }},
		{"the wording editor", func() {
			m.flow.focus = focusWording
			m.flow.applyFocus()
		}},
		{"help", func() { m.mode = screenHelp }},
		{"document", func() { m.mode = screenDoc; m.doc = &docState{title: "t", lines: []string{"a", "b"}} }},
	}
	for _, sc := range screens {
		sc.open()
		for _, size := range [][2]int{{110, 32}, {80, 24}, {60, 16}} {
			m.width, m.height = size[0], size[1]
			view := m.View()
			if got := len(strings.Split(view, "\n")); got != size[1] {
				t.Fatalf("%s at %dx%d: %d rows", sc.name, size[0], size[1], got)
			}
			for _, line := range strings.Split(view, "\n") {
				if w := len([]rune(line)); w > size[0] {
					t.Fatalf("%s at %dx%d: a line is %d cells wide:\n%s", sc.name, size[0], size[1], w, line)
				}
			}
		}
	}
}

// TestAValidatedOnlyWordingIsStillPending: a description that has only been through the validation
// set is not an iteration — the loop's unit is a train run — and it must not fall between the two
// states the ladder reads.
func TestAValidatedOnlyWordingIsStillPending(t *testing.T) {
	m, root, s := fixture(t)
	desc := s.SkillDescription("slide-builder")
	iterationDir(t, root, desc, 1, []runner.ScoreRow{
		row(3, "validation", true, true),
		row(4, "validation", false, true),
	})
	m.openSkill("slide-builder")
	if len(m.flow.iters) != 0 {
		t.Fatalf("iterations = %d, want none: no train run has happened", len(m.flow.iters))
	}
	if m.flow.pending() == nil {
		t.Fatal("a wording with no train measurement must read as pending, not as measured")
	}
	// The row under the bar names the split and counts the loop: the pending wording is what the
	// counter counts, because it is the one the next run would measure.
	m.width, m.height = 80, 24
	enterSection(m, secIteration)
	if got := stripMarks(strings.Split(m.View(), "\n")[2]); !strings.Contains(got, "train set") ||
		!strings.Contains(got, "1 of 5") {
		t.Fatalf("state line = %q, want the split and the loop's own counter", got)
	}
}

// TestTheASCIIProfileDrawsOnlyASCII is the promise `--glyphs ascii` makes: a terminal without UTF-8
// gets the same frame in plain characters, not a row of replacement boxes. Only the frame's own
// marks are covered — the queries, descriptions and notes are the set's text and are not ours to
// transliterate.
func TestTheASCIIProfileDrawsOnlyASCII(t *testing.T) {
	defer setName(unicodeGlyphs)
	setName(asciiGlyphs)
	v := reflect.ValueOf(asciiGlyphs)
	for i := 0; i < v.NumField(); i++ {
		mark := v.Field(i).String()
		for _, r := range mark {
			if r > 127 {
				t.Fatalf("the ascii profile carries %q in %s", mark, v.Type().Field(i).Name)
			}
		}
	}
	m, _, _ := fixture(t)
	m.width, m.height = 100, 30
	m.openSkill("slide-builder")
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.ContainsAny(line, "▸✓✗⟳…") {
			t.Fatalf("an ascii frame drew a unicode mark:\n%s", line)
		}
	}
}

// TestTheBarNamesTheToolAtTheRightEdge: the row that tells you where you are ends with the tool and
// the build, so a screenshot names its own binary from the corner, and the left edge is free for the
// scope. The marks are stripped first because the assertions are about what a person reads.
func TestTheBarNamesTheToolAtTheRightEdge(t *testing.T) {
	m, _, _ := fixture(t)
	m.width, m.height = 100, 30
	m.openSkill("slide-builder")
	plain := stripMarks(strings.Split(m.View(), "\n")[0])
	if !strings.HasSuffix(plain, "evalctl "+m.ver) {
		t.Fatalf("the bar must end with the tool and its build:\n%s", plain)
	}
	if strings.HasPrefix(plain, "evalctl") {
		t.Fatalf("the tool still opens the bar:\n%s", plain)
	}
	if !strings.HasPrefix(strings.TrimSpace(plain), "slide-builder > description") {
		t.Fatalf("the left edge must name the skill and the lane:\n%s", plain)
	}
}

// TestTheBarIsAPathNotASentence: the top row is a path — the optimization, the skill, the step —
// with `>` between the segments. It carried the loop's numbers too, separated by middots, and three
// words and two dots read as a sentence rather than as somewhere you are; the numbers live at the
// right end of the rail row now.
func TestTheBarIsAPathNotASentence(t *testing.T) {
	m, _, _ := fixture(t)
	m.width, m.height = 120, 30
	m.openSkill("slide-builder")
	enterSection(m, secIteration)
	lines := strings.Split(m.View(), "\n")
	bar := stripMarks(lines[0])
	if !strings.Contains(bar, "slide-builder > description") {
		t.Fatalf("the bar is not a path:\n%s", bar)
	}
	if strings.Contains(bar, " · ") {
		t.Fatalf("the bar still separates its segments with dots:\n%s", bar)
	}
	// The bar stops at the path: the split and the loop's count live on the row under it, where the
	// work surface names what the table is showing.
	if got := stripMarks(lines[2]); !strings.Contains(got, "train set") {
		t.Fatalf("the row under the bar does not name the split:\n%s", got)
	}
	if view := stripMarks(m.View()); !strings.Contains(view, "ITERATIONS") ||
		!strings.Contains(view, "of 5") {
		t.Fatalf("the iterations table does not carry the loop's count:\n%s", view)
	}
	// The path narrows with the screen instead of running over it.
	m.width = 60
	if got := stripMarks(strings.Split(m.View(), "\n")[0]); strings.Contains(got, "evalctl v") == false {
		t.Fatalf("the build left the bar:\n%s", got)
	}
}

// TestTheCornerNamesTheBuild: which binary is on screen is a fact of the frame, not of `evalctl
// version` in another window — a stale bin/ looks exactly like a fresh one otherwise.
func TestTheCornerNamesTheBuild(t *testing.T) {
	m, _, _ := fixture(t)
	m.ver, m.built = "v0.8.0+", "11:06"
	if got := stripMarks(strings.Split(m.View(), "\n")[0]); !strings.Contains(got, "v0.8.0+ · 11:06") {
		t.Fatalf("the build time is not in the corner:\n%s", got)
	}
	// An unstamped binary says nothing rather than a time it does not have.
	m.built = ""
	got := stripMarks(strings.Split(m.View(), "\n")[0])
	if strings.Contains(got, "·") || !strings.Contains(got, "evalctl v0.8.0+") {
		t.Fatalf("an unstamped build grew a corner it cannot fill:\n%s", got)
	}
}

// ---------------------------------------------------------------- the tree is live

// The chooser is a list of what is on disk, so a skill added while the tool is running has to appear
// on its own. The bug this pins: the store was read once at startup, and every screen was drawn from
// that reading — a new skill stayed invisible until the binary was restarted, which reads as the tool
// having lost it.
func TestANewSkillAppearsWithoutARestart(t *testing.T) {
	m, root, _ := fixture(t)
	if len(m.pick.rows) != 2 {
		t.Fatalf("rows = %d, want the fixture's one skill in both lanes", len(m.pick.rows))
	}
	write(t, filepath.Join(root, "dummy", "evals", "evals.json"), `{"skill_name":"dummy","evals":[]}`)
	write(t, filepath.Join(root, "dummy", "SKILL.md"), strings.Replace(skillMD, "slide-builder", "dummy", -1))

	m.syncTree()

	names := make([]string, 0, len(m.pick.rows))
	for _, r := range m.pick.rows {
		names = append(names, r.skill)
	}
	// Two rows per skill now, and the new skill sorts above the old one in both lanes.
	if got := strings.Join(names, ","); got != "dummy,dummy,slide-builder,slide-builder" {
		t.Fatalf("rows = %s, want the new skill listed where it sorts", got)
	}
	// The new row sorts above the one under the cursor, and the cursor follows the work, not the index.
	if m.pick.rows[m.pick.cursor].skill != "slide-builder" {
		t.Fatalf("the cursor moved to %q when a row was inserted above it", m.pick.rows[m.pick.cursor].skill)
	}
	if !strings.Contains(stripMarks(m.View()), "dummy") {
		t.Fatal("the new skill is in the model but not on the screen")
	}
}

// The loop a screen is inside can leave the tree too: the skill's dir gone means its set and its
// ledger are gone, and a flow drawn over them would be a screen with nothing behind it.
func TestASkillThatLeavesTheTreeGoesBackToTheList(t *testing.T) {
	m, root, _ := fixture(t)
	m.openWork(pickRow{skill: "slide-builder", lane: store.LaneTrigger})
	if m.mode != screenFlow {
		t.Fatalf("mode = %d, want the workbench", m.mode)
	}
	if err := os.RemoveAll(filepath.Join(root, "slide-builder")); err != nil {
		t.Fatal(err)
	}
	m.syncTree()
	if m.skill != "" || m.flow != nil || m.mode != screenPick {
		t.Fatalf("skill = %q flow = %v mode = %d, want the work list with no skill open", m.skill, m.flow, m.mode)
	}
	if len(m.pick.rows) != 0 {
		t.Fatalf("rows = %+v, want the list emptied of a skill that is gone", m.pick.rows)
	}
	if !strings.Contains(m.status, "no longer in the tree") {
		t.Fatalf("status = %q, want the disappearance said out loud", m.status)
	}
}

// A form carries the store index of the row it will write, and a question carries the row itself:
// swapping the store under either one would apply the edit to a row nobody is looking at. Those two
// screens hold the re-read back instead of losing the change.
func TestAChangeWaitsForTheForm(t *testing.T) {
	m, root, _ := fixture(t)
	m.openSkill("slide-builder")
	enterSection(m, secIteration)
	// A field with the caret holds a store index and a draft the user is looking at; that is the state
	// the re-read waits for.
	m.form = &formState{index: 0, active: true}
	write(t, filepath.Join(root, "dummy", "evals", "evals.json"), `{"skill_name":"dummy","evals":[]}`)
	held := m.store
	m.syncTree()
	if m.store != held {
		t.Fatal("the store was swapped under an open form")
	}
	// The keys come back: the next tick is the one that re-reads.
	m.form = nil
	m.syncTree()
	if m.store == held {
		t.Fatal("the change was forgotten once the form closed")
	}
	if m.store.Skill("dummy") == nil {
		t.Fatal("the new skill is still missing after the form closed")
	}
}

// A tree caught mid-write must not replace the reading that works: the store that opens cleanly is
// the one on screen, and the half-written file is retried rather than adopted.
func TestATreeCaughtMidWriteKeepsTheReadingAndRetries(t *testing.T) {
	m, root, _ := fixture(t)
	m.loadPick()
	held := m.store
	set := filepath.Join(root, "trigger", "trigger-evals.json")
	write(t, set, `{"queries": [`)
	m.syncTree()
	if m.store != held {
		t.Fatal("a half-written trigger set replaced the store")
	}
	write(t, set, strings.Replace(triggerSet, `"core ask"`, `"core ask, rewritten"`, 1))
	m.syncTree()
	if m.store == held {
		t.Fatal("the tree was never re-read after it became readable")
	}
	if len(m.pick.rows) != 2 || !strings.Contains(m.pick.rows[0].state, "measured") {
		t.Fatalf("rows = %+v, want the rewritten set read back", m.pick.rows)
	}
}

// ---------------------------------------------------------------- the loop's two failures

// TestARevisionWrittenBetweenRunsIsStillMeasurable: a wording saved and then revised again before any
// run leaves a candidate no train run ever measured. Nothing should measure it — a later revision
// stepped over it, and measuring it would put the superseded wording back into SKILL.md — so it is
// neither an iteration nor the run's target, and the ranking says that in words instead of leaving
// the row count to be guessed. A draft nothing has revised yet is the run's target, and it is the
// case the last-candidate check used to get wrong.
func TestARevisionWrittenBetweenRunsIsStillMeasurable(t *testing.T) {
	m, root, s := fixture(t)
	m.openSkill("slide-builder")
	first := s.SkillDescription("slide-builder")
	stranded := "Use this skill when an outline has to become slides, when the user says slides."
	third := "Use this skill when an outline has to become slides, even if the user says deck."
	if _, err := runner.New(s).SaveDescriptionCandidate("slide-builder", stranded, "named slides", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.New(s).SaveDescriptionCandidate("slide-builder", third, "named deck", 2); err != nil {
		t.Fatal(err)
	}
	// #1 and #3 are measured on train; #2 was written between the two runs and never was.
	iterationDir(t, root, first, 1, []runner.ScoreRow{row(1, "train", true, true), row(2, "train", false, true)})
	iterationDir(t, root, third, 2, []runner.ScoreRow{row(1, "train", true, true), row(2, "train", false, true)})
	m.reload()

	if len(m.flow.iters) != 2 {
		t.Fatalf("iterations = %d, want the two measured wordings", len(m.flow.iters))
	}
	if pend := m.flow.pending(); pend != nil {
		t.Fatalf("pending = %+v, want none: #2 was revised into #3, so it is history and not the run's target", pend)
	}
	draft, live := m.flow.draftOf()
	if draft == nil || draft.ID != 2 || live {
		t.Fatalf("draft = %+v live=%v, want the superseded #2 named as such", draft, live)
	}
	if len(m.flow.iters) != 2 || m.flow.iters[1].Cand.ID != 3 {
		t.Fatalf("the table is %+v, want #1 and #3 and nothing else", m.flow.iters)
	}

	// A draft nothing has revised is the wording the next train run measures, and the row key says
	// which candidate it is when "iteration 3" and "candidate #3" are not the same wording.
	fourth := "Use this skill when an outline has to become slides, however the user names the output."
	if _, err := runner.New(s).SaveDescriptionCandidate("slide-builder", fourth, "any output name", 3); err != nil {
		t.Fatal(err)
	}
	m.reload()
	pend := m.flow.pending()
	if pend == nil || pend.ID != 4 {
		t.Fatalf("pending = %+v, want the live draft #4", pend)
	}
}

// TestAPartialValidationNeverRanks: a batch stopped halfway leaves a rate over the queries that
// landed. It is drawn as a rate over that many of the set, offered for a re-run, and it does not
// compete with a wording that was measured on all of it.
func TestAPartialValidationNeverRanks(t *testing.T) {
	m, root, s := fixture(t)
	m.openSkill("slide-builder")
	first := s.SkillDescription("slide-builder")
	second := "Use this skill when an outline has to become slides — even when the user calls them a deck."
	if _, err := runner.New(s).SaveDescriptionCandidate("slide-builder", second, "named the deck", 1); err != nil {
		t.Fatal(err)
	}
	// The set has two validation rows. #1 was stopped after the first; #2 saw both.
	iterationDir(t, root, first, 1, []runner.ScoreRow{
		row(1, "train", true, false), row(2, "train", false, true),
		row(3, "validation", true, true),
	})
	iterationDir(t, root, second, 2, []runner.ScoreRow{
		row(1, "train", true, false), row(2, "train", false, true),
		row(3, "validation", true, false), row(4, "validation", false, true),
	})
	m.reload()

	if m.needsValidation() != 1 {
		t.Fatalf("needsValidation = %d, want the one stopped after part of the set", m.needsValidation())
	}
	if best := bestIteration(m.flow); best == nil || best.Cand.ID != 2 {
		t.Fatalf("best = %+v, want #2: a 1/1 over one of two queries is not a validation rate", best)
	}
	if m.flow.gen != nil {
		t.Fatalf("a partial rate queued a batch on its own: %+v", m.flow.gen)
	}
}

// TestCancelStopsTheValidationQueue: x is the operator stopping the batch. The queue behind the
// cancelled run is dropped, not advanced — advancing it started the next candidate's validation run
// the same second and made one cancel look like a batch that never stops.
func TestCancelStopsTheValidationQueue(t *testing.T) {
	m, _, _ := fixture(t)
	m.openSkill("slide-builder")
	m.flow.gen = &genState{queue: []int{1, 2, 3}, at: 1}
	m.run = &runState{kind: runValidation, cand: 2, gen: true, live: newLiveRun("validation run · candidate #2", false)}
	m.run.live.cancelled = true
	m.mode = screenRunLive

	if cmd := m.finishRun(); cmd != nil {
		t.Fatal("a cancelled batch started another run")
	}
	if m.flow.gen != nil {
		t.Fatalf("the queue survived the cancel: %+v", m.flow.gen)
	}
	if m.run == nil || m.run.cand != 2 {
		t.Fatal("a new run was started from the cancelled queue")
	}
	if !strings.Contains(m.run.live.summary, "stopped at candidate 2 of 3") {
		t.Fatalf("summary = %q, want the batch's stopping point", m.run.live.summary)
	}
}

// TestABareSkillCanBeStartedInBothLanes: a skill somebody has just made has no description and no
// cases, and both lanes are still rows on the list — the way to start them is what the list is for.
func TestABareSkillCanBeStartedInBothLanes(t *testing.T) {
	m, root, _ := fixture(t)
	// A dir with a SKILL.md (and no description in it) and no eval file at all.
	write(t, filepath.Join(root, "fresh", "SKILL.md"), "---\nname: fresh\ndescription:\n---\n\nNothing yet.\n")
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	m.store = s
	m.loadPick()

	var desc, out *pickRow
	for i := range m.pick.rows {
		r := &m.pick.rows[i]
		if r.skill != "fresh" {
			continue
		}
		if r.lane == store.LaneTrigger {
			desc = r
		} else {
			out = r
		}
	}
	if desc == nil || out == nil {
		t.Fatalf("a bare skill is not two rows: %+v", m.pick.rows)
	}
	if !strings.Contains(desc.state, "no description") || !strings.Contains(desc.action, "ctrl+s") {
		t.Fatalf("the description row does not say where to start: %+v", desc)
	}
	if !strings.Contains(out.state, "no cases") || !strings.Contains(out.action, "n makes the first case") {
		t.Fatalf("the output row does not say where to start: %+v", out)
	}

	// Both walk in: the wording block with nothing in it, and the set with nothing declared.
	for i, r := range m.pick.rows {
		if r.skill == "fresh" && r.lane == store.LaneTrigger {
			m.pick.cursor = i
		}
	}
	press(m, "enter")
	if m.mode != screenFlow || m.skill != "fresh" || m.flow == nil {
		t.Fatalf("the description row opened mode %v skill %q", m.mode, m.skill)
	}
	if m.flow.draft == nil || strings.TrimSpace(m.flow.draft.text.Value()) != "" {
		t.Fatalf("the wording block must start empty on a skill with no description")
	}
	press(m, "esc")
	press(m, "esc")

	for i, r := range m.pick.rows {
		if r.skill == "fresh" && r.lane == store.LaneOutput {
			m.pick.cursor = i
		}
	}
	press(m, "enter")
	if m.mode != screenQuality || m.quality == nil || m.quality.sec != qSetup {
		t.Fatalf("the output row opened mode %v section %v, want the set", m.mode, m.quality)
	}
	press(m, "n")
	if !m.caseNewActive() {
		t.Fatal("n must make the first case of a skill with no cases")
	}
}
