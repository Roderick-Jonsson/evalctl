package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"evalctl/internal/store"
)

// ---------------------------------------------------------------- validate

func runValidate(args []string) int {
	fs, c := newFS("validate")
	strict := fs.Bool("strict", false, "also fail on warnings")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	issues := c.s.Validate()
	errs, warns := 0, 0
	for _, i := range issues {
		if i.Severity == store.SeverityError {
			errs++
		} else {
			warns++
		}
	}
	if *asJSON {
		type row struct {
			Severity string `json:"severity"`
			Lane     string `json:"lane"`
			Skill    string `json:"skill,omitempty"`
			ID       int    `json:"id,omitempty"`
			Slug     string `json:"slug,omitempty"`
			Message  string `json:"message"`
		}
		rows := make([]row, 0, len(issues))
		for _, i := range issues {
			rows = append(rows, row{i.Severity.String(), i.Lane, i.Skill, i.ID, i.Slug, i.Msg})
		}
		if err := writeJSON(os.Stdout, map[string]any{
			"root": c.s.Root, "errors": errs, "warnings": warns, "issues": rows,
		}); err != nil {
			return fail(err)
		}
	} else {
		fmt.Println(styleDim.Render("root " + c.s.Root))
		for _, i := range issues {
			line := i.String()
			if i.Severity == store.SeverityError {
				fmt.Println(styleErr.Render(line))
			} else {
				fmt.Println(styleWarn.Render(line))
			}
		}
		if len(issues) == 0 {
			fmt.Println(styleYes.Render("clean: both lanes match evals/run.py's rules"))
		} else {
			fmt.Println()
			fmt.Printf("%d error(s), %d warning(s)\n", errs, warns)
		}
	}
	if errs > 0 || (*strict && warns > 0) {
		return 2
	}
	return 0
}

// ---------------------------------------------------------------- dupe finder

func runDupes(args []string) int {
	fs, c := newFS("dupes")
	lane := fs.String("lane", "both", "trigger, output or both")
	threshold := fs.Float64("threshold", store.DefaultDupeThreshold, "word-overlap above which two rows are the same test")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	if *threshold <= 0 || *threshold > 1 {
		return fail(fmt.Errorf("--threshold must be between 0 and 1"))
	}
	lanes := []string{store.LaneTrigger, store.LaneOutput}
	switch *lane {
	case "trigger":
		lanes = lanes[:1]
	case "output":
		lanes = lanes[1:]
	case "both":
	default:
		return fail(fmt.Errorf("--lane must be trigger, output or both"))
	}

	if *asJSON {
		out := map[string][][]store.Entry{}
		for _, l := range lanes {
			out[l] = c.s.DupeGroups(l, *threshold)
		}
		if err := writeJSON(os.Stdout, out); err != nil {
			return fail(err)
		}
		return 0
	}

	total := 0
	for _, l := range lanes {
		groups := c.s.DupeGroups(l, *threshold)
		if len(groups) == 0 {
			fmt.Println(styleDim.Render(fmt.Sprintf("%s: no near-duplicates at %.2f", l, *threshold)))
			continue
		}
		rows := 0
		for _, g := range groups {
			rows += len(g)
		}
		total += rows
		fmt.Println(styleTitle.Render(fmt.Sprintf("%s: %d group(s), %d row(s)", l, len(groups), rows)))
		for gi, g := range groups {
			fmt.Printf("  group %d — %d rows, keep one:\n", gi+1, len(g))
			for _, e := range g {
				label := ""
				if e.Label != "" {
					label = fmt.Sprintf(" %-3s %-10s", e.Label, e.Split)
				}
				name := e.Slug
				if name == "" {
					name = fmt.Sprintf("#%d", e.ID)
				}
				fmt.Printf("    %-26s %-22s%s  %s\n", e.Skill, name, label, clipText(e.Text, 62))
				fmt.Println(styleDim.Render(fmt.Sprintf("      ref: %s", refString(e))))
			}
		}
		fmt.Println()
	}
	if total > 0 {
		fmt.Println(styleDim.Render("trim interactively: evalctl   ·   or: evalctl ls --dupes   ·   rm --dupes --skill X --lane trigger"))
	}
	return 0
}

func refString(e store.Entry) string {
	if e.Lane == store.LaneTrigger {
		return fmt.Sprintf("t%d", e.ID)
	}
	return fmt.Sprintf("o:%s:%d", e.Skill, e.ID)
}

// ---------------------------------------------------------------- stats

func runStats(args []string) int {
	fs, c := newFS("stats")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	trigger := c.s.Stats(store.LaneTrigger)
	output := c.s.Stats(store.LaneOutput)
	if *asJSON {
		if err := writeJSON(os.Stdout, map[string]any{"trigger": trigger, "output": output}); err != nil {
			return fail(err)
		}
		return 0
	}

	fmt.Println(styleTitle.Render("trigger queries"))
	fmt.Printf("  %d rows · %d should-trigger / %d should-not-trigger · %d train / %d validation\n",
		trigger.Total, trigger.ByLabel["yes"], trigger.ByLabel["no"], trigger.BySplit["train"], trigger.BySplit["validation"])
	printCounts("  per skill", trigger.BySkill)
	fmt.Printf("  %d near-duplicate group(s) covering %d row(s)\n", trigger.Clusters, trigger.DupeRows)

	fmt.Println()
	fmt.Println(styleTitle.Render("output cases"))
	fmt.Printf("  %d cases in %d skill(s)\n", output.Total, len(output.BySkill))
	printCounts("  per skill", output.BySkill)
	fmt.Printf("  %d near-duplicate group(s) covering %d row(s)\n", output.Clusters, output.DupeRows)
	return 0
}

func printCounts(title string, m map[string]int) {
	if len(m) == 0 {
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	fmt.Println(title + ": " + strings.Join(parts, " · "))
}
