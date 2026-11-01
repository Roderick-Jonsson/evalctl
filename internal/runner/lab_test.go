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

// labFixture is a tree with one skill, a folded description, and no iterations yet.
func labFixture(t *testing.T) (*Runner, string) {
	t.Helper()
	s, root := newTree(t)
	r := New(s)
	// The description the loop starts from, in the same folded style the writer produces.
	if err := s.SetSkillDescription("slide-builder",
		"Use this skill when an outline has to become slides, even if the user never says slides."); err != nil {
		t.Fatal(err)
	}
	return r, root
}

// writeIteration fabricates one finished trigger iteration: the description it ran, and its score.
// It is the only way to test the loop without spending twenty minutes of agent time, and it exercises
// the same join the lab uses on real runs.
func writeIteration(t *testing.T, root, skill, description string, iteration int, rows []ScoreRow) {
	t.Helper()
	dir := filepath.Join(root, "trigger-workspace", fmt.Sprintf("trigger-%d", iteration))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := fmt.Sprintf(`{"iteration":%d,"lane":"trigger","started":"2026-10-30T10:0%d:00+0100","runs":3,"threshold":0.5,
  "provenance":{"descriptions":{%q:{"chars":%d,"description":%q}}}}`,
		iteration, iteration, skill, len([]rune(description)), description)
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

// TestLabJoinsByDescriptionHash: two iterations, two descriptions, and the ledger's candidates get
// the measurements of the text they actually carry — the join is the hash, never the order.
func TestLabJoinsByDescriptionHash(t *testing.T) {
	runtime, root := labFixture(t)
	if _, err := runtime.AdoptFileDescription("slide-builder"); err != nil {
		t.Fatal(err)
	}
	first := runtime.Store.SkillDescription("slide-builder")
	second := "Use this skill when an outline has to become slides — even when the user calls them a deck."
	cand, err := runtime.SaveDescriptionCandidate("slide-builder", second, "broadened to cover 'deck'", 1)
	if err != nil {
		t.Fatal(err)
	}
	pass := func(id int, split string, should, hit bool) ScoreRow {
		passed := hit
		rate := 1.0
		if !hit {
			rate = 0
		}
		return ScoreRow{ID: id, Skill: "slide-builder", Split: split, ShouldTrigger: should,
			Runs: 3, ValidRuns: 3, Triggers: map[bool]int{true: 3, false: 0}[hit], Rate: &rate, Passed: &passed}
	}
	writeIteration(t, root, "slide-builder", first, 1, []ScoreRow{
		pass(1, SplitTrain, true, true),
		pass(2, SplitTrain, false, true),
		pass(3, SplitValidation, true, false),
		pass(4, SplitValidation, false, true),
	})
	writeIteration(t, root, "slide-builder", second, 2, []ScoreRow{
		pass(1, SplitTrain, true, true),
		pass(2, SplitTrain, false, true),
		pass(3, SplitValidation, true, true),
		pass(4, SplitValidation, false, true),
	})

	lab, err := runtime.DescriptionLab("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	if len(lab.Candidates) != 2 {
		t.Fatalf("candidates = %d", len(lab.Candidates))
	}
	if len(lab.Candidates[0].Measures) != 1 || lab.Candidates[0].Measure().Iteration != 1 {
		t.Fatalf("#1 measures = %+v", lab.Candidates[0].Measures)
	}
	if len(lab.Candidates[1].Measures) != 1 || lab.Candidates[1].Measure().Iteration != 2 {
		t.Fatalf("#2 measures = %+v", lab.Candidates[1].Measures)
	}
	// The revision fixed the validation row it was meant to fix, and the ledger knows it.
	if fixed := lab.Candidates[1].Measure().Fixed; len(fixed) != 1 || fixed[0] != 3 {
		t.Fatalf("fixed = %v, want the validation row 3", fixed)
	}
	if lab.Best == nil || lab.Best.ID != cand.ID {
		t.Fatalf("best = %+v, want the candidate with the better validation rate", lab.Best)
	}
	if lab.Next.Key != "best" {
		t.Fatalf("next = %+v, want apply once the train split is clean", lab.Next)
	}
}

// TestLabAppliesTheBestNotTheLast: the guide's rule that costs people the most — the winner is the
// candidate with the best validation rate, and applying it puts its wording back into the file even
// though a later candidate was the one measured last.
func TestLabAppliesTheBestNotTheLast(t *testing.T) {
	runtime, root := labFixture(t)
	lab, err := runtime.DescriptionLab("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	if len(lab.Candidates) != 0 || lab.Next.Step != StepBaseline || !strings.Contains(lab.Next.Line, "adopt") {
		t.Fatalf("a skill with no loop should ask for its file's wording as #1: %+v", lab.Next)
	}
	first, err := runtime.AdoptFileDescription("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	base := runtime.Store.SkillDescription("slide-builder")

	// #1 measured: train clean, validation clean. A validation run judges the whole validation set —
	// a rate over one of its two rows is a stopped batch, and the lab refuses to rank it.
	writeIteration(t, root, "slide-builder", base, 1, []ScoreRow{
		{ID: 1, Skill: "slide-builder", Split: SplitTrain, ShouldTrigger: true, Runs: 3, ValidRuns: 3, Triggers: 3, Rate: ptr(1.0), Passed: ptr(true)},
		{ID: 3, Skill: "slide-builder", Split: SplitValidation, ShouldTrigger: true, Runs: 3, ValidRuns: 3, Triggers: 3, Rate: ptr(1.0), Passed: ptr(true)},
		{ID: 4, Skill: "slide-builder", Split: SplitValidation, ShouldTrigger: false, Runs: 3, ValidRuns: 3, Triggers: 0, Rate: ptr(1.0), Passed: ptr(true)},
	})
	// #2 is longer and its train split is spotless, but it loses one validation row: it fitted the
	// train sample, which is exactly what the validation split exists to catch.
	second, err := runtime.SaveDescriptionCandidate("slide-builder",
		base+" Decks, talks and handouts all count, and the outline may be called anything at all.", "wider", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.PrepareMeasure("slide-builder", second.ID); err != nil {
		t.Fatal(err)
	}
	writeIteration(t, root, "slide-builder", runtime.Store.SkillDescription("slide-builder"), 2, []ScoreRow{
		{ID: 1, Skill: "slide-builder", Split: SplitTrain, ShouldTrigger: true, Runs: 3, ValidRuns: 3, Triggers: 3, Rate: ptr(1.0), Passed: ptr(true)},
		{ID: 3, Skill: "slide-builder", Split: SplitValidation, ShouldTrigger: true, Runs: 3, ValidRuns: 3, Triggers: 1, Rate: ptr(0.33), Passed: ptr(false)},
		{ID: 4, Skill: "slide-builder", Split: SplitValidation, ShouldTrigger: false, Runs: 3, ValidRuns: 3, Triggers: 0, Rate: ptr(1.0), Passed: ptr(true)},
	})

	lab, err = runtime.DescriptionLab("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	if lab.Best == nil || lab.Best.ID != first.ID {
		t.Fatalf("best = %+v, want #%d on its validation rate", lab.Best, first.ID)
	}
	if lab.Next.Key != "best" {
		t.Fatalf("next = %+v, want apply once train is clean", lab.Next)
	}
	if _, err := runtime.ApplyDescription("slide-builder", lab.Best.ID); err != nil {
		t.Fatalf("apply: %v", err)
	}
	ledger, _, err := runtime.Store.LoadDescriptionLedger("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Applied == nil || ledger.Applied.Candidate != first.ID {
		t.Fatalf("applied = %+v", ledger.Applied)
	}
	if got := runtime.Store.SkillDescription("slide-builder"); store.HashText(got) != store.HashText(base) {
		t.Fatalf("SKILL.md does not carry the applied wording:\n%s", got)
	}
	after, err := runtime.DescriptionLab("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	if after.Drifted {
		t.Fatal("the applied description is in the file; that is not drift")
	}
	if after.Next.Step != StepApply {
		t.Fatalf("next = %+v, want the holdout step after applying", after.Next)
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
	base := runtime.Store.SkillDescription("slide-builder")
	parent := 0
	for i := 1; i <= 5; i++ {
		text := fmt.Sprintf("%s revision %d", base, i)
		if i == 1 {
			if _, err := runtime.AdoptFileDescription("slide-builder"); err != nil {
				t.Fatal(err)
			}
			text = base
		} else {
			c, err := runtime.SaveDescriptionCandidate("slide-builder", text, "another try", parent)
			if err != nil {
				t.Fatal(err)
			}
			parent = c.ID
		}
		writeIteration(t, root, "slide-builder", text, i, []ScoreRow{
			{ID: 1, Skill: "slide-builder", Split: SplitTrain, ShouldTrigger: true, Runs: 3, ValidRuns: 3, Triggers: 1, Rate: ptr(0.33), Passed: ptr(false)},
			{ID: 3, Skill: "slide-builder", Split: SplitValidation, ShouldTrigger: true, Runs: 3, ValidRuns: 3, Triggers: 3, Rate: ptr(1.0), Passed: ptr(true)},
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

func ptr[T any](v T) *T { return &v }

// row builds one scored row for the fixtures above: runs always 3, judged, and the verdict asked for.
func scoredRow(id int, split string, should, hit bool) ScoreRow {
	rate := 0.0
	if hit {
		rate = 1
	}
	triggers := 0
	if hit {
		triggers = 3
	}
	passed := hit
	return ScoreRow{ID: id, Skill: "slide-builder", Split: split, ShouldTrigger: should,
		Runs: 3, ValidRuns: 3, Triggers: triggers, Rate: &rate, Passed: &passed}
}

// TestLabJSONCarriesTheMeasurements: `lab --json` is the machine-readable view of the loop, and the
// ledger row alone is not that view. DescriptionCandidate's MarshalJSON used to be promoted onto
// LabCandidate and win, so the output lost sha, chars, measures, applied and in_file — every field
// the JSON exists for.
func TestLabJSONCarriesTheMeasurements(t *testing.T) {
	runtime, root := labFixture(t)
	first, err := runtime.AdoptFileDescription("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	writeIteration(t, root, "slide-builder", first.Text, 1, []ScoreRow{
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
	for _, key := range []string{"id", "text", "sha", "chars", "measures", "applied", "in_file"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("lab json is missing %q: %s", key, blob)
		}
	}
	measures, _ := got["measures"].([]any)
	if len(measures) != 1 {
		t.Fatalf("measures = %v, want the one measured iteration", got["measures"])
	}
}

// TestApplySurvivesTheValidationRunBeingLast: which half of the loop ran last is the operator's
// order, not a property of the candidate. Applying a wording whose newest measurement is its
// validation run used to fail with "measured on no train rows", and when it did not, it recorded the
// validation iteration and printed validation 0/0.
func TestApplySurvivesTheValidationRunBeingLast(t *testing.T) {
	runtime, root := labFixture(t)
	first, err := runtime.AdoptFileDescription("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	writeIteration(t, root, "slide-builder", first.Text, 1, []ScoreRow{
		scoredRow(1, SplitTrain, true, true),
		scoredRow(2, SplitTrain, false, true),
	})
	writeIteration(t, root, "slide-builder", first.Text, 2, []ScoreRow{
		scoredRow(3, SplitValidation, true, true),
		scoredRow(4, SplitValidation, false, false),
	})
	lab, err := runtime.DescriptionLab("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	if m := lab.Candidate(first.ID).Measure(); m == nil || m.Validation.Judged == 0 {
		t.Fatalf("fixture is not the case: the newest measure must be the validation run: %+v", m)
	}
	note, err := runtime.ApplyDescription("slide-builder", first.ID)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !strings.Contains(note, "train 2/2") || !strings.Contains(note, "validation 1/2") {
		t.Fatalf("note = %q, want both halves reported from their own runs", note)
	}
	ledger, _, err := runtime.Store.LoadDescriptionLedger("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Applied == nil || ledger.Applied.Iteration != 1 {
		t.Fatalf("applied = %+v, want the train iteration 1", ledger.Applied)
	}
}

// TestLabRanksOnlyAFullValidation: 1/1 out of a two-query validation set is a stopped batch, and the
// lab's own best-of rule must not hand it the win it would get by having less to fail.
func TestLabRanksOnlyAFullValidation(t *testing.T) {
	runtime, root := labFixture(t)
	first, err := runtime.AdoptFileDescription("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.SaveDescriptionCandidate("slide-builder",
		first.Text+" Decks and handouts count too.", "wider", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	// #1 was stopped after the first validation query and passed it; #2 saw both and failed one.
	writeIteration(t, root, "slide-builder", first.Text, 1, []ScoreRow{
		scoredRow(1, SplitTrain, true, true),
		scoredRow(3, SplitValidation, true, true),
	})
	writeIteration(t, root, "slide-builder", second.Text, 2, []ScoreRow{
		scoredRow(1, SplitTrain, true, true),
		scoredRow(3, SplitValidation, true, true),
		scoredRow(4, SplitValidation, false, false),
	})
	lab, err := runtime.DescriptionLab("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	if !lab.Complete(lab.Candidate(second.ID).MeasureOn(SplitValidation)) {
		t.Fatal("fixture: #2's validation judged both rows and must count as complete")
	}
	if lab.Complete(lab.Candidate(first.ID).MeasureOn(SplitValidation)) {
		t.Fatal("fixture: #1's validation judged one of two rows and must not count as complete")
	}
	if lab.Best == nil || lab.Best.ID != second.ID {
		t.Fatalf("best = %+v, want #%d: a partial rate is not a ranking", lab.Best, second.ID)
	}
	if strings.Contains(lab.Next.Line, "best validation 1/1") {
		t.Fatalf("next = %q, must not name a partial rate as the best", lab.Next.Line)
	}
}
