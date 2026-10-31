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
		if s[i] == 0x1b {
			for i < len(s) && !(s[i] >= 'a' && s[i] <= 'z') && s[i] != 'm' {
				i++
			}
			i++
			continue
		}
		out.WriteByte(s[i])
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

// ---------------------------------------------------------------- the loop's state

func TestResumeLandsOnTheStepTheDiskIsAt(t *testing.T) {
	m, _, _ := fixture(t)
	m.openSkill("slide-builder")
	// The skill opens on the choice between its two activities, on the one the disk is waiting in.
	if m.mode != screenSections || m.sections.cursor != secIteration {
		t.Fatalf("mode = %v cursor = %d, want the section choice on iteration", m.mode, m.sections.cursor)
	}
	enterSection(m, secIteration)
	if m.flow.sec != secIteration || m.flow.step != 2 {
		t.Fatalf("section = %d step = %d, want the loop: the train set is defined and nothing is measured", m.flow.sec, m.flow.step)
	}
	if m.flow.sub != subRun {
		t.Fatalf("rung = %d, want the training run", m.flow.sub)
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

func TestSpaceFlipsTheLabelAndWritesTheFile(t *testing.T) {
	m, _, _ := fixture(t)
	m.openSkill("slide-builder")
	enterSection(m, secIteration)
	m.jumpStep(1)
	if len(m.flow.rows) != 2 {
		t.Fatalf("train rows = %d", len(m.flow.rows))
	}
	before := m.flow.rows[0].Label
	press(m, "space")
	after := m.flow.rows[0].Label
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
	if got := m.iterHeadline(); !strings.Contains(got, "#1") {
		t.Fatalf("headline = %q, want candidate #1", got)
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
	if !strings.Contains(bar, "trigger optimization > slide-builder > iteration > iterate") {
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
