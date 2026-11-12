package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"evalctl/internal/scaffold"
	"evalctl/internal/store"
)

// runInit is the front door: a tree with one skill, one output case and one trigger set, in the
// three files the runner reads. The starter itself lives in internal/scaffold, because the TUI's
// new-project screen writes the same tree — a format described once, produced in one place.
func runInit(args []string) int {
	fs, _ := newFS("init")
	skill := fs.String("skill", "", "the skill to create (default: the directory's name)")
	dir := fs.String("dir", ".", "where to create the tree")
	profileFlag := fs.String("profile", "", "the Hermes profile this tree is measured in (default: whoever runs it)")
	force := fs.Bool("force", false, "overwrite files that already exist")
	if err := fs.Parse(args); err != nil {
		return fail(wrapUsage(err))
	}

	root, err := filepath.Abs(*dir)
	if err != nil {
		return fail(err)
	}
	name := strings.TrimSpace(*skill)
	if name == "" {
		name = scaffold.SuggestName(root)
	}
	if err := scaffold.CheckSkillName(name); err != nil {
		return fail(err)
	}

	// A tree is a project folder; a Hermes profile is not one.
	if profileName := scaffold.HermesProfile(root); profileName != "" {
		return fail(fmt.Errorf("%s is the Hermes profile %q, not a project folder — evalctl trees live "+
			"outside it:\n  mkdir -p <folder> && evalctl init --dir <folder> --profile %s\n  "+
			"evalctl profiles attach %s        # run in <folder>: then `evalctl` works from inside the profile too",
			root, profileName, profileName, profileName))
	}

	// A tree that already has skills is somebody's project: adding a second starter skill is fine,
	// silently replacing their files is not.
	if existing := scaffold.Existing(root, name); existing != "" && !*force {
		return fail(fmt.Errorf("%s already exists — pass --force to overwrite it", existing))
	}

	wrote, kept, err := scaffold.Write(root, scaffold.Tree(name, strings.TrimSpace(*profileFlag)), *force)
	if err != nil {
		return fail(err)
	}
	for _, p := range kept {
		fmt.Println(styleDim.Render("  kept " + p))
	}
	for _, p := range wrote {
		fmt.Println(styleChange.Render("  + ") + p)
	}

	fmt.Println()
	fmt.Printf("created a %s tree in %s\n\n", styleTitle.Render(name), root)
	for _, line := range initNextSteps(name, root) {
		fmt.Println(line)
	}
	return 0
}

// initNextSteps is the hand-off: what to edit, in the order the loop asks for it, and the one
// command that proves the files are well-formed before anything is spent.
func initNextSteps(skill, root string) []string {
	rel := func(p string) string { return filepath.ToSlash(filepath.Join(skill, p)) }
	return []string{
		styleTitle.Render("1 · write the skill") + "  " + rel("SKILL.md"),
		styleDim.Render("    the description is the whole of the trigger lane's input: say when the skill applies."),
		styleTitle.Render("2 · write the questions") + "  " + rel("evals/"+store.TriggerFileName),
		styleDim.Render("    replace the four placeholders with real requests. Two train, two validation; both labels in each."),
		styleTitle.Render("3 · write the case") + "  " + rel("evals/evals.json"),
		styleDim.Render("    a prompt, the expected output, and assertions the judge can check by quoting it."),
		"",
		styleTitle.Render("then:") + "  evalctl validate",
		styleDim.Render("       evalctl trigger --skill " + skill + " --dry-run   (what would run, and against which profile)"),
		styleDim.Render("       evalctl trigger --skill " + skill + "                (the first real measurement)"),
		"",
		styleDim.Render("the contract is the SOUL.md of the profile you run this from; `evalctl profiles status` names it, "),
		styleDim.Render("and `evalctl profiles check` says whether the eval sandboxes can sign in."),
	}
}
