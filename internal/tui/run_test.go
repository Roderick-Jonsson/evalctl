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
		"100% with the skill", "75% without", "graded", "the dojo — the failed assertions"} {
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

// `g` on an iteration with no verdicts is still the way to finish it: a run made with --no-grade, by
// an older build, or one whose grade failed. On a graded iteration the same key is a re-grade, and it
// asks first — the verdicts on screen would be replaced by the ones it buys.
func TestGOnAGradedIterationAsksBeforeSpending(t *testing.T) {
	m, _ := qualityFixture(t)
	m.openQualitySkill("dummy")
	press(m, "2")
	press(m, "g")
	if m.mode != screenAsk || m.ask == nil {
		t.Fatalf("g on a graded iteration = mode %v, want the question", m.mode)
	}
	if !strings.Contains(stripMarks(strings.Join(m.ask.lines, "\n")), "judge call") {
		t.Fatalf("the question does not say what it spends: %+v", m.ask.lines)
	}
	press(m, "y")
	if m.mode != screenRunLive || m.run == nil || m.run.kind != runGrade {
		t.Fatalf("y = mode %v, want the grading run", m.mode)
	}
	if !strings.Contains(m.run.scope, "re-grading") {
		t.Fatalf("scope = %q, want the re-grade named", m.run.scope)
	}
}

// TestGradeFinishShowsTheRates: a finished grade is the two rates per case and the dojo as the way
// on, because the number is what the run was for and the failures are what a revision starts from.
func TestGradeFinishShowsTheRates(t *testing.T) {
	m, _ := qualityFixture(t)
	m.openQualitySkill("dummy")
	outputRun(m, runGrade, 1)
	finish(m, "done")
	m.reloadQuality()
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	for _, want := range []string{"GRADED · ITERATION 1", "100% with the skill", "graded",
		"the dojo — the failed assertions"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the finished grade is missing %q:\n%s", want, body)
		}
	}
}

// An iteration with no verdicts is not a re-grade: there is nothing to replace, so `g` finishes it
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
	m.openQualitySkill("dummy")
	press(m, "2")
	press(m, "g")
	if m.mode != screenRunLive {
		t.Fatalf("g on an ungraded iteration = mode %v, want the grading run", m.mode)
	}
	if m.run == nil || m.run.kind != runGrade || !strings.Contains(m.run.scope, "grading run · iteration 2") {
		t.Fatalf("run = %+v, want the judge on iteration 2", m.run)
	}
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	if !strings.Contains(body, "GRADING · ITERATION 2") {
		t.Fatalf("a grading run must say so while it runs:\n%s", body)
	}
}
