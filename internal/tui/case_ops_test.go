package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/store"
)

// evalsSlugs is the case file as the next run would read it: the keys are only worth having if what
// they wrote is on disk, and a slug is what everything downstream — the fixture dir, the iteration
// dir, the grade — is named from.
func evalsSlugs(t *testing.T, root string) []string {
	t.Helper()
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	sk := s.Skill("dummy")
	if sk == nil {
		t.Fatal("dummy left the tree")
	}
	var out []string
	for _, c := range sk.Output.Evals {
		out = append(out, c.Slug)
	}
	return out
}

// TestInitializationMakesACaseAndDeletesIt is the pair of keys the view did not have. `n` opens the
// two fields a case cannot be written without, in the pane, and ctrl+s writes the row with the cursor
// on it; `d` asks first, then removes the row and the fixture directory it staged, because a case is
// its files as much as its lines.
func TestInitializationMakesACaseAndDeletesIt(t *testing.T) {
	m, root := gateFixture(t, false)
	press(m, "1")
	if m.quality.sec != qSetup {
		t.Fatalf("section = %d, want the initialization view", m.quality.sec)
	}
	before := len(m.quality.cases)
	was := readEvals(t, root)

	press(m, "n")
	if !m.caseNewActive() {
		t.Fatal("n must open the new case's fields")
	}
	// The id is the tool's next free one and the slug no case in the tree owns: both are the names
	// the fixture dir, the iteration dir and the grades are made from.
	if m.caseNew.id != 3 || m.caseNew.slug != "new-case-1" {
		t.Fatalf("the form opened on #%d %s, want #3 new-case-1", m.caseNew.id, m.caseNew.slug)
	}
	pane := stripMarks(strings.Join(m.casePane(max(40, m.width-m.caseListW()-3), m.bodyHeight()), "\n"))
	for _, want := range []string{"NEW CASE", "new-case-1", "prompt", "expected_output"} {
		if !strings.Contains(pane, want) {
			t.Fatalf("the pane is missing %q:\n%s", want, pane)
		}
	}
	// A case with no prompt is a case that cannot be run: the write is refused where it is typed,
	// and the file is left alone.
	press(m, "ctrl+s")
	if !m.caseNewActive() {
		t.Fatal("an empty prompt must not write a case")
	}
	if !strings.Contains(m.caseNew.errMsg, "no prompt") {
		t.Fatalf("the form does not say why it refused: %q", m.caseNew.errMsg)
	}
	if readEvals(t, root) != was {
		t.Fatal("the refused write touched the file")
	}

	m.caseNew.prompt = "Roll one d8.\n"
	m.caseNew.expected = "One face, 1 to 8."
	press(m, "ctrl+s")
	if m.caseNewActive() {
		t.Fatalf("the form stayed open after the write: %q", m.caseNew.errMsg)
	}
	if got := len(m.quality.cases); got != before+1 {
		t.Fatalf("%d cases on screen, want %d", got, before+1)
	}
	if c := m.quality.caseAt(); c == nil || c.Slug != "new-case-1" {
		t.Fatalf("the cursor is on %v, want the case n just wrote", c)
	}
	file := readEvals(t, root)
	for _, want := range []string{"new-case-1", "Roll one d8.", "One face, 1 to 8."} {
		if !strings.Contains(file, want) {
			t.Fatalf("evals.json is missing %q:\n%s", want, file)
		}
	}

	// The fixture a case stages is the other half of it.
	fixture := filepath.Join(root, "dummy", "evals", "files", "new-case-1")
	write(t, filepath.Join(fixture, "input.txt"), "a die\n")

	press(m, "d")
	if m.ask == nil {
		t.Fatalf("d must ask before deleting: %q", stripMarks(m.status))
	}
	// The question is a row, not a screen: the work stays drawn under it.
	if m.mode != screenQuality {
		t.Fatalf("mode %v while asking, want the view it was asked from", m.mode)
	}
	question := stripMarks(m.ask.line(m.width))
	if !strings.Contains(question, "delete case #3 new-case-1?") {
		t.Fatalf("the question does not say what it deletes:\n%s", question)
	}
	// The keys are the band's, in the same place every other key is named.
	if band := stripMarks(m.keyBand()); !strings.Contains(band, "y delete it") {
		t.Fatalf("the key band does not offer the answer:\n%s", band)
	}

	press(m, "y")
	if m.mode != screenQuality {
		t.Fatalf("mode %v after the delete, want the view it was asked from", m.mode)
	}
	if _, err := os.Stat(fixture); !os.IsNotExist(err) {
		t.Fatalf("the fixture dir survived the delete: %v", err)
	}
	if got := len(m.quality.cases); got != before {
		t.Fatalf("%d cases on screen after the delete, want %d", got, before)
	}
	if slugs := evalsSlugs(t, root); len(slugs) != before {
		t.Fatalf("evals.json holds %v, want new-case-1 gone", slugs)
	}
	if st := stripMarks(m.status); !strings.Contains(st, "deleted") {
		t.Fatalf("status = %q, want it to say what left", st)
	}
}

// TestTheNewCaseFormGivesTheKeysBack is the form's exits and its one move: esc throws the draft away,
// a keystroke is a character rather than a command (q and d are letters here), and tab walks on to the
// next field with the caret.
func TestTheNewCaseFormGivesTheKeysBack(t *testing.T) {
	m, root := gateFixture(t, false)
	press(m, "1")
	was := readEvals(t, root)
	press(m, "n")
	if !m.caseNewActive() {
		t.Fatal("n must open the new case's fields")
	}
	press(m, "q")
	press(m, "d")
	if m.mode != screenQuality || !m.caseNewActive() {
		t.Fatalf("typing left the form: mode %v", m.mode)
	}
	// The form opens on the slug, so the first letters are the name — and the name is the operator's.
	if m.caseNew.slug != "new-case-1"+"qd" {
		t.Fatalf("the slug did not take the keystrokes: %q", m.caseNew.slug)
	}
	// tab is huh's own move: it comes back as a command, and a form that does not pass it on is a
	// form where tab draws but never moves.
	cmd := press(m, "tab")
	if cmd != nil {
		if msg := cmd(); msg != nil {
			m.Update(msg)
		}
	}
	press(m, "zz")
	if strings.Contains(m.caseNew.slug, "zz") {
		t.Fatalf("tab did not leave the slug field: %q", m.caseNew.slug)
	}
	if !strings.Contains(m.caseNew.prompt, "zz") {
		t.Fatalf("tab did not put the caret in the prompt: %q", m.caseNew.prompt)
	}
	if readEvals(t, root) != was {
		t.Fatal("typing wrote the file; only ctrl+s does")
	}
	press(m, "esc")
	if m.caseNewActive() {
		t.Fatal("esc must close the fields")
	}
	if readEvals(t, root) != was {
		t.Fatal("esc wrote the file")
	}
}

// TestDeletingACaseCanBeCalledOff is the dialog's other key: the set is the instrument the loop is
// measured on, so esc has to leave the file exactly as it was.
func TestDeletingACaseCanBeCalledOff(t *testing.T) {
	m, root := gateFixture(t, false)
	m.openQualitySkill("dummy")
	press(m, "d")
	if m.ask == nil {
		t.Fatalf("d must ask before deleting: %q", stripMarks(m.status))
	}
	press(m, "n")
	if m.mode != screenQuality || m.ask != nil {
		t.Fatalf("mode %v ask %v after the cancel, want the view it was asked from", m.mode, m.ask != nil)
	}
	if slugs := evalsSlugs(t, root); len(slugs) != 2 {
		t.Fatalf("evals.json holds %v, want the two cases untouched", slugs)
	}
}
