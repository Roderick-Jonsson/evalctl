package tui

import (
	"strings"
	"testing"

	"evalctl/internal/runner"
)

// One cursor, one region.
//
// These screens show up to four keyboard areas at once, and the question a person actually asks is
// "where do my keys go right now". Before this was a rule the screen answered it four different
// ways — an accented column head, a brightened pane title, a textarea caret that blinks off screen,
// and two cursor marks on the same frame. So the rule is now asserted rather than described: every
// screen draws exactly one block, in front of the name of the region that has the keys, and at most
// one cursor mark, inside it.

// marks counts the two focus marks in a finished frame.
func marks(m *Model) (cursor, block int) {
	view := stripMarks(m.View())
	return strings.Count(view, glyphs.Cursor), strings.Count(view, glyphs.Block)
}

// regionsOf is the focus rule as a table: the screen, how to put the keys somewhere, and whether a
// cursor row is expected there. A field with the caret has no cursor mark of its own — the caret is
// the mark — so those cases expect zero.
func TestOneRegionHasTheKeys(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, false),
		row(3, "validation", true, true), row(4, "validation", false, false),
	})
	m.openSkill("slide-builder")

	check := func(name string, wantCursor int) {
		t.Helper()
		cursor, block := marks(m)
		if block != 1 {
			t.Fatalf("%s: %d region marks on the frame, want exactly one — the region with the keys:\n%s",
				name, block, stripMarks(m.View()))
		}
		if cursor != wantCursor {
			t.Fatalf("%s: %d cursor marks, want %d — the cursor belongs to the region with the keys:\n%s",
				name, cursor, wantCursor, stripMarks(m.View()))
		}
	}

	// The two screens before the loop, and the one after it: a list with a head of its own.
	m.mode = screenLane
	check("the door", 1)
	m.mode = screenSkill
	m.loadSkills()
	check("the chooser", 1)
	m.mode = screenFlow
	m.flow.sec, m.flow.step, m.flow.forced = secGeneralization, 3, true
	m.reload()
	check("the results", 1)

	// The section screen: the list, then the pane beside it.
	m.openSkill("slide-builder")
	m.mode = screenSections
	m.sections.focus = sectionFocusList
	m.loadSectionRows()
	check("sections · list", 1)
	m.sections.focus = sectionFocusSet
	check("sections · pane", 1)

	// The workbench: the set, the description, the note, and the fields under the set.
	m.mode = screenFlow
	m.flow.sec, m.flow.step = secIteration, 1
	m.reload()
	m.ensureDraft()
	for _, c := range []struct {
		name   string
		focus  int
		cursor int
	}{
		{"workbench · set", focusSet, 1},
		{"workbench · description", focusWording, 0},
		{"workbench · note", focusNote, 0},
	} {
		m.flow.focus = c.focus
		m.flow.applyFocus()
		check(c.name, c.cursor)
	}

	// With the caret in the set's fields, the set is not the region with the keys.
	m.flow.focus = focusSet
	m.flow.applyFocus()
	m.focusForm()
	check("workbench · editor", 0)
}

// statusHere is the status row's own answer to "where am I", which must agree with the frame.
func TestTheStatusRowNamesTheRegionWithTheKeys(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{row(1, "train", true, true)})
	m.openSkill("slide-builder")
	m.mode = screenSections

	m.sections.focus = sectionFocusList
	if got, want := stripMarks(m.hereLine()), "sections · 1 / 2 "; got != want {
		t.Fatalf("here = %q, want %q", got, want)
	}
	m.sections.focus = sectionFocusSet
	if got := stripMarks(m.hereLine()); !strings.HasPrefix(got, "train set · row 1 of 2") {
		t.Fatalf("here = %q, want the train set's row", got)
	}
	if got := m.region(); got != "train set" {
		t.Fatalf("region = %q", got)
	}

	m.mode = screenFlow
	m.flow.sec, m.flow.step = secIteration, 1
	m.reload()
	m.ensureDraft()
	m.flow.focus = focusNote
	if got := m.region(); got != "note" {
		t.Fatalf("region = %q, want the note", got)
	}
	m.flow.focus = focusSet
	if got := m.region(); got != "train set" {
		t.Fatalf("region = %q, want the set", got)
	}
}

// TestTheCaretSurvivesAFrame is the bug the region rule exposed: the set's fields are re-seeded
// whenever their width does not match the pane's, and the workbench used to tell them one width and
// draw them at another — so the caret was thrown away on the very next frame, between two
// keystrokes, and `e` looked like a key that did nothing.
func TestTheCaretSurvivesAFrame(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{row(1, "train", true, true)})
	enterSection(m, secIteration)
	press(m, "e")
	if m.form == nil || !m.form.active {
		t.Fatal("e must put the caret in the fields")
	}
	// A frame is the thing that used to lose it.
	if _, block := marks(m); block != 1 {
		t.Fatalf("%d region marks", block)
	}
	if !m.form.active {
		t.Fatal("drawing the frame took the caret out of the fields")
	}
	before := m.form.query
	press(m, "z")
	if m.form.query == before {
		t.Fatalf("the ask did not take the keystroke after a frame: %q", m.form.query)
	}
	if got := m.region(); got != "editor" {
		t.Fatalf("region = %q, want the editor", got)
	}
}
