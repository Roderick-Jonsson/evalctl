package tui

import (
	"strings"
	"testing"
)

// The dojo's right hand is the guide's table: one row per declared assertion, both arms' verdicts in
// columns, in the order the judge read them. It is not a list of failures — a case whose two
// assertions both pass and a case whose two assertions both fail look the same width here, which is
// the point: what the skill changes is the difference between the two columns, not the absence of ✗.
func TestTheDojoIsATableOfAssertions(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qDojo)
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	for _, want := range []string{"ASSERTION", "WITH SKILL", "WITHOUT SKILL", "Three faces", "The total adds up"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the table is missing %q:\n%s", want, body)
		}
	}
	// Declaration order, not failures first: the passing assertion is above the failing one because
	// that is the order the case declares them in and the order the judge read them.
	if strings.Index(body, "Three faces") > strings.Index(body, "The total adds up") {
		t.Fatalf("the rows are not in declaration order:\n%s", body)
	}
	// The one that failed only without the skill: the pattern the loop exists to find.
	if !strings.Contains(body, "1 of 2 pass with the skill and fail without it") {
		t.Fatalf("the table does not say what its pattern means:\n%s", body)
	}
}

// Every row is selectable, passing ones included, and tab is what moves the keyboard between the case
// list and the table — a second axis, because j k on the left answers "which case" and j k on the
// right answers "which assertion", and one pair of keys for both makes one question unanswerable.
func TestTheAssertionCursorWalksEveryRow(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qDojo)
	if m.quality.asel != 1 {
		t.Fatalf("the assertion cursor opened on row %d, want the failed one", m.quality.asel)
	}
	press(m, "tab")
	if !m.quality.tbl || m.mode != screenQuality || m.quality.sec != qDojo {
		t.Fatalf("tab left the view: tbl=%v mode=%v section=%d", m.quality.tbl, m.mode, m.quality.sec)
	}
	// Up onto the assertion both arms passed: its row is selectable and its evidence says why there is
	// none, which is a different sentence from "nothing here".
	press(m, "k")
	if m.quality.asel != 0 {
		t.Fatalf("k moved the assertion cursor to %d, want 0", m.quality.asel)
	}
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	if !strings.Contains(body, "both arms passed it") {
		t.Fatalf("the passing row has no record under it:\n%s", body)
	}
	if strings.Contains(body, "the reply said so") {
		t.Fatalf("the evidence of the failing row is still under a passing one:\n%s", body)
	}
	// g G are the ends of the list that has the keys, and k at the top stays put.
	press(m, "k")
	if m.quality.asel != 0 {
		t.Fatalf("k ran past the top: %d", m.quality.asel)
	}
	press(m, "G")
	if m.quality.asel != 1 {
		t.Fatalf("G = %d, want the last assertion", m.quality.asel)
	}
	// tab back: the case list has the keys again, and moving it re-seats the assertion cursor on the
	// first row that failed in the new case.
	press(m, "tab")
	if m.quality.tbl {
		t.Fatal("the second tab did not give the case list its keys back")
	}
	press(m, "k")
	if m.quality.sel != 0 {
		t.Fatalf("the case cursor is on %d, want the first case", m.quality.sel)
	}
	if m.quality.asel != 0 {
		t.Fatalf("the assertion cursor did not re-seat on the new case: %d", m.quality.asel)
	}
}

// The case says what its own pattern means, and the two halves of the guide's claim are different
// sentences: a case where every assertion passes with the skill and fails without it measures the
// skill; a case where nothing does measures the model.
func TestTheMeasureLineReadsTheCase(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qDojo)
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	if !strings.Contains(body, "1 of 2 pass with the skill and fail without it") {
		t.Fatalf("a half-measuring case is not read that way:\n%s", body)
	}
	press(m, "home")
	press(m, "tab")
	body = stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	if !strings.Contains(body, "no assertion passes with and fails without the skill") {
		t.Fatalf("a case both arms pass is not read that way:\n%s", body)
	}
}

// The feedback is the case's, and it opens from any row — a person arguing with a verdict that passed
// is writing the note this pane exists for. The row under the cursor is what they were looking at.
func TestFeedbackOpensFromAPassingRow(t *testing.T) {
	m, _ := qualityFixture(t)
	openView(m, "dummy", qDojo)
	press(m, "tab")
	press(m, "k") // the assertion both arms passed
	press(m, "f")
	if m.quality.feedback == nil {
		t.Fatal("f on a passing row opened no feedback")
	}
	if m.quality.feedback.slug != "dummy-2-three-dice" {
		t.Fatalf("the note is on %q, want the case that owns the row", m.quality.feedback.slug)
	}
	if st := stripMarks(m.status); !strings.Contains(st, "the total was one off") && !strings.Contains(st, "feedback") {
		t.Fatalf("the status says nothing about what was opened: %q", st)
	}
}
