package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

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
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	if *skill == "" {
		return fail(usageErr{fmt.Errorf("--skill is required: the loop runs on one skill's description")})
	}
	if c.s.Skill(*skill) == nil {
		return fail(fmt.Errorf("unknown skill %q (known: %s)", *skill, strings.Join(c.s.SkillNames(), ", ")))
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
	for _, cand := range lab.Candidates {
		state := ""
		if cand.Applied {
			state = "applied"
		}
		if cand.InFile {
			state += " file"
		}
		rows = append(rows, []string{
			fmt.Sprintf("#%d", cand.ID), strings.TrimSpace(state),
			rateCell(cand, store.SplitTrain), rateCell(cand, store.SplitValidation),
			fmt.Sprint(cand.Chars), shortStamp(cand.Created), truncate(cand.Note, 46),
		})
	}
	if len(rows) > 0 {
		table(os.Stdout, []string{"CAND", "", "TRAIN", "VALID", "CHARS", "WHEN", "CHANGE"}, rows, nil)
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

// rateCell is one candidate's split rate, or an em dash when that split never ran.
func rateCell(c runner.LabCandidate, split string) string {
	m := c.Measure()
	if m == nil {
		return "—"
	}
	t := m.Train
	if split == store.SplitValidation {
		t = m.Validation
	}
	if t.Judged == 0 {
		return "—"
	}
	return fmt.Sprintf("%d/%d", t.Passed, t.Judged)
}

// shortStamp keeps the ledger's date readable in a table: the year is in the file, and two
// candidates minutes apart are the case the column exists for.
func shortStamp(s string) string {
	if len(s) >= 16 {
		return strings.ReplaceAll(s[:16], "T", " ")
	}
	return s
}
