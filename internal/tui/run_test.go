package tui

import (
	"strings"
	"testing"
	"time"
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

// TestOutputRunFinishIsAHeadline: when the run ends the screen stops being a progress table. The
// outcome is the head, the numbers that matter are under it, and the run's own next move is the
// action — not a line of small print at the bottom of the window.
func TestOutputRunFinishIsAHeadline(t *testing.T) {
	m, _ := gateFixture(t, true)
	m.openQualitySkill("dummy")
	outputRun(m, runQuality, 1)
	finish(m, "done")
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	for _, want := range []string{"RUN FINISHED · ITERATION 1", "4 of 4 arms",
		"every case ran both arms", "grade it — a run is outputs"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the finished run is missing %q:\n%s", want, body)
		}
	}
	var keys []string
	for _, h := range m.hints() {
		keys = append(keys, h.key)
	}
	if !strings.Contains(strings.Join(keys, " "), "g") {
		t.Fatalf("hints = %v, want the grade offered on the run's own screen", keys)
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
