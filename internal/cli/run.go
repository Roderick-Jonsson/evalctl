package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"evalctl/internal/markdown"
	"evalctl/internal/profile"
	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// runCommand is the shared plumbing for the two lanes: flags that mean the same thing, one
// progress printer, one exclusive lock so two runs never share a profile home.
type runCommand struct {
	skills    string
	model     string
	budget    int
	resume    bool
	force     bool
	fill      bool
	dryRun    bool
	list      bool
	keep      bool
	iteration int
	lane      string
	logLevel  string
}

func (rc *runCommand) register(fs *flag.FlagSet) {
	fs.StringVar(&rc.skills, "skill", "all", "all (default) or comma-separated skill names")
	fs.StringVar(&rc.model, "model", "", "pass -m MODEL to hermes chat")
	fs.IntVar(&rc.budget, "budget", 0, "wall-clock seconds per run (default 1200 output, 240 trigger)")
	fs.BoolVar(&rc.resume, "resume", false, "skip arms/queries that already finished")
	fs.BoolVar(&rc.force, "force", false, "with --resume: rerun anyway")
	fs.BoolVar(&rc.fill, "fill", false,
		"output lane: write into the newest iteration while it is still open, instead of starting a new number")
	fs.BoolVar(&rc.dryRun, "dry-run", false, "print the plan and the commands, change nothing")
	fs.BoolVar(&rc.list, "list", false, "list the selected cases and exit")
	fs.BoolVar(&rc.keep, "keep", false, "keep the sandbox profiles when the run ends (they are removed by default)")
	fs.IntVar(&rc.iteration, "iteration", 0, "iteration number (default: next free)")
	fs.StringVar(&rc.lane, "lane", store.LaneOutput, "lane: output (default) or trigger")
	fs.StringVar(&rc.logLevel, "log", "full", "live agent activity on stderr: full, tools or off")
}

func (rc *runCommand) skillsList() []string {
	var out []string
	for _, s := range strings.Split(rc.skills, ",") {
		if s = strings.TrimSpace(s); s != "" && s != "all" {
			out = append(out, s)
		}
	}
	return out
}

// ids parses "1,6,9"; an empty string means "every row".
func ids(raw string) []int {
	var out []int
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if n, err := strconv.Atoi(part); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// ---------------------------------------------------------------- run / trigger

func runRun(args []string) int {
	fs, c := newFS("run")
	rc := runCommand{}
	rc.register(fs)
	cases := fs.String("case", "", "comma-separated case ids, default all")
	arms := fs.String("arms", strings.Join(runner.AllArms, ","), "with_skill,without_skill")
	noVerify := fs.Bool("no-verify", false, "skip the skill's scripts/ verify checks")
	grade := fs.Bool("grade", true, "output lane: grade the new iteration with the judge after running")
	noGrade := fs.Bool("no-grade", false, "output lane: run the arms and stop — no verdicts, no iteration in the loop")
	isolate := fs.Bool("isolate", true, "run in a temp root and copy the result back (default); "+
		"--isolate=false runs inside the results dir, for debugging only")
	queries := fs.String("query", "", "trigger lane: comma-separated query ids, default all")
	split := fs.String("split", "all", "trigger lane: all, train or validation")
	runs := fs.Int("runs", 0, "trigger lane: runs per query (default from the file, else 3)")
	threshold := fs.Float64("threshold", 0, "trigger lane: pass threshold on the trigger rate")
	maxTurns := fs.Int("max-turns", 0, "trigger lane: --max-turns per run (default 8)")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	opts := runner.Options{
		Lane: rc.lane, Skills: rc.skillsList(), CaseIDs: ids(*cases), QueryIDs: ids(*queries),
		Split: *split, Arms: strings.Split(*arms, ","), Model: rc.model, Resume: rc.resume,
		Force: rc.force, Fill: rc.fill, NoVerify: *noVerify, Isolate: *isolate, DryRun: rc.dryRun, List: rc.list,
		Iteration: rc.iteration, Runs: *runs, Threshold: *threshold, MaxTurns: *maxTurns,
		Grade: *grade && !*noGrade, Keep: rc.keep,
	}
	if rc.budget > 0 {
		opts.Budget = time.Duration(rc.budget) * time.Second
	}
	for i, arm := range opts.Arms {
		opts.Arms[i] = strings.TrimSpace(arm)
	}
	level, err := runner.ParseActivity(rc.logLevel)
	if err != nil {
		return fail(err)
	}
	opts.Log = level
	for _, name := range rc.skillsList() {
		if err := needSkill(c.s, name); err != nil {
			return fail(err)
		}
	}

	r := runner.New(c.s)
	// A dry run's "progress" lines are its document: the banner and the case headers belong to the
	// plan, so they stay on stdout with the commands under them. A real run's are messaging, and go
	// to stderr where a redirected stdout cannot collect them.
	progress := os.Stderr
	if rc.dryRun {
		progress = os.Stdout
	}
	r.Emit = progressPrinter(os.Stdout, progress, os.Stderr, level)
	return execute(c.s, r, opts)
}

// execute runs a lane under the exclusive lock, with Ctrl-C wired to cancellation. The lock is the
// measured profile's, so a second checkout measuring the same profile waits rather than grading a
// half-synced sandbox.
func execute(s *store.Store, r *runner.Runner, opts runner.Options) int {
	unlock, err := lockRun(r.LockPath(), r.ProfileName())
	if err != nil {
		return fail(err)
	}
	defer unlock()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var runErr error
	if opts.Lane == runner.LaneTrigger {
		runErr = r.RunTrigger(ctx, opts)
	} else {
		runErr = r.RunQuality(ctx, opts)
	}
	if runErr != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			fmt.Fprintln(os.Stderr, styleWarn.Render("evalctl: cancelled — finished arms are kept, resume with --resume"))
			return 130
		}
		return fail(runErr)
	}
	return 0
}

// lockRun keeps two runners off one profile at a time: an eval arm writes skills/ and memories/
// inside its sandbox, so a second concurrent run would grade a half-synced profile. The file is the
// profile's (see Runner.LockPath), which is what makes two trees measuring one profile collide.
func lockRun(lockPath, profileName string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("another evalctl run is already measuring profile %s (lock: %s) — wait for it, "+
			"or `evalctl profiles reap` if it was killed", profileName, lockPath)
	}
	_ = f.Truncate(0)
	_, _ = f.WriteString(fmt.Sprintf("%d\n", os.Getpid()))
	return func() {
		_ = unlockFile(f)
		f.Close()
	}, nil
}

// lockTree is the write lock for changes to the sets themselves (a promotion writes fixtures a run
// might be staging). It is separate from the run lock, which belongs to the profile: two trees can
// promote at once, but not while a run is measuring either of them.
func lockTree(root string) (func(), error) {
	dir := filepath.Join(root, ".evalctl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "tree.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("another evalctl writer is editing this tree (lock: %s)", path)
	}
	return func() {
		_ = unlockFile(f)
		f.Close()
	}, nil
}

// progressPrinter is the CLI face of the runner's events: one line per thing that happened.
//
// Two writers, because the two kinds of line are read differently: out (stdout) carries what the
// command was asked for — the plan, the case list, the closing summary — and progress (stderr)
// carries the fact that it is happening, so `evalctl run --list > cases.txt` never collects
// "with_skill: running…" between its lines. Arms run one at a time and announce themselves before
// their first activity line, so the activity needs no per-line tag.
func progressPrinter(out, progress, activity io.Writer, level runner.ActivityLevel) func(runner.Event) {
	return func(e runner.Event) {
		switch e.Kind {
		case runner.EventStart:
			fmt.Fprintln(progress, "\n"+styleTitle.Render(e.Message))
		case runner.EventCase:
			fmt.Fprintln(progress, "\n"+styleTitle.Render(e.Message))
		case runner.EventArmStart, runner.EventTriggerRun:
			fmt.Fprintf(progress, "  %s: running…\n", e.Arm)
		case runner.EventActivity:
			if level == runner.ActivityOff {
				return
			}
			fmt.Fprintln(activity, "    "+styleActivity(e.Message))
		case runner.EventArmDone:
			line := "  " + e.Message
			if e.Result != nil && !e.Result.Checks.Containment.OK {
				fmt.Fprintln(progress, styleErr.Render(line))
				return
			}
			fmt.Fprintln(progress, styleYes.Render(line))
		case runner.EventPlan:
			fmt.Fprintln(out, styleDim.Render(e.Message))
		case runner.EventMessage:
			fmt.Fprintln(out, e.Message)
		case runner.EventScored, runner.EventDone:
			fmt.Fprintln(out, "\n"+styleTitle.Render(e.Message))
		}
	}
}

// styleActivity colours one activity line by what it is, the same glyphs the TUI uses.
func styleActivity(line string) string {
	switch {
	case strings.HasPrefix(line, "▸"):
		return styleDupe.Render(line)
	case strings.HasPrefix(line, "✓"):
		return styleYes.Render(line)
	case strings.HasPrefix(line, "✗"):
		return styleErr.Render(line)
	}
	return styleDim.Render(line)
}

// ---------------------------------------------------------------- results

func runRuns(args []string) int {
	fs, c := newFS("runs")
	asJSON := fs.Bool("json", false, "machine-readable output")
	lane := fs.String("lane", "", "only one lane: output or trigger")
	skill := fs.String("skill", "", "only one skill's iterations: its output runs and the trigger runs that measured it")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	if err := noPositional(fs); err != nil {
		return fail(err)
	}
	if err := needSkill(c.s, *skill); err != nil {
		return fail(err)
	}
	r := runner.New(c.s)
	infos := r.ListRunsFor(*skill)
	if *lane != "" && *lane != store.LaneTrigger && *lane != store.LaneOutput {
		return fail(fmt.Errorf("--lane must be output or trigger"))
	}
	if *asJSON {
		if err := writeJSON(os.Stdout, infos); err != nil {
			return fail(err)
		}
		return 0
	}
	var rows [][]string
	for _, info := range infos {
		if *lane != "" && info.Lane != *lane {
			continue
		}
		// Percent only: "100% (4/4)" printed the same fact twice, and the ratio is in the report.
		pass := "-"
		if info.PassRate != nil {
			pass = fmt.Sprintf("%.0f%%", *info.PassRate*100)
		}
		runsCell := ""
		if info.Runs > 0 {
			runsCell = fmt.Sprintf("%d", info.Runs)
		}
		// Scoped to one skill, the SKILL column is a constant — and a trigger row has no skill of
		// its own to print — so it drops, the way it does in the lanes.
		cells := []string{info.Name}
		if *skill == "" {
			cells = append(cells, info.Skill)
		}
		cells = append(cells, info.Lane, fmt.Sprintf("%d", info.Cases), runsCell, pass,
			runner.ShortTime(info.Started))
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		if *skill != "" {
			fmt.Println(styleDim.Render("no iterations for " + *skill + " — `evalctl run --dry-run` plans one"))
		} else {
			fmt.Println(styleDim.Render("no iterations under any *-workspace — `evalctl run --dry-run` plans one"))
		}
		return 0
	}
	// No WORKSPACE-"—" column and no REPORT-"yes" column: a trigger run has no workspace and every
	// finished run has a report. Both columns repeated one value on every row.
	headers := []string{"RUN", "LANE", "CASES", "RUNS", "PASS", "STARTED"}
	if *skill == "" {
		headers = []string{"RUN", "SKILL", "LANE", "CASES", "RUNS", "PASS", "STARTED"}
	}
	table(os.Stdout, headers, rows, nil)
	fmt.Println()
	fmt.Println(styleDim.Render("open one with: evalctl report <skill>@<run>"))
	return 0
}

func runReport(args []string) int {
	fs, c := newFS("report")
	lane := fs.String("lane", "", "lane of the ref: output or trigger")
	raw := fs.Bool("raw", false, "print the markdown source instead of rendering it")
	width := fs.Int("width", 0, "render at this wrap width, even into a pipe")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	refs := fs.Args()
	if len(refs) != 1 {
		return fail(fmt.Errorf("usage: evalctl report <run>   (3, source-research@3, trigger-2)"))
	}
	r := runner.New(c.s)
	info, err := r.ResolveRun(refs[0], *lane)
	if err != nil {
		return fail(err)
	}
	text, err := runner.ReadReport(r.ReportPath(info))
	if err != nil {
		return fail(fmt.Errorf("%s has no report.md yet — it may still be running", info.Name))
	}
	// A pipe gets the source: the rendered form is for a human at a terminal, and `--raw` is the
	// escape hatch for one who wants it there too. Naming a width, though, is asking for the
	// layout, so it renders whatever stdout is.
	if *raw || (*width == 0 && !stdoutIsTerminal()) {
		fmt.Print(text)
		return 0
	}
	w := *width
	if w <= 0 {
		w = terminalWidth(markdown.DefaultWidth)
	}
	out, err := markdown.Render(text, w)
	if err != nil {
		fmt.Print(text)
		return 0
	}
	fmt.Print(out)
	return 0
}

// stdoutIsTerminal reports whether stdout is a terminal, so `evalctl report > file.md` keeps
// writing the markdown source instead of an ANSI-painted copy of it.
func stdoutIsTerminal() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// terminalWidth is the room stdout offers, minus a column so the last glyph never wraps.
func terminalWidth(fallback int) int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return fallback
	}
	return max(markdown.MinWidth, w-1)
}

// ---------------------------------------------------------------- profiles

func runProfiles(args []string) int {
	fs, c := newFS("profiles")
	skills := fs.String("skills", "", "sync: comma-separated skill names")
	dryRun := fs.Bool("dry-run", false, "print the plan, change nothing")
	asJSON := fs.Bool("json", false, "machine-readable output (status, check)")
	includeReal := fs.Bool("real", false, "ensure/sync/render/scrub: also write into the real profile (default: sandboxes only)")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	rest := fs.Args()
	command := "status"
	var names []string
	if len(rest) > 0 {
		switch rest[0] {
		case "status", "check", "ensure", "render", "sync", "scrub", "reap", "attach", "link", "unlink":
			command = rest[0]
			names = rest[1:]
		default:
			names = rest
		}
	}
	m := profile.New(c.s.Root, c.s.SkillNames(), c.s.Rules)

	// attach names the tree a profile is measured from. It runs before targets are resolved: the
	// profile it is given need not be one of the five (a profile with skills of its own can be
	// measured too), and the file it writes is the only thing that lets `evalctl` started inside a
	// profile find the sets instead of walking up to an empty folder.
	if command == "attach" {
		target := m.Real
		if len(names) > 0 {
			target = names[0]
		}
		path := filepath.Join(m.Home(target), store.TreeMarker)
		if _, err := os.Stat(m.Home(target)); err != nil {
			return fail(fmt.Errorf("attach %s: no profile at %s", target, m.Home(target)))
		}
		msg := fmt.Sprintf("%s ← %s", path, c.s.Root)
		if *dryRun {
			fmt.Println(msg)
			return 0
		}
		if err := os.WriteFile(path, []byte(c.s.Root+"\n"), 0o644); err != nil {
			return fail(err)
		}
		fmt.Println(msg)
		fmt.Println(styleDim.Render(fmt.Sprintf(
			"  evalctl started anywhere in %s now opens %s", target, c.s.Root)))
		return 0
	}

	targets, err := m.Named(names)
	if err != nil {
		return fail(err)
	}

	// Provisioning verbs write. The real profile is where somebody actually works, so it is opt-in
	// by name or by --real: `ensure` used to copy skills into it without saying so in its own name.
	// link/unlink write config.yaml, so they obey the same rule as the other writers: the real
	// profile is opt-in, and here it is the whole point — the link exists to make it usable by hand.
	switch command {
	case "link", "unlink":
		if len(names) == 0 {
			names = []string{m.Real}
			targets = []string{m.Real}
		}
	case "ensure", "sync", "render", "scrub":
		if !*includeReal {
			var kept []string
			for _, name := range targets {
				if name != m.Real {
					kept = append(kept, name)
				}
			}
			if len(kept) != len(targets) {
				fmt.Println(styleDim.Render(fmt.Sprintf(
					"  %s: the real profile %s is left alone — pass --real (or name it) to write there",
					command, m.Real)))
			}
			targets = kept
		}
	}

	switch command {
	case "reap":
		// Teardown for a run that died before its own cleanup. Every sandbox on disk is a leftover
		// by definition — a live one belongs to a run that is using it — which is why this takes
		// the run lock before touching anything.
		unlock, err := lockRun(m.LockPath(), m.Real)
		if err != nil {
			return fail(err)
		}
		defer unlock()
		if len(names) == 0 {
			targets = m.ReapTargets()
		}
		owned := map[string]bool{}
		for _, name := range m.Sandboxes() {
			owned[name] = true
		}
		var reaped []string
		for _, name := range targets {
			if name == m.Real {
				// The real profile is not a sandbox. Named by hand it is a mistake worth refusing;
				// reached through the default list it is simply not this verb's business.
				if len(names) > 0 {
					return fail(fmt.Errorf("reap %s: refused — the real profile is not a sandbox", name))
				}
				continue
			}
			if !m.Live(name) {
				// Nothing to delete, or a ghost a crashed create left: name it, never rmtree it.
				if _, err := os.Stat(m.Home(name)); err != nil {
					continue
				}
				fmt.Println(styleWarn.Render(fmt.Sprintf("  ✗ %s: %s is not a profile — remove that directory by hand",
					name, m.Home(name))))
				continue
			}
			if !owned[name] {
				// Sandbox-shaped but not this tree's: another project may be measuring in it right
				// now, so it is only removed when its own lock is free.
				unlock, err := lockRun(m.LockPathFor(name), name)
				if err != nil {
					fmt.Println(styleWarn.Render(fmt.Sprintf("  · %s: not this tree's and in use — left alone", name)))
					continue
				}
				fmt.Println(styleDim.Render(fmt.Sprintf("  · %s: another tree's leftover sandbox", name)))
				line, err := m.Remove(name, *dryRun)
				unlock()
				if err != nil {
					return fail(err)
				}
				fmt.Println(line)
				reaped = append(reaped, name)
				continue
			}
			line, err := m.Remove(name, *dryRun)
			if err != nil {
				return fail(err)
			}
			fmt.Println(line)
			reaped = append(reaped, name)
		}
		if len(reaped) == 0 {
			fmt.Println(styleDim.Render("nothing to reap — no sandbox is on disk"))
			return 0
		}
		if *dryRun {
			return 0
		}
		fmt.Println(styleDim.Render(fmt.Sprintf("%d sandbox(es) removed — the next run creates what it needs",
			len(reaped))))
		return 0
	case "check":
		rows, problems := m.Check(targets)
		if *asJSON {
			if err := writeJSON(os.Stdout, struct {
				Rows     []profile.Status `json:"profiles"`
				Problems []string         `json:"problems"`
			}{rows, problems}); err != nil {
				return fail(err)
			}
			if len(problems) > 0 {
				return 1
			}
			return 0
		}
		printProfileTable(m, rows)
		if len(problems) > 0 {
			for _, p := range problems {
				fmt.Println(styleErr.Render("  ✗ " + p))
			}
			return 1
		}
		fmt.Println("\n" + styleYes.Render("all profiles in their intended state"))
		return 0
	case "status":
		rows := make([]profile.Status, 0, len(targets))
		for _, name := range targets {
			rows = append(rows, m.Status(name))
		}
		if *asJSON {
			if err := writeJSON(os.Stdout, rows); err != nil {
				return fail(err)
			}
			return 0
		}
		printProfileTable(m, rows)
		// The lab half of the split: a profile with no link is a profile you cannot work in, which
		// is what made it look like clutter.
		if m.LinkedTo(m.Real, c.s.Root) {
			fmt.Println(styleDim.Render(fmt.Sprintf("  %s scans %s (external skills — no copy)", m.Real, c.s.Root)))
		} else if len(m.ExternalDirs(m.Real)) == 0 {
			fmt.Println(styleDim.Render(fmt.Sprintf(
				"  %s carries no project skills — `evalctl profiles link %s` makes it the lab you work in by hand",
				m.Real, m.Real)))
		}
		return 0
	case "sync":
		wanted := []string{}
		for _, s := range strings.Split(*skills, ",") {
			if s = strings.TrimSpace(s); s != "" {
				wanted = append(wanted, s)
			}
		}
		if len(wanted) == 0 {
			return fail(fmt.Errorf("sync needs --skills (or use ensure)"))
		}
		for _, name := range targets {
			line, err := m.SyncSkills(name, wanted, *dryRun)
			if err != nil {
				return fail(err)
			}
			fmt.Println(line)
		}
		return 0
	case "link", "unlink":
		for _, name := range targets {
			var line string
			var err error
			if command == "link" {
				line, err = m.Link(name, c.s.Root, *dryRun)
			} else {
				line, err = m.Unlink(name, *dryRun)
			}
			if err != nil {
				return fail(err)
			}
			fmt.Println(line)
		}
		if command == "link" && !*dryRun {
			fmt.Println(styleDim.Render(fmt.Sprintf(
				"  %s now scans this folder beside its own skills — open it in the desktop to work by hand; "+
					"eval sandboxes are unaffected (they are copies)", targets[0])))
		}
		return 0
	case "scrub", "render":
		for _, name := range targets {
			var line string
			var err error
			if command == "scrub" {
				line, err = m.Scrub(name, *dryRun)
			} else {
				line, err = m.Render(name, *dryRun)
			}
			if err != nil {
				return fail(err)
			}
			fmt.Println(line)
		}
		return 0
	case "ensure":
		for _, name := range targets {
			steps, err := m.Ensure(name, *dryRun)
			if err != nil {
				return fail(err)
			}
			for _, step := range steps {
				fmt.Println(step)
			}
		}
		return 0
	}
	return fail(fmt.Errorf("unknown profiles command %q", command))
}

func printProfileTable(m *profile.Manager, rows []profile.Status) {
	var tableRows [][]string
	for _, row := range rows {
		if !row.Exists {
			// A sandbox that is not on disk is the normal state between runs, not a broken one.
			tableRows = append(tableRows, []string{row.Profile, "—", "—", "—", "—", "—", "—"})
			continue
		}
		contract := "differs"
		if row.SoulOK {
			contract = "ok"
		}
		// CWD is a reading, not a rule: a profile's working directory is its own, and the harness
		// only cares that a sandbox has none.
		cwd := "—"
		if row.TerminalCwd != "" {
			cwd = row.TerminalCwd
		}
		skills := fmt.Sprintf("%d", len(row.Skills))
		memories := fmt.Sprintf("%d", len(row.Memories))
		auth := fmt.Sprintf("%d", len(row.AuthFiles))
		tableRows = append(tableRows, []string{row.Profile, skills, memories, contract, auth, row.Model, cwd})
	}
	table(os.Stdout, []string{"PROFILE", "SKILLS", "MEMORIES", "SOUL", "LOGIN", "MODEL", "CWD"}, tableRows,
		[]int{0, 0, 0, 0, 0, 0, 0})
}
