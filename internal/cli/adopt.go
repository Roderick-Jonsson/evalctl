package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"evalctl/internal/profile"
	"evalctl/internal/scaffold"
	"evalctl/internal/stage"
)

// runAdopt declares a folder that already holds skills for evalctl: it writes the eval sets a skill
// is missing, the tree's conventions file, and — with --profile — names the profile the tree is
// measured in. It is the CLI spelling of what the TUI's door does in one screen, and it never
// replaces a file that is already there.
func runAdopt(args []string) int {
	fs, _ := newFS("adopt")
	dir := fs.String("dir", ".", "the folder holding the skills")
	profileFlag := fs.String("profile", "", "the Hermes profile this tree is measured in")
	link := fs.Bool("link", false, "also create that profile if it is missing, and link it to this folder")
	skillsFlag := fs.String("skills", "", "only these skills (default: every skill the folder holds)")
	if err := fs.Parse(args); err != nil {
		return fail(wrapUsage(err))
	}

	root, err := filepath.Abs(*dir)
	if err != nil {
		return fail(err)
	}
	if name := scaffold.HermesProfile(root); name != "" {
		return fail(fmt.Errorf("%s is the Hermes profile %q, not a project folder — attach it to a tree "+
			"instead: evalctl profiles attach %s", root, name, name))
	}

	var names []string
	for _, s := range strings.Split(*skillsFlag, ",") {
		if s = strings.TrimSpace(s); s != "" {
			names = append(names, s)
		}
	}
	found := scaffold.Detect(root)
	if len(found) == 0 {
		return fail(fmt.Errorf("no skill in %s (no directory with a SKILL.md) — `evalctl init --skill <name>` "+
			"starts one", root))
	}

	mgr := profile.New(root, nil, stage.Rules{})
	report, err := scaffold.Adopt(root, names, strings.TrimSpace(*profileFlag))
	if err != nil {
		return fail(err)
	}
	for _, p := range report.Kept {
		fmt.Println(styleDim.Render("  kept " + p))
	}
	for _, p := range report.Wrote {
		fmt.Println(styleChange.Render("  + ") + p)
	}
	if len(report.Wrote) == 0 {
		fmt.Println(styleDim.Render("  nothing to add — every skill already carries its sets"))
	}
	fmt.Println()
	fmt.Printf("adopted %s in %s\n", strings.Join(report.Skills, ", "), root)

	if !*link {
		fmt.Println(styleDim.Render(fmt.Sprintf("  next: evalctl validate · evalctl profiles link %s",
			displayProfile(mgr, *profileFlag))))
		return 0
	}

	// One command for the whole flow the TUI does in a screen: make the profile if it is not there,
	// then point it at this folder so it can be worked in by hand.
	name := displayProfile(mgr, *profileFlag)
	if name == "" {
		return fail(fmt.Errorf("--link needs --profile: the folder has to name the profile it is measured in"))
	}
	mg := profile.New(root, nil, stage.Rules{Profile: name})
	if line, _, err := mg.CreateIfMissing(name, false); err != nil {
		return fail(err)
	} else if line != "" {
		fmt.Println(line)
	}
	line, err := mg.Link(name, root, false)
	if err != nil {
		return fail(err)
	}
	fmt.Println(line)
	fmt.Println(styleDim.Render(fmt.Sprintf("  %s scans this folder beside its own skills — open it in the "+
		"desktop to work by hand; eval sandboxes stay copies", name)))
	return 0
}

// displayProfile is the profile a run would use: the flag, or the tree's own declaration, or the
// profile this process is standing in — the same resolution a run reports before it spends.
func displayProfile(m *profile.Manager, flag string) string {
	if flag = strings.TrimSpace(flag); flag != "" {
		return flag
	}
	return m.Real
}
