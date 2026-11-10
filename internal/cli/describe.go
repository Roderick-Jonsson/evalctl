package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"evalctl/internal/diff"
	"evalctl/internal/llm"
	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// runDescribe is the description lane's step 3 with a model doing the writing: it reads one measured
// wording's train failures and asks the provider for a new paragraph. It prints the character-level
// diff and writes nothing — `--write` records the paragraph as the ledger's next candidate, and even
// then the run is what puts it into SKILL.md.
//
// It exists for two reasons, and the second is the one that matters on a bad day: a shell is how the
// call is exercised without a terminal (the TUI path cannot be driven from a test), and it is how a
// description gets rewritten without leaving the tree's own record behind.
func runDescribe(args []string) int {
	fs, c := newFS("describe")
	skill := fs.String("skill", "", "the skill whose description is rewritten")
	candidate := fs.Int("candidate", 0, "the measured wording to revise (default: the newest measured one)")
	model := fs.String("model", "", "override the model for this call ($EVALCTL_DESCRIBE_MODEL)")
	budget := fs.Int("budget", 0, "wall-clock seconds for the call (default 180)")
	dryRun := fs.Bool("dry-run", false, "print the prompt and the provider, write nothing")
	write := fs.Bool("write", false, "record the proposal as the ledger's next candidate (SKILL.md is untouched)")
	jsonOut := fs.Bool("json", false, "the proposal as JSON")
	if err := c.parse(fs, args); err != nil {
		return fail(err)
	}
	if strings.TrimSpace(*skill) == "" {
		return fail(usageErr{fmt.Errorf("--skill is required: a description belongs to one skill")})
	}
	if c.s.Skill(*skill) == nil {
		return fail(fmt.Errorf("unknown skill %q (known: %s)", *skill, strings.Join(c.s.SkillNames(), ", ")))
	}

	r := runner.New(c.s)
	id := *candidate
	if id == 0 {
		lab, err := r.DescriptionLab(*skill)
		if err != nil {
			return fail(err)
		}
		// The newest wording a train run actually measured. A draft written after it has no failures
		// to answer, so it is not the candidate a proposal is about.
		for i := len(lab.Candidates) - 1; i >= 0; i-- {
			if lab.Candidates[i].MeasureOn(store.SplitTrain) != nil {
				id = lab.Candidates[i].ID
				break
			}
		}
		if id == 0 {
			return fail(fmt.Errorf("%s has no measured wording — run the train set first (evalctl trigger --skill %s)",
				*skill, *skill))
		}
	}

	opts := runner.DescribeOptions{Budget: runner.DescriptionBudget, Model: *model, DryRun: *dryRun}
	if *budget > 0 {
		opts.Budget = time.Duration(*budget) * time.Second
	}
	cfg, err := llm.FromEnv()
	if err != nil {
		return fail(err)
	}
	opts.Config = &cfg

	if *dryRun {
		prompt, in, err := r.BuildDescriptionPrompt(*skill, id)
		if err != nil {
			return fail(err)
		}
		fmt.Printf("%s · candidate #%d · %d chars · %d failed · %d passed\n", *skill, in.Candidate, in.Chars, in.Fails, in.Passes)
		fmt.Printf("%s · %s · key from %s\n\n", cfg.Model, cfg.ChatURL(), cfg.KeyFrom)
		for _, line := range strings.Split(prompt, "\n") {
			fmt.Println(styleDim.Render(line))
		}
		fmt.Println(styleDim.Render("\ndry run: nothing was called and nothing was written"))
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	prop, err := r.ReviseDescription(ctx, *skill, id, opts)
	if err != nil {
		return fail(err)
	}
	if *jsonOut {
		if err := writeJSON(os.Stdout, prop); err != nil {
			return fail(err)
		}
		return 0
	}
	printProposal(prop)
	if !*write {
		fmt.Println(styleDim.Render(fmt.Sprintf(
			"nothing was written — `evalctl describe --skill %s --candidate %d --write` records it as the next candidate",
			prop.Skill, prop.Candidate)))
		return 0
	}
	if _, err := runner.New(c.s).SaveDescriptionCandidate(prop.Skill, prop.Text); err != nil {
		return fail(err)
	}
	fmt.Printf("draft staged in %s-workspace — `evalctl lab --skill %s` says what the loop asks for next\n", prop.Skill, prop.Skill)
	return 0
}

// printProposal is the shell's version of the screen: what the model changed, and the paragraph with
// its removals and its additions marked in the same two colours the revision screen uses.
func printProposal(prop runner.DescriptionProposal) {
	segs := diff.Segments(prop.Baseline, prop.Text)
	added, removed := diff.SegmentsStat(segs)
	fmt.Printf("%s · candidate #%d · %s · +%d −%d characters\n\n", prop.Skill, prop.Candidate, prop.Model, added, removed)
	if prop.Why != "" {
		fmt.Println(styleWarn.Render("why: ") + prop.Why)
		fmt.Println()
	}
	for _, line := range diff.WrapSegs(segs, 100) {
		var b strings.Builder
		for _, s := range line {
			switch s.Kind {
			case diff.Del:
				b.WriteString(styleErr.Render(s.Text))
			case diff.Add:
				b.WriteString(styleChange.Render(s.Text))
			default:
				b.WriteString(styleTitle.Render(s.Text))
			}
		}
		fmt.Println("  " + b.String())
	}
	fmt.Println()
	fmt.Println(styleDim.Render("red is gone, green is new · the wording on screen is " + fmt.Sprint(prop.Chars) + " characters"))
	if len(prop.Issues) > 0 {
		fmt.Println()
		for _, issue := range prop.Issues {
			fmt.Println(styleWarn.Render("! ") + issue)
		}
	}
}
