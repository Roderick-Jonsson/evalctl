package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// gateFixture is a skill whose set is declared and whose cases carry no assertion: the state the
// lane used to open on with an empty case list and an `R` that spent six agent calls on outputs
// nothing could judge.
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

// TestGateOpensInsteadOfAnEmptyLoop is the entry's contract with the screen: with nothing declared
// measured, the lane opens on the checklist, draws no rail, and offers no run.
func TestGateOpensInsteadOfAnEmptyLoop(t *testing.T) {
	m, _ := gateFixture(t, false)
	if !m.quality.gate {
		t.Fatalf("the lane opened on section %d, want the initialization checklist", m.quality.sec)
	}
	if rail := stripMarks(m.contextLine(m.width)); rail != "" {
		t.Fatalf("a rail is drawn before the loop exists: %q", rail)
	}
	if bar := stripMarks(m.barLeft()); !strings.HasSuffix(bar, "init") {
		t.Fatalf("path = %q, want it to end at init", bar)
	}
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	for _, want := range []string{"INITIALIZATION", "cases", "assertions", "run", "grade",
		"2 declared", "none of 2 cases carry one", "e  an assertion for every case"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the checklist is missing %q:\n%s", want, body)
		}
	}
	// R is the spend this screen exists to prevent, so it is not even offered.
	for _, h := range m.hints() {
		if h.key == "R" {
			t.Fatalf("hints offer a run over a set with nothing to judge: %v", m.hints())
		}
	}
	if st := stripMarks(m.status); !strings.Contains(st, "assertion") {
		t.Fatalf("status = %q, want the assertion the entry is asking for", st)
	}
}

// TestGateRefusesTheRunAndTheSections: the keys that would do nothing still say why, and R does not
// leave the screen.
func TestGateRefusesTheRunAndTheSections(t *testing.T) {
	m, _ := gateFixture(t, false)
	press(m, "2")
	if m.quality.gate != true || m.quality.sec != qSetup {
		t.Fatalf("2 left the entry: gate=%v sec=%d", m.quality.gate, m.quality.sec)
	}
	if st := stripMarks(m.status); !strings.Contains(st, "not open yet") {
		t.Fatalf("status = %q, want the loop's reason", st)
	}
	press(m, "R")
	if m.mode != screenQuality {
		t.Fatalf("R started a run over an unasserted set: mode %v, status %q", m.mode, stripMarks(m.status))
	}
	if st := stripMarks(m.status); !strings.Contains(st, "carry no assertion") {
		t.Fatalf("status = %q, want the assertions named", st)
	}
}

// TestGateHandsOverTheDeclaration: the entry's other job is the file, and the declaration lists the
// cases of a set that has never run — the screen that was blank before.
func TestGateHandsOverTheDeclaration(t *testing.T) {
	m, _ := gateFixture(t, false)
	press(m, "enter")
	if m.quality.gate {
		t.Fatalf("enter did not hand over the declaration")
	}
	body := stripMarks(strings.Join(m.qualityBody(m.bodyHeight()), "\n"))
	for _, want := range []string{"CASES", "d-1", "d-2", "prompt", "Roll a die.", "expected_output"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the declaration is missing %q:\n%s", want, body)
		}
	}
	// The gate is where the disk says you are: reopening the lane comes back to it.
	m.openQualitySkill("dummy")
	if !m.quality.gate {
		t.Fatalf("reopening the lane skipped the entry while the loop is still closed")
	}
}

// TestGateClosesOnTheGrade: the entry's last errand is the judge's verdict, and finishing it opens
// the loop on the failures of the run that opened it.
func TestGateClosesOnTheGrade(t *testing.T) {
	m, root := gateFixture(t, true)
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
	if !m.quality.gate {
		t.Fatalf("an ungraded run opened the loop")
	}
	if body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n")); !strings.Contains(body, "no verdicts") {
		t.Fatalf("the entry does not say the run is ungraded:\n%s", body)
	}

	writeGrading(t, iter, "d-1", "with_skill", []graded{{"the reply reports one face", false}})
	writeGrading(t, iter, "d-1", "without_skill", []graded{{"the reply reports one face", false}})
	writeGrading(t, iter, "d-2", "with_skill", []graded{{"the reply reports one face", true}})
	writeGrading(t, iter, "d-2", "without_skill", []graded{{"the reply reports one face", false}})
	m.reloadQuality()
	if m.quality.gate {
		t.Fatalf("the gate is still drawn after the first grade: %+v", m.quality.lab.Checks)
	}
	if m.quality.sec != qDojo {
		t.Fatalf("section = %d, want the dojo on the failures the grade found", m.quality.sec)
	}
	if len(m.quality.fails) == 0 {
		t.Fatalf("the dojo has no failures after a failing grade")
	}
}

// TestGateShowsTheSetBeforeItRuns: the entry is the last screen before a run costs money, so it
// shows what that run will be judged on — the cases as a table, and the assertions of the one under
// the cursor as their own band.
func TestGateShowsTheSetBeforeItRuns(t *testing.T) {
	m, _ := gateFixture(t, true)
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	for _, want := range []string{"STEP", "WHAT", "NOW", "KEY", "THE SET", "CASE", "ASSERTIONS",
		"STATE", "d-1", "d-2", "declared", "ASSERTIONS · D-1", "the reply reports one face"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the entry does not show the set: missing %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "the total adds up") {
		t.Fatalf("the assertions under the list are not the selected case's:\n%s", body)
	}
	// The whole point of the cursor: the other case's assertions, without leaving the entry.
	press(m, "j")
	body = stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	for _, want := range []string{"ASSERTIONS · D-2", "the reply reports three faces", "the total adds up"} {
		if !strings.Contains(body, want) {
			t.Fatalf("j did not walk the set: missing %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "the reply reports one face") {
		t.Fatalf("the list still reads the first case after j:\n%s", body)
	}
	// And the keys that act on the selection are offered.
	var keys []string
	for _, h := range m.hints() {
		keys = append(keys, h.key)
	}
	if got := strings.Join(keys, " "); !strings.Contains(got, "j k") || !strings.Contains(got, "enter") {
		t.Fatalf("hints = %q, want the set walkable and the declaration reachable", got)
	}
}

// TestGateSpendsRoomOnTheCasesFirst: in a short window the assertion band is what gives way, not the
// case rows and not the action line.
func TestGateSpendsRoomOnTheCasesFirst(t *testing.T) {
	m, _ := gateFixture(t, true)
	m.height = 20
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	if !strings.Contains(body, "run iteration 1") {
		t.Fatalf("a short window cut the action line:\n%s", body)
	}
	for _, want := range []string{"THE SET", "d-1", "d-2", "2 declared"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the case rows gave way before the band: missing %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "ASSERTIONS ·") {
		t.Fatalf("the assertion band squeezed the cases out of a short window:\n%s", body)
	}
}

// TestAssertionBandNamesTheCut: a list that does not fit says how much it left out, because a list
// that just stops reads as a complete one.
func TestAssertionBandNamesTheCut(t *testing.T) {
	c := runner.QualityCase{Slug: "d-1", Rows: []runner.QualityAssertion{
		{Text: "one face"}, {Text: "two"}, {Text: "three"}, {Text: "four"},
	}}
	out := stripMarks(strings.Join(assertionLines(c, 80, 6), "\n"))
	if !strings.Contains(out, "ASSERTIONS · D-1") || !strings.Contains(out, "one face") {
		t.Fatalf("the band does not open with its own name and first row:\n%s", out)
	}
	if !strings.Contains(out, "more — enter for the record") {
		t.Fatalf("the cut is not named:\n%s", out)
	}
}

// TestGradedFixtureDoesNotGate: a skill already in the loop opens where it left off, unchanged.
func TestGradedFixtureDoesNotGate(t *testing.T) {
	m, _ := qualityFixture(t)
	m.lane = store.LaneOutput
	m.openQualitySkill("dummy")
	if m.quality.gate {
		t.Fatalf("a graded, asserted set was sent back to the entry")
	}
	if m.quality.sec != qDojo {
		t.Fatalf("section = %d, want the dojo", m.quality.sec)
	}
}
