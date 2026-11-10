package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/store"
)

const labBase = "Use this skill when an outline has to become slides, even if the user never says slides."

// labFixture is a tree with one skill, a folded description, and no iterations yet.
func labFixture(t *testing.T) (*Runner, string) {
	t.Helper()
	s, root := newTree(t)
	r := New(s)
	// The description the loop starts from, in the same folded style the writer produces.
	if err := s.SetSkillDescription("slide-builder", labBase); err != nil {
		t.Fatal(err)
	}
	return r, root
}

// writeIteration fabricates one finished trigger iteration for one skill: the description it ran
// and the score its own rows produced. It is the only way to test the loop without spending twenty
// minutes of agent time, and it writes exactly what a real run writes, in the same place.
func writeIteration(t *testing.T, root, skill, description string, iteration int, rows []ScoreRow) {
	t.Helper()
	dir := filepath.Join(root, skill+"-workspace", fmt.Sprintf("trigger-%d", iteration))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := fmt.Sprintf(`{"iteration":%d,"skill":%q,"lane":"trigger","started":"2026-10-30T10:0%d:00+0100","runs":3,"threshold":0.5,
  "provenance":{"descriptions":{%q:{"chars":%d,"description":%q}}}}`,
		iteration, skill, iteration, skill, len([]rune(description)), description)
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	score := TriggerScore{Threshold: 0.5, Runs: 3, Rows: rows, Skills: map[string]SkillScore{},
		Splits: map[string]Tally{}, Loop: tally(LoopRows(rows)), Overall: tally(rows)}
	blob, err := json.MarshalIndent(struct {
		Score TriggerScore `json:"score"`
	}{score}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "trigger.json"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }

// row builds one scored row for the fixtures above: runs always 3, judged, and the verdict asked for.
func scoredRow(id int, split string, should, hit bool) ScoreRow {
	rate := 0.0
	triggers := 0
	if hit {
		rate, triggers = 1, 3
	}
	passed := hit
	return ScoreRow{ID: id, Skill: "slide-builder", Split: split, ShouldTrigger: should,
		Runs: 3, ValidRuns: 3, Triggers: triggers, Rate: &rate, Passed: &passed}
}

// TestLabJoinsByDescriptionHash: two iterations, two wordings, and the candidate each wording
// becomes carries the measurements of the text it actually ran — the join is the hash, never the
// order.
func TestLabJoinsByDescriptionHash(t *testing.T) {
	runtime, root := labFixture(t)
	second := "Use this skill when an outline has to become slides — even when the user calls them a deck."
	writeIteration(t, root, "slide-builder", labBase, 1, []ScoreRow{
		scoredRow(1, SplitTrain, true, true),
		scoredRow(2, SplitTrain, false, true),
		scoredRow(3, SplitValidation, true, false),
		scoredRow(4, SplitValidation, false, true),
	})
	writeIteration(t, root, "slide-builder", second, 2, []ScoreRow{
		scoredRow(1, SplitTrain, true, true),
		scoredRow(2, SplitTrain, false, true),
		scoredRow(3, SplitValidation, true, true),
		scoredRow(4, SplitValidation, false, true),
	})

	lab, err := runtime.DescriptionLab("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	if len(lab.Candidates) != 2 {
		t.Fatalf("candidates = %+v", lab.Candidates)
	}
	// The id is the first iteration that measured the wording, so prose and the run directory agree.
	if lab.Candidates[0].ID != 1 || lab.Candidates[0].Iterations[0] != 1 {
		t.Fatalf("#1 = %+v", lab.Candidates[0])
	}
	if lab.Candidates[1].ID != 2 || lab.Candidates[1].Iterations[0] != 2 {
		t.Fatalf("#2 = %+v", lab.Candidates[1])
	}
	// The revision fixed the validation row it was meant to fix.
	if fixed := lab.Candidates[1].Measure().Fixed; len(fixed) != 1 || fixed[0] != 3 {
		t.Fatalf("fixed = %v, want the validation row 3", fixed)
	}
	if lab.Candidates[1].Parent != 1 || lab.Candidates[1].Change == "" {
		t.Fatalf("change cell = %q (parent %d)", lab.Candidates[1].Change, lab.Candidates[1].Parent)
	}
	if lab.Best == nil || lab.Best.ID != 2 {
		t.Fatalf("best = %+v, want the wording with the better validation rate", lab.Best)
	}
}

// TestLabAsksForTheFirstRun: with no trigger run on disk, the loop's first move is to measure the
// wording the router actually reads. There is nothing to adopt — the file already holds it.
func TestLabAsksForTheFirstRun(t *testing.T) {
	runtime, _ := labFixture(t)
	lab, err := runtime.DescriptionLab("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	if len(lab.Candidates) != 0 || lab.Next.Step != StepBaseline ||
		!strings.Contains(lab.Next.Line, "evaluate the description") {
		t.Fatalf("a skill with no loop should ask for its file's wording to be run: %+v", lab.Next)
	}
}

// TestLabAppliesTheBestNotTheLast: the guide's rule that costs people the most — the winner is the
// wording with the best validation rate, and applying it puts its text back into the file even
// though a later wording was the one measured last.
func TestLabAppliesTheBestNotTheLast(t *testing.T) {
	runtime, root := labFixture(t)
	writeIteration(t, root, "slide-builder", labBase, 1, []ScoreRow{
		scoredRow(1, SplitTrain, true, true),
		scoredRow(3, SplitValidation, true, true),
		scoredRow(4, SplitValidation, false, true),
	})
	// The second wording's train split is spotless, but it loses one validation row: it fitted the
	// train sample, which is exactly what the validation split exists to catch.
	wider := labBase + " Decks, talks and handouts all count, and the outline may be called anything at all."
	if err := runtime.Store.SetSkillDescription("slide-builder", wider); err != nil {
		t.Fatal(err)
	}
	writeIteration(t, root, "slide-builder", wider, 2, []ScoreRow{
		scoredRow(1, SplitTrain, true, true),
		scoredRow(3, SplitValidation, true, false),
		scoredRow(4, SplitValidation, false, true),
	})

	lab, err := runtime.DescriptionLab("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	if lab.Best == nil || lab.Best.ID != 1 {
		t.Fatalf("best = %+v, want #1 on its validation rate", lab.Best)
	}
	if lab.Drifted {
		t.Fatal("the file carries the newest wording; that is not drift")
	}
	if _, err := runtime.ApplyDescription("slide-builder", lab.Best.ID); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := runtime.Store.SkillDescription("slide-builder"); store.HashText(got) != store.HashText(labBase) {
		t.Fatalf("SKILL.md does not carry the applied wording:\n%s", got)
	}
	after, err := runtime.DescriptionLab("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	if after.Drifted {
		t.Fatal("the applied wording was measured; that is not drift")
	}
	if after.Next.Step != StepApply {
		t.Fatalf("next = %+v, want the holdout step after applying", after.Next)
	}
}

// TestLabSeesAnUnmeasuredEdit: editing SKILL.md by hand after a run is drift — the file's wording has
// no measurement of its own, so the rate beside it describes something else.
func TestLabSeesAnUnmeasuredEdit(t *testing.T) {
	runtime, root := labFixture(t)
	writeIteration(t, root, "slide-builder", labBase, 1, []ScoreRow{
		scoredRow(1, SplitTrain, true, true),
		scoredRow(3, SplitValidation, true, true),
		scoredRow(4, SplitValidation, false, true),
	})
	if err := runtime.Store.SetSkillDescription("slide-builder", labBase+" Talks count too."); err != nil {
		t.Fatal(err)
	}
	lab, err := runtime.DescriptionLab("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	if !lab.Drifted || lab.Next.Step != StepEvaluate {
		t.Fatalf("drift = %v, next = %+v, want the new wording measured", lab.Drifted, lab.Next)
	}
}

// TestHoldoutIsNeverPartOfTheLoop: the fresh queries are excluded from the loop's own run and from
// the numbers the loop ranks on, whatever else the file says.
func TestHoldoutIsNeverPartOfTheLoop(t *testing.T) {
	s, _ := newTree(t)
	if _, err := New(s).AddHoldoutQuery("slide-builder", "turn this into the deck for Tuesday", "fresh", true); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	r := New(s)
	loop, err := r.LoadTriggerQueries([]string{"slide-builder"}, nil, "all")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range loop {
		if c.Split == store.SplitHoldout {
			t.Fatalf("a holdout row was measured by the loop: %+v", c)
		}
	}
	if len(loop) != 4 {
		t.Fatalf("loop rows = %d, want the 4 train+validation rows", len(loop))
	}
	fresh, err := r.LoadTriggerQueries([]string{"slide-builder"}, nil, store.SplitHoldout)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 1 || fresh[0].ID != 5 {
		t.Fatalf("holdout rows = %+v", fresh)
	}
}

// TestHoldoutRateIsScoredApart: even when a run includes the holdout, the loop's tally leaves it out.
func TestHoldoutRateIsScoredApart(t *testing.T) {
	s, _ := newTree(t)
	cases, err := New(s).LoadTriggerQueries([]string{"slide-builder"}, nil, "all")
	if err != nil {
		t.Fatal(err)
	}
	holdout := TriggerCase{ID: 9, Skill: "slide-builder", Split: SplitHoldout, ShouldTrigger: true, Query: "fresh"}
	cases = append(cases, holdout)
	hit := TriggerRecord{ID: 9, Skill: "slide-builder", Split: SplitHoldout, ShouldTrigger: true,
		TargetLoaded: true, Evidence: true, Loaded: []string{"slide-builder"}, RootGuard: Guard{OK: true}}
	miss := hit
	miss.ID, miss.TargetLoaded, miss.Loaded = 1, false, []string{}
	score := New(s).ScoreTrigger(cases, map[int][]*TriggerRecord{1: {&miss}, 9: {&hit}}, 3, 0.5)
	skill := score.Skills["slide-builder"]
	if skill.Holdout.Judged != 1 || skill.Holdout.Passed != 1 {
		t.Fatalf("holdout tally = %+v", skill.Holdout)
	}
	if skill.Loop.Rows != skill.All.Rows-1 {
		t.Fatalf("loop rows = %d, all rows = %d", skill.Loop.Rows, skill.All.Rows)
	}
	for _, row := range score.Rows {
		if row.Split == SplitHoldout && row.Skill == "slide-builder" && row.Passed == nil {
			t.Fatal("the holdout row was scored")
		}
	}
}

// TestLabStopsAfterFiveFlatIterations: the guide's own stopping rule, said in numbers rather than
// advice — five revisions in, no train gain, and the wording is not what is failing.
func TestLabStopsAfterFiveFlatIterations(t *testing.T) {
	runtime, root := labFixture(t)
	for i := 1; i <= 5; i++ {
		text := labBase
		if i > 1 {
			text = fmt.Sprintf("%s revision %d", labBase, i)
		}
		writeIteration(t, root, "slide-builder", text, i, []ScoreRow{
			scoredRow(1, SplitTrain, true, false),
			scoredRow(3, SplitValidation, true, true),
		})
	}
	lab, err := runtime.DescriptionLab("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	if lab.Measured != 5 {
		t.Fatalf("measured = %d", lab.Measured)
	}
	if lab.Next.Key != "stuck" {
		t.Fatalf("next = %+v, want the guide's stop rule at five flat iterations", lab.Next)
	}
}

// TestLiftedWordsCatchesThePaste: a revision that copies a noun out of the query it is meant to fix
// is the overfitting the guide names by hand.
func TestLiftedWordsCatchesThePaste(t *testing.T) {
	failures := []ScoreRow{{ID: 7, Query: "pull the controller-runtime docs out of ~/src and write them up"}}
	parent := "Use this skill when sources have to become cited notes."
	draft := "Use this skill for sources, including controller-runtime docs, when notes must be cited."
	lifted := LiftedWords(draft, parent, failures)
	if len(lifted) != 2 || lifted[0] != "controller-runtime" || lifted[1] != "docs" {
		t.Fatalf("lifted = %v, want the pasted name and its noun", lifted)
	}
	if got := LiftedWords("Use this skill when unfamiliar documentation has to become cited notes.", parent, failures); len(got) != 0 {
		t.Fatalf("a category-level revision was flagged: %v", got)
	}
}

// TestLabJSONCarriesTheMeasurements: `lab --json` is the machine-readable view of the loop, so a
// candidate has to carry its text, its hash, its size and its measurements — and must not carry an
// `applied` flag it can no longer know.
func TestLabJSONCarriesTheMeasurements(t *testing.T) {
	runtime, root := labFixture(t)
	writeIteration(t, root, "slide-builder", labBase, 1, []ScoreRow{
		scoredRow(1, SplitTrain, true, true),
		scoredRow(3, SplitValidation, true, true),
		scoredRow(4, SplitValidation, false, true),
	})
	lab, err := runtime.DescriptionLab("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := json.Marshal(lab.Candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(blob, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "text", "sha", "chars", "measures", "in_file", "iterations"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("lab json is missing %q: %s", key, blob)
		}
	}
	if _, ok := got["applied"]; ok {
		t.Fatalf("lab json still claims an applied flag: %s", blob)
	}
	measures, _ := got["measures"].([]any)
	if len(measures) != 1 {
		t.Fatalf("measures = %v, want the one measured iteration", got["measures"])
	}
}

// TestApplySurvivesTheValidationRunBeingLast: which half of the loop ran last is the operator's
// order, not a property of the wording. Applying a wording whose newest measurement is its
// validation run used to fail with "measured on no train rows".
func TestApplySurvivesTheValidationRunBeingLast(t *testing.T) {
	runtime, root := labFixture(t)
	writeIteration(t, root, "slide-builder", labBase, 1, []ScoreRow{
		scoredRow(1, SplitTrain, true, true),
		scoredRow(2, SplitTrain, false, true),
	})
	writeIteration(t, root, "slide-builder", labBase, 2, []ScoreRow{
		scoredRow(3, SplitValidation, true, true),
		scoredRow(4, SplitValidation, false, false),
	})
	lab, err := runtime.DescriptionLab("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	// Two iterations, one wording: the halves of the loop merge on the text they measured.
	if len(lab.Candidates) != 1 || len(lab.Candidates[0].Measures) != 2 {
		t.Fatalf("candidates = %+v, want one wording with two measures", lab.Candidates)
	}
	if m := lab.Candidate(1).Measure(); m == nil || m.Validation.Judged == 0 {
		t.Fatalf("fixture is not the case: the newest measure must be the validation run: %+v", m)
	}
	note, err := runtime.ApplyDescription("slide-builder", 1)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !strings.Contains(note, "train 2/2") || !strings.Contains(note, "validation 1/2") {
		t.Fatalf("note = %q, want both halves reported from their own runs", note)
	}
	if !strings.Contains(note, "commit it: `description #1:") {
		t.Fatalf("note = %q, want the commit message git can keep", note)
	}
}

// TestLabRanksOnlyAFullValidation: 1/1 out of a two-query validation set is a stopped batch, and the
// lab's own best-of rule must not hand it the win it would get by having less to fail.
func TestLabRanksOnlyAFullValidation(t *testing.T) {
	runtime, root := labFixture(t)
	wider := labBase + " Decks and handouts count too."
	// The first wording was stopped after the first validation query and passed it; the second saw
	// both and failed one.
	writeIteration(t, root, "slide-builder", labBase, 1, []ScoreRow{
		scoredRow(1, SplitTrain, true, true),
		scoredRow(3, SplitValidation, true, true),
	})
	writeIteration(t, root, "slide-builder", wider, 2, []ScoreRow{
		scoredRow(1, SplitTrain, true, true),
		scoredRow(3, SplitValidation, true, true),
		scoredRow(4, SplitValidation, false, false),
	})
	lab, err := runtime.DescriptionLab("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	if !lab.Complete(lab.Candidate(2).MeasureOn(SplitValidation)) {
		t.Fatal("fixture: #2's validation judged both rows and must count as complete")
	}
	if lab.Complete(lab.Candidate(1).MeasureOn(SplitValidation)) {
		t.Fatal("fixture: #1's validation judged one of two rows and must not count as complete")
	}
	if lab.Best == nil || lab.Best.ID != 2 {
		t.Fatalf("best = %+v, want #2: a partial rate is not a ranking", lab.Best)
	}
	if strings.Contains(lab.Next.Line, "best validation 1/1") {
		t.Fatalf("next = %q, must not name a partial rate as the best", lab.Next.Line)
	}
}

// TestDraftIsStagedAndBecomesTheFile: the ledger's unmeasured candidate is now a file in the
// workspace — the loop's staging slot — and measuring it writes it into SKILL.md and drops the draft.
func TestDraftIsStagedAndBecomesTheFile(t *testing.T) {
	runtime, root := labFixture(t)
	writeIteration(t, root, "slide-builder", labBase, 1, []ScoreRow{
		scoredRow(1, SplitTrain, true, false),
		scoredRow(3, SplitValidation, true, true),
	})
	draft, err := runtime.SaveDescriptionCandidate("slide-builder", labBase+" Talks count too.")
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	lab, err := runtime.DescriptionLab("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	pending := lab.latest()
	if pending == nil || !pending.Pending || pending.ID != 2 || pending.Text != draft.Text {
		t.Fatalf("pending candidate = %+v", pending)
	}
	if lab.Next.Step != StepEvaluate || !strings.Contains(lab.Next.Line, "evaluate #2") {
		t.Fatalf("next = %+v, want the draft measured next", lab.Next)
	}
	if _, err := runtime.PrepareMeasure("slide-builder", pending.ID); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if got := runtime.Store.SkillDescription("slide-builder"); store.HashText(got) != store.HashText(draft.Text) {
		t.Fatalf("SKILL.md does not carry the draft:\n%s", got)
	}
	if _, ok := runtime.PendingDescription("slide-builder"); ok {
		t.Fatal("the draft survived becoming the file")
	}
	// Staging the wording the file already carries is refused: it cannot be measured apart from it.
	if _, err := runtime.SaveDescriptionCandidate("slide-builder", draft.Text); err == nil {
		t.Fatal("a draft equal to SKILL.md was accepted")
	}
}
