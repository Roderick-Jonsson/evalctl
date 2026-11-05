package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/runner"
)

// TestThePlanIsTheCaseAndTheSet is the whole difference between `r` and `R`: one list of ids. The ids
// are what the runner filters on, so this is the spend, not a label on it.
func TestThePlanIsTheCaseAndTheSet(t *testing.T) {
	m, _ := gateFixture(t, true)
	press(m, "1")
	c := m.quality.caseAt()
	if c == nil {
		t.Fatal("no case under the cursor")
	}

	one := m.outputRunPlan([]runner.QualityCase{*c})
	if len(one.ids) != 1 || one.ids[0] != c.ID {
		t.Fatalf("a case run plans ids %v, want just %d", one.ids, c.ID)
	}
	if one.iter != 1 {
		t.Fatalf("the first run plans iteration %d, want 1", one.iter)
	}
	if !strings.Contains(one.scope, c.Slug) {
		t.Fatalf("the scope %q does not name the case", one.scope)
	}
	if !strings.Contains(one.hint, "no rate yet") {
		t.Fatalf("the hint %q does not say the run will not rank", one.hint)
	}

	all := m.outputRunPlan(m.quality.cases)
	if len(all.ids) != len(m.quality.cases) {
		t.Fatalf("the set run plans ids %v, want every case", all.ids)
	}
	if !strings.Contains(all.hint, "the set is whole") {
		t.Fatalf("the set run is a measurement, and does not say so: %q", all.hint)
	}

	// The next run's number is the last iteration's plus one, for both keys.
	if n := len(m.quality.lab.Iterations); n > 0 {
		t.Errorf("an unrun fixture has %d iterations", n)
	}
}

// TestRIsOfferedWheneverThereIsASet: the whole-set run is the one spend the view must never hide —
// a declared set is measurable, and a key that appears only once every case is asserted is a key you
// look for and cannot find.
func TestRIsOfferedWheneverThereIsASet(t *testing.T) {
	m, _ := gateFixture(t, false)
	press(m, "1")
	keys := strings.Join(hintKeys(m.hints()), " ")
	for _, want := range []string{"r", "R"} {
		if !strings.Contains(keys, want) {
			t.Fatalf("the band %q does not offer %q", keys, want)
		}
	}
	// The pane's own action line follows the case under the cursor: nothing has run yet, so the errand
	// is the first run — the output the assertions are written from.
	pane := stripMarks(strings.Join(m.casePane(max(40, m.width-m.caseListW()-3), m.bodyHeight()), "\n"))
	if !strings.Contains(pane, "run it once") {
		t.Fatalf("the pane does not point at the first run:\n%s", pane)
	}
}

// TestRProbesACaseWithNoAssertion: the first run of a case exists to show what the agent writes, so a
// case with nothing to judge is runnable — that is step B. What is not runnable is a case with no
// prompt: the arm would be handed an empty ask.
func TestRProbesACaseWithNoAssertion(t *testing.T) {
	m, _ := gateFixture(t, false)
	press(m, "1")
	press(m, "r")
	if m.mode != screenRunLive {
		t.Fatalf("r refused a case with no assertion: mode %v, status %q", m.mode, stripMarks(m.status))
	}
	defer func() {
		if m.run != nil && m.run.live != nil && m.run.live.cancel != nil {
			m.run.live.cancel()
		}
	}()
	if n := len(m.run.live.cases); n != 1 {
		t.Fatalf("the probe holds %d cases, want the one under the cursor", n)
	}
	if !strings.Contains(m.run.hint, "no judge") {
		t.Fatalf("the run screen says %q, want it to say nothing is judged yet", m.run.hint)
	}

	// The same key refuses a case whose prompt is not written. The case is cleared on the model rather
	// than on disk: walking into the view re-reads the file, so a write here would be undone by the
	// view's own reload.
	m, _ = gateFixture(t, true)
	press(m, "1")
	m.quality.cases[0].Prompt = ""
	m.startCaseRun()
	if m.mode != screenQuality {
		t.Fatalf("r started a run over a case with no prompt: mode %v", m.mode)
	}
	if st := stripMarks(m.status); !strings.Contains(st, "no prompt") {
		t.Fatalf("status = %q, want the missing prompt named", st)
	}
}

// TestRStartsAOneCaseRun is the screen: the live run is the case under the cursor and nothing else —
// the set is what `R` is for, and a probe that quietly ran the set would be the spend this view is
// meant to shrink.
func TestRStartsAOneCaseRun(t *testing.T) {
	m, _ := gateFixture(t, true)
	press(m, "1")
	press(m, "r")
	if m.mode != screenRunLive {
		t.Fatalf("r did not start a run: mode %v, status %q", m.mode, stripMarks(m.status))
	}
	defer func() {
		if m.run != nil && m.run.live != nil && m.run.live.cancel != nil {
			m.run.live.cancel()
		}
	}()
	if m.run == nil || m.run.live == nil {
		t.Fatal("no live run")
	}
	if n := len(m.run.live.cases); n != 1 {
		t.Fatalf("the live run holds %d cases, want one", n)
	}
	if got := m.run.live.cases[0].name; got != "d-1" {
		t.Fatalf("the live run is on %q, want d-1", got)
	}
	if n := len(m.run.live.cases[0].runs); n != len(runner.AllArms) {
		t.Fatalf("%d arms seeded, want %d", n, len(runner.AllArms))
	}
}

// ungradedArm stages what a run writes for one arm before anything is judged: the response, the cost,
// and a grading.json with no summary — the shape `RunArm` leaves behind on purpose.
func ungradedArm(t *testing.T, iter, slug, arm, response string) {
	t.Helper()
	write(t, filepath.Join(iter, slug, arm, "grading.json"),
		`{"assertion_results":[],"response":"`+response+`","checks":{}}`)
	write(t, filepath.Join(iter, slug, arm, "timing.json"),
		`{"duration_ms":1200,"total_tokens":300,"exit_code":0}`)
}

// TestInitializationWalksTheFirstIteration is the five steps of a case's first pass, in one test: the
// run that produces something to read, the outputs that are read, the assertions written from them,
// the grade, and the human review — each one changing what the pane offers next.
func TestInitializationWalksTheFirstIteration(t *testing.T) {
	m, root := gateFixture(t, false)
	press(m, "1")
	if k, _ := m.setupAction(*m.quality.caseAt()); k != "r" {
		t.Fatalf("a case that has never run offers %q, want the run", k)
	}

	// The run's own output, as the arms write it: nothing judged, two responses to read.
	iter := filepath.Join(root, "dummy-workspace", "iteration-1")
	write(t, filepath.Join(iter, "meta.json"), `{"iteration":1,"skill":"dummy","started":"2026-11-01T12:00:00+0100"}`)
	ungradedArm(t, iter, "d-1", "with_skill", "The die came up 4.")
	ungradedArm(t, iter, "d-1", "without_skill", "4")
	m.reloadQuality()
	c := m.quality.caseAt()
	if c == nil || !c.Ran {
		t.Fatalf("the case the iteration reached does not say it ran: %+v", c)
	}
	// C: the outputs are what the assertions are written from, so the errand is the list.
	if k, _ := m.setupAction(*c); k != "e" {
		t.Fatalf("a run case with no assertion offers %q, want the assertions", k)
	}
	// v reads what the arms wrote, without a judge ever having run.
	press(m, "v")
	if m.mode != screenDoc {
		t.Fatalf("v did not open the outputs: mode %v, status %q", m.mode, stripMarks(m.status))
	}
	doc := stripMarks(strings.Join(m.doc.lines, "\n"))
	for _, want := range []string{"WITH SKILL", "WITHOUT SKILL", "The die came up 4.", "Roll a die."} {
		if !strings.Contains(doc, want) {
			t.Fatalf("the outputs are missing %q:\n%s", want, doc)
		}
	}
	press(m, "esc")

	// D: the sentences are declared, so the errand is the judge.
	sk := m.store.Skill("dummy")
	cse := sk.Output.Evals[0]
	cse.Assertions = []string{"The reply reports one face"}
	if _, err := m.store.UpdateOutput("dummy", 0, cse); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SaveSkill("dummy"); err != nil {
		t.Fatal(err)
	}
	m.reloadQuality()
	if k, _ := m.setupAction(*m.quality.caseAt()); k != "g" {
		t.Fatalf("a declared, ungraded case offers %q, want the grade", k)
	}

	// E: with verdicts on disk the case is graded, and the note is the next move.
	writeGrading(t, iter, "d-1", "with_skill", []graded{{"The reply reports one face", true}})
	writeGrading(t, iter, "d-1", "without_skill", []graded{{"The reply reports one face", false}})
	m.reloadQuality()
	if k, _ := m.setupAction(*m.quality.caseAt()); k != "f" {
		t.Fatalf("a graded case offers %q, want the review", k)
	}
	press(m, "f")
	if !m.feedbackActive() {
		t.Fatalf("f did not open the review: status %q", stripMarks(m.status))
	}
	pane := stripMarks(strings.Join(m.casePane(max(40, m.width-m.caseListW()-3), m.bodyHeight()), "\n"))
	if !strings.Contains(pane, "FEEDBACK") {
		t.Fatalf("the review is not drawn where the case is:\n%s", pane)
	}
	press(m, "esc")
	if m.feedbackActive() {
		t.Fatal("esc must close the review")
	}
}

// TestAProbeLandsBackOnTheSet: a run that leaves the loop closed has no verdicts to read, so the screen
// it hands back is the one where the next step lives — the set — and not a dojo with nothing in it.
func TestAProbeLandsBackOnTheSet(t *testing.T) {
	m, _ := gateFixture(t, false)
	press(m, "1")
	m.run = &runState{kind: runQuality, live: newLiveRun("output run · iteration 1", false)}
	m.leaveRun()
	if m.mode != screenQuality || m.quality.sec != qSetup {
		t.Fatalf("a probe run landed on mode %v section %d, want the set", m.mode, m.quality.sec)
	}
	if st := stripMarks(m.status); !strings.Contains(st, "e writes the assertions") {
		t.Fatalf("status = %q, want the next step named", st)
	}

	// With the loop open the same key lands where the verdicts are.
	m2, _ := qualityFixture(t)
	openView(m2, "dummy", qSetup)
	m2.run = &runState{kind: runQuality, live: newLiveRun("output run · iteration 2", false)}
	m2.leaveRun()
	if m2.quality.sec != qDojo {
		t.Fatalf("a graded run landed on section %d, want the iteration view", m2.quality.sec)
	}
}

// TestACaseRunJoinsTheOpenIteration: an iteration is the set measured once, so a case run on its own
// lands in the iteration being built — the run that closes the set says so, and a case the iteration
// already holds is measured again in place rather than doubling it.
func TestACaseRunJoinsTheOpenIteration(t *testing.T) {
	m, root := gateFixture(t, true)
	iter := filepath.Join(root, "dummy-workspace", "iteration-1")
	write(t, filepath.Join(iter, "meta.json"),
		`{"iteration":1,"skill":"dummy","started":"2026-11-01T12:00:00+0100","cases":["d-1"]}`)
	write(t, filepath.Join(iter, "d-1", "with_skill", "response.md"), "one face")
	press(m, "1")

	// The case the iteration already holds: same number, measured again, nothing else touched.
	again := m.outputRunPlan([]runner.QualityCase{m.quality.cases[0]})
	if again.iter != 1 || !again.replace || len(again.ids) != 1 {
		t.Fatalf("re-running a case the iteration holds planned iter=%d replace=%v ids=%v",
			again.iter, again.replace, again.ids)
	}

	// The open case: added to the same iteration, and it is the run that closes the set.
	last := m.outputRunPlan([]runner.QualityCase{m.quality.cases[1]})
	if last.iter != 1 || last.replace || len(last.ids) != 1 || last.ids[0] != m.quality.cases[1].ID {
		t.Fatalf("an open case did not join its iteration: iter=%d replace=%v ids=%v",
			last.iter, last.replace, last.ids)
	}
	if !strings.Contains(last.hint, "the set is whole") {
		t.Fatalf("the run that closes the set says %q", last.hint)
	}

	// `R` over the whole set runs only what is missing: the case already on disk is not bought again.
	set := m.outputRunPlan(m.quality.cases)
	if len(set.ids) != 1 || set.ids[0] != m.quality.cases[1].ID {
		t.Fatalf("the set run covers %v, want only the open case", set.ids)
	}
	if set.iter != 1 {
		t.Fatalf("the set run planned iteration %d, want the open one", set.iter)
	}
}
