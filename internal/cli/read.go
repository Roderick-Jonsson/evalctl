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
	in := fs.String("in", "", "scope --search to one column: id, skill, slug, text, why …")
	sortSpec := fs.String("sort", "", "order by a column: skill, id:desc, or skill,id:desc")
	group := fs.String("group", "", "draw the table in sections by a column: skill, split, dupe …")
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
	if *group != "" && *sortSpec == "" {
		// a grouped table reads best in the group's own order; --sort still overrides it
		*sortSpec = *group
	}
	keys, err := store.ParseSort(*sortSpec)
	if err != nil {
		return fail(err)
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
	// A column must exist somewhere in the selected lanes; one lane missing it (split on output)
	// is not an error — that lane simply keeps its file order. A single lane gets the exact
	// "this lane has: …" list instead.
	if len(lanes) == 1 {
		if err := (store.Order{Keys: keys, Group: *group}).Validate(lanes[0]); err != nil {
			return fail(err)
		}
	} else {
		for _, k := range keys {
			if !laneHasColumn(lanes, k.Field) {
				return fail(fmt.Errorf("--sort: unknown column %q", k.Field))
			}
		}
		if *group != "" && !laneHasColumn(lanes, *group) {
			return fail(fmt.Errorf("--group: unknown column %q", *group))
		}
	}
	if *in != "" && !laneHasColumn(lanes, *in) {
		return fail(fmt.Errorf("--in: unknown column %q", *in))
	}
	filter := store.Filter{Skill: *skill, Label: *label, Split: *split, Search: *search, In: *in, Dupes: *dupes}

	var all []store.Entry
	for _, l := range lanes {
		all = append(all, c.s.RowsOrdered(l, filter, laneOrder(l, keys, *group))...)
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
		rows := c.s.RowsOrdered(l, filter, laneOrder(l, keys, *group))
		if len(rows) == 0 {
			continue
		}
		printLane(c.s, l, rows, *width, laneOrder(l, keys, *group))
		fmt.Println()
	}
	printStats(c.s, lanes, filter)
	return 0
}

func laneHasColumn(lanes []string, field string) bool {
	for _, l := range lanes {
		if store.ColumnExists(l, field) {
			return true
		}
	}
	return false
}

// laneOrder keeps the keys and the group column a lane actually has, so `--lane both --sort split`
// orders the trigger lane and leaves the output lane in file order instead of failing outright.
func laneOrder(lane string, keys []store.Sort, group string) store.Order {
	o := store.Order{}
	for _, k := range keys {
		if store.ColumnExists(lane, k.Field) {
			o.Keys = append(o.Keys, k)
		}
	}
	if group != "" && store.ColumnExists(lane, group) {
		o.Group = group
	}
	return o
}

func printLane(s *store.Store, lane string, rows []store.Entry, width int, order store.Order) {
	textWidth := width - 54
	if textWidth < 24 {
		textWidth = 24
	}
	var header []string
	var limits []int
	cells := func(e store.Entry) []string {
		if lane == store.LaneTrigger {
			return []string{fmt.Sprintf("%d", e.ID), e.Skill, labelStyled(e.Label), e.Split,
				dupeCell(e.Dupe), oneLine(e.Text)}
		}
		fix := "-"
		if s.FixtureExists(e.Skill, e.Slug) {
			fix = fmt.Sprintf("%d file(s)", len(s.FixtureFiles(e.Skill, e.Slug)))
		}
		asr := "-"
		if e.Assert > 0 {
			asr = fmt.Sprintf("%d", e.Assert)
		}
		return []string{fmt.Sprintf("%d", e.ID), e.Skill, e.Slug, asr, dupeCell(e.Dupe), oneLine(e.Text), fix}
	}
	if lane == store.LaneTrigger {
		fmt.Println(styleTitle.Render(fmt.Sprintf("trigger queries · %d", len(rows))))
		header, limits = []string{"ID", "SKILL", "TRIG", "SPLIT", "DUP", "QUERY"}, []int{4, 20, 0, 11, 4, textWidth}
	} else {
		fmt.Println(styleTitle.Render(fmt.Sprintf("output cases · %d", len(rows))))
		header, limits = []string{"ID", "SKILL", "SLUG", "ASR", "DUP", "PROMPT", "FIXTURE"},
			[]int{4, 20, 24, 4, 4, textWidth, 9}
	}
	groups := store.GroupEntries(rows, order.Group)
	out := make([]tableGroup, 0, len(groups))
	for _, g := range groups {
		tg := tableGroup{Label: store.GroupLabel(order.Group, g.Key)}
		if order.Group == "" {
			tg.Label = ""
		}
		for _, e := range g.Entries {
			tg.Rows = append(tg.Rows, cells(e))
		}
		out = append(out, tg)
	}
	tableGroups(os.Stdout, header, out, limits)
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
		if prov, err := store.DecodePromotedQuery(q); err == nil && prov != nil {
			fmt.Printf("\n  promoted     %s case #%d (%s)\n", prov.Skill, prov.Case, prov.Slug)
		}
		if keys := joinKeys(q.Extra); keys != "" {
			fmt.Printf("\n  extra keys   %s\n", keys)
		}
		return 0
	}

	sk := c.s.Skill(ref.Skill)
	cse := sk.Output.Evals[ref.Index]
	fmt.Println(styleTitle.Render(fmt.Sprintf("%s case #%d", sk.Name, cse.ID)))
	fmt.Printf("  slug         %s\n", cse.Slug)
	fmt.Printf("  prompt       %s\n", wrapIndent(cse.Prompt, 15))
	fmt.Printf("\n  expected     %s\n", wrapIndent(cse.ExpectedOutput, 15))
	if len(cse.Assertions) > 0 {
		lines := make([]string, len(cse.Assertions))
		for i, a := range cse.Assertions {
			lines[i] = fmt.Sprintf("%d. %s", i+1, a)
		}
		fmt.Printf("\n  assertions   %s\n", strings.Join(lines, "\n               "))
	} else {
		fmt.Printf("\n  assertions   (none yet)\n")
	}
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
	if keys := joinKeys(cse.Extra); keys != "" {
		fmt.Printf("  extra keys   %s\n", keys)
	}
	if prov, err := store.DecodePromotedFrom(cse); err == nil && prov != nil {
		fmt.Printf("\n  promoted     %s/%s/%s\n", prov.Run, prov.Case, prov.Arm)
		fmt.Printf("  fixture sha  %s\n", prov.FixtureSHA256)
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

// joinKeys lists a row's unknown keys, leaving out the provenance an earlier line already
// spelled out: a promotion is not an "extra key" to a reader.
func joinKeys(m map[string]json.RawMessage) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		if k == store.ProvenanceKey {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		keys[i] = "`" + k + "`"
	}
	return strings.Join(keys, ", ")
}
