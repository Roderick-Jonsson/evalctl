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
func enterSection(m *Model, sec int) {
	m.mode = screenSections
	m.sections.cursor = sec
	press(m, "enter")
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

func TestFrontDoorOffersBothLanes(t *testing.T) {
	m, _, _ := fixture(t)
	view := m.laneBody(20)
	joined := strings.Join(view, "\n")
	if !strings.Contains(joined, "trigger optimization") || !strings.Contains(joined, "output optimization") {
		t.Fatalf("the door must name both optimizations:\n%s", joined)
	}
	if strings.Contains(joined, "not built") {
		t.Fatalf("both optimizations are built and the door must not say otherwise:\n%s", joined)
	}
	// Down to output, then enter: the skill chooser, in the output lane.
	press(m, "j")
	press(m, "enter")
	if m.mode != screenSkill || m.lane != "output" {
		t.Fatalf("output opened mode %v lane %q, want the output skill list", m.mode, m.lane)
	}
}

func TestTriggerLaneOpensTheSkillList(t *testing.T) {
	m, _, _ := fixture(t)
	press(m, "enter")
	if m.mode != screenSkill {
		t.Fatalf("mode = %v, want the skill list", m.mode)
	}
	m.loadSkills()
	if len(m.skills.rows) != 1 || m.skills.rows[0].name != "slide-builder" {
		t.Fatalf("rows = %+v", m.skills.rows)
	}
	if !strings.Contains(m.skills.rows[0].state, "measured") {
		t.Fatalf("a skill with no measurements must say where the loop stands: %q", m.skills.rows[0].state)
	}
	press(m, "enter")
	if m.mode != screenSections || m.skill != "slide-builder" {
		t.Fatalf("mode = %v skill = %q, want the section choice", m.mode, m.skill)
	}
}

// ---------------------------------------------------------------- the section screen

// TestTheSectionScreenShowsTheSetThatSectionWorks: the pane is the split the cursor stands on, not a
// paragraph about it, and it follows the cursor.
func TestTheSectionScreenShowsTheSetThatSectionWorks(t *testing.T) {
	m, _, _ := fixture(t)
	m.openSkill("slide-builder")
	if m.sections.focus != sectionFocusList {
		t.Fatalf("focus = %d, want the list: the screen opens on the choice", m.sections.focus)
	}
	pane := func() string { return stripMarks(strings.Join(m.sectionsBody(m.bodyHeight()), "\n")) }
	if got := pane(); !strings.Contains(got, setRegionName(true)) || !strings.Contains(got, "build me slides from this outline") {
		t.Fatalf("the pane must show the train set beside iteration:\n%s", got)
	}
	press(m, "j") // generalization
	if m.sections.cursor != secGeneralization {
		t.Fatalf("cursor = %d", m.sections.cursor)
	}
	if len(m.sections.rows) != 2 || m.sections.rows[0].Split != store.SplitValidation {
		t.Fatalf("rows = %+v, want the validation split", m.sections.rows)
	}
	if got := pane(); !strings.Contains(got, setRegionName(false)) || !strings.Contains(got, "turn PO-RevB into a deck") {
		t.Fatalf("the pane must follow the cursor onto the validation set:\n%s", got)
	}
}

// TestThePaneCarriesTheRunsKnob: the run's settings are a block of their own in the pane, with a name
// and two knobs — `tab` walks into them from the table the way tab walks into a field, the arrows
// change the knob under the cursor, and running settings that belong to the set are written into the
// file. A dry run is the one setting that is not the file's: it is what the next run is asked to be.
func TestThePaneCarriesTheRunsKnob(t *testing.T) {
	m, root, _ := fixture(t)
	m.openSkill("slide-builder")
	press(m, "tab") // the pane
	pane := func() string { return stripMarks(strings.Join(m.sectionsBody(m.bodyHeight()), "\n")) }
	for _, want := range []string{"THE RUN", "runs per query", "‹ 3 ›", "dry run", "‹ off ›"} {
		if !strings.Contains(pane(), want) {
			t.Fatalf("the pane must show the run's knobs, %q is missing:\n%s", want, pane())
		}
	}
	// The block is drawn before it has the keys, and it says so only when it does.
	if strings.Contains(pane(), "▌ THE RUN") {
		t.Fatalf("the block has the accent while the table has the keys:\n%s", pane())
	}
	press(m, "tab") // into the block
	if !m.sections.runFocus || m.sections.runSel != knobRuns {
		t.Fatalf("tab must land on the first knob: focus = %v sel = %d", m.sections.runFocus, m.sections.runSel)
	}
	if !strings.Contains(pane(), "▌ THE RUN") {
		t.Fatalf("the block with the keys must be the one region in the accent:\n%s", pane())
	}

	press(m, "right")
	if m.flow.lab.Runs != 4 {
		t.Fatalf("runs = %d, want 4", m.flow.lab.Runs)
	}
	blob, err := os.ReadFile(filepath.Join(root, "trigger", "trigger-evals.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blob), `"runs": 4`) {
		t.Fatalf("the knob is the file's and the file did not move:\n%s", blob)
	}
	// It stops at the runner's range instead of wrapping.
	for i := 0; i < 10; i++ {
		press(m, "right")
	}
	if m.flow.lab.Runs != 9 {
		t.Fatalf("runs = %d, want the 9 ceiling", m.flow.lab.Runs)
	}
	// The second knob is a boolean, and it is not written anywhere: it belongs to this session.
	press(m, "tab")
	if m.sections.runSel != knobDry {
		t.Fatalf("tab must move to the dry run knob: sel = %d", m.sections.runSel)
	}
	press(m, "right")
	if !m.flow.dry {
		t.Fatal("the dry run knob did not flip")
	}
	if !strings.Contains(pane(), "‹ on ›") {
		t.Fatalf("the knob must say it is on:\n%s", pane())
	}
	if !strings.Contains(m.flow.stateText(), "dry run") {
		t.Fatalf("the workbench must say the next run writes nothing: %q", m.flow.stateText())
	}
	blob, _ = os.ReadFile(filepath.Join(root, "trigger", "trigger-evals.json"))
	if strings.Contains(string(blob), "dry") {
		t.Fatalf("a dry run is not a property of the set:\n%s", blob)
	}
	// esc leaves the block before it leaves the screen.
	press(m, "esc")
	if m.sections.runFocus || m.mode != screenSections {
		t.Fatalf("esc must hand the keys back to the table: focus = %v mode = %d", m.sections.runFocus, m.mode)
	}
}

// TestThePaneIsOrderedRunThenSetThenRow: the run's settings govern every row of the split, so they
// lead the pane; the set follows; and the fields of the row under the cursor are last, under the row
// they edit. Reading order is the whole of it — a knob under the fields read as a footnote.
func TestThePaneIsOrderedRunThenSetThenRow(t *testing.T) {
	m, _, _ := fixture(t)
	m.openSkill("slide-builder")
	press(m, "tab")
	view := stripMarks(strings.Join(m.sectionsBody(m.bodyHeight()), "\n"))
	run := strings.Index(view, "THE RUN")
	set := strings.Index(view, "TRAIN SET")
	row := strings.Index(view, "build me slides from this outline")
	fields := strings.Index(view, "should the skill load?")
	if run < 0 || set < 0 || row < 0 || fields < 0 {
		t.Fatalf("the pane is missing a part of the order (run %d, set %d, row %d, fields %d):\n%s",
			run, set, row, fields, view)
	}
	if !(run < set && set < row && row < fields) {
		t.Fatalf("the pane must read run, set, row, fields — got run %d, set %d, row %d, fields %d:\n%s",
			run, set, row, fields, view)
	}
}

// TestThePaneKeepsTheSplitOfTheSectionItIsOn: the knob is the set's, so it is on screen beside both
// splits — the validation runs read the same number.
func TestThePaneKeepsTheSplitOfTheSectionItIsOn(t *testing.T) {
	m, _, _ := fixture(t)
	m.openSkill("slide-builder")
	press(m, "j") // generalization
	if m.sections.cursor != secGeneralization {
		t.Fatalf("cursor = %d", m.sections.cursor)
	}
	press(m, "tab")
	if got := stripMarks(strings.Join(m.sectionsBody(m.bodyHeight()), "\n")); !strings.Contains(got, "runs per query") {
		t.Fatalf("the validation pane must carry the same run settings:\n%s", got)
	}
}

// TestThePaneEditsTheSetItShows: the pane's keys are the step screen's keys, and they write the file.
func TestThePaneEditsTheSetItShows(t *testing.T) {
	m, root, _ := fixture(t)
	m.openSkill("slide-builder")
	press(m, "tab")
	if m.sections.focus != sectionFocusSet {
		t.Fatalf("focus = %d, want the set", m.sections.focus)
	}
	before := m.sections.rows[0].Label
	press(m, "space")
	if m.sections.rows[0].Label == before {
		t.Fatalf("the label did not move (%q)", before)
	}
	blob, err := os.ReadFile(filepath.Join(root, "trigger", "trigger-evals.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := `"should_trigger": false`
	if before == "no" {
		want = `"should_trigger": true`
	}
	if !strings.Contains(string(blob), want) {
		t.Fatalf("the pane wrote nothing:\n%s", blob)
	}
}

// TestThePaneDeletesAndComesBackToItself: a delete from the pane returns to the section screen, not to
// a step screen the pane was never inside.
func TestThePaneDeletesAndComesBackToItself(t *testing.T) {
	m, _, _ := fixture(t)
	m.openSkill("slide-builder")
	press(m, "tab")
	press(m, "d")
	if m.mode != screenAsk {
		t.Fatalf("mode = %v, want the confirmation", m.mode)
	}
	press(m, "y")
	if m.mode != screenSections {
		t.Fatalf("mode = %v, want the section screen back", m.mode)
	}
	if len(m.sections.rows) != 1 {
		t.Fatalf("rows = %d, want the one that is left", len(m.sections.rows))
	}
}

// TestAQueryAddedFromThePaneLandsInTheSplitOnScreen: the form writes the split the pane is showing, in
// the pane, and the section screen never goes away.
func TestAQueryAddedFromThePaneLandsInTheSplitOnScreen(t *testing.T) {
	m, _, s := fixture(t)
	m.openSkill("slide-builder")
	press(m, "j") // generalization
	press(m, "tab")
	press(m, "n")
	if m.mode != screenSections || m.inlineForm() == nil || m.form.split != store.SplitValidation {
		t.Fatalf("mode = %v split = %q, want the validation split in the pane", m.mode, m.form.split)
	}
	m.form.query = "make a deck from these notes"
	m.form.why = "held back"
	m.form.apply(m)
	// The pane keeps its fields — they are part of it — but they are the file's again, not a draft.
	if m.mode != screenSections || m.form == nil || m.form.active {
		t.Fatalf("mode = %v form = %+v, want the pane's fields, quiet", m.mode, m.form)
	}
	if m.form.query != "make a deck from these notes" {
		t.Fatalf("the fields must be re-seeded from the write: %q", m.form.query)
	}
	rows := s.Rows(store.LaneTrigger, store.Filter{Skill: "slide-builder", Split: store.SplitValidation})
	if len(rows) != 3 || rows[2].Text != "make a deck from these notes" {
		t.Fatalf("rows = %+v, want the new validation row", rows)
	}
	if len(m.sections.rows) != 3 {
		t.Fatalf("the pane must re-read the split: %d rows", len(m.sections.rows))
	}
}

// TestEnterInThePaneOpensTheWorkbenchOnItsSet: enter on either side goes deeper, and the pane's own
// enter opens the step that owns the set it is showing.
func TestEnterInThePaneOpensTheWorkbenchOnItsSet(t *testing.T) {
	m, _, _ := fixture(t)
	m.openSkill("slide-builder")
	press(m, "j") // generalization, in the list
	press(m, "tab")
	press(m, "enter")
	if m.mode != screenFlow || m.flow.sec != secGeneralization || m.flow.step != 1 {
		t.Fatalf("mode = %v section = %d step = %d, want the validation set",
			m.mode, m.flow.sec, m.flow.step)
	}
	if len(m.flow.rows) != 2 || m.flow.rows[0].Split != store.SplitValidation {
		t.Fatalf("the workbench opened on the wrong split: %+v", m.flow.rows)
	}
}

func TestResumeLandsOnTheStepTheDiskIsAt(t *testing.T) {
	m, _, _ := fixture(t)
	m.openSkill("slide-builder")
	// The skill opens on the choice between its two activities, on the one the disk is waiting in.
	if m.mode != screenSections || m.sections.cursor != secIteration {
		t.Fatalf("mode = %v cursor = %d, want the section choice on iteration", m.mode, m.sections.cursor)
	}
	enterSection(m, secIteration)
	if m.flow.sec != secIteration || m.flow.step != 1 {
		t.Fatalf("section = %d step = %d, want the one iteration screen", m.flow.sec, m.flow.step)
	}
	if len(m.flow.lab.Candidates) != 1 {
		t.Fatalf("candidates = %d, want the baseline adopted from SKILL.md", len(m.flow.lab.Candidates))
	}
}

func TestIterationJoinsTheTwoSplitsByDescription(t *testing.T) {
	m, root, s := fixture(t)
	desc := s.SkillDescription("slide-builder")
	// The iteration runs the train set…
	iterationDir(t, root, desc, 1, []runner.ScoreRow{
		row(1, "train", true, true),
		row(2, "train", false, false),
	})
	// …and generalization runs the validation set against the same wording.
	iterationDir(t, root, desc, 2, []runner.ScoreRow{
		row(3, "validation", true, true),
		row(4, "validation", false, true),
	})
	m.openSkill("slide-builder")
	if len(m.flow.iters) != 1 {
		t.Fatalf("iterations = %d, want one wording measured twice", len(m.flow.iters))
	}
	it := m.flow.iters[0]
	if it.Train == nil || it.Train.Iteration != 1 {
		t.Fatalf("train measure = %+v", it.Train)
	}
	if it.Valid == nil || it.Valid.Iteration != 2 {
		t.Fatalf("validation measure = %+v, want the later run", it.Valid)
	}
	if got := pct(it.Train.Train.Passed, it.Train.Train.Judged); got != "50%" {
		t.Fatalf("train = %s", got)
	}
	if got := pct(it.Valid.Validation.Passed, it.Valid.Validation.Judged); got != "100%" {
		t.Fatalf("validation = %s", got)
	}
}

func TestGateHoldsUntilFiveIterations(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{row(1, "train", true, true), row(2, "train", false, true)})
	if len(m.flow.iters) != 1 {
		t.Fatalf("iterations = %d", len(m.flow.iters))
	}
	// Generalization's run step is behind the gate, and the jump refuses it.
	enterSection(m, secGeneralization)
	m.jumpStep(1)
	press(m, "2")
	if m.flow.step != 1 {
		t.Fatalf("step = %d, want the validation set to hold the section", m.flow.step)
	}
	if !strings.Contains(m.status, "ctrl+g") {
		t.Fatalf("status = %q, want the way out named", m.status)
	}
	// ctrl+g is the stated exception.
	press(m, "ctrl+g")
	if m.flow.sec != secGeneralization || m.flow.step != 2 {
		t.Fatalf("section = %d step = %d, want validate forced", m.flow.sec, m.flow.step)
	}
	if !m.flow.gateOpen() {
		t.Fatal("the gate must record that it was forced")
	}
}

// ---------------------------------------------------------------- steps 1 and 2

// TestSpaceFlipsTheLabelAndWritesTheFile: the label is flipped where the row is written — the set
// beside the two sections, or the validation step. The workbench reports the set and does not write it.
func TestSpaceFlipsTheLabelAndWritesTheFile(t *testing.T) {
	m, _, _ := fixture(t)
	m.openSkill("slide-builder")
	press(m, "tab") // the set beside the two sections
	if len(m.sections.rows) != 2 {
		t.Fatalf("train rows = %d", len(m.sections.rows))
	}
	before := m.sections.rows[0].Label
	press(m, "space")
	after := m.sections.rows[0].Label
	if before == after {
		t.Fatalf("label did not move (%q)", before)
	}
	blob, err := os.ReadFile(filepath.Join(m.store.Root, "trigger", "trigger-evals.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := `"should_trigger": false`
	if before == "no" {
		want = `"should_trigger": true`
	}
	if !strings.Contains(string(blob), want) {
		t.Fatalf("the file was not written:\n%s", blob)
	}
}

func TestEnterFromTheLastQuerySetOpensTheLoop(t *testing.T) {
	m, _, _ := fixture(t)
	m.openSkill("slide-builder")
	enterSection(m, secGeneralization)
	// With the gate closed the set's forward move is refused, and it says why.
	press(m, "enter")
	if m.flow.step != 1 || !strings.Contains(m.status, "ctrl+g") {
		t.Fatalf("step = %d status = %q, want the gate named", m.flow.step, m.status)
	}
	// Forced, the same key opens the run.
	m.flow.forced = true
	press(m, "enter")
	if m.flow.sec != secGeneralization || m.flow.step != 2 {
		t.Fatalf("section = %d step = %d, want validate", m.flow.sec, m.flow.step)
	}
}

// ---------------------------------------------------------------- step 4

func TestTheBestIterationIsTheBestValidation(t *testing.T) {
	m, root, s := fixture(t)
	// The baseline is adopted first, exactly as the flow does it, so the two wordings below are
	// candidates #1 and #2 of one ledger.
	m.openSkill("slide-builder")
	first := s.SkillDescription("slide-builder")
	second := "Use this skill when an outline has to become slides — even when the user calls them a deck."
	if _, err := runner.New(s).SaveDescriptionCandidate("slide-builder", second, "named the deck", 1); err != nil {
		t.Fatal(err)
	}
	// #1 is perfect on train and poor on validation; #2 the other way round.
	iterationDir(t, root, first, 1, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, true),
		row(3, "validation", true, false), row(4, "validation", false, true),
	})
	iterationDir(t, root, second, 2, []runner.ScoreRow{
		row(1, "train", true, false), row(2, "train", false, true),
		row(3, "validation", true, true), row(4, "validation", false, true),
	})
	m.reload()
	if len(m.flow.iters) != 2 {
		t.Fatalf("iterations = %d", len(m.flow.iters))
	}
	best := bestIteration(m.flow)
	if best == nil || best.Cand.ID != 2 {
		t.Fatalf("best = %+v, want candidate #2 on validation", best)
	}
	if m.flow.gateOpen() {
		t.Fatal("two measured iterations opened the gate — it counts iterations, not validation runs")
	}
}

// ---------------------------------------------------------------- the ranking

// TestTheRankingIsOneTableWithTheRatesItHas: generalization's second step is the table itself, not a
// screen that lists what is about to be measured and a second one that lists what was. The rate the
// run produced is in its column and the rule's pick is stated under the table.
func TestTheRankingIsOneTableWithTheRatesItHas(t *testing.T) {
	m, root, s := fixture(t)
	first := s.SkillDescription("slide-builder")
	iterationDir(t, root, first, 1, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, true),
		row(3, "validation", true, true), row(4, "validation", false, true),
	})
	m.openSkill("slide-builder")
	m.flow.forced = true
	m.flow.sec, m.flow.step = secGeneralization, 2
	m.flow.enterStep(m)
	body := stripMarks(strings.Join(m.rankingBody(24), "\n"))
	for _, want := range []string{"ITERATION", "TRAIN", "VALIDATION", "GAP"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the ranking is missing %q:\n%s", want, body)
		}
	}
	// The pick is a word on its own row, not a tick: the tick sat in the note column as a glyph the
	// eye reads as "pass" and skips, and the one row on this table that is a decision has to say so.
	var pick string
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "2/2 100%") {
			pick = line
		}
	}
	if !strings.Contains(pick, "Recommended") {
		t.Fatalf("the recommended row does not carry the tag:\n%s", body)
	}
	// The tail is the owed run and nothing else: the pick is the tag above, the columns are named
	// by their own head, and a table that explains itself twice is text where the screen is data.
	for _, gone := range []string{"by validation", "validation is the ranking", "learned rather than earned"} {
		if strings.Contains(body, gone) {
			t.Fatalf("the ranking's tail still explains the table (%q):\n%s", gone, body)
		}
	}
	if strings.Contains(body, "with no rate") {
		t.Fatalf("every wording is validated, so nothing is owed:\n%s", body)
	}
}

// TestAnUnvalidatedRowStaysInTheTableWithADash: a wording the validation set has not seen is a row
// with a blank in that column — the fact the screen exists to show — and the rate it is missing is
// run from the same screen, for exactly the rows that are missing one.
func TestAnUnvalidatedRowStaysInTheTableWithADash(t *testing.T) {
	m, root, s := fixture(t)
	m.openSkill("slide-builder")
	first := s.SkillDescription("slide-builder")
	second := "Use this skill when an outline has to become slides — even when the user calls them a deck."
	if _, err := runner.New(s).SaveDescriptionCandidate("slide-builder", second, "named the deck", 1); err != nil {
		t.Fatal(err)
	}
	// #1 has only a train run; #2 has both.
	iterationDir(t, root, first, 1, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, false),
	})
	iterationDir(t, root, second, 2, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, true),
		row(3, "validation", true, true), row(4, "validation", false, true),
	})
	m.reload()
	// The section opens on its set, and ctrl+g is the way past the five-iteration bar.
	enterSection(m, secGeneralization)
	press(m, "ctrl+g")
	if m.flow.step != 2 || m.mode != screenFlow {
		t.Fatalf("mode = %v step = %d, want the ranking", m.mode, m.flow.step)
	}
	if len(m.flow.iters) != 2 {
		t.Fatalf("iterations = %d, want both measured wordings drawn", len(m.flow.iters))
	}
	if m.needsValidation() != 1 {
		t.Fatalf("needsValidation = %d, want the one without a rate", m.needsValidation())
	}
	body := stripMarks(strings.Join(m.rankingBody(24), "\n"))
	// The row with no rate is drawn, with a dash where the rate would be.
	var row1 string
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "1/2 50%") {
			row1 = line
		}
	}
	if row1 == "" || !strings.Contains(row1, "—") {
		t.Fatalf("the unvalidated row is not drawn with a dash:\n%s", body)
	}
	if !strings.Contains(body, "validate 1 iteration · 2 queries × 3 runs = 6 agent calls") {
		t.Fatalf("the table must offer to run the rates it is missing:\n%s", body)
	}
	// The cursor opens on the recommendation, and v queues exactly the row that has no rate.
	if m.flow.res != 1 {
		t.Fatalf("res = %d, want the recommended row (#2) under the cursor", m.flow.res)
	}
	press(m, "v")
	if m.flow.gen == nil || len(m.flow.gen.queue) != 1 || m.flow.gen.queue[0] != 1 {
		t.Fatalf("the batch must queue only the unvalidated wording: %+v", m.flow.gen)
	}
	if m.mode != screenRunLive {
		t.Fatalf("mode = %v, want the run the key started", m.mode)
	}
}

// TestEnterOnTheRankingAppliesTheRowUnderTheCursor: the selection is the write. The table opens on
// the recommended wording, the arrows move off it, and the row you land on is the one SKILL.md gets.
func TestEnterOnTheRankingAppliesTheRowUnderTheCursor(t *testing.T) {
	m, root, s := fixture(t)
	m.openSkill("slide-builder")
	first := s.SkillDescription("slide-builder")
	second := "Use this skill when an outline has to become slides — even when the user calls them a deck."
	if _, err := runner.New(s).SaveDescriptionCandidate("slide-builder", second, "named the deck", 1); err != nil {
		t.Fatal(err)
	}
	// #1 is the better validation rate; #2 is the later revision the loop would keep by default.
	iterationDir(t, root, first, 1, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, true),
		row(3, "validation", true, true), row(4, "validation", false, true),
	})
	iterationDir(t, root, second, 2, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, true),
		row(3, "validation", true, false), row(4, "validation", false, false),
	})
	m.reload()
	enterSection(m, secGeneralization)
	press(m, "ctrl+g")
	if len(m.flow.iters) != 2 {
		t.Fatalf("iterations = %d, want both wordings", len(m.flow.iters))
	}
	if m.flow.res != 0 || m.flow.iters[m.flow.res].Cand.ID != 1 {
		t.Fatalf("the table must open on the recommendation: res = %d", m.flow.res)
	}
	// The pick stays yours: walk to the other row and apply that one.
	press(m, "j")
	if m.flow.iters[m.flow.res].Cand.ID != 2 {
		t.Fatalf("j must move the cursor off the recommendation, res = %d", m.flow.res)
	}
	press(m, "enter")
	if m.mode != screenAsk {
		t.Fatalf("mode = %v, want the confirmation before the write", m.mode)
	}
	press(m, "y")
	if got := s.SkillDescription("slide-builder"); got != strings.Join(strings.Fields(second), " ") {
		t.Fatalf("SKILL.md = %q, want the row that was picked %q", got, second)
	}
}

// TestAFinishedBatchLandsOnTheRankingWithTheCursorOnThePick: the batch writes its rates and hands the
// keys back to the same table, with the cursor on what the new rates recommend — no third screen and
// no walking to the winner by hand.
func TestAFinishedBatchLandsOnTheRankingWithTheCursorOnThePick(t *testing.T) {
	m, root, s := fixture(t)
	m.openSkill("slide-builder")
	first := s.SkillDescription("slide-builder")
	second := "Use this skill when an outline has to become slides — even when the user calls them a deck."
	if _, err := runner.New(s).SaveDescriptionCandidate("slide-builder", second, "named the deck", 1); err != nil {
		t.Fatal(err)
	}
	// The batch has just written #2's validation run; #1 is the better rate and must be offered.
	iterationDir(t, root, first, 1, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, true),
		row(3, "validation", true, true), row(4, "validation", false, true),
	})
	iterationDir(t, root, second, 2, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, true),
		row(3, "validation", true, false), row(4, "validation", false, false),
	})
	m.reload()
	m.flow.forced = true
	m.flow.sec, m.flow.step = secGeneralization, 2
	m.mode = screenRunLive
	m.run = &runState{kind: runValidation, gen: true, live: &liveRun{}}
	m.leaveRun()
	if m.mode != screenFlow || m.flow.step != 2 {
		t.Fatalf("mode = %v step = %d, want the ranking back", m.mode, m.flow.step)
	}
	if m.flow.iters[m.flow.res].Cand.ID != 1 {
		t.Fatalf("res = %d, want the recommended wording under the cursor", m.flow.res)
	}
	if !allValidated(m.flow.lab) {
		t.Fatal("the table must read the rates the batch just wrote")
	}
}

func TestApplyWritesTheWinnerIntoSkillMD(t *testing.T) {
	m, root, s := fixture(t)
	first := s.SkillDescription("slide-builder")
	iterationDir(t, root, first, 1, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, true),
	})
	m.openSkill("slide-builder")
	m.flow.forced = true
	m.flow.sec, m.flow.step = secGeneralization, 2
	m.askApply(m.flow.iters[0])
	if m.mode != screenAsk {
		t.Fatalf("mode = %v, want the confirmation", m.mode)
	}
	press(m, "y")
	if got := s.SkillDescription("slide-builder"); got != strings.Join(strings.Fields(first), " ") {
		t.Fatalf("SKILL.md = %q, want the applied wording %q", got, first)
	}
	if lab, err := runner.New(s).DescriptionLab("slide-builder"); err != nil || lab.Applied == nil {
		t.Fatalf("the ledger did not record the application: %v", err)
	}
}

// ---------------------------------------------------------------- the frame

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
		{"door", func() { m.mode = screenLane }},
		{"skills", func() { m.mode = screenSkill; m.loadSkills() }},
		{"sections", func() { m.openSkill("slide-builder"); m.mode = screenSections }},
		{"train set", func() {
			m.flow.sec, m.flow.step = secIteration, 1
			m.flow.forced = false
			m.mode = screenFlow
			m.reload()
		}},
		{"validation set", func() { m.flow.sec, m.flow.step = secGeneralization, 1; m.reload() }},
		{"ranking locked", func() { m.flow.sec, m.flow.step = secGeneralization, 2; m.flow.forced = false }},
		{"ranking", func() {
			m.flow.forced = true
			m.flow.sec, m.flow.step = secGeneralization, 2
			m.flow.enterStep(m)
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
	// The loop's counter counts the wording that is pending — and it is the top row's only where
	// the workbench is stacked: the table counts measured runs, so the fallback keeps the row.
	m.width, m.height = 80, 24
	enterSection(m, secIteration)
	if got := stripMarks(strings.Split(m.View(), "\n")[2]); !strings.Contains(got, "iteration 1 of 5") {
		t.Fatalf("state line = %q, want the loop's own counter", got)
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
	if !strings.HasPrefix(strings.TrimSpace(plain), "trigger optimization") {
		t.Fatalf("the left edge must name the scope:\n%s", plain)
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
	if !strings.Contains(bar, "trigger optimization > slide-builder > iteration") {
		t.Fatalf("the bar is not a path:\n%s", bar)
	}
	if strings.Contains(bar, " · ") {
		t.Fatalf("the bar still separates its segments with dots:\n%s", bar)
	}
	// The loop's numbers are the iterations table's, not a second row under the bar: the head
	// counts the loop and the newest row rates the last run, so the top row stops at the path.
	if got := stripMarks(lines[2]); strings.Contains(got, "iteration ") {
		t.Fatalf("the top row still repeats the loop's numbers:\n%s", got)
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

// TestTheDoorIsASelectorWithAPane: the door is two rows and the one under the cursor explained beside
// them. The pane follows the cursor, and below the width that buys a pane the row carries its own
// one-line explanation instead — nothing is hidden behind a divider that is not there.
func TestTheDoorIsASelectorWithAPane(t *testing.T) {
	m, _, _ := fixture(t)
	m.width, m.height = 120, 32
	body := strings.Join(m.laneBody(m.bodyHeight()), "\n")
	if !strings.Contains(body, glyphs.Seam) {
		t.Fatalf("the door has no pane:\n%s", body)
	}
	for _, want := range []string{"TRIGGER OPTIMIZATION", "The two activities", "iteration", "generalization", "It writes", "pick the skill"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the trigger pane is missing %q:\n%s", want, body)
		}
	}
	press(m, "j")
	body = strings.Join(m.laneBody(m.bodyHeight()), "\n")
	if !strings.Contains(body, "OUTPUT OPTIMIZATION") || !strings.Contains(body, "the output loop") {
		t.Fatalf("the pane did not follow the cursor:\n%s", body)
	}
	if strings.Contains(body, "The two activities") {
		t.Fatalf("the pane still explains the row above the cursor:\n%s", body)
	}
	m.width = 60
	narrow := strings.Join(m.laneBody(m.bodyHeight()), "\n")
	if strings.Contains(narrow, glyphs.Seam) {
		t.Fatalf("a narrow door drew a pane it has no room for:\n%s", narrow)
	}
	if !strings.Contains(narrow, "does the work come out right") {
		t.Fatalf("a narrow door hid the explanation entirely:\n%s", narrow)
	}
}

// TestTheSkillTableIsATable: the chooser is a table in the same shape as the eval tables — a quiet
// header, counts right-aligned so 1 and 7 end on one edge, and the row under the cursor spelled out
// in a block below the list.
func TestTheSkillTableIsATable(t *testing.T) {
	m, root, _ := fixture(t)
	// A second skill, so the cursor has somewhere to go and the block has to follow it.
	write(t, filepath.Join(root, "learning-path", "evals", "evals.json"), `{"skill_name":"learning-path","evals":[]}`)
	write(t, filepath.Join(root, "learning-path", "SKILL.md"), strings.Replace(skillMD, "slide-builder", "learning-path", -1))
	m.store, _ = store.Open(root)
	m.width, m.height = 120, 28
	press(m, "enter")
	m.loadSkills()

	lines := strings.Split(m.View(), "\n")
	head, row := "", ""
	for _, line := range lines {
		plain := stripMarks(line)
		if strings.HasPrefix(plain, "  SKILL") {
			head = plain
		}
		if strings.Contains(plain, "slide-builder") && strings.HasPrefix(plain, " ") {
			row = plain
		}
	}
	if head == "" || row == "" {
		t.Fatalf("no header/row pair in the chooser:\n%s", strings.Join(lines, "\n"))
	}
	// Right alignment: the number's last cell is the header's last cell.
	at := strings.Index(head, "TRAIN")
	if at < 0 || row[at+len("TRAIN")-1] != '2' {
		t.Fatalf("the TRAIN column is not right-aligned under its head:\n%s\n%s", head, row)
	}
	// The pane names the row under the cursor, in capitals, and follows it.
	first := m.skills.rows[m.skills.cursor].name
	body := stripMarks(m.View())
	if !strings.Contains(body, strings.ToUpper(first)) {
		t.Fatalf("the pane does not name the row under the cursor:\n%s", body)
	}
	press(m, "j")
	second := m.skills.rows[m.skills.cursor].name
	body = stripMarks(m.View())
	if second == first || !strings.Contains(body, strings.ToUpper(second)) || strings.Contains(body, strings.ToUpper(first)) {
		t.Fatalf("the pane did not follow the cursor:\n%s", body)
	}
	// Below the width that buys a pane the block under the table does the same job.
	m.width = 64
	narrow := stripMarks(m.View())
	if strings.Contains(narrow, glyphs.Seam) || !strings.Contains(narrow, "── "+second+" ─") {
		t.Fatalf("a narrow chooser lost its block:\n%s", narrow)
	}
}

// ---------------------------------------------------------------- the tree is live

// The chooser is a list of what is on disk, so a skill added while the tool is running has to appear
// on its own. The bug this pins: the store was read once at startup, and every screen was drawn from
// that reading — a new skill stayed invisible until the binary was restarted, which reads as the tool
// having lost it.
func TestANewSkillAppearsWithoutARestart(t *testing.T) {
	m, root, _ := fixture(t)
	press(m, "enter") // the door, then the chooser
	m.loadSkills()
	if len(m.skills.rows) != 1 {
		t.Fatalf("rows = %d, want the one skill the fixture has", len(m.skills.rows))
	}
	write(t, filepath.Join(root, "dummy", "evals", "evals.json"), `{"skill_name":"dummy","evals":[]}`)
	write(t, filepath.Join(root, "dummy", "SKILL.md"), strings.Replace(skillMD, "slide-builder", "dummy", -1))

	m.syncTree()

	names := make([]string, 0, len(m.skills.rows))
	for _, r := range m.skills.rows {
		names = append(names, r.name)
	}
	if got := strings.Join(names, ","); got != "dummy,slide-builder" {
		t.Fatalf("rows = %s, want the new skill listed where it sorts", got)
	}
	// The new row sorts above the one under the cursor, and the cursor is on a skill, not on a row.
	if m.skills.rows[m.skills.cursor].name != "slide-builder" {
		t.Fatalf("the cursor moved to %q when a row was inserted above it", m.skills.rows[m.skills.cursor].name)
	}
	if !strings.Contains(stripMarks(m.View()), "dummy") {
		t.Fatal("the new skill is in the model but not on the screen")
	}
}

// The loop a screen is inside can leave the tree too: the skill's dir gone means its set and its
// ledger are gone, and a flow drawn over them would be a screen with nothing behind it.
func TestASkillThatLeavesTheTreeGoesBackToTheChooser(t *testing.T) {
	m, root, _ := fixture(t)
	m.openSkill("slide-builder")
	enterSection(m, secIteration)
	if err := os.RemoveAll(filepath.Join(root, "slide-builder")); err != nil {
		t.Fatal(err)
	}
	m.syncTree()
	if m.skill != "" || m.flow != nil || m.mode != screenSkill {
		t.Fatalf("skill = %q flow = %v mode = %d, want the chooser with no skill open", m.skill, m.flow, m.mode)
	}
	// The trigger set still mentions the skill, so its name stays in the chooser — its rows are
	// still editable — and the state column says the tree no longer has it.
	if len(m.skills.rows) != 1 || m.skills.rows[0].state != "not on disk" {
		t.Fatalf("rows = %+v, want one row saying it is not on disk", m.skills.rows)
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
	press(m, "enter")
	m.loadSkills()
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
	if len(m.skills.rows) != 1 || m.skills.rows[0].train != 2 {
		t.Fatalf("rows = %+v, want the rewritten set read back", m.skills.rows)
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
	m.flow.forced = true
	m.flow.sec, m.flow.step = secGeneralization, 2
	m.flow.enterStep(m)
	body := stripMarks(strings.Join(m.rankingBody(24), "\n"))
	if !strings.Contains(body, "#2 is in the ledger but not this table") ||
		!strings.Contains(body, "a later revision stepped over it") {
		t.Fatalf("the ranking must say why the ledger has a row the table does not:\n%s", body)
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
	m.flow.sec, m.flow.step = secGeneralization, 2
	m.flow.enterStep(m)
	body = stripMarks(strings.Join(m.rankingBody(24), "\n"))
	if !strings.Contains(body, "#4 is in the ledger but not this table") ||
		!strings.Contains(body, "the workbench's run measures it") {
		t.Fatalf("a live draft must be named as the run's next target:\n%s", body)
	}
	if !strings.Contains(body, "2·#3") {
		t.Fatalf("the row must carry the candidate id when it differs from the iteration number:\n%s", body)
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
	m.flow.forced = true
	m.flow.sec, m.flow.step = secGeneralization, 2
	m.flow.enterStep(m)
	body := stripMarks(strings.Join(m.rankingBody(28), "\n"))
	if !strings.Contains(body, "1/1 100% of 2") {
		t.Fatalf("the partial rate must say how much of the set it measured:\n%s", body)
	}
	if !strings.Contains(body, "of N — judged of the split; a stopped batch never ranks") {
		t.Fatalf("the table must say what the partial cell means:\n%s", body)
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
