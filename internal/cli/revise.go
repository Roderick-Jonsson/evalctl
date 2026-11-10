package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"evalctl/internal/diff"
	"evalctl/internal/runner"
)

// runRevise is step 1 of the guide's loop with an agent doing the writing: it reads one iteration's
// failed assertions, the operator's feedback and the skill's text, and asks the revise sandbox for a
// new SKILL.md. It prints the proposal and a diff and writes nothing — applying is `--apply`, and it
// is the only path in this tool that overwrites a skill body from a machine's answer.
func runRevise(args []string) int {
	fs, c := newFS("revise")
	skill := fs.String("skill", "", "the stage skill to revise")
	iteration := fs.Int("iteration", 0, "iteration whose evidence the revision answers (default: newest)")
	model := fs.String("model", "", "pass -m MODEL to the revision call")
	budget := fs.Int("budget", 0, "wall-clock seconds for the call (default 300)")
	apply := fs.Bool("apply", false, "write the proposal into SKILL.md (backed up first)")
	dryRun := fs.Bool("dry-run", false, "print the prompt and the command, write nothing")
	keep := fs.Bool("keep", false, "keep the reviser's sandbox profile when the call ends (removed by default)")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	if strings.TrimSpace(*skill) == "" {
		return fail(usageErr{fmt.Errorf("--skill is required: a revision answers one skill's iteration")})
	}
	sk := c.s.Skill(*skill)
	if sk == nil {
		return fail(fmt.Errorf("unknown skill %q (known: %s)", *skill, strings.Join(c.s.SkillNames(), ", ")))
	}
	r := runner.New(c.s)
	iter := *iteration
	if iter == 0 {
		iter = r.LatestQualityIteration(*skill)
	}
	if iter == 0 {
		return fail(fmt.Errorf("%s has no iteration to revise from — run one first", *skill))
	}
	opts := runner.ReviseOptions{Model: *model, DryRun: *dryRun, Budget: 300 * time.Second, Keep: *keep}
	if *budget > 0 {
		opts.Budget = time.Duration(*budget) * time.Second
	}
	if *dryRun {
		prompt, in, err := r.BuildRevisionPrompt(*skill, iter)
		if err != nil {
			return fail(err)
		}
		fmt.Printf("%s@%d · %d failed case(s) · %d feedback · %d passing\n\n", *skill, iter, in.Failed, in.Feedbacks, in.Passed)
		for _, line := range strings.Split(prompt, "\n") {
			fmt.Println(styleDim.Render(line))
		}
		fmt.Println(styleDim.Render("\ndry run: nothing was called and nothing was written"))
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	rev, err := r.Revise(ctx, *skill, iter, opts)
	if err != nil {
		return fail(err)
	}
	lines := diff.Lines(rev.Baseline, rev.Text)
	added, removed := diff.Stat(lines)
	fmt.Printf("%s@%d · revision %d · +%d −%d\n\n", rev.Skill, rev.Iteration, rev.N, added, removed)
	if rev.Why != "" {
		fmt.Println("why: " + rev.Why)
		fmt.Println()
	}
	// The same hunked shape the screen draws: a change and its neighbourhood, with the untouched
	// middle folded — a whole-file diff of a skill is not a review, it is a document.
	prev := 0
	for _, h := range diff.Hunks(lines, 3) {
		if h.From > prev {
			fmt.Println(styleDim.Render(fmt.Sprintf("  ⋯ %d unchanged", h.From-prev)))
		}
		added, removed := diff.StatOf([]diff.Hunk{h}, nil)
		fmt.Println(styleHead.Render(fmt.Sprintf("  @@ - %d,%d + %d,%d @@  +%d −%d",
			h.OldStart, h.OldCount, h.NewStart, h.NewCount, added, removed)))
		for _, l := range h.Lines {
			fmt.Println(paintDiff(l))
		}
		prev = h.To
	}
	if prev < len(lines) {
		fmt.Println(styleDim.Render(fmt.Sprintf("  ⋯ %d unchanged", len(lines)-prev)))
	}
	fmt.Println()
	fmt.Println(styleDim.Render(fmt.Sprintf("proposal: %s", rev.Path)))
	if !*apply {
		fmt.Println(styleDim.Render(fmt.Sprintf("SKILL.md is untouched — `evalctl revise --skill %s --iteration %d --apply` writes it", rev.Skill, rev.Iteration)))
		return 0
	}
	if err := r.ApplyRevision(*skill, rev); err != nil {
		return fail(err)
	}
	fmt.Printf("%s written — back up is in .evalctl/backups/ · the next iteration measures it\n",
		filepath.Join(sk.Dir, "SKILL.md"))
	return 0
}

// paintDiff is one diff line as the shell shows it: green addition, red removal, dim context.
func paintDiff(l diff.Line) string {
	switch l.Kind {
	case diff.Add:
		return styleChange.Render("+ " + l.Text)
	case diff.Del:
		return styleErr.Render("- " + l.Text)
	}
	return styleDim.Render("  " + l.Text)
}
