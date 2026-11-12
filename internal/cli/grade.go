package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"evalctl/internal/runner"
)

// runGrade is the judge step of the output loop: one call per arm, verdicts written into the arm's
// grading.json, then the benchmark and the report re-aggregated. It reads a finished iteration, so
// it is safe to run it again after editing an assertion — --force re-grades, no flag skips what is
// already judged.
func runGrade(args []string) int {
	fs, c := newFS("grade")
	skill := fs.String("skill", "", "the stage skill whose iteration gets graded")
	iteration := fs.Int("iteration", 0, "iteration number (default: the newest on disk)")
	cases := fs.String("case", "", "comma-separated case ids, default all in that iteration")
	model := fs.String("model", "", "pass -m MODEL to the judge call")
	force := fs.Bool("force", false, "re-grade arms that already carry verdicts")
	dryRun := fs.Bool("dry-run", false, "print the judge commands, write nothing")
	budget := fs.Int("budget", 0, "wall-clock seconds per judge call (default 180)")
	keep := fs.Bool("keep", false, "keep the judge's sandbox profile when the pass ends (removed by default)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	if strings.TrimSpace(*skill) == "" {
		return fail(usageErr{fmt.Errorf("--skill is required: grading reads one skill's iteration")})
	}
	if c.s.Skill(*skill) == nil {
		return fail(fmt.Errorf("unknown skill %q (known: %s)", *skill, strings.Join(c.s.SkillNames(), ", ")))
	}
	opts := runner.GradeOptions{Model: *model, CaseIDs: ids(*cases), Force: *force, DryRun: *dryRun, Keep: *keep}
	if *budget > 0 {
		opts.Budget = time.Duration(*budget) * time.Second
	}
	// A grade recreates the judge's sandbox and removes it, so it is a writer like a run: the same
	// lock keeps it off a live run's profiles and off a concurrent reap.
	r := runner.New(c.s)
	unlock, err := lockRun(r.LockPath(), r.ProfileName())
	if err != nil {
		return fail(err)
	}
	defer unlock()
	level, _ := runner.ParseActivity("off")
	r.Emit = progressPrinter(os.Stdout, os.Stderr, os.Stderr, level)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	out, err := r.GradeIteration(ctx, *skill, *iteration, opts)
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		if err := writeJSON(os.Stdout, out); err != nil {
			return fail(err)
		}
		return 0
	}
	if *dryRun {
		return 0
	}
	// The per-arm lines and the closing EventDone carry the summary; the only thing left to say is
	// when there was nothing to read at all.
	if out.Graded == 0 && out.Skipped == 0 {
		fmt.Println(styleDim.Render(fmt.Sprintf("%s@%d: no arm directory to grade — run it first",
			out.Skill, out.Iteration)))
	}
	return 0
}
