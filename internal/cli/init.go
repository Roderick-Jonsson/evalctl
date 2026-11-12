package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"evalctl/internal/model"
	"evalctl/internal/profile"
	"evalctl/internal/stage"
	"evalctl/internal/store"
)

// runInit is the front door: a tree with one skill, one output case and one trigger set, in the
// three files the runner reads. It exists because the format was the first hurdle for anybody who
// was not the person who wrote it — every field is documented, but a person starting a project
// should be editing an example, not assembling one from a description.
//
// The starter is deliberately small and deliberately valid: `evalctl validate` is clean on it, so
// the first run of the loop is an edit away and not a scavenger hunt through error messages.
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
	name := *skill
	if strings.TrimSpace(name) == "" {
		name = sanitizeName(filepath.Base(root))
	}
	if err := checkSkillName(name); err != nil {
		return fail(err)
	}

	// A tree is a project folder; a Hermes profile is not one. Scaffolding inside a profile would
	// give the same files a second home, with the profile's own contract around them and no
	// evalctl.json anyone else could share — the layout this tool deliberately does not read.
	if name := hermesProfileAt(root); name != "" {
		return fail(fmt.Errorf("%s is the Hermes profile %q, not a project folder — evalctl trees live "+
			"outside it:\n  mkdir -p <folder> && evalctl init --dir <folder> --profile %s\n  "+
			"evalctl profiles attach %s        # run in <folder>: then `evalctl` works from inside the profile too",
			root, name, name, name))
	}

	// A tree that already has skills is somebody's project: adding a second starter skill is fine,
	// silently replacing their files is not.
	if existing := existingSkill(root, name); existing != "" && !*force {
		return fail(fmt.Errorf("%s already exists — pass --force to overwrite it", existing))
	}

	plan := starterTree(root, name, strings.TrimSpace(*profileFlag))
	wrote := 0
	for _, f := range plan {
		path := filepath.Join(root, filepath.FromSlash(f.Path))
		if _, err := os.Stat(path); err == nil && !*force {
			fmt.Println(styleDim.Render("  kept " + f.Path))
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fail(err)
		}
		if err := os.WriteFile(path, []byte(f.Body), 0o644); err != nil {
			return fail(err)
		}
		fmt.Println(styleChange.Render("  + ") + f.Path)
		wrote++
	}

	fmt.Println()
	fmt.Printf("created a %s tree in %s\n\n", styleTitle.Render(name), root)
	for _, line := range initNextSteps(name, root) {
		fmt.Println(line)
	}
	return 0
}

// starterFile is one path and its contents. Paths are slash-separated and relative to the tree.
type starterFile struct {
	Path string
	Body string
}

// starterTree is everything `init` writes. It is a pure function so a test can assert the starter
// is valid without touching a disk.
func starterTree(root, skill, profileName string) []starterFile {
	rules := map[string]string{"note": "This project's own conventions. Every field is optional; empty means \"no convention\". " +
		"skill_order lists the skills; skill_outputs says what each skill's answer looks like; containment_prefixes " +
		"and workspace_prefixes are the folder names a run may write and a promotion may carry; env_keys are the " +
		"credentials an eval sandbox inherits; profile names the Hermes profile this tree is measured in."}
	if profileName != "" {
		rules["profile"] = profileName
	}
	rulesJSON, _ := json.MarshalIndent(rules, "", "  ")

	trigger := model.TriggerFile{
		Note: "Placeholders. Replace every query with a real request a user would type, and keep the labels honest: " +
			"should_trigger=true means this skill is the right one, false means some other skill (or none) is.",
		Queries: []model.TriggerQuery{
			{ID: 1, Skill: skill, ShouldTrigger: true, Split: store.SplitTrain,
				Why: "a request this skill exists for", Query: "TODO: a request that should load " + skill},
			{ID: 2, Skill: skill, ShouldTrigger: false, Split: store.SplitTrain,
				Why: "close enough to be tempting, but another skill's job", Query: "TODO: a request that should not load " + skill},
			{ID: 3, Skill: skill, ShouldTrigger: true, Split: store.SplitValidation,
				Why:   "same intent, different wording — the validation half must not be paraphrases of train",
				Query: "TODO: another request that should load " + skill},
			{ID: 4, Skill: skill, ShouldTrigger: false, Split: store.SplitValidation,
				Why: "a second negative the validation half can fail on", Query: "TODO: another request that should not load " + skill},
		},
	}
	output := model.OutputFile{
		SkillName: skill,
		Evals: []model.OutputCase{{
			ID:             1,
			Slug:           "example",
			Prompt:         "TODO: the request a user types, written the way they would type it.",
			ExpectedOutput: "TODO: what a good answer contains — the shape, the facts, the file it writes.",
			Files:          []string{},
			Assertions: []string{
				"TODO: one verifiable claim about the output (a file exists, a section is present, a number is right)",
			},
		}},
	}
	outputJSON, _ := json.MarshalIndent(output, "", "  ")
	triggerJSON, _ := json.MarshalIndent(trigger, "", "  ")

	return []starterFile{
		{Path: stage.FileName, Body: string(rulesJSON) + "\n"},
		{Path: filepath.ToSlash(filepath.Join(skill, "SKILL.md")), Body: skillTemplate(skill)},
		{Path: filepath.ToSlash(filepath.Join(skill, "evals", "evals.json")), Body: string(outputJSON) + "\n"},
		{Path: filepath.ToSlash(filepath.Join(skill, "evals", store.TriggerFileName)), Body: string(triggerJSON) + "\n"},
		{Path: filepath.ToSlash(filepath.Join(skill, "evals", "files", "README.md")), Body: fixtureNote},
	}
}

// skillTemplate is a SKILL.md whose frontmatter is the part the trigger lane measures: the name and
// the one paragraph an agent decides from.
func skillTemplate(skill string) string {
	title := strings.ReplaceAll(skill, "-", " ")
	if title != "" {
		title = strings.ToUpper(title[:1]) + title[1:]
	}
	return fmt.Sprintf(`---
name: %s
description: Use when <the situation a user is in> — <what this skill does, in one sentence>.
---

# %s

One paragraph of instructions: what this skill does, in what order, and what it must never do.
Write it for an agent that has just been handed a half-finished task.

## Steps

1. Say what the input is and what the output must be.
2. Do the work in the order it has to happen.
3. Check the result against the output's own rules before answering.

## What it writes

Name the files or folders this skill produces. Nothing else is its output.
`, skill, title)
}

const fixtureNote = `# Fixtures

One directory per case slug, holding the input files that case starts from.

A case's "files" list names paths relative to its fixture directory. Leave the list empty when the
case starts from nothing — an empty workspace is a fixture too.

Delete this file once you have a fixture that means something.
`

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

// hermesProfileAt names the Hermes profile a directory is, or "" when it is an ordinary folder.
// Two signals, because neither alone is enough: the path under <hermes>/profiles/, and the shape a
// profile has (SOUL.md + config.yaml + skills/).
func hermesProfileAt(dir string) string {
	hermes := os.Getenv("HERMES_ROOT")
	if hermes == "" {
		if home, err := os.UserHomeDir(); err == nil {
			hermes = filepath.Join(home, ".hermes")
		}
	}
	if hermes != "" {
		if name := profile.ProfileAt(dir, hermes); name != "" {
			return name
		}
		if same, err := filepath.EvalSymlinks(dir); err == nil {
			if root, err2 := filepath.EvalSymlinks(hermes); err2 == nil && same == root {
				return "default"
			}
		}
	}
	looks := func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}
	if looks(filepath.Join(dir, "SOUL.md")) && looks(filepath.Join(dir, "config.yaml")) &&
		looks(filepath.Join(dir, "skills")) {
		return filepath.Base(dir)
	}
	return ""
}

// checkSkillName keeps a skill name usable as a folder, a profile directory and a workspace prefix.
func checkSkillName(name string) error {
	if name == "" {
		return fmt.Errorf("--skill is empty")
	}
	if name != sanitizeName(name) {
		return fmt.Errorf("--skill %q must be lowercase letters, digits and hyphens (like %q)", name, sanitizeName(name))
	}
	if name == "evals" || strings.HasSuffix(name, "-workspace") {
		return fmt.Errorf("--skill %q is reserved: evals/ holds a skill's cases and <name>-workspace its runs", name)
	}
	return nil
}

func sanitizeName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// existingSkill names a directory the starter would collide with, so --force is an informed choice.
// An evalctl.json that is already there is not a collision: the writer keeps it and adds the skill.
func existingSkill(root, name string) string {
	if _, err := os.Stat(filepath.Join(root, name, "SKILL.md")); err == nil {
		return filepath.ToSlash(filepath.Join(name, "SKILL.md"))
	}
	return ""
}
