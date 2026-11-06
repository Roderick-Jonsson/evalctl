package tui

import (
	"strings"
	"testing"

	"evalctl/internal/runner"
)

// The validation half shows its numbers: the block carries both rates, `v` with nothing owed lands on
// the ranking, the ranking says when the set cannot separate the wordings, and the two documented
// keys that had lost their bindings (`f`, `h`) open what they say they open.

// TestTheBlockPrintsBothRates: the ITERATIONS block used to print the train rate alone, so a tree
// where every wording had been validated showed no validation number anywhere on the work surface.
func TestTheBlockPrintsBothRates(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, false),
		row(3, "validation", true, true), row(4, "validation", false, true),
	})
	enterSection(m, secIteration)
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	for _, want := range []string{"TRAIN", "VALID", "GAP", "2/2 100%"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the iterations block is missing %q:\n%s", want, body)
		}
	}
}

// TestValidateWithNothingOwedOpensTheRanking: `v` was the only key that led to the validation numbers,
// and with every rate measured it answered with a status line — the screen read as a blank result.
func TestValidateWithNothingOwedOpensTheRanking(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, true),
		row(3, "validation", true, true), row(4, "validation", false, true),
	})
	enterSection(m, secGeneralization)
	m.flow.forced = true // ctrl+g: the gate is five iterations, and this fixture has one
	press(m, "v")
	if m.mode != screenDoc || m.doc == nil || m.doc.scope != "ranking" {
		t.Fatalf("v with nothing owed must open the ranking: mode = %d, doc = %+v", m.mode, m.doc)
	}
	body := stripMarks(strings.Join(m.doc.lines, "\n"))
	for _, want := range []string{"ITERATION", "TRAIN", "VALIDATION", "GAP", "Recommended"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the ranking is missing %q:\n%s", want, body)
		}
	}
	if m.flow.gen != nil {
		t.Fatal("v spent a batch with nothing to validate")
	}
}

// TestTheRankingSaysWhenTheSetCannotSeparateThem: two wordings at the same validation rate rank
// nothing, and the recommendation then falls to the earliest. The table has to say that out loud —
// a tag on row one with six rows at 100% reads as a bug.
func TestTheRankingSaysWhenTheSetCannotSeparateThem(t *testing.T) {
	m, root, s := fixture(t)
	m.openSkill("slide-builder")
	first := s.SkillDescription("slide-builder")
	second := "Use this skill when an outline has to become slides — even when the user calls them a deck."
	if _, err := runner.New(s).SaveDescriptionCandidate("slide-builder", second, "named the deck", 1); err != nil {
		t.Fatal(err)
	}
	rows := []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, false),
		row(3, "validation", true, true), row(4, "validation", false, true),
	}
	iterationDir(t, root, first, 1, rows)
	iterationDir(t, root, second, 2, rows)
	m.reload()

	if rate, n, tied := tieOf(m.flow); !tied || rate != 100 || n != 2 {
		t.Fatalf("tieOf = %d%% over %d wordings, tied = %v; want 100%% over 2", rate, n, tied)
	}
	enterSection(m, secGeneralization)
	m.flow.forced = true
	press(m, "v")
	body := stripMarks(strings.Join(m.doc.lines, "\n"))
	for _, want := range []string{"tie at 100% of 2", "does not separate them", "#1"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the ranking does not explain its own tie (%q missing):\n%s", want, body)
		}
	}
	// The head of the block carries the same fact in one breath, for the reader who never opens it.
	enterSection(m, secIteration)
	block := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	if !strings.Contains(block, "2 tied at 100%") {
		t.Fatalf("the iterations block does not mark the tie:\n%s", block)
	}
}

// TestTheRecordIsOneWordingInFull: `f` opened the train failures, so the validation rate and the gap
// of the wording on screen were unreachable from the workbench.
func TestTheRecordIsOneWordingInFull(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, false),
		row(3, "validation", true, true), row(4, "validation", false, true),
	})
	enterSection(m, secIteration)
	press(m, "f")
	if m.mode != screenDoc || m.doc == nil || m.doc.scope != "iteration" {
		t.Fatalf("f must open the wording's record: mode = %d, doc = %+v", m.mode, m.doc)
	}
	body := stripMarks(strings.Join(m.doc.lines, "\n"))
	for _, want := range []string{"train", "validation", "gap", "chars"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the record is missing the %q fact:\n%s", want, body)
		}
	}
}

// TestTheHoldoutKeyIsBound: `h` was documented in the help screen and bound nowhere.
func TestTheHoldoutKeyIsBound(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{row(1, "train", true, true)})
	enterSection(m, secGeneralization)
	press(m, "h")
	if !strings.Contains(m.status, "holdout") {
		t.Fatalf("h answered %q; it must reach the holdout's own guard", m.status)
	}
}

// TestTheRecordReturnsToTheTableItCameFrom: the ranking is a cursor over rows, and one esc from a
// row's record must not cost the reader their place in the table.
func TestTheRecordReturnsToTheTableItCameFrom(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, true),
		row(3, "validation", true, true), row(4, "validation", false, true),
	})
	enterSection(m, secGeneralization)
	m.flow.forced = true
	press(m, "v")
	if m.mode != screenDoc || m.doc.scope != "ranking" {
		t.Fatalf("v did not open the ranking: %+v", m.doc)
	}
	press(m, "f")
	if m.doc.scope != "iteration" {
		t.Fatalf("f on the ranking opened %q", m.doc.scope)
	}
	press(m, "esc")
	if m.mode != screenDoc || m.doc == nil || m.doc.scope != "ranking" {
		t.Fatalf("esc from the record landed on mode %d / %+v, want the ranking back", m.mode, m.doc)
	}
	press(m, "esc")
	if m.mode != screenFlow {
		t.Fatalf("esc from the ranking landed on mode %d, want the work surface", m.mode)
	}
}

// TestTheRankingWalksItsRows: the cursor is the selection the apply key writes, so it has to move and
// the table has to redraw around it.
func TestTheRankingWalksItsRows(t *testing.T) {
	m, root, s := fixture(t)
	m.openSkill("slide-builder")
	first := s.SkillDescription("slide-builder")
	second := "Use this skill when an outline has to become slides — even when the user calls them a deck."
	if _, err := runner.New(s).SaveDescriptionCandidate("slide-builder", second, "named the deck", 1); err != nil {
		t.Fatal(err)
	}
	lo := []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, false),
		row(3, "validation", true, true), row(4, "validation", false, true),
	}
	hi := []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", true, true),
		row(3, "validation", true, true), row(4, "validation", false, true),
	}
	iterationDir(t, root, first, 1, lo)
	iterationDir(t, root, second, 2, hi)
	m.reload()
	enterSection(m, secGeneralization)
	m.flow.forced = true
	press(m, "v")
	if m.flow.res != 0 {
		t.Fatalf("the table must open on its recommendation, res = %d", m.flow.res)
	}
	press(m, "j")
	if m.flow.res != 1 {
		t.Fatalf("j moved the cursor to %d, want 1", m.flow.res)
	}
	marked := stripMarks(strings.Join(m.doc.lines, "\n"))
	if !strings.Contains(marked, "▶ 2") {
		t.Fatalf("the second row is not drawn with its cursor:\n%s", marked)
	}
	// Applying the row under the cursor asks about that row, not about the recommendation.
	press(m, "a")
	if m.ask == nil || !strings.Contains(m.ask.title, "#2 ") {
		t.Fatalf("a did not ask about the row under the cursor: %+v", m.ask)
	}
}
