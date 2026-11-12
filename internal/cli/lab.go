package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"evalctl/internal/format"
	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// runLab prints where a skill stands in the description-optimization loop: the candidates, what
// each one measured, the set's readiness, and the guide's next move in one line. It reads only —
// every write in the loop (adopt, revise, measure, apply) is a decision, and the TUI is where a
// decision has the evidence beside it.
func runLab(args []string) int {
	fs, c := newFS("lab")
	skill := fs.String("skill", "", "the stage skill whose description is being optimized")
	lane := fs.String("lane", store.LaneTrigger, "which optimization the loop runs")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	if err := noPositional(fs); err != nil {
		return fail(err)
	}
	if *skill == "" {
		return fail(usageErr{fmt.Errorf("--skill is required: the loop runs on one skill")})
	}
	if c.s.Skill(*skill) == nil {
		return fail(fmt.Errorf("unknown skill %q (known: %s)", *skill, strings.Join(c.s.SkillNames(), ", ")))
	}
	if *lane == store.LaneOutput {
		return qualityLab(c, *skill, *asJSON)
	}
	if err := requireOneOf("lab", "lane", *lane); err != nil {
		return fail(err)
	}
	lab, err := runner.New(c.s).DescriptionLab(*skill)
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		if err := writeJSON(os.Stdout, lab); err != nil {
			return fail(err)
		}
		return 0
	}

	fmt.Printf("%s  %s\n", styleTitle.Render("description lab · "+lab.Skill),
		styleDim.Render(fmt.Sprintf("%d chars · %d iterations · %d run(s) per query · threshold %s",
			lab.FileChars, lab.Iterations, lab.Runs, strconv.FormatFloat(lab.Threshold, 'g', 3, 64))))

	if len(lab.Candidates) == 0 {
		fmt.Println(styleDim.Render("  no candidates yet — the loop starts by adopting the description SKILL.md carries"))
	}
	rows := make([][]string, 0, len(lab.Candidates))
	partial := false
	for _, cand := range lab.Candidates {
		state := ""
		if cand.InFile {
			state = "applied"
		}
		if cand.InFile {
			state += " file"
		}
		valid := rateCell(cand, store.SplitValidation, lab.ValidationRows)
		train := rateCell(cand, store.SplitTrain, lab.TrainRows)
		if strings.HasSuffix(valid, "~") || strings.HasSuffix(train, "~") {
			partial = true
		}
		rows = append(rows, []string{
			fmt.Sprintf("#%d", cand.ID), strings.TrimSpace(state),
			train, valid,
			fmt.Sprint(cand.Chars), shortStamp(cand.Created), truncate(cand.Change, 46),
		})
	}
	if len(rows) > 0 {
		table(os.Stdout, []string{"CAND", "", "TRAIN", "VALID", "CHARS", "WHEN", "CHANGE"}, rows, nil)
	}
	if partial {
		fmt.Println(styleDim.Render("  ~ = judged on part of that split's queries — the batch stopped, and a partial validation rate never ranks"))
	}

	if !lab.Ready {
		fmt.Println()
		for _, chk := range lab.Checks {
			if chk.Blocking && !chk.OK {
				fmt.Printf("%s %s\n", styleErr.Render("blocking"), chk.Key+": "+chk.Detail)
			}
		}
	}
	fmt.Println()
	fmt.Printf("%s %s\n", styleDim.Render("next:"), lab.Next.Line)
	if lab.Next.Detail != "" {
		fmt.Printf("      %s\n", styleDim.Render(lab.Next.Detail))
	}
	return 0
}

// rateCell is one candidate's split rate, or an em dash when that split never ran. It asks per
// split because it must: a candidate's newest run is whichever half of the loop ran last, and
// reading that one alone prints a dash in the column the other run filled.
//
// A rate over part of the split carries a trailing ~: 1/1 out of three validation queries is a batch
// that stopped, and printing it as a plain rate is how a stopped batch gets picked as the best one.
func rateCell(c runner.LabCandidate, split string, rows int) string {
	m := c.MeasureOn(split)
	if !judgedSplit(m, split) {
		return "—"
	}
	t := m.Train
	if split == store.SplitValidation {
		t = m.Validation
	}
	cell := fmt.Sprintf("%d/%d", t.Passed, t.Judged)
	if rows > 0 && t.Judged < rows {
		cell += "~"
	}
	return cell
}

// judgedSplit is the runner's per-split judgement, spelled here because the lab's rows carry the
// flag rather than the split name.
func judgedSplit(m *runner.LabMeasure, split string) bool {
	if m == nil {
		return false
	}
	if split == store.SplitValidation {
		return m.Validation.Judged > 0
	}
	return m.Train.Judged > 0
}

// shortStamp keeps the ledger's date readable in a table: the year is in the file, and two
// candidates minutes apart are the case the column exists for.
func shortStamp(s string) string {
	if len(s) >= 16 {
		return strings.ReplaceAll(s[:16], "T", " ")
	}
	return s
}

// qualityLab prints the output loop's state: each iteration's two rates, the delta, and the guide's
// next move. It reads only — grading, revising and running are decisions, and the TUI is where a
// decision has the evidence beside it.
func qualityLab(c *common, skill string, asJSON bool) int {
	lab, err := runner.New(c.s).QualityLab(skill)
	if err != nil {
		return fail(err)
	}
	if asJSON {
		if err := writeJSON(os.Stdout, lab); err != nil {
			return fail(err)
		}
		return 0
	}
	fmt.Printf("%s  %s\n", styleTitle.Render("quality lab · "+lab.Skill),
		styleDim.Render(fmt.Sprintf("%s · %d assertion(s) · %s",
			format.Plural(lab.Cases, "case"), lab.Assertions, format.Plural(len(lab.Iterations), "iteration"))))
	if len(lab.Iterations) == 0 {
		fmt.Println(styleDim.Render("  nothing measured yet — the loop starts by running the cases"))
	} else {
		var rows [][]string
		for _, it := range lab.Iterations {
			state := "complete"
			switch {
			case it.Complete && it.N == lab.Best:
				state = "complete · best"
			case !it.SetComplete:
				state = "subset of the set"
			case it.Partial:
				state = "partial"
			case !it.Complete:
				state = "no verdicts"
			}
			rows = append(rows, []string{
				fmt.Sprintf("#%d", it.N),
				qualityCell(it.With),
				qualityCell(it.Without),
				qualityDelta(it),
				fmt.Sprint(it.Cases),
				shortStamp(it.Started),
				state,
			})
		}
		table(os.Stdout, []string{"ITER", "WITH", "WITHOUT", "DELTA", "CASES", "WHEN", "STATE"}, rows, nil)
		if lab.Ungraded > 0 {
			fmt.Println(styleDim.Render("  ~ = some cases have no verdicts; a partial rate never ranks"))
		}
	}
	fmt.Println()
	for _, chk := range lab.Checks {
		if chk.Blocking && !chk.OK {
			fmt.Printf("%s %s: %s\n", styleErr.Render("blocking"), chk.Key, chk.Detail)
		}
	}
	fmt.Printf("%s %s\n", styleDim.Render("next:"), lab.Next.Line)
	if lab.Next.Detail != "" {
		fmt.Printf("      %s\n", styleDim.Render(lab.Next.Detail))
	}
	return 0
}

// qualityCell is one arm's rate as counts and a percentage: the percentage alone hides whether 100%
// is 1/1 or 30/30, and the lab exists to make that difference visible.
func qualityCell(a runner.QualityArm) string {
	if !a.Graded() {
		return "—"
	}
	cell := fmt.Sprintf("%.0f%% %d/%d", a.Rate*100, a.Passed, a.Judged)
	if a.Cases < a.CaseCount {
		cell += "~"
	}
	return cell
}

func qualityDelta(it runner.QualityIteration) string {
	if !it.With.Graded() || !it.Without.Graded() {
		return "—"
	}
	return fmt.Sprintf("%+.0f", it.Delta*100)
}
