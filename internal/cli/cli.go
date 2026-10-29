// Package cli is the non-interactive face of evalctl: read (ls/show/stats/dupes), write
// (add/edit/rm/mv/renumber), validate, and run — both eval lanes, plus the results browser and
// the profile commands. Running lives in internal/runner; this package only parses flags and
// prints progress.
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"evalctl/internal/store"
	"evalctl/internal/tui"
)

// Version is stamped at build time (-ldflags "-X evalctl/internal/cli.Version=...").
var Version = "dev"

const usage = `evalctl — manage and run the eval sets: trigger queries and output cases.

  evalctl                          interactive TUI (list, filter, multi-select, edit, run)
  evalctl tui                      same, explicit
  evalctl ls                       list rows (--lane, --skill, --label, --split, --search, --dupes)
  evalctl show <ref>               one row in full (ref: t12, o:skill:3, or 12 --lane)
  evalctl add  --lane trigger ...  create a row
  evalctl edit <ref> ...           change only the flags you pass
  evalctl rm   <ref...> | --filters  delete rows (--with-fixture for output cases)
  evalctl mv   <ref> --to-skill S  move an output case to another stage skill
  evalctl dupes                    near-duplicate groups, the trim shortlist
  evalctl stats                    counts per skill / label / split
  evalctl validate                 check the tree against the runner's rules
  evalctl renumber                 close id gaps (breaks comparability with old results)

  evalctl run [flags]              output lane: each case with and without the skill
  evalctl trigger [flags]          trigger lane: does the right skill load?
  evalctl runs                     iterations under evals/results, with pass rates
  evalctl report <run>             print an iteration's report.md
  evalctl profiles [cmd]           status/check/ensure/render/sync/scrub the eval profiles

Run flags: --skill, --case, --iteration, --arms, --model, --budget, --resume, --force,
           --no-verify, --isolate=false, --dry-run, --list, --log full|tools|off
Trigger:   --query, --split, --runs, --threshold, --max-turns (same shared flags)

--log full (default) streams each arm's tool calls and prose to stderr while it runs, and writes
the same lines to activity.log beside stream.jsonl; tools drops the prose, off writes nothing.

Global: --root DIR (default: nearest dir with the eval tree, or $EVALCTL_ROOT)
Refs:   12 --lane trigger | t12 | trigger:12 | o:slide-builder:3 | slide-builder:3
Every write to the sets is atomic and leaves a copy in .evalctl/backups/.
`

// Run dispatches a command line and returns the process exit code.
func Run(args []string) int {
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
		fmt.Println("evalctl", Version)
		return 0
	case "tui":
		return runTUI(rest, store.LaneTrigger)
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
	default:
		fmt.Fprintf(os.Stderr, "evalctl: unknown command %q\n\n%s", cmd, usage)
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
			return fmt.Errorf("unknown flag %q", a)
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
	return fs.Parse(append(flags, pos...))
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

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "evalctl:", err)
	return 2
}

var (
	styleDim    = lipgloss.NewStyle().Faint(true)
	styleHead   = lipgloss.NewStyle().Bold(true).Underline(true)
	styleYes    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleNo     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleDupe   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleChange = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleErr    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleTitle  = lipgloss.NewStyle().Bold(true)
)

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
		return store.RowRef{}, fmt.Errorf("no %s row with id %d", where, id)
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
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = lipgloss.Width(h)
	}
	for _, row := range rows {
		for i := range widths {
			if i < len(row) {
				if n := lipgloss.Width(row[i]); n > widths[i] {
					widths[i] = n
				}
			}
		}
	}
	for i := range widths {
		if i < len(limits) && limits[i] > 0 && widths[i] > limits[i] {
			widths[i] = limits[i]
		}
	}
	fmt.Fprintln(w, styleHead.Render(padRow(header, widths)))
	for _, row := range rows {
		fmt.Fprintln(w, padRow(row, widths))
	}
}

func padRow(cells []string, widths []int) string {
	var b strings.Builder
	for i := range widths {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		cell = truncate(cell, widths[i])
		if i == len(widths)-1 {
			b.WriteString(cell)
			break
		}
		b.WriteString(cell)
		b.WriteString(strings.Repeat(" ", widths[i]-lipgloss.Width(cell)+2))
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
	line := fmt.Sprintf("  validate: %d error(s), %d warning(s) — evalctl validate", errs, warns)
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
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *laneFlag != store.LaneTrigger && *laneFlag != store.LaneOutput {
		return fail(fmt.Errorf("--lane must be trigger or output"))
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
	return tui.Run(s, *laneFlag)
}
