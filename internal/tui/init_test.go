package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/store"
)

// gateFixture is a skill whose set is declared and whose cases carry no assertion: the state the
// lane used to open on with an empty case list and an `R` that spent six agent calls on outputs
// nothing could judge. `assertions` declares a list, so the same fixture can be the closed loop at
// its second step (a set with something to judge and nothing run).
func gateFixture(t *testing.T, assertions bool) (*Model, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HERMES_BIN", filepath.Join(root, "no-such-hermes"))
	t.Setenv("HERMES_ROOT", filepath.Join(root, "hermes"))
	t.Setenv("EVAL_MASTERCLASS_ROOT", filepath.Join(root, "masterclasses"))
	write(t, filepath.Join(root, "trigger", "trigger-evals.json"), triggerSet)
	write(t, filepath.Join(root, "dummy", "SKILL.md"),
		"---\nname: dummy\ndescription: Roll dice and report the faces that come up.\n---\n")
	list := `[]`
	if assertions {
		list = `["the reply reports one face"]`
	}
	second := list
	if assertions {
		second = `["the reply reports three faces", "the total adds up"]`
	}
	write(t, filepath.Join(root, "dummy", "evals", "evals.json"), `{
  "skill_name": "dummy",
  "evals": [
    {"id": 1, "slug": "d-1", "prompt": "Roll a die.", "expected_output": "One face.", "files": [], "assertions": `+list+`},
    {"id": 2, "slug": "d-2", "prompt": "Roll three.", "expected_output": "Three faces.", "files": [], "assertions": `+second+`}
  ]
}`)
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(s)
	m.width, m.height = 120, 34
	m.lane = store.LaneOutput
	m.openQualitySkill("dummy")
	return m, root
}

// TestTheSetOpensOnItsBand is the closed loop's contract with the screen: the menu's cursor is on the
// set, and the set's view says in one band what the loop needs before it exists — the steps as chips
// and the one errand that opens it — above the cases the view is about.
func TestTheSetOpensOnItsBand(t *testing.T) {
	m, _ := gateFixture(t, false)
	if m.mode != screenQualityMenu || m.quality.vcursor != qSetup {
		t.Fatalf("lane opened at mode %v row %d, want the menu on the set", m.mode, m.quality.vcursor)
	}
	// The status row says what the lane is waiting for before anything is pressed: the first key
	// clears it, which is why it is read here.
	if st := stripMarks(m.status); !strings.Contains(st, "assertion") {
		t.Fatalf("status = %q, want the assertion the loop is asking for", st)
	}
	press(m, "1")
	if m.quality.sec != qSetup {
		t.Fatalf("section = %d, want test cases", m.quality.sec)
	}
	if bar := stripMarks(m.barLeft()); !strings.HasSuffix(bar, "test cases") {
		t.Fatalf("path = %q, want it to end at test cases", bar)
	}
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	// Three steps here, not four: the grade is part of the run, so it is not a step of its own before
	// the first run. The cases are declared; the assertions are the first step that is not done.
	for _, want := range []string{"TEST CASES", "the loop is not open yet · 1 of 3",
		"cases", "assertions", "run",
		"an assertion for every case", "CASES", "d-1", "d-2"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the band is missing %q:\n%s", want, body)
		}
	}
	// R is the spend the band exists to prevent, so it is not even offered.
	for _, h := range m.hints() {
		if h.key == "R" {
			t.Fatalf("hints offer a run over a set with nothing to judge: %v", m.hints())
		}
	}
}

// TestRRefusesAnUnassertedSet: the run key is global to the lane, so the guard has to be on the run
// and not on the screen it was pressed from.
func TestRRefusesAnUnassertedSet(t *testing.T) {
	m, _ := gateFixture(t, false)
	press(m, "1")
	press(m, "R")
	if m.mode != screenQuality {
		t.Fatalf("R started a run over an unasserted set: mode %v, status %q", m.mode, stripMarks(m.status))
	}
	if st := stripMarks(m.status); !strings.Contains(st, "carry no assertion") {
		t.Fatalf("status = %q, want the assertions named", st)
	}
}

// TestTheBandClosesOnTheGrade: the band is the disk's reading, not a screen the lane is stuck on. A
// run nobody judged leaves the loop closed with the grade as its errand; the first verdicts open it,
// and the band goes with them.
func TestTheBandClosesOnTheGrade(t *testing.T) {
	m, root := gateFixture(t, true)
	press(m, "1")
	iter := filepath.Join(root, "dummy-workspace", "iteration-1")
	write(t, filepath.Join(iter, "meta.json"),
		`{"iteration":1,"skill":"dummy","started":"2026-11-01T12:00:00+0100"}`)
	// The run exists and the arms are on disk, but nobody has judged them yet.
	for _, slug := range []string{"d-1", "d-2"} {
		for _, arm := range []string{"with_skill", "without_skill"} {
			write(t, filepath.Join(iter, slug, arm, "response.md"), "one face")
		}
	}
	m.reloadQuality()
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	if !strings.Contains(body, "the loop is not open yet · 3 of 4") {
		t.Fatalf("an ungraded run must leave the loop closed at the grade:\n%s", body)
	}
	if !strings.Contains(body, "no verdicts") {
		t.Fatalf("the band does not say why the grade is owed:\n%s", body)
	}

	writeGrading(t, iter, "d-1", "with_skill", []graded{{"the reply reports one face", false}})
	writeGrading(t, iter, "d-1", "without_skill", []graded{{"the reply reports one face", false}})
	writeGrading(t, iter, "d-2", "with_skill", []graded{{"the reply reports one face", true}})
	writeGrading(t, iter, "d-2", "without_skill", []graded{{"the reply reports one face", false}})
	m.reloadQuality()
	body = stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	if strings.Contains(body, "the loop is not open yet") {
		t.Fatalf("the band is still drawn after the first grade: %+v", m.quality.lab.Checks)
	}
	if len(m.quality.fails) == 0 {
		t.Fatalf("the iteration view has no failures after a failing grade")
	}
	// Reopening the lane puts the cursor on the iteration: its newest run has failures to read.
	m.openQualitySkill("dummy")
	if m.quality.vcursor != qDojo {
		t.Fatalf("reopened at row %d, want the iteration the grade left failures in", m.quality.vcursor)
	}
}

// TestTheBandRidesAboveTheCases: the band is a state of the set's view, not a screen of its own — a
// closed loop still shows the set, and still shows it in a short window.
func TestTheBandRidesAboveTheCases(t *testing.T) {
	m, _ := gateFixture(t, true)
	m.width, m.height = 120, 18
	press(m, "1")
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	for _, want := range []string{"TEST CASES", "the loop is not open yet · 2 of 3", "cases", "d-1", "d-2"} {
		if !strings.Contains(body, want) {
			t.Fatalf("a short window lost %q:\n%s", want, body)
		}
	}
	for _, line := range strings.Split(body, "\n") {
		if w := len([]rune(line)); w > m.width {
			t.Fatalf("a line is %d cells in a %d-cell frame: %q", w, m.width, line)
		}
	}
}

// TestAClosedLoopLocksTheTwoViews: the loop starts closed, and the two views that would answer
// "nothing measured yet" are shut until it opens — dim in the menu, with the errand that opens them
// where it belongs, and a refusal (not a screen) if you try to walk into one. The cursor cannot get
// onto them either (TestTheMenuCannotWalkIntoALockedView pins that move); the pane is drawn here for
// a row the cursor cannot reach, because the shape it refuses in is what the guard exists to keep.
func TestAClosedLoopLocksTheTwoViews(t *testing.T) {
	m, _ := gateFixture(t, false)
	// The menu marks them, and only the set's row carries the action that opens one.
	menu := stripMarks(strings.Join(m.qualityMenuBody(m.bodyHeight()), "\n"))
	if !strings.Contains(menu, "locked until the loop opens") {
		t.Fatalf("a closed loop's locked rows say nothing about it:\n%s", menu)
	}
	m.quality.vcursor = qDojo // a locked row, which no key would put the cursor on
	locked := stripMarks(strings.Join(m.qualityMenuBody(m.bodyHeight()), "\n"))
	if strings.Contains(locked, "→ enter") {
		t.Fatalf("a locked row offers an action line it cannot honour:\n%s", locked)
	}
	if !strings.Contains(locked, "locked — ") || !strings.Contains(locked, "assertion for every case") {
		t.Fatalf("a locked row's pane does not say what opens it:\n%s", locked)
	}
	m.quality.vcursor = qSetup
	for _, sec := range []int{qDojo, qQuality} {
		press(m, fmt.Sprint(sec+1))
		if m.mode != screenQualityMenu {
			t.Fatalf("%d walked into view %d from a closed loop", sec+1, m.quality.sec)
		}
		if st := stripMarks(m.status); !strings.Contains(st, "locked") {
			t.Fatalf("%d was refused with %q, want the reason", sec+1, st)
		}
	}
	// A move key cannot carry the cursor to a locked row, so the only `enter` this menu has is the
	// set's — and it opens the set.
	press(m, "j")
	if m.quality.vcursor != qSetup {
		t.Fatalf("j moved the cursor to row %d on a closed loop", m.quality.vcursor)
	}
	press(m, "enter")
	if m.mode != screenQuality || m.quality.sec != qSetup {
		t.Fatalf("enter on the set = mode %v section %d, want test cases", m.mode, m.quality.sec)
	}
	// And once the grade lands, the same two rows open.
	m2, root := gateFixture(t, true)
	iter := filepath.Join(root, "dummy-workspace", "iteration-1")
	write(t, filepath.Join(iter, "meta.json"), `{"iteration":1,"skill":"dummy","started":"2026-11-01T12:00:00+0100"}`)
	writeGrading(t, iter, "d-1", "with_skill", []graded{{"a", true}})
	writeGrading(t, iter, "d-1", "without_skill", []graded{{"a", false}})
	m2.reloadQuality()
	if m2.quality.lab.InitPending {
		t.Skip("the fixture's grade did not open the loop")
	}
	m2.openQualitySkill("dummy")
	press(m2, "2")
	if m2.mode != screenQuality || m2.quality.sec != qDojo {
		t.Fatalf("an open loop still locks the iteration: mode %v section %d (%q)",
			m2.mode, m2.quality.sec, stripMarks(m2.status))
	}
}

// TestAnOpenLoopHasNoBand: a skill already in the loop opens on the menu with the cursor on the
// iteration, and its set's view draws the cases and nothing about a loop that is already open.
func TestAnOpenLoopHasNoBand(t *testing.T) {
	m, _ := qualityFixture(t)
	m.lane = store.LaneOutput
	m.openQualitySkill("dummy")
	if m.quality.vcursor != qDojo {
		t.Fatalf("row = %d, want the iteration", m.quality.vcursor)
	}
	press(m, "1")
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	if strings.Contains(body, "the loop is not open yet") {
		t.Fatalf("a graded, asserted set was described as a closed loop:\n%s", body)
	}
	if !strings.Contains(body, "dummy-1-one-die") {
		t.Fatalf("the set's view does not list the cases:\n%s", body)
	}
}
