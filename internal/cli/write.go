package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"

	"strings"

	"evalctl/internal/model"
	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// ---------------------------------------------------------------- field helpers

// value resolves a flag value; "-" means "read stdin", which is how long prompts get in
// without shell quoting games.
func value(raw string) (string, error) {
	if raw != "-" {
		return raw, nil
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\n"), nil
}

func setFlags(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' }) {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitAssertions reads a grading list: one assertion per line, or ';' when it came in on one
// line. Commas are never a separator — an assertion that reads "March, July and November" is one
// statement, and splitting it would silently grade half of it. The splitter itself lives in model,
// because the workbench writes the same list and two of them would eventually disagree.
func splitAssertions(raw string) []string { return model.SplitAssertions(raw) }

// ---------------------------------------------------------------- add

func runAdd(args []string) int {
	fs, c := newFS("add")
	lane := fs.String("lane", "", "trigger or output (required)")
	id := fs.Int("id", 0, "row id (default: next free id)")
	skill := fs.String("skill", "", "stage skill under test (required)")
	query := fs.String("query", "", "trigger: the user prompt being tested (- for stdin)")
	why := fs.String("why", "", "trigger: why the row exists (read when it fails)")
	split := fs.String("split", "", "trigger: train or validation")
	label := fs.String("label", "", "trigger: yes (should trigger) or no")
	slug := fs.String("slug", "", "output: case slug, also its fixture dir name")
	prompt := fs.String("prompt", "", "output: the case prompt (- for stdin)")
	expected := fs.String("expected", "", "output: expected_output (- for stdin)")
	files := fs.String("files", "", "output: comma-separated fixture files, relative to the slug dir")
	assertions := fs.String("assertions", "", "output: newline- or ;-separated assertions, the grading list")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	if *lane != store.LaneTrigger && *lane != store.LaneOutput {
		return fail(fmt.Errorf("--lane must be trigger or output"))
	}
	if *skill == "" {
		return fail(fmt.Errorf("--skill is required"))
	}

	if *lane == store.LaneTrigger {
		q := model.TriggerQuery{ID: *id, Skill: *skill, Split: *split, Why: *why}
		if q.ID == 0 {
			q.ID = c.s.NextTriggerID()
		}
		adoptWarning(c.s.Root, store.LaneTrigger, *skill, q.ID)
		var err error
		if q.Query, err = value(*query); err != nil {
			return fail(err)
		}
		if q.Why, err = value(*why); err != nil {
			return fail(err)
		}
		switch *label {
		case "yes":
			q.ShouldTrigger = true
		case "no":
			q.ShouldTrigger = false
		default:
			return fail(fmt.Errorf("--label must be yes or no"))
		}
		if err := c.s.AddTrigger(q); err != nil {
			return fail(err)
		}
		return saveTouched(c, []string{fmt.Sprintf("added trigger #%d (%s, %s)", q.ID, q.Skill, splitOrLabel(q.ShouldTrigger, q.Split))},
			map[string]bool{store.LaneTrigger: true})
	}

	cse := model.OutputCase{ID: *id, Slug: *slug, Files: splitList(*files), Assertions: splitAssertions(*assertions)}
	if cse.ID == 0 {
		cse.ID = c.s.NextCaseID(*skill)
	}
	adoptWarning(c.s.Root, store.LaneOutput, *skill, cse.ID)
	var err error
	if cse.Prompt, err = value(*prompt); err != nil {
		return fail(err)
	}
	if cse.ExpectedOutput, err = value(*expected); err != nil {
		return fail(err)
	}
	if err := c.s.AddOutput(*skill, cse); err != nil {
		return fail(err)
	}
	return saveTouched(c, []string{fmt.Sprintf("added %s case #%d (%s)", *skill, cse.ID, cse.Slug)},
		map[string]bool{*skill: true})
}

func splitOrLabel(should bool, split string) string {
	if should {
		return split + ", should-trigger"
	}
	return split + ", should-not-trigger"
}

// ---------------------------------------------------------------- edit

func runEdit(args []string) int {
	fs, c := newFS("edit")
	lane := fs.String("lane", "", "trigger or output (needed for a bare numeric ref)")
	id := fs.Int("id", 0, "new id (it is cited by earlier iterations, so this change asks first)")
	skill := fs.String("skill", "", "new skill")
	query := fs.String("query", "", "trigger: new query (- for stdin)")
	why := fs.String("why", "", "trigger: new why")
	split := fs.String("split", "", "trigger: new split")
	label := fs.String("label", "", "trigger: yes or no")
	slug := fs.String("slug", "", "output: new slug (moves the fixture dir)")
	prompt := fs.String("prompt", "", "output: new prompt (- for stdin)")
	expected := fs.String("expected", "", "output: new expected_output (- for stdin)")
	files := fs.String("files", "", "output: new fixture file list, comma-separated ('' to clear)")
	assertions := fs.String("assertions", "", "output: new assertion list, one per line or ';'-separated ('' to clear)")
	dry := fs.Bool("dry-run", false, "show the change without writing")
	yes := fs.Bool("yes", false, "with --id: move it without asking")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	if fs.NArg() == 0 {
		return fail(fmt.Errorf("usage: evalctl edit <ref> [flags]"))
	}
	ref, err := parseRef(c.s, *lane, fs.Arg(0))
	if err != nil {
		return fail(err)
	}
	set := setFlags(fs)
	if len(set) == 0 || (len(set) == 1 && set["root"]) {
		return fail(fmt.Errorf("nothing to change: pass at least one field flag"))
	}
	if set["id"] && *id <= 0 {
		return fail(fmt.Errorf("--id must be a positive integer"))
	}

	if ref.Lane == store.LaneTrigger {
		q := c.s.Trigger.Queries[ref.Index]
		before := q
		if set["id"] {
			q.ID = *id
		}
		if set["skill"] {
			q.Skill = *skill
		}
		if set["split"] {
			q.Split = *split
		}
		if set["label"] {
			switch *label {
			case "yes":
				q.ShouldTrigger = true
			case "no":
				q.ShouldTrigger = false
			default:
				return fail(fmt.Errorf("--label must be yes or no"))
			}
		}
		if set["query"] {
			if q.Query, err = value(*query); err != nil {
				return fail(err)
			}
		}
		if set["why"] {
			if q.Why, err = value(*why); err != nil {
				return fail(err)
			}
		}
		if set["id"] && q.ID != before.ID {
			if !idChangeGuard(c, store.LaneTrigger, before.Skill, before.ID, q.ID, *dry, *yes) {
				return 0
			}
		}
		if *dry {
			printTriggerDiff(before, q)
			return 0
		}
		if err := c.s.UpdateTrigger(ref.Index, q); err != nil {
			return fail(err)
		}
		var changes []string
		if diff := triggerDiff(before, q); diff != "" {
			changes = append(changes, fmt.Sprintf("trigger #%d: %s", q.ID, diff))
		}
		return saveTouched(c, changes, map[string]bool{store.LaneTrigger: true})
	}

	sk := c.s.Skill(ref.Skill)
	cse := sk.Output.Evals[ref.Index]
	before := cse
	if set["id"] && set["skill"] && *skill != ref.Skill {
		return fail(fmt.Errorf("a move re-ids the case (the target skill's next free id): move it first, then edit the id"))
	}
	if set["id"] {
		cse.ID = *id
	}
	if set["slug"] {
		cse.Slug = *slug
	}
	if set["prompt"] {
		if cse.Prompt, err = value(*prompt); err != nil {
			return fail(err)
		}
	}
	if set["expected"] {
		if cse.ExpectedOutput, err = value(*expected); err != nil {
			return fail(err)
		}
	}
	if set["files"] {
		cse.Files = splitList(*files)
	}
	if set["assertions"] {
		cse.Assertions = splitAssertions(*assertions)
	}
	if set["id"] && cse.ID != before.ID {
		if !idChangeGuard(c, store.LaneOutput, ref.Skill, before.ID, cse.ID, *dry, *yes) {
			return 0
		}
	}
	if *dry {
		printCaseDiff(before, cse)
		return 0
	}
	if set["skill"] && *skill != ref.Skill {
		notes, err := c.s.MoveOutput(ref.Skill, ref.Index, *skill)
		if err != nil {
			return fail(err)
		}
		return saveTouched(c, notes, map[string]bool{ref.Skill: true, *skill: true})
	}
	notes, err := c.s.UpdateOutput(ref.Skill, ref.Index, cse)
	if err != nil {
		return fail(err)
	}
	changes := []string{fmt.Sprintf("%s case #%d: %s", ref.Skill, cse.ID, caseDiff(before, cse))}
	changes = append(changes, notes...)
	return saveTouched(c, changes, map[string]bool{ref.Skill: true})
}

func triggerDiff(before, after model.TriggerQuery) string {
	var parts []string
	if before.ID != after.ID {
		parts = append(parts, fmt.Sprintf("id %d -> %d", before.ID, after.ID))
	}
	if before.Skill != after.Skill {
		parts = append(parts, fmt.Sprintf("skill %s -> %s", before.Skill, after.Skill))
	}
	if before.ShouldTrigger != after.ShouldTrigger {
		parts = append(parts, fmt.Sprintf("label -> %s", boolLabel(after.ShouldTrigger)))
	}
	if before.Split != after.Split {
		parts = append(parts, fmt.Sprintf("split %s -> %s", before.Split, after.Split))
	}
	if before.Query != after.Query {
		parts = append(parts, fmt.Sprintf("query %q -> %q", clipText(before.Query, 30), clipText(after.Query, 30)))
	}
	if before.Why != after.Why {
		parts = append(parts, "why updated")
	}
	return strings.Join(parts, ", ")
}

func caseDiff(before, after model.OutputCase) string {
	var parts []string
	if before.ID != after.ID {
		parts = append(parts, fmt.Sprintf("id %d -> %d", before.ID, after.ID))
	}
	if before.Slug != after.Slug {
		parts = append(parts, fmt.Sprintf("slug %s -> %s", before.Slug, after.Slug))
	}
	if before.Prompt != after.Prompt {
		parts = append(parts, "prompt updated")
	}
	if before.ExpectedOutput != after.ExpectedOutput {
		parts = append(parts, "expected_output updated")
	}
	if strings.Join(before.Files, ",") != strings.Join(after.Files, ",") {
		parts = append(parts, fmt.Sprintf("files %v -> %v", before.Files, after.Files))
	}
	if strings.Join(before.Assertions, "\n") != strings.Join(after.Assertions, "\n") {
		parts = append(parts, fmt.Sprintf("assertions %d -> %d", len(before.Assertions), len(after.Assertions)))
	}
	if len(parts) == 0 {
		return "no change"
	}
	return strings.Join(parts, ", ")
}

func printTriggerDiff(before, after model.TriggerQuery) {
	fmt.Println(styleTitle.Render("would change trigger #" + fmt.Sprint(before.ID)))
	fmt.Println("  " + triggerDiff(before, after))
}

func printCaseDiff(before, after model.OutputCase) {
	fmt.Println(styleTitle.Render("would change case #" + fmt.Sprint(before.ID)))
	fmt.Println("  " + caseDiff(before, after))
}

func boolLabel(b bool) string {
	if b {
		return "should-trigger"
	}
	return "should-not-trigger"
}

func clipText(s string, n int) string {
	return truncate(oneLine(s), n)
}

// ---------------------------------------------------------------- rm

func runRemove(args []string) int {
	fs, c := newFS("rm")
	lane := fs.String("lane", "", "trigger or output (needed for bare numeric refs)")
	skill := fs.String("skill", "", "bulk: only rows of this skill")
	label := fs.String("label", "", "bulk trigger: yes or no")
	split := fs.String("split", "", "bulk trigger: train or validation")
	search := fs.String("search", "", "bulk: substring filter")
	dupes := fs.Bool("dupes", false, "bulk: only near-duplicate rows")
	all := fs.Bool("all", false, "bulk: every row in the lane(s) — needs --lane")
	withFixture := fs.Bool("with-fixture", false, "output: also delete the fixture dirs")
	dry := fs.Bool("dry-run", false, "show what would go, write nothing")
	yes := fs.Bool("yes", false, "do not ask")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}

	var refs []store.RowRef
	if fs.NArg() > 0 {
		var err error
		if refs, err = parseRefs(c.s, *lane, fs.Args()); err != nil {
			return fail(err)
		}
	} else {
		f := store.Filter{Skill: *skill, Label: *label, Split: *split, Search: *search, Dupes: *dupes}
		if !*all && *skill == "" && *label == "" && *split == "" && *search == "" && !*dupes {
			return fail(fmt.Errorf("nothing selected: pass refs, filters, or --lane with --all"))
		}
		lanes := []string{store.LaneTrigger, store.LaneOutput}
		switch *lane {
		case "trigger":
			lanes = lanes[:1]
		case "output":
			lanes = lanes[1:]
		case "":
			if *label != "" || *split != "" {
				lanes = lanes[:1] // label and split are trigger-only
			}
		}
		for _, l := range lanes {
			for _, e := range c.s.Rows(l, f) {
				refs = append(refs, store.RowRef{Lane: l, Skill: e.Skill, Index: e.Index})
			}
		}
	}
	if len(refs) == 0 {
		fmt.Println(styleDim.Render("nothing matches — nothing deleted"))
		return 0
	}

	rows := describeRefs(c.s, refs)
	fmt.Println(styleTitle.Render(fmt.Sprintf("%d row(s) selected for deletion", len(refs))))
	for _, line := range rows {
		fmt.Println("  " + line)
	}
	if *withFixture {
		n := 0
		for _, r := range refs {
			if r.Lane != store.LaneOutput {
				continue
			}
			sk := c.s.Skill(r.Skill)
			if sk != nil && c.s.FixtureExists(r.Skill, sk.Output.Evals[r.Index].Slug) {
				n++
			}
		}
		fmt.Println(styleWarn.Render(fmt.Sprintf("  + %d fixture dir(s) will be deleted", n)))
	}
	if *dry {
		fmt.Println(styleDim.Render("  dry run: nothing written"))
		return 0
	}
	if !*yes {
		ok, err := confirm(fmt.Sprintf("delete %d row(s)?", len(refs)))
		if err != nil {
			return fail(err)
		}
		if !ok {
			fmt.Println("aborted")
			return 0
		}
	}

	touched := map[string]bool{}
	for _, r := range refs {
		if r.Lane == store.LaneTrigger {
			touched[store.LaneTrigger] = true
		} else {
			touched[r.Skill] = true
		}
	}
	changes, err := c.s.DeleteRows(refs, *withFixture)
	if err != nil {
		return fail(err)
	}
	return saveTouched(c, changes, touched)
}

func describeRefs(s *store.Store, refs []store.RowRef) []string {
	var out []string
	for _, r := range refs {
		if r.Lane == store.LaneTrigger {
			q := s.Trigger.Queries[r.Index]
			out = append(out, fmt.Sprintf("#%-3d %-20s %-3s %-10s %s",
				q.ID, q.Skill, boolShort(q.ShouldTrigger), q.Split, clipText(q.Query, 60)))
			continue
		}
		sk := s.Skill(r.Skill)
		if sk == nil {
			continue
		}
		cse := sk.Output.Evals[r.Index]
		out = append(out, fmt.Sprintf("#%-3d %-20s %-22s %s",
			cse.ID, sk.Name, cse.Slug, clipText(cse.Prompt, 50)))
	}
	return out
}

func boolShort(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func confirm(prompt string) (bool, error) {
	fmt.Printf("%s [y/N] ", prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

// idChangeGuard says what still cites the old id and asks before it moves. An id is a citation
// key, not just a field: trigger result dirs are q<id>-<skill> and every report prints the number,
// so an edit re-points that history at a different row without touching a single file that cites
// it. That is the one silent cost this tool must not charge, so the change asks the way renumber
// does — and --yes is the answer a script gives. A dry run prints the same warning and writes
// nothing.
func idChangeGuard(c *common, lane, skill string, oldID, newID int, dry, yes bool) bool {
	refs := runner.IDReferences(c.s.Root, lane, skill, oldID)
	fmt.Println(styleWarn.Render(fmt.Sprintf("  id %d -> %d — earlier iterations record this row as #%d", oldID, newID, oldID)))
	if len(refs) == 0 {
		fmt.Println(styleDim.Render("  nothing on disk cites it yet (no result dirs, no reports)"))
	} else {
		fmt.Println(styleDim.Render(fmt.Sprintf("  %d artifact(s) still cite #%d:", len(refs), oldID)))
		const limit = 8
		for i, ref := range refs {
			if i == limit {
				fmt.Println(styleDim.Render(fmt.Sprintf("  … and %d more", len(refs)-limit)))
				break
			}
			fmt.Println(styleDim.Render(fmt.Sprintf("  %-50s %s", ref.Path, ref.What)))
		}
	}
	if dry || yes {
		return true
	}
	ok, err := confirm(fmt.Sprintf("change #%d -> #%d anyway?", oldID, newID))
	if err != nil || !ok {
		fmt.Println("aborted")
		return false
	}
	return true
}

// adoptWarning says that a row is being created with an id that history still cites. A gap in the
// file is not a gap on disk: the deleted row's result dirs and reports are there, so the new row
// would inherit results it never produced. It warns rather than asks — a create destroys nothing,
// and a warning must never stop a script.
func adoptWarning(root, lane, skill string, id int) {
	refs := runner.IDReferences(root, lane, skill, id)
	if len(refs) == 0 {
		return
	}
	fmt.Println(styleWarn.Render(fmt.Sprintf("  #%d is free in the file but not on disk — %d artifact(s) still cite it:", id, len(refs))))
	const limit = 5
	for i, ref := range refs {
		if i == limit {
			fmt.Println(styleDim.Render(fmt.Sprintf("  … and %d more", len(refs)-limit)))
			break
		}
		fmt.Println(styleDim.Render(fmt.Sprintf("  %-50s %s", ref.Path, ref.What)))
	}
	fmt.Println(styleDim.Render("  this row would adopt that history — pick the next free id to avoid it"))
}

// ---------------------------------------------------------------- mv

func runMove(args []string) int {
	fs, c := newFS("mv")
	lane := fs.String("lane", "", "trigger or output")
	to := fs.String("to-skill", "", "destination stage skill (required)")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	if *to == "" {
		return fail(fmt.Errorf("--to-skill is required"))
	}
	if fs.NArg() == 0 {
		return fail(fmt.Errorf("usage: evalctl mv <ref> --to-skill <skill>"))
	}
	ref, err := parseRef(c.s, *lane, fs.Arg(0))
	if err != nil {
		return fail(err)
	}
	if ref.Lane != store.LaneOutput {
		return fail(fmt.Errorf("only output cases move between skills; trigger rows change skill with `edit --skill`"))
	}
	if c.s.Skill(*to) == nil && !isPlainName(*to) {
		return fail(fmt.Errorf("%q is not a usable skill name", *to))
	}
	notes, err := c.s.MoveOutput(ref.Skill, ref.Index, *to)
	if err != nil {
		return fail(err)
	}
	return saveTouched(c, notes, map[string]bool{ref.Skill: true, *to: true})
}

func isPlainName(s string) bool {
	if s == "" {
		return false
	}
	return !strings.ContainsAny(s, "/\\ ")
}

// ---------------------------------------------------------------- renumber

func runRenumber(args []string) int {
	fs, c := newFS("renumber")
	lane := fs.String("lane", "both", "trigger, output or both")
	yes := fs.Bool("yes", false, "do not ask")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	var changes []string
	touched := map[string]bool{}
	switch *lane {
	case "trigger":
		changes = append(changes, c.s.RenumberTrigger()...)
		touched[store.LaneTrigger] = true
	case "output":
		for _, name := range c.s.SkillNames() {
			changes = append(changes, c.s.RenumberOutput(name)...)
			touched[name] = true
		}
	case "both":
		changes = append(changes, c.s.RenumberTrigger()...)
		touched[store.LaneTrigger] = true
		for _, name := range c.s.SkillNames() {
			changes = append(changes, c.s.RenumberOutput(name)...)
			touched[name] = true
		}
	default:
		return fail(fmt.Errorf("--lane must be trigger, output or both"))
	}
	if len(changes) == 0 {
		fmt.Println(styleDim.Render("ids are already contiguous"))
		return 0
	}
	for _, ch := range changes {
		fmt.Println("  " + ch)
	}
	fmt.Println(styleWarn.Render("  renumbering breaks comparability with earlier result dirs (q<id>-<skill>) and with `--query`/`--case` notes"))
	if !*yes {
		ok, err := confirm("renumber anyway?")
		if err != nil {
			return fail(err)
		}
		if !ok {
			fmt.Println("aborted")
			return 0
		}
	}
	return saveTouched(c, changes, touched)
}
