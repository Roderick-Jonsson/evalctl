package tui

import (
	"regexp"
	"strings"
	"testing"

	"evalctl/internal/model"
	"evalctl/internal/runner"
)

// The four quadrants, and the four things each one has to say.

func quadView(m *Model) string { return stripMarks(m.View()) }

func TestTheWorkbenchIsFourQuadrants(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, true),
		row(3, "validation", true, true), row(4, "validation", false, false),
	})
	enterSection(m, secIteration)
	view := quadView(m)
	for _, want := range []string{"FAILED QUERY", "TRAIN SET", "THE DESCRIPTION", "ITERATIONS"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the workbench is missing the %q quadrant:\n%s", want, view)
		}
	}
	// The quadrants are a grid, not a stack: failure top left, the set top right, the description
	// bottom left, the iterations bottom right. The two rows are read as two questions — what went
	// wrong, and where the answer is written.
	failRow, failCol := at(t, view, "FAILED QUERY")
	setRow, setCol := at(t, view, "TRAIN SET")
	descRow, descCol := at(t, view, "THE DESCRIPTION")
	iterRow, iterCol := at(t, view, "ITERATIONS")
	if failCol >= setCol || setRow != failRow {
		t.Fatalf("the failure belongs above the set's left, on one row:\n%s", view)
	}
	if descRow <= failRow || iterRow <= setRow || iterCol <= descCol || descRow != iterRow {
		t.Fatalf("the description belongs under the failure and left of the iterations:\n%s", view)
	}
}

// TestTheWorkbenchSpendsWithoutAScreen: enter on the set is the run, and there is no confirmation
// screen in front of it — the runs per query and the dry run were set beside the set, so the key has
// nothing left to ask. What it starts is a dry run when the section armed one.
func TestTheWorkbenchSpendsWithoutAScreen(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{row(1, "train", true, true)})
	m.flow.dry = true // armed with ctrl+d on the section's pane
	enterSection(m, secIteration)

	press(m, "enter")
	if m.mode != screenRunLive || m.run == nil || m.run.live == nil {
		t.Fatalf("enter must land on the live log: mode = %d", m.mode)
	}
	if !m.run.dry {
		t.Fatal("the run must be the dry one the section armed")
	}
	if m.run.runs != m.flow.lab.Runs {
		t.Fatalf("runs = %d, want the set's %d", m.run.runs, m.flow.lab.Runs)
	}
	if body := stripMarks(strings.Join(m.run.liveBody(m, 20), "\n")); !strings.Contains(body, "dry run") {
		t.Fatalf("the live screen must say nothing is being written:\n%s", body)
	}
}

// at is where a quadrant's name sits in a finished frame: its row and the column it starts at.
func at(t *testing.T, view, name string) (row, col int) {
	t.Helper()
	line := strings.Index(view, name)
	if line < 0 {
		t.Fatalf("%q is not on the screen:\n%s", name, view)
	}
	row = strings.Count(view[:line], "\n")
	start := strings.LastIndex(view[:line], "\n") + 1
	return row, line - start
}

// TestTheWorkbenchReportsTheSetAndEditsNothing pins what the workbench is for: the description is the
// one thing a revision is, so it is the one thing this screen writes. The set is drawn with the run's
// verdicts and no fields, and the keys that write a row — n, e, d, space — are the step screens' and
// the section pane's, not this one's.
func TestTheWorkbenchReportsTheSetAndEditsNothing(t *testing.T) {
	m, root := measured(t, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, false),
	})
	enterSection(m, secIteration)
	view := quadView(m)
	if m.form != nil {
		t.Fatalf("the workbench seeded an editor for the set: %+v", m.form)
	}
	for _, unwanted := range []string{"should the skill load?", "split", "EDITOR", "edit q1"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("the set is not edited on this screen, and %q says it is:\n%s", unwanted, view)
		}
	}
	// The four quadrants are still the four quadrants.
	for _, want := range []string{"FAILED QUERY", "THE DESCRIPTION", "ITERATIONS", "TRAIN SET"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the workbench is missing %q:\n%s", want, view)
		}
	}
	// The row commands do nothing here: no form, and the label is the file's.
	before := triggerFile(t, root)
	for _, key := range []string{"e", "n", "d", "space"} {
		press(m, key)
		if m.form != nil || m.mode != screenFlow {
			t.Fatalf("%q opened an editor on the workbench: mode = %d form = %+v", key, m.mode, m.form)
		}
	}
	if got := triggerFile(t, root); got != before {
		t.Fatalf("a row command wrote the file from the workbench:\n%s", got)
	}
}

func TestTheSetSaysWhichRowsPassed(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, false),
		row(3, "validation", true, true), row(4, "validation", true, true),
	})
	enterSection(m, secIteration)
	view := quadView(m)
	if !strings.Contains(view, "RESULT") {
		t.Fatalf("the set needs a verdict column:\n%s", view)
	}
	if !strings.Contains(view, "pass") || !strings.Contains(view, "FAIL") {
		t.Fatalf("one row passed and one failed, and the column must say so:\n%s", view)
	}
	// A candidate that has not been run has not graded to show, and the column says that too.
	m.flow.draft = nil
	if _, err := runner.New(m.store).SaveDescriptionCandidate("slide-builder", "a new wording entirely", "testing", 1); err != nil {
		t.Fatal(err)
	}
	m.reload()
	m.flow.draft = nil
	view = quadView(m)
	if !strings.Contains(view, "—") {
		t.Fatalf("an unmeasured candidate's rows are not verdicts:\n%s", view)
	}
	if regexp.MustCompile(`\bFAIL\b`).MatchString(view) {
		t.Fatalf("an unmeasured candidate must not look failed:\n%s", view)
	}
}

func TestTheAdviceFollowsTheFailure(t *testing.T) {
	narrow := adaptHints(runner.ScoreRow{ShouldTrigger: true, Runs: 3, Triggers: 0})
	broad := adaptHints(runner.ScoreRow{ShouldTrigger: false, Runs: 3, Triggers: 3})
	unstable := adaptHints(runner.ScoreRow{ShouldTrigger: true, Runs: 3, Triggers: 1})

	if !strings.Contains(strings.Join(narrow, " "), "Name the context") {
		t.Fatalf("a missed trigger is answered by naming the context: %v", narrow)
	}
	if !strings.Contains(strings.Join(broad, " "), "overlaps") {
		t.Fatalf("a wrong trigger is answered by narrowing: %v", broad)
	}
	if !strings.Contains(strings.Join(unstable, " "), "not explicit") {
		t.Fatalf("a flapping row is answered by saying what makes it a yes: %v", unstable)
	}
	if len(broad) != len(narrow) {
		t.Fatalf("the three advice blocks are the same shape: %d vs %d", len(broad), len(narrow))
	}
}

func TestTheIterationTableListsEveryRun(t *testing.T) {
	m, root := measured(t, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, false),
	})
	s := m.store
	for n := 2; n <= 3; n++ {
		text := strings.Repeat("word ", n) + "description"
		if _, err := runner.New(s).SaveDescriptionCandidate("slide-builder", text, "revision "+string(rune('0'+n)), n-1); err != nil {
			t.Fatal(err)
		}
		iterationDir(t, root, text, n, []runner.ScoreRow{
			row(1, "train", true, true), row(2, "train", false, true),
		})
	}
	m.reload()
	enterSection(m, secIteration)
	view := quadView(m)
	for _, want := range []string{"revision 2", "revision 3", "3 of 5"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the iteration table is missing %q:\n%s", want, view)
		}
	}
	if len(m.flow.iters) != 3 {
		t.Fatalf("%d iterations measured, want 3", len(m.flow.iters))
	}
}

// The description is editable in place, which means the field owns its keys.

func TestTypingEditsTheDescription(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{row(1, "train", true, true), row(2, "train", false, true)})
	enterSection(m, secIteration)
	before := m.flow.draft.text.Value()

	press(m, "tab")
	if m.flow.focus != focusWording {
		t.Fatalf("tab must move the caret into the description, focus = %d", m.flow.focus)
	}
	for _, key := range []string{"q", "?", "3", "x"} {
		press(m, key)
	}
	got := m.flow.draft.text.Value()
	if !strings.HasSuffix(got, "q?3x") {
		t.Fatalf("the description did not take the keystrokes: %q", got)
	}
	// q quits, and ? opens the keys — neither may fire while a field has the caret.
	if m.mode != screenFlow {
		t.Fatalf("typing a q must not quit: mode=%d", m.mode)
	}
	if !m.flow.draftChanged() {
		t.Fatal("four keystrokes later the draft is changed")
	}
	_ = before
}

func TestEscLeavesTheFieldBeforeItLeavesTheSection(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{row(1, "train", true, true)})
	enterSection(m, secIteration)
	press(m, "tab")
	press(m, "esc")
	if m.flow.focus != focusSet {
		t.Fatalf("esc gives the keys back to the set, focus = %d", m.flow.focus)
	}
	if m.mode != screenFlow {
		t.Fatalf("esc in a field stays in the section, mode = %d", m.mode)
	}
	press(m, "esc")
	if m.mode != screenSections {
		t.Fatalf("esc on the set leaves the section, mode = %d", m.mode)
	}
}

func TestEnterSavesTheWordingThenMeasuresIt(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{row(1, "train", true, true), row(2, "train", false, false)})
	enterSection(m, secIteration)

	// Enter with an unchanged wording measures the candidate already in the ledger, and writes
	// nothing: a run is not a revision. It lands on the live log — the run's settings are the set's,
	// changed on the section pane, so no screen stands between the key and the spend.
	press(m, "enter")
	if m.mode != screenRunLive || m.run == nil || m.run.live == nil {
		t.Fatalf("enter runs the stored candidate: mode = %d", m.mode)
	}
	if m.run.cand != 1 || !strings.Contains(m.run.scope, "candidate #1") {
		t.Fatalf("the run must name what it measures: cand = %d, scope = %q", m.run.cand, m.run.scope)
	}
	led, _, err := m.store.LoadDescriptionLedger("slide-builder")
	if err != nil || len(led.Candidates) != 1 {
		t.Fatalf("a run wrote a candidate: %d %v", len(led.Candidates), err)
	}
	// The test leaves the live screen the way the run's own leave key would, without waiting for the
	// stub binary to fail: what is under test is the key, not the run.
	m.run, m.mode = nil, screenFlow

	// Typed wording is saved as the next candidate, and measured in the same keypress.
	press(m, "tab") // wording
	for _, key := range []string{" ", "+", "d", "e", "c", "k"} {
		press(m, key)
	}
	press(m, "tab") // note
	press(m, "tab") // set
	press(m, "enter")

	led, _, err = m.store.LoadDescriptionLedger("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	if len(led.Candidates) != 2 {
		t.Fatalf("%d candidates, want the baseline and the one written here", len(led.Candidates))
	}
	last := led.Candidates[len(led.Candidates)-1]
	if !strings.HasSuffix(strings.TrimSpace(last.Text), "+deck") {
		t.Fatalf("the candidate kept the wrong text: %q", last.Text)
	}
	if last.Parent != 1 {
		t.Fatalf("the revision must name what it revises: parent = %d", last.Parent)
	}
	if m.mode != screenRunLive {
		t.Fatalf("enter runs what it saved: mode = %d", m.mode)
	}
	if m.run == nil || m.run.cand != 2 {
		t.Fatalf("the run must measure the candidate just written, not the one before it")
	}
	if !strings.Contains(m.run.scope, "candidate #2") {
		t.Fatalf("the run must name the candidate it measures: %q", m.run.scope)
	}
}

// TestTheWorkbenchCursorWalksTheFailures: the set is drawn whole — passing rows are half of what says
// whether the wording works — but only the rows the run failed can be stood on, because the pane beside
// them has something to say about a failure and nothing at all about a pass.
func TestTheWorkbenchCursorWalksTheFailures(t *testing.T) {
	m, root, s := fixture(t)
	desc := s.SkillDescription("slide-builder")
	for _, q := range []model.TriggerQuery{
		{ID: 5, Skill: "slide-builder", ShouldTrigger: true, Split: "train",
			Query: "turn the retro notes into a deck", Why: "missed"},
		{ID: 6, Skill: "slide-builder", ShouldTrigger: false, Split: "train",
			Query: "summarise the incident", Why: "noise"},
	} {
		if err := s.AddTrigger(q); err != nil {
			t.Fatal(err)
		}
	}
	iterationDir(t, root, desc, 1, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, true),
		row(5, "train", true, false), row(6, "train", false, false),
	})
	m.openSkill("slide-builder")
	m.width, m.height = 120, 34
	enterSection(m, secIteration)

	if got := m.flow.failIdxs(); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("failures are at rows %v, want the two added train rows (2 and 3)", got)
	}
	if m.flow.sel != 2 {
		t.Fatalf("the workbench opened on row %d, want the first failure", m.flow.sel+1)
	}
	if got := m.position(); got != "failure 1 of 2" {
		t.Fatalf("the status row says %q, want the count of failures the cursor can reach", got)
	}
	press(m, "k")
	if m.flow.sel != 2 {
		t.Fatalf("k stepped off the first failure onto row %d", m.flow.sel+1)
	}
	press(m, "j")
	if m.flow.sel != 3 {
		t.Fatalf("j stopped on row %d instead of the last failure", m.flow.sel+1)
	}
	press(m, "j")
	if m.flow.sel != 3 {
		t.Fatalf("j ran past the last failure onto row %d", m.flow.sel+1)
	}
	// The mark follows the cursor onto the failing row and nowhere else: a cursor on a pass reads as
	// a failure the pane is not talking about.
	view := quadView(m)
	var marked []string
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, glyphs.Cursor) {
			marked = append(marked, line)
		}
	}
	if len(marked) != 1 || !strings.Contains(marked[0], "FAIL") {
		t.Fatalf("the cursor is on %d rows, want one failing row:\n%s", len(marked), view)
	}
}

func TestTheDraftSurvivesLeavingTheSection(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{row(1, "train", true, true)})
	enterSection(m, secIteration)
	press(m, "tab")
	press(m, "x")
	press(m, "esc")
	press(m, "esc")
	if m.mode != screenSections {
		t.Fatalf("mode = %d, want the sections", m.mode)
	}
	enterSection(m, secIteration)
	if !strings.HasSuffix(m.flow.draft.text.Value(), "x") {
		t.Fatalf("a half-typed sentence must survive the section screen: %q", m.flow.draft.text.Value())
	}
}

func TestTheNarrowWorkbenchStacksAndKeepsTheEditor(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{row(1, "train", true, true), row(2, "train", false, false)})
	m.width, m.height = 80, 30
	enterSection(m, secIteration)
	view := quadView(m)
	if !strings.Contains(view, "TRAIN SET") || !strings.Contains(view, "THE DESCRIPTION") {
		t.Fatalf("a narrow terminal keeps the set and the editor:\n%s", view)
	}
	if strings.Contains(view, "FAILED QUERY") {
		t.Fatalf("there is no room for four quadrants at 80 columns:\n%s", view)
	}
}

// TestTheFailurePaneIsThreeMoves pins the pane's job: the ask that failed, the way it failed, and the
// numbered hints that say what to change. Everything the loop also knows about the row — its why, the
// skill that loaded instead, the verdict in a sentence — is the failures screen's (`f`), because none
// of it changes the sentence being written in the pane below.
func TestTheFailurePaneIsThreeMoves(t *testing.T) {
	missed := row(3, "train", true, false)
	missed.Query = "turn today's retro notes into a deck"
	m, _ := measured(t, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, true), missed,
	})
	m.width, m.height = 120, 32
	enterSection(m, secIteration)
	view := quadView(m)
	for _, want := range []string{
		"Query     turn today's retro notes into a",
		"Expected  to trigger",
		"Fired     0 of 3 runs",
		"Verdict   too narrow",
		"Hint 1", "Hint 2", "Hint 3",
		"Name the context this belongs to",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("the failure pane is missing %q:\n%s", want, view)
		}
	}
	for _, gone := range []string{"why it is here", "what loaded", "What to adapt", "Never paste"} {
		if strings.Contains(view, gone) {
			t.Fatalf("the pane is still a report — %q belongs to the failures screen:\n%s", gone, view)
		}
	}
}
