package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"

	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// A set is one pane: the table above, the row under the cursor in its fields below, always. There is
// nothing between looking at a row and changing it — no screen to open, no key that has to reveal the
// fields first. `e` is only the move that puts the caret in them.
//
// The iteration workbench is the exception, on purpose: it reports the set and does not edit it,
// because the one thing that screen edits is the description.

// trainPane is where a train row is written: the set beside the two sections, with the keys in it.
func trainPane(t *testing.T) (*Model, string) {
	t.Helper()
	m, root := measured(t, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, false),
		row(3, "validation", true, true), row(4, "validation", false, false),
	})
	press(m, "tab")
	if m.sections.focus != sectionFocusSet || m.sectionPaneWidth() == 0 {
		t.Fatal("the pane beside the two sections must take the keys")
	}
	return m, root
}

func triggerFile(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "trigger", "trigger-evals.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTheRowsFieldsAreUnderTheTableWithoutAKey(t *testing.T) {
	m, _ := trainPane(t)
	if m.form == nil {
		t.Fatal("the set must draw the selected row's fields without being asked")
	}
	if m.form.active {
		t.Fatal("the fields must not have the caret until they are asked for: j and k still move the table")
	}
	view := stripMarks(m.View())
	for _, want := range []string{"ID", "TRIGGER", "QUERY", "query", "why",
		"should the skill load?", "split", "build me slides from this outline"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the pane is missing %q:\n%s", want, view)
		}
	}
	// The table stays above the fields, and the fields carry the row under the cursor.
	if strings.Index(view, "TRIGGER") > strings.Index(view, "should the skill load?") {
		t.Fatalf("the fields must be drawn under the table:\n%s", view)
	}
	// Moving in the table moves the fields, and none of it puts the caret in them.
	press(m, "j")
	if m.form.id != m.sections.rows[1].ID || m.form.active {
		t.Fatalf("the fields must follow the pane's cursor and stay quiet: %+v", m.form)
	}
}

func TestEditingARowInPlace(t *testing.T) {
	m, root := trainPane(t)
	press(m, "e")
	if !m.form.active {
		t.Fatal("e must put the caret in the fields the pane is already showing")
	}
	before := m.form.query
	// With the caret in the ask, q and ? are characters — the frame keys belong to the table.
	press(m, "q")
	press(m, "?")
	if m.form == nil || !m.form.active || m.form.query == before {
		t.Fatalf("the ask did not take the keystrokes: %q", m.form.query)
	}
	if m.status != "" {
		t.Fatalf("typing ran a command: status = %q", m.status)
	}
	press(m, "ctrl+s")
	if !strings.Contains(triggerFile(t, root), "build me slides from this outlineq?") {
		t.Fatalf("the file does not carry the edit:\n%s", triggerFile(t, root))
	}
	// The pane keeps its fields after the save; they are seeded again from what was written.
	if m.form == nil || m.form.active || m.form.query != m.store.Queries()[0].Query {
		t.Fatalf("the fields were not re-seeded from the write: %+v", m.form)
	}
	if m.sections.sel != 0 {
		t.Fatalf("the cursor must stay on the row that was saved: %d", m.sections.sel)
	}
}

func TestEscGivesTheKeysBackWithoutWriting(t *testing.T) {
	m, root := trainPane(t)
	was := triggerFile(t, root)
	press(m, "e")
	press(m, "x")
	press(m, "esc")
	if m.form == nil || m.form.active {
		t.Fatal("esc must hand the keys back to the table and leave the fields where they are")
	}
	if m.mode != screenSections {
		t.Fatalf("esc in the fields stays on the section screen: mode = %d", m.mode)
	}
	if triggerFile(t, root) != was {
		t.Fatalf("a discarded edit wrote the file:\n%s", triggerFile(t, root))
	}
}

func TestARowMovesBetweenSplitsFromTheSameFields(t *testing.T) {
	m, root := trainPane(t)
	press(m, "e")
	m.form.splitSel = store.SplitValidation
	press(m, "ctrl+s")

	for _, e := range m.sections.rows {
		if e.ID == 1 {
			t.Fatal("the row is still in the train set after being moved to validation")
		}
	}
	if !strings.Contains(m.status, "moved train → validation") {
		t.Fatalf("status = %q", m.status)
	}
	if got := m.store.Rows(store.LaneTrigger, store.Filter{Skill: "slide-builder", Split: store.SplitValidation}); len(got) != 3 {
		t.Fatalf("validation rows = %d, want 3", len(got))
	}
	if !strings.Contains(triggerFile(t, root), `"split": "validation"`) {
		t.Fatalf("the split was not written:\n%s", triggerFile(t, root))
	}
	if m.sections.sel >= len(m.sections.rows) {
		t.Fatalf("the cursor was left past the end of the set: %d of %d", m.sections.sel, len(m.sections.rows))
	}
	if m.form == nil || m.form.id != m.sections.rows[m.sections.sel].ID {
		t.Fatalf("the fields must follow the cursor to the row it landed on: %+v", m.form)
	}
}

func TestANewRowIsWrittenIntoTheSplitOnScreen(t *testing.T) {
	m, root := trainPane(t)
	press(m, "n")
	if m.form == nil || m.form.editing || !m.form.active {
		t.Fatalf("n must open empty fields in the pane, with the caret: %+v", m.form)
	}
	if m.form.id != m.store.NextTriggerID() {
		t.Fatalf("a new row takes the next free id: %d", m.form.id)
	}
	m.form.query = "turn this outline into a deck"
	press(m, "ctrl+s")
	if len(m.sections.rows) != 3 {
		t.Fatalf("train rows = %d, want the new one", len(m.sections.rows))
	}
	if !strings.Contains(triggerFile(t, root), "turn this outline into a deck") {
		t.Fatalf("the new row is not in the file:\n%s", triggerFile(t, root))
	}
	if m.sections.rows[m.sections.sel].Text != "turn this outline into a deck" {
		t.Fatalf("the cursor must land on the row just written: %+v", m.sections.rows[m.sections.sel])
	}
}

func TestTheValidationSetHasTheSameFieldsUnderItsTable(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, false),
		row(3, "validation", true, true), row(4, "validation", false, false),
	})
	m.openSectionStep(secGeneralization, 1)
	if m.form == nil || m.form.split != store.SplitValidation || m.form.active {
		t.Fatalf("the validation set draws the same fields: %+v", m.form)
	}
	press(m, "e")
	m.form.why = "held back because it is the same job with a different artifact"
	press(m, "ctrl+s")
	if !strings.Contains(m.store.Rows(store.LaneTrigger, store.Filter{Skill: "slide-builder", Split: store.SplitValidation})[0].Why, "held back because") {
		t.Fatal("the why was not written through the store")
	}
	if m.mode != screenFlow || m.form == nil || m.form.active {
		t.Fatalf("the save hands the keys back to the table: mode = %d form = %+v", m.mode, m.form)
	}
}

// A row's own split and label are what the fields open on, and what a save writes: the selects are
// positioned from the bound value, so a negative row never opens looking like a positive one.
func TestTheFieldsOpenOnTheRowsOwnLabelAndSplit(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", false, false),
		row(3, "validation", true, true), row(4, "validation", false, false),
	})
	m.openSectionStep(secGeneralization, 1)
	press(m, "j") // q4, the validation negative
	if m.form.splitSel != store.SplitValidation || m.form.labelSel != labelWord(false) {
		t.Fatalf("the fields opened on %q / %q, want this row's split and label", m.form.splitSel, m.form.labelSel)
	}
	// Flip the label through the form — two fields on, the way tab moves the caret — and save.
	press(m, "e")
	m.passToForm(huh.NextField())
	m.passToForm(huh.NextField())
	press(m, "up")
	if m.form.labelSel != labelWord(true) {
		t.Fatalf("the select did not take the keystroke: %q", m.form.labelSel)
	}
	press(m, "ctrl+s")
	if !m.store.Queries()[3].ShouldTrigger {
		t.Fatalf("the flip was shown and not written: %+v", m.store.Queries()[3])
	}
	if !strings.Contains(m.status, "should trigger") {
		t.Fatalf("status = %q", m.status)
	}
}

// The set beside the two sections is the same pane: the two halves of the loop stay on the left, the
// rows stay above the fields, and no screen replaces either.
func TestTheSectionPaneHasTheSameFields(t *testing.T) {
	m, _, s := fixture(t)
	m.openSkill("slide-builder")
	press(m, "tab")
	if m.form == nil || m.form.active {
		t.Fatalf("focusing the pane must show the row's fields: %+v", m.form)
	}
	view := stripMarks(m.View())
	if !strings.Contains(view, "iteration") || !strings.Contains(view, "generalization") {
		t.Fatalf("the two halves of the loop must stay on screen:\n%s", view)
	}
	if !strings.Contains(view, "train set") || !strings.Contains(view, "should the skill load?") {
		t.Fatalf("the fields must be drawn under the pane's table:\n%s", view)
	}
	if strings.Index(view, "TRIGGER") > strings.Index(view, "should the skill load?") {
		t.Fatalf("the table must stay above the fields:\n%s", view)
	}
	press(m, "e")
	m.form.why = "core ask, rewritten"
	press(m, "ctrl+s")
	rows := s.Rows(store.LaneTrigger, store.Filter{Skill: "slide-builder", Split: store.SplitTrain})
	if len(rows) == 0 || rows[0].Why != "core ask, rewritten" {
		t.Fatalf("the pane's save did not land: %+v", rows)
	}
	if m.mode != screenSections || m.form == nil || m.form.active {
		t.Fatalf("the save hands the keys back to the pane: mode = %d form = %+v", m.mode, m.form)
	}
}
