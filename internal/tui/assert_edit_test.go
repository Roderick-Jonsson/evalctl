package tui

import (
	"fmt"
	"strings"
	"testing"
)

// openAssertList opens the list of the case under the cursor in the initialization view.
func openAssertList(t *testing.T, m *Model) *assertState {
	t.Helper()
	openView(m, "dummy", qSetup)
	press(m, "j")
	press(m, "e")
	if !m.assertEditActive() {
		t.Fatal("e must open the case's assertion list")
	}
	return m.assertEdit
}

// TestTheAssertionBlockGrowsWithTheList: every assertion is drawn when the window has room for it, and
// only a window too short loses rows — with the assertion being typed kept in view. The block is not a
// field with a fixed height that scrolls inside itself.
func TestTheAssertionBlockGrowsWithTheList(t *testing.T) {
	m, _ := qualityFixture(t)
	m.width, m.height = 110, 44
	a := openAssertList(t, m)
	a.lines = nil
	for i := 1; i <= 12; i++ {
		a.lines = append(a.lines, fmt.Sprintf("check-%02d", i))
	}
	a.cur, a.col = 11, 0

	pane := stripMarks(strings.Join(a.pane(80, 30), "\n"))
	for i := 1; i <= 12; i++ {
		if !strings.Contains(pane, fmt.Sprintf("check-%02d", i)) {
			t.Fatalf("a window with room lost check-%02d:\n%s", i, pane)
		}
	}

	// Ten rows is not room for twelve assertions and the head: the tail is drawn, and the caret's own
	// assertion is on screen.
	short := stripMarks(strings.Join(a.pane(80, 10), "\n"))
	if !strings.Contains(short, "check-12") {
		t.Fatalf("a short window lost the assertion being typed:\n%s", short)
	}
	if strings.Contains(short, "check-01") {
		t.Fatalf("a short window should have scrolled past the first assertions:\n%s", short)
	}
}

// TestEnterStartsTheNextAssertion is the semantics of the bullet: enter is a new assertion, not a
// newline inside one, and the mark that says so is drawn by the pane and never typed into the text.
func TestEnterStartsTheNextAssertion(t *testing.T) {
	m, _ := qualityFixture(t)
	a := openAssertList(t, m)
	a.lines = []string{"the reply is a number"}
	a.cur, a.col = 0, len("the reply is a number")

	press(m, "enter")
	if len(a.lines) != 2 || a.lines[0] != "the reply is a number" || a.lines[1] != "" {
		t.Fatalf("enter must open an empty assertion under the first: %q", a.lines)
	}
	press(m, "x")
	if a.lines[1] != "x" || !strings.Contains(a.lines[0], "number") {
		t.Fatalf("typing went to the wrong assertion: %q", a.lines)
	}
	if strings.Contains(strings.Join(a.lines, ""), glyphs.Bullet) {
		t.Fatalf("the bullet is part of an assertion: %q", a.lines)
	}
	view := stripMarks(strings.Join(a.pane(80, 20), "\n"))
	if n := strings.Count(view, glyphs.Bullet); n != 2 {
		t.Fatalf("%d bullets for two assertions:\n%s", n, view)
	}
}

// TestTheCaretWalksTheAssertions: the arrows move inside a sentence and across assertions, and
// backspace at the head of one joins it to the assertion above.
func TestTheCaretWalksTheAssertions(t *testing.T) {
	m, _ := qualityFixture(t)
	a := openAssertList(t, m)
	a.lines = []string{"first", "second"}
	a.cur, a.col = 0, 0

	press(m, "right")
	if a.col != 1 {
		t.Fatalf("right moved the caret to %d", a.col)
	}
	press(m, "end")
	if a.col != len("first") {
		t.Fatalf("end put the caret at %d, want the sentence's end", a.col)
	}
	press(m, "right") // across the boundary
	if a.cur != 1 || a.col != 0 {
		t.Fatalf("right at the end of an assertion went to %d/%d, want the next one", a.cur, a.col)
	}
	press(m, "up")
	if a.cur != 0 || a.col != 0 {
		t.Fatalf("up went to %d/%d", a.cur, a.col)
	}
	press(m, "down")
	press(m, "backspace") // at the head of the second: join it to the first
	if len(a.lines) != 1 || a.lines[0] != "firstsecond" || a.cur != 0 || a.col != len("first") {
		t.Fatalf("backspace at the head did not join the assertions: %q cur=%d col=%d", a.lines, a.cur, a.col)
	}
	press(m, "end")
	press(m, "backspace")
	if a.lines[0] != "firstsecon" || a.col != len("firstsecon") {
		t.Fatalf("backspace deleted the wrong character: %q col=%d", a.lines[0], a.col)
	}
}
