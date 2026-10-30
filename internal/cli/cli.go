// Package cli is the non-interactive face of evalctl: read (ls/show/stats/dupes), write
// (add/edit/rm/mv/renumber), validate, and run — both eval lanes, plus the results browser and
// the profile commands. Running lives in internal/runner; this package only parses flags and
// prints progress.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"evalctl/internal/format"
	"evalctl/internal/store"
	"evalctl/internal/tui"
)

// usage is the whole command surface. It is the one place an operator finds a verb they half
// remember, so it stays complete — but every line states a grammar and nothing else, and it is only
// printed for `evalctl help`, never after an error (that is one line plus a pointer).
const usage = `evalctl — manage and run the eval sets: trigger queries and output cases.

  evalctl                            interactive TUI, opening on the skill list
  evalctl tui [--skill S] [--lane L]  open a skill directly, or the unscoped lane view
  evalctl skills                     the set by skill: cases, fixtures, trigger rows, health
  evalctl ls                         list rows
      --lane trigger|output|both  --skill S  --label yes|no  --split train|validation
      --search Q [--in COL]  --sort skill,id:desc  --group COL  --dupes  --json  --width N
  evalctl show <ref> [--lane]        one row in full (ref: t12, o:skill:3, or 12 --lane)
  evalctl add  --lane trigger ...    create a row
  evalctl edit <ref> ...             change only the flags you pass
  evalctl rm   <ref...>              delete rows by ref
  evalctl rm   --skill S --label yes|no --split … --search Q --dupes   delete by filter
  evalctl rm   --all --lane L        delete every row of a lane
      --with-fixture  --dry-run  --yes
  evalctl mv   <ref> --to-skill S    move an output case to another stage skill
  evalctl promote --from R/C[/A] ...  a run's output becomes the next stage's fixture + case
  evalctl promote --to-references ...  that run output becomes the skill's references/ example
  evalctl promote --to-trigger ...    a case's prompt becomes a trigger query
  evalctl dupes [--lane] [--threshold 0.60]   near-duplicate groups, the trim shortlist
  evalctl stats                      counts per skill / label / split
  evalctl validate [--strict] [--json]  check the tree against the runner's rules
  evalctl renumber                   close id gaps (breaks comparability with old results)
  evalctl version                    which build this is (version, commit, dirty, go)

  evalctl run [flags]                output lane: each case with and without the skill
  evalctl trigger [flags]            trigger lane: does the right skill load?
  evalctl runs [--lane] [--skill]     iterations per workspace, with pass rates
  evalctl report <run>               an iteration's report.md, rendered (--raw for the source)
  evalctl profiles [cmd]             status/check/ensure/render/sync/scrub the eval profiles
  evalctl theme [name]               the colour schemes, with each role's contrast on its background

Run flags: --skill, --case, --iteration, --arms, --model, --budget, --resume, --force,
           --no-verify, --isolate=false, --dry-run, --list, --log full|tools|off
Trigger:   --query, --split, --runs, --threshold, --max-turns (same shared flags)

--log full (default) streams each arm's tool calls and prose to stderr while it runs, and writes
the same lines to activity.log beside stream.jsonl; tools drops the prose, off writes nothing.

Global: --root DIR (default: nearest dir with the eval tree, or $EVALCTL_ROOT)
         --theme NAME (auto, midnight, high-contrast, daylight; or $EVALCTL_THEME)
Refs:   12 --lane trigger | t12 | trigger:12 | o:slide-builder:3 | slide-builder:3

Exit codes: 0 done · 1 interrupted · 2 refused (bad usage, unknown ref, validation errors).
Every write to the sets is atomic and leaves a copy in .evalctl/backups/.
`

// Run dispatches a command line and returns the process exit code.
func Run(args []string) int {
	// Colour is process-wide, so the global --theme is taken out here, before any flag set sees
	// it: one flag works on every command, and $EVALCTL_THEME is the way to make it stick.
	name, args := takeTheme(args)
	if err := resolveTheme(name); err != nil {
		return fail(err)
	}
	if len(args) == 0 {
		return runTUI(nil, store.LaneTrigger)
	}
	// `evalctl --root DIR` is the TUI with a global flag, not a command named "--root".
	if strings.HasPrefix(args[0], "-") && args[0] != "-h" && args[0] != "--help" && args[0] != "--version" {
		return runTUI(args, store.LaneTrigger)
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	case "version", "--version":
		return runVersion(rest)
	case "tui":
		return runTUI(rest, store.LaneTrigger)
	case "skills", "skill":
		return runSkills(rest)
	case "ls", "list":
		return runList(rest)
	case "show":
		return runShow(rest)
	case "add":
		return runAdd(rest)
	case "edit":
		return runEdit(rest)
	case "rm", "delete":
		return runRemove(rest)
	case "mv", "move":
		return runMove(rest)
	case "promote":
		return runPromote(rest)
	case "dupes", "duplicates":
		return runDupes(rest)
	case "stats":
		return runStats(rest)
	case "validate", "check":
		return runValidate(rest)
	case "renumber":
		return runRenumber(rest)
	case "run":
		return runRun(rest)
	case "trigger":
		// `evalctl trigger` is the run command with the trigger lane selected.
		return runRun(append([]string{"--lane", store.LaneTrigger}, rest...))
	case "runs", "results":
		return runRuns(rest)
	case "report":
		return runReport(rest)
	case "profiles", "profile":
		return runProfiles(rest)
	case "theme", "themes", "colour", "color":
		return runTheme(rest)
	default:
		// One line, then a pointer. Dumping 45 lines of usage to stderr scrolls the actual error
		// off the screen, which is the opposite of what an error is for.
		fmt.Fprintf(os.Stderr, "evalctl: unknown command %q\ntry: evalctl help   (or evalctl <verb> -h)\n", cmd)
		return 2
	}
}

// ---------------------------------------------------------------- plumbing

type common struct {
	root string
	s    *store.Store
}

func newFS(name string) (*flag.FlagSet, *common) {
	c := &common{}
	fs := flag.NewFlagSet("evalctl "+name, flag.ContinueOnError)
	fs.StringVar(&c.root, "root", "", "project root (default: nearest dir holding the eval tree)")
	return fs, c
}

// parseInterspersed lets flags follow the positional ref (`edit t3 --split train`), which the
// stdlib flag package refuses: it stops at the first non-flag argument. Values that look like
// flags ("-" for stdin, negative numbers) are consumed with their flag, so they survive.
func parseInterspersed(fs *flag.FlagSet, args []string) error {
	var flags, pos []string
	for i := 0; i < len(args); {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			i++
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			i++
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			// -h/-help is the flag package's own: it is not in the set, but Parse answers it with
			// the verb's defaults, which is exactly what the operator asked for.
			if name == "h" || name == "help" {
				flags = append(flags, a)
				i++
				continue
			}
			return usageErr{fmt.Errorf("unknown flag %q", a)}
		}
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			i++
			continue
		}
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i += 2
			continue
		}
		i++
	}
	return wrapUsage(fs.Parse(append(flags, pos...)))
}

// usageErr marks an error as a grammar problem ("what you typed cannot be parsed"), which is the
// only kind that should be answered with "try <verb> -h". A missing row id is not a grammar
// problem, and pointing at the flag reference for it sends the operator in the wrong direction.
type usageErr struct{ err error }

func (u usageErr) Error() string { return u.err.Error() }
func (u usageErr) Unwrap() error { return u.err }

func wrapUsage(err error) error {
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return err
	}
	return usageErr{err}
}

// parse runs the flag set and opens the tree.
func (c *common) parse(fs *flag.FlagSet, args []string) error {
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	root := c.root
	if root == "" {
		root = os.Getenv("EVALCTL_ROOT")
	}
	if root == "" {
		root = "."
	}
	s, err := store.Open(root)
	if err != nil {
		return err
	}
	c.s = s
	return nil
}

// fail is the one error contract: "evalctl: <what went wrong>", exit 2, and — only when the problem
// is the grammar of the command — a pointer to that verb's own -h. `-h` itself is not an error.
func fail(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	fmt.Fprintln(os.Stderr, "evalctl:", err)
	var ue usageErr
	if errors.As(err, &ue) {
		fmt.Fprintln(os.Stderr, "try: evalctl <verb> -h")
	}
	return 2
}

// The CLI's style table lives in theme.go, built from the active palette.

func labelStyled(label string) string {
	switch label {
	case "yes":
		return styleYes.Render("yes")
	case "no":
		return styleNo.Render("no")
	}
	return ""
}

// ---------------------------------------------------------------- refs

// parseRefs turns CLI positionals into row references. A bare number needs --lane because the
// two lanes number independently (trigger #3 and slide-builder case #3 are different rows).
func parseRefs(s *store.Store, lane string, args []string) ([]store.RowRef, error) {
	var refs []store.RowRef
	for _, arg := range args {
		ref, err := parseRef(s, lane, arg)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func parseRef(s *store.Store, lane, arg string) (store.RowRef, error) {
	raw := strings.TrimSpace(arg)
	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "trigger:") || strings.HasPrefix(lower, "t:"):
		id, err := strconv.Atoi(strings.TrimPrefix(strings.TrimPrefix(lower, "trigger:"), "t:"))
		if err != nil {
			return store.RowRef{}, fmt.Errorf("bad ref %q: expected trigger:<id>", arg)
		}
		return refTrigger(s, id)
	case strings.HasPrefix(lower, "output:") || strings.HasPrefix(lower, "o:"):
		rest := strings.TrimPrefix(strings.TrimPrefix(raw, "output:"), "o:")
		return refOutput(s, rest)
	case strings.HasPrefix(lower, "t") && isAllDigits(strings.TrimPrefix(lower, "t")):
		id, _ := strconv.Atoi(strings.TrimPrefix(lower, "t"))
		return refTrigger(s, id)
	case strings.HasPrefix(lower, "o") && isAllDigits(strings.TrimPrefix(lower, "o")):
		id, _ := strconv.Atoi(strings.TrimPrefix(lower, "o"))
		return refByID(s, store.LaneOutput, "", id)
	case strings.Contains(raw, ":"):
		return refOutput(s, raw)
	case isAllDigits(raw):
		id, _ := strconv.Atoi(raw)
		if lane == "" {
			return store.RowRef{}, fmt.Errorf("ref %q is ambiguous: pass --lane trigger or --lane output", arg)
		}
		return refByID(s, lane, "", id)
	}
	return store.RowRef{}, fmt.Errorf("bad ref %q", arg)
}

func refTrigger(s *store.Store, id int) (store.RowRef, error) {
	return refByID(s, store.LaneTrigger, "", id)
}

func refOutput(s *store.Store, rest string) (store.RowRef, error) {
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		skill, idPart := rest[:i], rest[i+1:]
		id, err := strconv.Atoi(idPart)
		if err != nil {
			return store.RowRef{}, fmt.Errorf("bad output ref %q: expected skill:id", rest)
		}
		return refByID(s, store.LaneOutput, skill, id)
	}
	id, err := strconv.Atoi(rest)
	if err != nil {
		return store.RowRef{}, fmt.Errorf("bad output ref %q: expected skill:id", rest)
	}
	return refByID(s, store.LaneOutput, "", id)
}

func refByID(s *store.Store, lane, skill string, id int) (store.RowRef, error) {
	var hits []store.RowRef
	switch lane {
	case store.LaneTrigger:
		for i, q := range s.Trigger.Queries {
			if q.ID == id {
				hits = append(hits, store.RowRef{Lane: lane, Index: i})
			}
		}
	case store.LaneOutput:
		for _, sk := range s.Skills {
			if skill != "" && sk.Name != skill {
				continue
			}
			for i, c := range sk.Output.Evals {
				if c.ID == id {
					hits = append(hits, store.RowRef{Lane: lane, Skill: sk.Name, Index: i})
				}
			}
		}
	default:
		return store.RowRef{}, fmt.Errorf("unknown lane %q", lane)
	}
	switch len(hits) {
	case 0:
		where := lane
		if skill != "" {
			where = skill
		}
		// Name what does exist. With ids 1-10 and 21-32, "no trigger row with id 12" leaves an
		// operator with a dead end; the valid range and the closest id are one line away.
		return store.RowRef{}, fmt.Errorf("no %s row with id %d — %s ids: %s",
			where, id, where, idSpan(s, lane, skill))
	case 1:
		return hits[0], nil
	default:
		var names []string
		for _, h := range hits {
			names = append(names, h.Skill)
		}
		sort.Strings(names)
		return store.RowRef{}, fmt.Errorf("id %d exists in several skills (%s): pass --lane or a skill-scoped ref like %s:%d",
			id, strings.Join(names, ", "), names[0], id)
	}
}

// idSpan is the id space a lane (or a skill's slice of it) actually holds, compressed to ranges:
// "1-10, 21-32" says in one line that 12 was never issued and that deleting rows leaves gaps.
func idSpan(s *store.Store, lane, skill string) string {
	var ids []int
	add := func(id int) { ids = append(ids, id) }
	switch lane {
	case store.LaneTrigger:
		for _, q := range s.Trigger.Queries {
			add(q.ID)
		}
	case store.LaneOutput:
		for _, sk := range s.Skills {
			if skill != "" && sk.Name != skill {
				continue
			}
			for _, c := range sk.Output.Evals {
				add(c.ID)
			}
		}
	}
	if len(ids) == 0 {
		return "none"
	}
	sort.Ints(ids)
	var parts []string
	lo, prev := ids[0], ids[0]
	for _, id := range ids[1:] {
		if id == prev+1 {
			prev = id
			continue
		}
		parts = append(parts, span(lo, prev))
		lo, prev = id, id
	}
	parts = append(parts, span(lo, prev))
	return strings.Join(parts, ", ")
}

func span(lo, hi int) string {
	if lo == hi {
		return strconv.Itoa(lo)
	}
	return fmt.Sprintf("%d-%d", lo, hi)
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------- output helpers

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// table renders rows under a header. limits caps a column (0 = natural width); every column
// is truncated to its width so one long prompt cannot push the table off screen.
func table(w io.Writer, header []string, rows [][]string, limits []int) {
	tableGroups(w, header, []tableGroup{{Rows: rows}}, limits)
}

// tableGroup is one section of a table: a label (empty for an ungrouped table) and its rows.
type tableGroup struct {
	Label string
	Rows  [][]string
}

// tableGroups prints one header over several sections, computing widths across every row so a
// section label can never make the columns wobble between groups.
func tableGroups(w io.Writer, header []string, groups []tableGroup, limits []int) {
	right := numericColumns(header, groups)
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = lipgloss.Width(h)
	}
	for _, g := range groups {
		for _, row := range g.Rows {
			for i := range widths {
				if i < len(row) {
					if n := lipgloss.Width(row[i]); n > widths[i] {
						widths[i] = n
					}
				}
			}
		}
	}
	for i := range widths {
		if i < len(limits) && limits[i] > 0 && widths[i] > limits[i] {
			widths[i] = limits[i]
		}
	}
	fmt.Fprintln(w, styleHead.Render(padRow(header, widths, right)))
	for gi, g := range groups {
		if g.Label != "" {
			if gi > 0 {
				fmt.Fprintln(w)
			}
			fmt.Fprintln(w, styleGroup.Render(fmt.Sprintf("── %s · %d", g.Label, len(g.Rows))))
		}
		for _, row := range g.Rows {
			fmt.Fprintln(w, padRow(row, widths, right))
		}
	}
}

// numericColumns reports which columns hold counts. It is derived from the data and not declared
// per call site, so a column cannot drift: every digit in a column that is all digits (or "3/4",
// "100%") ends on the same edge, and every name starts on one.
func numericColumns(header []string, groups []tableGroup) []bool {
	out := make([]bool, len(header))
	filled := make([]bool, len(header))
	for i := range out {
		out[i] = true
	}
	for _, g := range groups {
		for _, row := range g.Rows {
			for i := range out {
				if i >= len(row) || row[i] == "" || row[i] == "-" {
					continue
				}
				filled[i] = true
				if !isNumericCell(row[i]) {
					out[i] = false
				}
			}
		}
	}
	for i := range out {
		out[i] = out[i] && filled[i]
	}
	return out
}

func isNumericCell(s string) bool {
	s = strings.TrimSuffix(s, "%")
	if a, b, ok := strings.Cut(s, "/"); ok {
		return isAllDigits(a) && isAllDigits(b)
	}
	return isAllDigits(s)
}

// padRow fits cells to their columns. Numeric columns are padded on the left so their digits line
// up; a table of counts that leans left cannot be added down a column by eye.
func padRow(cells []string, widths []int, right []bool) string {
	var b strings.Builder
	for i := range widths {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		cell = truncate(cell, widths[i])
		pad := strings.Repeat(" ", max(0, widths[i]-lipgloss.Width(cell)))
		if i == len(widths)-1 {
			// No trailing padding on the last column: it is invisible on screen and it is
			// whitespace the moment anyone copies the output into a pipe or an editor.
			if i < len(right) && right[i] {
				b.WriteString(pad + cell)
			} else {
				b.WriteString(cell)
			}
			break
		}
		if i < len(right) && right[i] {
			b.WriteString(pad + cell)
		} else {
			b.WriteString(cell + pad)
		}
		b.WriteString("  ")
	}
	return b.String()
}

func truncate(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	r := []rune(s)
	if width <= 1 {
		return string(r[:width])
	}
	return string(r[:width-1]) + "…"
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// reportSave prints what changed and what the tree now says about itself.
func reportSave(s *store.Store, changes []string, touched map[string]bool) {
	for _, c := range changes {
		fmt.Println(styleChange.Render("  ✓ ") + c)
	}
	if len(changes) == 0 {
		fmt.Println(styleDim.Render("  no field changed"))
	}
	var files []string
	for f := range touched {
		if f == store.LaneTrigger {
			files = append(files, "evals/"+store.TriggerFileName)
		} else {
			files = append(files, f+"/evals/evals.json")
		}
	}
	sort.Strings(files)
	for _, f := range files {
		fmt.Println(styleDim.Render("  wrote " + f + "  (backup in .evalctl/backups/)"))
	}
	issues := s.Validate()
	errs, warns := 0, 0
	for _, i := range issues {
		if i.Severity == store.SeverityError {
			errs++
		} else {
			warns++
		}
	}
	if errs+warns == 0 {
		fmt.Println(styleDim.Render("  validate: clean"))
		return
	}
	line := fmt.Sprintf("  validate: %s, %s — evalctl validate", format.Plural(errs, "error"), format.Plural(warns, "warning"))
	if errs > 0 {
		fmt.Println(styleErr.Render(line))
	} else {
		fmt.Println(styleWarn.Render(line))
	}
}

// saveTouched writes every lane the caller touched and reports it.
func saveTouched(c *common, changes []string, touched map[string]bool) int {
	for lane := range touched {
		var err error
		if lane == store.LaneTrigger {
			err = c.s.SaveTrigger()
		} else {
			err = c.s.SaveSkill(lane)
		}
		if err != nil {
			return fail(err)
		}
	}
	reportSave(c.s, changes, touched)
	return 0
}

func runTUI(args []string, lane string) int {
	fs, c := newFS("tui")
	laneFlag := fs.String("lane", lane, "lane to open: trigger or output")
	skillFlag := fs.String("skill", "", "open scoped to this stage skill (default: the skill list)")
	// A terminal without UTF-8 gets the frame's marks and rules in ASCII rather than a row of
	// replacement boxes. auto follows the locale; $EVALCTL_GLYPHS is the default for callers that
	// wrap the binary and cannot pass a flag.
	glyphFlag := fs.String("glyphs", os.Getenv("EVALCTL_GLYPHS"), "glyph profile: auto, unicode or ascii")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *laneFlag != store.LaneTrigger && *laneFlag != store.LaneOutput {
		return fail(fmt.Errorf("--lane must be trigger or output"))
	}
	if _, err := tui.Glyphs(*glyphFlag, os.Getenv("LANG"), os.Getenv("LC_CTYPE"), os.Getenv("LC_ALL")); err != nil {
		return fail(err)
	}
	root := c.root
	if root == "" {
		root = os.Getenv("EVALCTL_ROOT")
	}
	if root == "" {
		root = "."
	}
	s, err := store.Open(root)
	if err != nil {
		return fail(err)
	}
	// No terminal (piped output, a cron job, an agent's shell): point at the CLI rather than
	// dying inside bubbletea's TTY setup.
	if info, err := os.Stdout.Stat(); err == nil && info.Mode()&os.ModeCharDevice == 0 {
		fmt.Fprintln(os.Stderr, "evalctl: the TUI needs an interactive terminal — use `evalctl ls`, `evalctl stats`, `evalctl dupes` or `evalctl validate` instead")
		return 2
	}
	if *skillFlag != "" && s.Skill(*skillFlag) == nil {
		return fail(fmt.Errorf("unknown skill %q (known: %s)", *skillFlag, strings.Join(s.KnownSkills(), ", ")))
	}
	return tui.Run(s, *skillFlag, *laneFlag, *glyphFlag)
}

// runSkills is the CLI face of the TUI's front door: one row per stage skill, so the state of a
// skill — its cases, how many carry assertions, its fixtures, its trigger rows, its findings —
// is one command instead of a filter away in each lane.
func runSkills(args []string) int {
	fs, c := newFS("skills")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	sums := c.s.Summaries()
	if *asJSON {
		if err := writeJSON(os.Stdout, sums); err != nil {
			return fail(err)
		}
		return 0
	}
	if len(sums) == 0 {
		fmt.Println(styleDim.Render("no stage skills in this tree"))
		return 0
	}
	rows := make([][]string, 0, len(sums))
	for _, sum := range sums {
		graded := "-"
		if sum.Cases > 0 {
			graded = fmt.Sprintf("%d/%d", sum.Graded, sum.Cases)
		}
		health := styleChange.Render(sum.Health())
		switch {
		case sum.Errors > 0:
			health = styleErr.Render(sum.Health())
		case sum.Warnings > 0:
			health = styleWarn.Render(sum.Health())
		}
		name := sum.Skill
		if !sum.OnDisk {
			name += " (no evals.json)"
		}
		// One number per column, the same columns the TUI draws: "3 dirs · 10 files" packed two
		// counts into one cell and had to be re-read every time.
		rows = append(rows, []string{name, fmt.Sprint(sum.Cases), graded,
			fmt.Sprint(sum.Fixtures), fmt.Sprint(sum.Files),
			fmt.Sprint(sum.Should), fmt.Sprint(sum.ShouldNot), health})
	}
	table(os.Stdout, []string{"SKILL", "CASES", "GRADED", "FIXT", "FILES", "TRIG+", "TRIG-", "HEALTH"}, rows, nil)
	fmt.Println(styleDim.Render(fmt.Sprintf(
		"  TRIG+ should load · TRIG- should not · assertions: %d across %d graded case(s)",
		totalAssertions(sums), totalGraded(sums))))
	return 0
}

func totalAssertions(sums []store.SkillSummary) int {
	n := 0
	for _, s := range sums {
		n += s.Assertions
	}
	return n
}

func totalGraded(sums []store.SkillSummary) int {
	n := 0
	for _, s := range sums {
		n += s.Graded
	}
	return n
}
