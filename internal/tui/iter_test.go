package tui

import (
	"regexp"
	"strings"
	"testing"

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
	// The failure quadrant must sit above the description and left of the set: the guide reads
	// left to right, what went wrong and then where to write the answer.
	if strings.Index(view, "FAILED QUERY") > strings.Index(view, "TRAIN SET") {
		t.Fatalf("the failure must be the first quadrant:\n%s", view)
	}
	if strings.Index(view, "THE DESCRIPTION") < strings.Index(view, "TRAIN SET") {
		t.Fatalf("the description belongs under the failure:\n%s", view)
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
	// A candidate that has not been run has no verdicts to show, and the column says that too.
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

	if !strings.Contains(strings.Join(narrow, " "), "Name this context") {
		t.Fatalf("a missed trigger is answered by naming the context: %v", narrow)
	}
	if !strings.Contains(strings.Join(broad, " "), "overlaps") {
		t.Fatalf("a wrong trigger is answered by narrowing: %v", broad)
	}
	if !strings.Contains(strings.Join(unstable, " "), "ambiguous") {
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
	// nothing: a run is not a revision.
	press(m, "enter")
	if m.mode != screenRunAsk {
		t.Fatalf("enter runs the stored candidate: mode = %d", m.mode)
	}
	led, _, err := m.store.LoadDescriptionLedger("slide-builder")
	if err != nil || len(led.Candidates) != 1 {
		t.Fatalf("a run wrote a candidate: %d %v", len(led.Candidates), err)
	}
	press(m, "esc")
	if m.mode != screenFlow {
		t.Fatalf("the ask cancels back to the workbench: mode = %d", m.mode)
	}

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
	if m.mode != screenRunAsk {
		t.Fatalf("enter runs what it saved: mode = %d", m.mode)
	}
	if m.run == nil || m.run.cand != 2 {
		t.Fatalf("the run must measure the candidate just written, not the one before it")
	}
	if body := stripMarks(strings.Join(m.run.askBody(m, 20), " ")); !strings.Contains(body, "candidate #2") {
		t.Fatalf("the run must name the candidate it measures:\n%s", body)
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
