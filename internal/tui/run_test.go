package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// outputRun is a run state as the lane starts one: the whole set seeded, both arms waiting.
func outputRun(m *Model, kind runKind, iter int) {
	m.mode = screenRunLive
	m.run = &runState{kind: kind, scope: "output run · iteration 1", hint: "both arms, every case"}
	m.run.live = newLiveRun(m.run.scope, false)
	m.seedOutputRun(m.quality.cases, iter)
	m.run.live.started = time.Now().Add(-40 * time.Second)
}

// finish marks every seeded arm in the state the run would leave it.
func finish(m *Model, status string) {
	m.run.live.done = true
	m.run.live.finished = time.Now()
	for i := range m.run.live.cases {
		for j := range m.run.live.cases[i].runs {
			m.run.live.cases[i].runs[j].status = status
		}
	}
}

// TestOutputRunShowsTheWholeSetFromTheStart is the first frame: every case, both arms, before any
// agent call. A table that grows as the run goes reads as "this run has one case" — the question the
// operator is asking while it spends his money.
func TestOutputRunShowsTheWholeSetFromTheStart(t *testing.T) {
	m, _ := gateFixture(t, true)
	m.openQualitySkill("dummy")
	outputRun(m, runQuality, 1)
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	for _, want := range []string{"OUTPUT RUN · ITERATION 1", "0 of 4 arms", "CASE", "WITH", "WITHOUT",
		"STATE", "d-1", "d-2", "2 cases × 2 arms"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the first frame is missing %q:\n%s", want, body)
		}
	}
	// Nothing has started, so nothing may claim to have: both cases are waiting.
	if got := strings.Count(body, "waiting"); got != 2 {
		t.Fatalf("waiting rows = %d, want one per case:\n%s", got, body)
	}
}

// The judge is the run's second pass, so a finished run is a record of both: the arms' outputs and
// the verdicts they were read into. `g` is not the next step any more — the run grades itself.
func TestARunThatGradedEndsAsOneRecord(t *testing.T) {
	m, _ := qualityFixture(t)
	m.openQualitySkill("dummy")
	outputRun(m, runQuality, 1)
	m.applyEvent(runner.Event{Kind: runner.EventPhase, Lane: store.LaneOutput, Phase: "grade",
		Message: "judging iteration 1 — one judge call per arm"})
	if !m.run.live.judging {
		t.Fatal("the phase event did not move the run into its judge pass")
	}
	finish(m, "done")
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	for _, want := range []string{"RUN + GRADED · ITERATION 1", "4 of 4 arms",
		"100% with the skill", "75% without", "graded", "the iteration — the failed assertions"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the finished run is missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "grade it") {
		t.Fatalf("a run that graded still offers the judge as the next step:\n%s", body)
	}
}

// The judge pass is not a second run of the arm: the arm cells keep what the agent did, and the
// state says which pass the row is in. Folding the two into one status made the same arm look like it
// was running twice, which reads as a hang.
func TestTheJudgePassIsItsOwnPass(t *testing.T) {
	m, _ := gateFixture(t, true)
	m.openQualitySkill("dummy")
	outputRun(m, runQuality, 1)
	for i := range m.run.live.cases {
		for j := range m.run.live.cases[i].runs {
			m.run.live.cases[i].runs[j].status = "done"
		}
	}
	m.applyEvent(runner.Event{Kind: runner.EventPhase, Lane: store.LaneOutput, Phase: "grade",
		Message: "judging iteration 1 — one judge call per arm"})
	m.applyEvent(runner.Event{Kind: runner.EventArmStart, Lane: store.LaneOutput, Case: "d-1", Arm: "with_skill"})
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	for _, want := range []string{"GRADING · ITERATION 1", "judging · 0 of 4 arms", "judging"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the judge pass is missing %q:\n%s", want, body)
		}
	}
	// The arm's own outcome is untouched: the judge reads the outputs, it does not redo them.
	pc := m.run.live.caseAt("d-1")
	for _, a := range pc.runs {
		if a.name == "with_skill" && a.status != "done" {
			t.Fatalf("the judge overwrote the arm's own status: %+v", a)
		}
	}
	m.applyEvent(runner.Event{Kind: runner.EventArmDone, Lane: store.LaneOutput, Case: "d-1", Arm: "with_skill"})
	if got := m.run.live.caseAt("d-1").runs[0].judged; got != "done" {
		t.Fatalf("the judged column = %q, want done", got)
	}
}

// `g` grades what the judge has not read and nothing else: on a graded iteration with nothing stale
// there is no spend to make, and after an edit it is the edited case's two judge calls, not the whole
// iteration's. The verdicts on screen are replaced by the ones it buys, so it asks first.
func TestGRegradesOnlyTheCasesThatOweAVerdict(t *testing.T) {
	m, _ := qualityFixture(t)
	m.openQualitySkill("dummy")
	press(m, "2")

	// Nothing is stale: every assertion of every case has a verdict, so there is nothing to buy.
	press(m, "g")
	if m.ask != nil {
		t.Fatalf("g asked to spend on an iteration with nothing owing: %s", stripMarks(m.ask.title))
	}
	if st := stripMarks(m.status); !strings.Contains(st, "nothing to grade") {
		t.Fatalf("status = %q, want it to say there is nothing to grade", st)
	}

	// One assertion rewritten: the case that holds it owes a verdict, and the re-grade is scoped to it.
	press(m, "e")
	m.assertEdit.lines = []string{"Three faces, each 1 to 20", "The total adds up"}
	press(m, "ctrl+s")
	press(m, "g")
	if m.ask == nil {
		t.Fatalf("g after an edit = mode %v, want the question", m.mode)
	}
	question := stripMarks(m.ask.line(m.width))
	if !strings.Contains(question, "1 of 2 cases owe a verdict") {
		t.Fatalf("the question does not say what owes a verdict:\n%s", question)
	}
	if !strings.Contains(question, "2 judge call(s)") {
		t.Fatalf("the question does not price the re-grade by the case:\n%s", question)
	}
	press(m, "y")
	if m.mode != screenRunLive || m.run == nil || m.run.kind != runGrade {
		t.Fatalf("y = mode %v, want the grading run", m.mode)
	}
	defer func() {
		if m.run != nil && m.run.live != nil && m.run.live.cancel != nil {
			m.run.live.cancel()
		}
	}()
	if !strings.Contains(m.run.scope, "re-grading") || !strings.Contains(m.run.scope, "1 case") {
		t.Fatalf("scope = %q, want the re-grade named and scoped", m.run.scope)
	}
	if n := len(m.run.live.cases); n != 1 {
		t.Fatalf("the live grade shows %d cases, want the one that owes a verdict", n)
	}
}

// TestGradeFinishShowsTheRates: a finished grade is the two rates per case and the iteration as the way
// on, because the number is what the run was for and the failures are what a revision starts from.
func TestGradeFinishShowsTheRates(t *testing.T) {
	m, _ := qualityFixture(t)
	m.openQualitySkill("dummy")
	outputRun(m, runGrade, 1)
	finish(m, "done")
	m.reloadQuality()
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	for _, want := range []string{"GRADED · ITERATION 1", "100% with the skill", "graded",
		"the iteration — the failed assertions"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the finished grade is missing %q:\n%s", want, body)
		}
	}
}

// An iteration with not graded is not a re-grade: there is nothing to replace, so `g` finishes it
// without a question and the screen says which pass it is waiting on.
func TestGFinishesAnUngradedIterationWithoutAsking(t *testing.T) {
	m, root := qualityFixture(t)
	iter2 := filepath.Join(root, "dummy-workspace", "iteration-2")
	write(t, filepath.Join(iter2, "meta.json"), `{"iteration":2,"started":"2026-11-01T13:00:00+0100"}`)
	// The arms ran and wrote their outputs; the judge never came (a --no-grade run, or a grade that was
	// cancelled), so there is nothing to replace.
	for _, slug := range []string{"dummy-1-one-die", "dummy-2-three-dice"} {
		for _, arm := range []string{"with_skill", "without_skill"} {
			write(t, filepath.Join(iter2, slug, arm, "prompt.txt"), "p")
		}
	}
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	m = newModel(s)
	m.width, m.height = 120, 34
	// An ungraded run leaves the loop closed, so the two views after the set are locked: the errand
	// is `g`, and it is asked for in the set's view — which is where the band says so.
	m.openQualitySkill("dummy")
	press(m, "1")
	press(m, "g")
	if m.mode != screenRunLive {
		t.Fatalf("g on an ungraded iteration = mode %v, want the grading run (status %q)", m.mode, stripMarks(m.status))
	}
	if m.run == nil || m.run.kind != runGrade || !strings.Contains(m.run.scope, "grading run · iteration 2") {
		t.Fatalf("run = %+v, want the judge on iteration 2", m.run)
	}
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	if !strings.Contains(body, "GRADING · ITERATION 2") {
		t.Fatalf("a grading run must say so while it runs:\n%s", body)
	}
}
