// Package scaffold writes the starter tree `evalctl init` and the TUI's new-project screen both
// produce: one skill with its description, its trigger set, its first case and a fixture dir, plus
// the tree's optional conventions file.
//
// It exists because the format was the first hurdle for anybody who did not write the tool — every
// field is documented, but a person starting a project should be editing an example, not assembling
// one from a description. The starter is deliberately small and deliberately valid: `validate` is
// clean on it, so the first run of the loop is one edit away and not a scavenger hunt.
//
// Two surfaces, one writer: the CLI prints what it wrote, the TUI shows the same plan in a form.
package scaffold

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

// File is one path and its contents. Paths are slash-separated and relative to the tree. Replace
// marks a file that is a merge rather than a fresh one — the only case where an existing file is
// rewritten, and only by the caller that computed the merged body from it.
type File struct {
	Path    string
	Body    string
	Replace bool
}

// RulesFile is the tree's conventions file, on its own — `adopt` writes it without touching a skill.
func RulesFile(profileName string) File {
	rules := map[string]string{"note": "This project's own conventions. Every field is optional; empty means \"no convention\". " +
		"skill_order lists the skills; skill_outputs says what each skill's answer looks like; containment_prefixes " +
		"and workspace_prefixes are the folder names a run may write and a promotion may carry; env_keys are the " +
		"credentials an eval sandbox inherits; profile names the Hermes profile this tree is measured in."}
	if profileName != "" {
		rules["profile"] = profileName
	}
	b, _ := json.MarshalIndent(rules, "", "  ")
	return File{Path: stage.FileName, Body: string(b) + "\n"}
}

// Tree is everything a starter tree holds. It is a pure function, so a test can assert the starter
// is valid without touching a disk.
func Tree(skill, profileName string) []File {
	triggerJSON, outputJSON := triggerFile(skill), outputFile(skill)

	return []File{
		RulesFile(profileName),
		{Path: filepath.ToSlash(filepath.Join(skill, "SKILL.md")), Body: skillTemplate(skill)},
		{Path: filepath.ToSlash(filepath.Join(skill, "evals", "evals.json")), Body: string(outputJSON) + "\n"},
		{Path: filepath.ToSlash(filepath.Join(skill, "evals", store.TriggerFileName)), Body: string(triggerJSON) + "\n"},
		{Path: filepath.ToSlash(filepath.Join(skill, "evals", "files", "README.md")), Body: fixtureNote},
	}
}

// Summary is the one line a form shows before it writes: the four files, by their paths.
func Summary(skill string) string {
	skill = strings.TrimSpace(skill)
	if skill == "" {
		skill = "<skill>"
	}
	var paths []string
	for _, f := range Tree(skill, "") {
		paths = append(paths, f.Path)
	}
	return strings.Join(paths, ", ")
}

// Write puts the files on disk. An existing file is kept unless force says otherwise, and both lists
// are returned so a caller can print what happened rather than guess.
func Write(root string, files []File, force bool) (wrote, kept []string, err error) {
	for _, f := range files {
		path := filepath.Join(root, filepath.FromSlash(f.Path))
		if _, statErr := os.Stat(path); statErr == nil && !force && !f.Replace {
			kept = append(kept, f.Path)
			continue
		}
		if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return wrote, kept, err
		}
		if err = os.WriteFile(path, []byte(f.Body), 0o644); err != nil {
			return wrote, kept, err
		}
		wrote = append(wrote, f.Path)
	}
	return wrote, kept, nil
}

// SuggestName is the skill name a folder would get: its own name, made legal.
func SuggestName(dir string) string { return Sanitize(filepath.Base(dir)) }

// Sanitize makes a name usable as a folder, a profile directory and a workspace prefix.
func Sanitize(name string) string {
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

// CheckSkillName is the rule a skill's folder name has to satisfy, said in one line.
func CheckSkillName(name string) error {
	if name == "" {
		return fmt.Errorf("the skill needs a name")
	}
	if name != Sanitize(name) {
		return fmt.Errorf("%q must be lowercase letters, digits and hyphens (like %q)", name, Sanitize(name))
	}
	if name == "evals" || strings.HasSuffix(name, "-workspace") {
		return fmt.Errorf("%q is reserved: evals/ holds a skill's cases and <name>-workspace its runs", name)
	}
	return nil
}

// Existing names a file the starter would collide with. An evalctl.json that is already there is not
// a collision: the writer keeps it and adds the skill.
func Existing(root, skill string) string {
	if _, err := os.Stat(filepath.Join(root, skill, "SKILL.md")); err == nil {
		return filepath.ToSlash(filepath.Join(skill, "SKILL.md"))
	}
	return ""
}

// HermesProfile names the Hermes profile a directory is, or "" when it is an ordinary folder. A tree
// is a project folder: scaffolding inside a profile would give the same files a second home, with the
// profile's own contract around them and no evalctl.json anybody could share.
func HermesProfile(dir string) string {
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
	// The shape of a profile, for a Hermes home that is not where this machine keeps it.
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

// triggerFile is a skill's trigger set: four placeholders, both labels in both loop halves, so the
// first measurement is a real one and not a one-sided rate.
func triggerFile(skill string) string {
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
	b, _ := json.MarshalIndent(trigger, "", "  ")
	return string(b) + "\n"
}

// outputFile is one placeholder case: a prompt, the expected answer, and one assertion the judge can
// check by quoting the output.
func outputFile(skill string) string {
	output := model.OutputFile{
		SkillName: skill,
		Evals: []model.OutputCase{{
			ID: 1,
			// The slug names the fixture dir and the result dir, and it is unique across the tree —
			// a starter is written for every skill somebody adopts, so "example" twice is a real
			// collision on the second skill.
			Slug:           skill + "-1",
			Prompt:         "TODO: the request a user types, written the way they would type it.",
			ExpectedOutput: "TODO: what a good answer contains — the shape, the facts, the file it writes.",
			Files:          []string{},
			Assertions: []string{
				"TODO: one verifiable claim about the output (a file exists, a section is present, a number is right)",
			},
		}},
	}
	b, _ := json.MarshalIndent(output, "", "  ")
	return string(b) + "\n"
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
