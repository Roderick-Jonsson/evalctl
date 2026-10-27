package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"evalctl/internal/store"
)

func runList(args []string) int {
	fs, c := newFS("ls")
	lane := fs.String("lane", "both", "trigger, output or both")
	skill := fs.String("skill", "", "only this stage skill")
	label := fs.String("label", "", "trigger only: yes (should trigger) or no")
	split := fs.String("split", "", "trigger only: train or validation")
	search := fs.String("search", "", "case-insensitive substring of query/prompt/slug/why")
	dupes := fs.Bool("dupes", false, "only rows with a near-duplicate twin")
	asJSON := fs.Bool("json", false, "machine-readable output")
	width := fs.Int("width", 120, "wrap width for the text column")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	if *label != "" && *label != "yes" && *label != "no" {
		return fail(fmt.Errorf("--label must be yes or no"))
	}
	if *split != "" && *split != "train" && *split != "validation" {
		return fail(fmt.Errorf("--split must be train or validation"))
	}
	filter := store.Filter{Skill: *skill, Label: *label, Split: *split, Search: *search, Dupes: *dupes}

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

	var all []store.Entry
	for _, l := range lanes {
		all = append(all, c.s.Rows(l, filter)...)
	}
	if *asJSON {
		if err := writeJSON(os.Stdout, all); err != nil {
			return fail(err)
		}
		return 0
	}

	if len(all) == 0 {
		fmt.Println(styleDim.Render("no rows match"))
		return 0
	}
	for _, l := range lanes {
		rows := c.s.Rows(l, filter)
		if len(rows) == 0 {
			continue
		}
		printLane(c.s, l, rows, *width, filter)
		fmt.Println()
	}
	printStats(c.s, lanes, filter)
	return 0
}

func printLane(s *store.Store, lane string, rows []store.Entry, width int, f store.Filter) {
	textWidth := width - 50
	if textWidth < 24 {
		textWidth = 24
	}
	if lane == store.LaneTrigger {
		fmt.Println(styleTitle.Render(fmt.Sprintf("trigger queries · %d", len(rows))))
		header := []string{"ID", "SKILL", "TRIG", "SPLIT", "DUP", "QUERY"}
		var cells [][]string
		for _, e := range rows {
			cells = append(cells, []string{
				fmt.Sprintf("%d", e.ID), e.Skill, labelStyled(e.Label), e.Split, dupeCell(e.Dupe),
				oneLine(e.Text),
			})
		}
		table(os.Stdout, header, cells, []int{4, 20, 0, 11, 4, textWidth})
		return
	}
	fmt.Println(styleTitle.Render(fmt.Sprintf("output cases · %d", len(rows))))
	header := []string{"ID", "SKILL", "SLUG", "DUP", "PROMPT", "FIXTURE"}
	var cells [][]string
	for _, e := range rows {
		fix := "-"
		if s.FixtureExists(e.Skill, e.Slug) {
			fix = fmt.Sprintf("%d file(s)", len(s.FixtureFiles(e.Skill, e.Slug)))
		}
		cells = append(cells, []string{
			fmt.Sprintf("%d", e.ID), e.Skill, e.Slug, dupeCell(e.Dupe), oneLine(e.Text), fix,
		})
	}
	table(os.Stdout, header, cells, []int{4, 20, 24, 4, textWidth, 9})
}

func dupeCell(cluster int) string {
	if cluster == 0 {
		return "-"
	}
	return styleDupe.Render(fmt.Sprintf("#%d", cluster))
}

func printStats(s *store.Store, lanes []string, f store.Filter) {
	var parts []string
	for _, l := range lanes {
		rows := s.Rows(l, f)
		if l == store.LaneTrigger {
			yes, no, train, val := 0, 0, 0, 0
			for _, e := range rows {
				if e.Label == "yes" {
					yes++
				} else {
					no++
				}
				if e.Split == "train" {
					train++
				} else if e.Split == "validation" {
					val++
				}
			}
			parts = append(parts, fmt.Sprintf("trigger %d (%d yes / %d no · %d train / %d validation)",
				len(rows), yes, no, train, val))
			continue
		}
		parts = append(parts, fmt.Sprintf("output %d", len(rows)))
	}
	st := s.Validate()
	errs := 0
	for _, i := range st {
		if i.Severity == store.SeverityError {
			errs++
		}
	}
	line := strings.Join(parts, " · ")
	if errs > 0 {
		line += fmt.Sprintf(" · %d validation error(s)", errs)
		fmt.Println(styleErr.Render(line))
	} else {
		fmt.Println(styleDim.Render(line))
	}
}

func runShow(args []string) int {
	fs, c := newFS("show")
	lane := fs.String("lane", "", "trigger or output (required for a bare numeric ref)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	if fs.NArg() == 0 {
		return fail(fmt.Errorf("usage: evalctl show <ref> [--lane]"))
	}
	ref, err := parseRef(c.s, *lane, fs.Arg(0))
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		return showJSON(c.s, ref)
	}

	if ref.Lane == store.LaneTrigger {
		q := c.s.Trigger.Queries[ref.Index]
		fmt.Println(styleTitle.Render(fmt.Sprintf("trigger #%d", q.ID)))
		fmt.Printf("  skill        %s\n", q.Skill)
		label := "should not trigger"
		if q.ShouldTrigger {
			label = "should trigger"
		}
		fmt.Printf("  label        %s\n", label)
		fmt.Printf("  split        %s\n", q.Split)
		if q.Why != "" {
			fmt.Printf("  why          %s\n", q.Why)
		}
		fmt.Printf("\n  query        %s\n", wrapIndent(q.Query, 6))
		if len(q.Extra) > 0 {
			fmt.Printf("\n  extra keys   %s\n", joinKeys(q.Extra))
		}
		return 0
	}

	sk := c.s.Skill(ref.Skill)
	cse := sk.Output.Evals[ref.Index]
	fmt.Println(styleTitle.Render(fmt.Sprintf("%s case #%d", sk.Name, cse.ID)))
	fmt.Printf("  slug         %s\n", cse.Slug)
	fmt.Printf("  prompt       %s\n", wrapIndent(cse.Prompt, 15))
	fmt.Printf("\n  expected     %s\n", wrapIndent(cse.ExpectedOutput, 15))
	if len(cse.Files) > 0 {
		fmt.Printf("\n  files        %s\n", strings.Join(cse.Files, "\n               "))
	} else {
		fmt.Printf("\n  files        (none — the run root starts empty)\n")
	}
	dir := c.s.FixtureDir(sk.Name, cse.Slug)
	if c.s.FixtureExists(sk.Name, cse.Slug) {
		fmt.Printf("  fixture      %s\n", dir)
	} else {
		fmt.Printf("  fixture      %s (absent)\n", dir)
	}
	if len(cse.Extra) > 0 {
		fmt.Printf("  extra keys   %s\n", joinKeys(cse.Extra))
	}
	return 0
}

func showJSON(s *store.Store, ref store.RowRef) int {
	if ref.Lane == store.LaneTrigger {
		out := struct {
			Lane  string `json:"lane"`
			Index int    `json:"index"`
			Query any    `json:"query"`
		}{Lane: ref.Lane, Index: ref.Index, Query: s.Trigger.Queries[ref.Index]}
		if err := writeJSON(os.Stdout, out); err != nil {
			return fail(err)
		}
		return 0
	}
	sk := s.Skill(ref.Skill)
	out := struct {
		Lane    string `json:"lane"`
		Skill   string `json:"skill"`
		Index   int    `json:"index"`
		Fixture string `json:"fixture_dir"`
		Case    any    `json:"case"`
	}{Lane: ref.Lane, Skill: ref.Skill, Index: ref.Index, Fixture: s.FixtureDir(ref.Skill, sk.Output.Evals[ref.Index].Slug),
		Case: sk.Output.Evals[ref.Index]}
	if err := writeJSON(os.Stdout, out); err != nil {
		return fail(err)
	}
	return 0
}

func wrapIndent(text string, indent int) string {
	pad := strings.Repeat(" ", indent)
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

func joinKeys(m map[string]json.RawMessage) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		keys[i] = "`" + k + "`"
	}
	return strings.Join(keys, ", ")
}
