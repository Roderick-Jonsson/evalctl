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
	if !strings.Contains(joined, "not built") {
		t.Fatalf("output is not built and the door must say so:\n%s", joined)
	}
	// Down to output, then enter: the door stays put and says why.
	press(m, "j")
	press(m, "enter")
	if m.mode != screenLane {
		t.Fatalf("output opened a screen: mode = %v", m.mode)
	}
	if !strings.Contains(m.status, "not built") {
		t.Fatalf("status = %q", m.status)
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

func TestResultListShowsTrainValidationAndTheGap(t *testing.T) {
	m, root, s := fixture(t)
	first := s.SkillDescription("slide-builder")
	iterationDir(t, root, first, 1, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, true),
		row(3, "validation", true, true), row(4, "validation", false, true),
	})
	m.openSkill("slide-builder")
	m.flow.sec, m.flow.step = secGeneralization, 3
	body := strings.Join(m.resultBody(24), "\n")
	for _, want := range []string{"ITERATION", "TRAIN", "VALIDATION", "GAP", "best by validation"} {
		if !strings.Contains(body, want) {
			t.Fatalf("result list is missing %q:\n%s", want, body)
		}
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
	m.flow.sec, m.flow.step = secGeneralization, 3
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
	m, _, _ := fixture(t)
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
		{"iteration ladder", func() { m.flow.sec, m.flow.step = secIteration, 2; m.mode = screenFlow }},
		{"validation set", func() { m.flow.sec, m.flow.step = secGeneralization, 1; m.reload() }},
		{"validate locked", func() { m.flow.sec, m.flow.step = secGeneralization, 2; m.flow.forced = false }},
		{"results", func() { m.flow.forced = true; m.flow.sec, m.flow.step = secGeneralization, 3 }},
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
	if got := m.flow.stateLine(); !strings.Contains(got, "iteration 1 of 5") {
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
	rail := stripMarks(lines[2])
	if !strings.Contains(rail, "iteration 1 of 5") {
		t.Fatalf("the loop's numbers left the rail row:\n%s", rail)
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
	if !strings.Contains(body, "OUTPUT OPTIMIZATION") || !strings.Contains(body, "not built") {
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
	if !strings.Contains(narrow, "does the skill produce") {
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
