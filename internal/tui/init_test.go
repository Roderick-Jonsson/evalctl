package tui

import (
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
	m.openQualitySkill("dummy")
	if m.mode != screenQuality || m.quality.sec != qSetup {
		t.Fatalf("lane opened at mode %v section %d, want the set", m.mode, m.quality.sec)
	}
	// The status row says what the lane is waiting for before anything is pressed: the first key
	// clears it, which is why it is read here.
	if st := stripMarks(m.status); !strings.Contains(st, "run iteration 1") {
		t.Fatalf("status = %q, want the first run the loop is asking for", st)
	}
	press(m, "1")
	if m.quality.sec != qSetup {
		t.Fatalf("section = %d, want test cases", m.quality.sec)
	}
	if bar := stripMarks(m.barLeft()); !strings.HasSuffix(bar, "initialization") {
		t.Fatalf("path = %q, want it to end at initialization", bar)
	}
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	// Three steps here, not four: the grade is part of the run, so it is not a step of its own before
	// the first run. The cases are declared; the assertions are the first step that is not done.
	for _, want := range []string{"INITIALIZATION", "the loop is not open yet · 1 of 3",
		"cases", "run", "assertions",
		"nothing is judged yet", "CASES", "d-1", "d-2"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the band is missing %q:\n%s", want, body)
		}
	}
	// R is the whole set in one spend, and it is always on offer: the run itself refuses a set with
	// nothing to judge, which is where the refusal belongs — a hidden key is a key you look for.
	found := false
	for _, h := range m.hints() {
		if h.key == "R" {
			found = true
		}
	}
	if !found {
		t.Fatalf("R is not offered with a set on screen: %v", m.hints())
	}
	// r is the per-case probe the view exists for, and it is offered for the same reason.
	if !strings.Contains(strings.Join(hintKeys(m.hints()), " "), "r") {
		t.Fatalf("r is not offered: %v", m.hints())
	}
}

// hintKeys is the band's keys as words, for the tests that read the band rather than the screen.
func hintKeys(hs []hint) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, h.key)
	}
	return out
}

// TestRRunsTheSetBeforeTheAssertionsExist: the run key is global to the lane, and a set with no
// assertion is exactly what the first run is for — it writes the outputs the sentences are written
// from, so the guard is on having cases at all, not on having something to judge.
func TestRRunsTheSetBeforeTheAssertionsExist(t *testing.T) {
	m, _ := gateFixture(t, false)
	press(m, "1")
	press(m, "R")
	if m.mode != screenRunLive {
		t.Fatalf("R refused a set with no assertion: mode %v, status %q", m.mode, stripMarks(m.status))
	}
	defer func() {
		if m.run != nil && m.run.live != nil && m.run.live.cancel != nil {
			m.run.live.cancel()
		}
	}()
	if !strings.Contains(m.run.hint, "no judge") {
		t.Fatalf("the run screen says %q, want it to say nothing is judged", m.run.hint)
	}
	if n := len(m.run.live.cases); n != 2 {
		t.Fatalf("the live run holds %d cases, want the whole set", n)
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
	if !strings.Contains(body, "not graded") {
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
	if m.quality.sec != qDojo {
		t.Fatalf("reopened at section %d, want the iteration the grade left failures in", m.quality.sec)
	}
}

// TestTheBandRidesAboveTheCases: the band is a state of the set's view, not a screen of its own — a
// closed loop still shows the set, and still shows it in a short window.
func TestTheBandRidesAboveTheCases(t *testing.T) {
	m, _ := gateFixture(t, true)
	m.width, m.height = 120, 18
	press(m, "1")
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	for _, want := range []string{"INITIALIZATION", "the loop is not open yet · 2 of 3", "cases", "d-1", "d-2"} {
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

// TestAClosedLoopOpensOnTheSet: with the loop closed there is one place the work can start — the
// declaration — so the surface opens there, and the band above the cases says what opens it. There is
// no menu to lock a row on any more: a state you cannot reach is a state you cannot be lost in.
func TestAClosedLoopOpensOnTheSet(t *testing.T) {
	m, _ := gateFixture(t, false)
	m.openQualitySkill("dummy")
	if m.mode != screenQuality || m.quality.sec != qSetup {
		t.Fatalf("a closed loop opened on mode %v section %d, want the set", m.mode, m.quality.sec)
	}
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	if !strings.Contains(body, "the loop is not open yet") {
		t.Fatalf("the set does not say what opens the loop:\n%s", body)
	}
	if st := stripMarks(m.status); !strings.Contains(st, "run iteration 1") {
		t.Fatalf("status = %q, want the first run the loop is asking for", st)
	}

	// Once the grade lands, the same row opens on the iteration: what failed is the work.
	m2, _ := qualityFixture(t)
	m2.openQualitySkill("dummy")
	if m2.mode != screenQuality || m2.quality.sec != qDojo {
		t.Fatalf("an open loop opened on section %d (%q), want the iteration",
			m2.quality.sec, stripMarks(m2.status))
	}
}

// TestAnOpenLoopHasNoBand: a skill already in the loop opens on the iteration, and its declaration
// draws the cases and nothing about a loop that is already open.
func TestAnOpenLoopHasNoBand(t *testing.T) {
	m, _ := qualityFixture(t)
	m.lane = store.LaneOutput
	m.openQualitySkill("dummy")
	if m.quality.sec != qDojo {
		t.Fatalf("section = %d, want the iteration", m.quality.sec)
	}
	m.setQualitySection(qSetup)
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	if strings.Contains(body, "the loop is not open yet") {
		t.Fatalf("an open loop still draws its closed band:\n%s", body)
	}
	if !strings.Contains(body, "dummy-1-one-die") {
		t.Fatalf("the declaration does not list the cases:\n%s", body)
	}
}
