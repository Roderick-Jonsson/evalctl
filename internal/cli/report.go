package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"evalctl/internal/format"
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
	if err := noPositional(fs); err != nil {
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
			fmt.Println(styleYes.Render("clean: both lanes match the runner's rules"))
		} else {
			fmt.Println()
			fmt.Printf("%s, %s\n", format.Plural(errs, "error"), format.Plural(warns, "warning"))
		}
	}
	// 3, not 2: a tree that fails its own rules is a different answer from a command that could not
	// be parsed, and a script that gates on `validate` should be able to tell the two apart.
	if errs > 0 || (*strict && warns > 0) {
		return 3
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
	if err := noPositional(fs); err != nil {
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
		fmt.Println(styleDim.Render("trim: evalctl rm --dupes --lane <lane>   (add --dry-run to see the list first)"))
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
	if err := noPositional(fs); err != nil {
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

	// Two aligned blocks instead of two prose sentences: the row a reader wants (how many, split
	// how, duplicated where) is a column, and the per-skill breakdown is its own table.
	printLaneStats("trigger queries", trigger, true)
	fmt.Println()
	printLaneStats("output cases", output, false)
	return 0
}

// printLaneStats prints the counts that apply to a lane. label and split are trigger-only, so the
// output lane shows two columns and not four zeroes: a column of zeroes reads as a fact about the
// set when it is really a field the lane does not have.
func printLaneStats(name string, st store.Stats, trigger bool) {
	fmt.Println(styleTitle.Render(fmt.Sprintf("%s · %s", name, format.Plural(st.Total, "row"))))
	if trigger {
		table(os.Stdout, []string{"SHOULD", "NOT", "TRAIN", "VALID", "DUPES", "DUP ROWS"},
			[][]string{{
				fmt.Sprint(st.ByLabel["yes"]), fmt.Sprint(st.ByLabel["no"]),
				fmt.Sprint(st.BySplit["train"]), fmt.Sprint(st.BySplit["validation"]),
				fmt.Sprint(st.Clusters), fmt.Sprint(st.DupeRows),
			}}, nil)
	} else {
		table(os.Stdout, []string{"DUPES", "DUP ROWS"},
			[][]string{{fmt.Sprint(st.Clusters), fmt.Sprint(st.DupeRows)}}, nil)
	}
	if len(st.BySkill) > 0 {
		fmt.Println()
		rows := make([][]string, 0, len(st.BySkill))
		for _, k := range sortedKeys(st.BySkill) {
			rows = append(rows, []string{k, fmt.Sprint(st.BySkill[k])})
		}
		table(os.Stdout, []string{"SKILL", "ROWS"}, rows, nil)
	}
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
