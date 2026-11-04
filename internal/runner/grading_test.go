package runner

import (
	"strings"
	"testing"
)

func TestParseJudgeReplyKeepsTheDeclaredOrder(t *testing.T) {
	reply := `Here is my grading:
{"assertion_results":[
 {"text":"the total adds up","passed":true,"evidence":"reply says 6+9=15"},
 {"text":"three faces","passed":true,"evidence":"reply lists 6, 9, 1"}
]}`
	got, err := parseJudgeReply(reply, []string{"three faces", "the total adds up"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Text != "three faces" || got[1].Text != "the total adds up" {
		t.Fatalf("results were not put in the declared order: %+v", got)
	}
	if !got[0].Passed || !got[1].Passed {
		t.Fatalf("a pass was lost: %+v", got)
	}
}

func TestParseJudgeReplyFailsAMissingVerdict(t *testing.T) {
	reply := `{"assertion_results":[{"text":"three faces","passed":true,"evidence":"listed 6, 9, 1"}]}`
	got, err := parseJudgeReply(reply, []string{"three faces", "the total adds up"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want one verdict per assertion, got %+v", got)
	}
	if got[1].Passed || !strings.Contains(got[1].Evidence, "no verdict") {
		t.Fatalf("a missing verdict must fail with the reason: %+v", got[1])
	}
}

func TestParseJudgeReplyWithoutJSONIsAnError(t *testing.T) {
	if _, err := parseJudgeReply("Looks good to me!", []string{"a"}); err == nil {
		t.Fatal("prose without a JSON object must not grade an arm")
	}
}

// A judge that answers its object and then appends a stray fragment — the shape that aborted a whole
// iteration on 2026-11-04 — is one object followed by junk. The first complete object is the answer;
// slicing to the last brace glues the two together and nothing parses.
func TestParseJudgeReplyTakesTheFirstCompleteObject(t *testing.T) {
	reply := `{"assertion_results":[{"text":"three faces","passed":true,"evidence":"3, 17, 9"}]}` +
		"\n" + `,"summary":{"passed":1,"failed":0,"total":1,"pass_rate":1}}`
	got, err := parseJudgeReply(reply, []string{"three faces"})
	if err != nil {
		t.Fatalf("a trailing fragment must not fail the parse: %v", err)
	}
	if len(got) != 1 || !got[0].Passed {
		t.Fatalf("verdicts = %+v, want the one the object carries", got)
	}
}

func TestAPassWithoutEvidenceIsNotAPass(t *testing.T) {
	got := finishResult("a", AssertionResult{Text: "a", Passed: true, Evidence: "  "})
	if got.Passed {
		t.Fatalf("a pass with no evidence was kept: %+v", got)
	}
	if !strings.Contains(got.Evidence, "without evidence") {
		t.Fatalf("the downgrade must say why: %+v", got)
	}
}

func TestBenchmarkAggregatesPassRates(t *testing.T) {
	runs := []CollectedRun{
		{Slug: "c1", Assertions: []string{"a", "b"}, Arms: map[string]ArmResult{
			ArmWith:    {Timing: Timing{ExitCode: ptr(0), DurationMS: 1000}, Grading: graded(2, 2)},
			ArmWithout: {Timing: Timing{ExitCode: ptr(0), DurationMS: 800}, Grading: graded(1, 2)},
		}},
		{Slug: "c2", Assertions: []string{"c", "d"}, Arms: map[string]ArmResult{
			ArmWith:    {Timing: Timing{ExitCode: ptr(0), DurationMS: 2000}, Grading: graded(2, 2)},
			ArmWithout: {Timing: Timing{ExitCode: ptr(0), DurationMS: 1200}, Grading: graded(2, 2)},
		}},
	}
	bench := BenchmarkOf(runs, 1)
	with := bench.RunSummary[ArmWith]
	without := bench.RunSummary[ArmWithout]
	if with.PassRate == nil || with.PassRate.Mean != 1 {
		t.Fatalf("with-skill rate = %+v, want 1.0", with.PassRate)
	}
	if without.PassRate == nil || without.PassRate.Mean != 0.75 {
		t.Fatalf("baseline rate = %+v, want 0.75 over the two cases", without.PassRate)
	}
	if with.Graded == nil || with.Graded.Passed != 4 || with.Graded.Judged != 4 {
		t.Fatalf("counts = %+v, want 4/4", with.Graded)
	}
	if bench.Delta == nil || bench.Delta.PassRate == nil || *bench.Delta.PassRate != 0.25 {
		t.Fatalf("delta = %+v, want +0.25", bench.Delta)
	}
	if !strings.Contains(bench.Assertions, "4 assertion") {
		t.Fatalf("the benchmark does not name the assertion count: %q", bench.Assertions)
	}
	// An ungraded iteration has no rate at all — not a zero.
	ungraded := BenchmarkOf([]CollectedRun{{Slug: "c1", Assertions: []string{"a"}, Arms: map[string]ArmResult{
		ArmWith: {Timing: Timing{ExitCode: ptr(0)}}}}}, 2)
	if ungraded.RunSummary[ArmWith].PassRate != nil {
		t.Fatalf("an ungraded arm grew a pass rate: %+v", ungraded.RunSummary[ArmWith].PassRate)
	}
	if !strings.Contains(ungraded.Assertions, "not graded") {
		t.Fatalf("the benchmark does not say it is ungraded: %q", ungraded.Assertions)
	}
}

func TestPatternsNameWhatTheRateHides(t *testing.T) {
	runs := []CollectedRun{{Slug: "c1", Arms: map[string]ArmResult{
		ArmWith: {Grading: Grading{Summary: &GradeSummary{Total: 4, Passed: 3},
			AssertionResults: []AssertionResult{
				{Text: "dead", Passed: true}, {Text: "broken", Passed: false},
				{Text: "value", Passed: true}, {Text: "regressed", Passed: false}}}},
		ArmWithout: {Grading: Grading{Summary: &GradeSummary{Total: 4, Passed: 2},
			AssertionResults: []AssertionResult{
				{Text: "dead", Passed: true}, {Text: "broken", Passed: false},
				{Text: "value", Passed: false}, {Text: "regressed", Passed: true}}}},
	}}}
	lines := strings.Join(patternLines(runs), "\n")
	for _, want := range []string{"no signal", "dead", "broken or too hard", "broken",
		"the skill's contribution", "value", "regression", "regressed"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("patterns are missing %q:\n%s", want, lines)
		}
	}
}

func graded(passed, total int) Grading {
	results := make([]AssertionResult, 0, total)
	for i := 0; i < total; i++ {
		results = append(results, AssertionResult{Text: "a", Passed: i < passed, Evidence: "seen"})
	}
	rate := float64(passed) / float64(total)
	return Grading{AssertionResults: results, Summary: &GradeSummary{Passed: passed,
		Failed: total - passed, Total: total, PassRate: rate}}
}
