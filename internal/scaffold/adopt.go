package scaffold

// Adopting a folder: the common case is not an empty directory, it is a project whose skills already
// exist — written for Hermes, never measured. Making that person retype a skill name into a starter
// would create a second copy of something already there, so `adopt` reads what the folder holds and
// adds only what is missing: the eval sets, the fixture dir, and the tree's conventions file.
//
// Nothing it writes replaces anything. A skill that already has `evals/evals.json` keeps it, a tree
// that already declares conventions keeps them (only `profile` is filled in when it is empty), and
// `SKILL.md` is never touched — it is the thing being measured.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"evalctl/internal/stage"
	"evalctl/internal/store"
)

// Found is one skill already in the folder: its name, and its path relative to the tree root.
type Found struct {
	Name string
	Rel  string
}

// Detect lists the skills a folder already holds — a directory with a SKILL.md, up to four levels
// down, skipping the folders that are never a skill (a skill's own evals/, scripts/, a workspace).
func Detect(root string) []Found {
	var out []Found
	seen := map[string]bool{}
	var walk func(dir, rel string, depth int)
	walk = func(dir, rel string, depth int) {
		if depth > 0 {
			if info, err := os.Stat(filepath.Join(dir, "SKILL.md")); err == nil && !info.IsDir() {
				name := filepath.Base(dir)
				if !seen[name] {
					seen[name] = true
					out = append(out, Found{Name: name, Rel: rel})
				}
				return
			}
		}
		if depth >= 4 {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || strings.HasPrefix(name, ".") || name == "evals" || name == "scripts" ||
				name == "references" || name == "assets" || name == "templates" || name == "node_modules" ||
				strings.HasSuffix(name, "-workspace") {
				continue
			}
			walk(filepath.Join(dir, name), filepath.ToSlash(filepath.Join(rel, name)), depth+1)
		}
	}
	walk(root, "", 0)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Report is what an adopt wrote and what it left alone, so both surfaces can say it plainly.
type Report struct {
	Skills []string `json:"skills"`
	Wrote  []string `json:"wrote"`
	Kept   []string `json:"kept"`
}

// Adopt declares an existing folder for evalctl. With no skills named it adopts whatever Detect
// found; a named skill that is not in the folder is refused, because adopt does not invent skills.
func Adopt(root string, skills []string, profileName string) (Report, error) {
	report := Report{}
	found := Detect(root)
	byName := map[string]Found{}
	for _, f := range found {
		byName[f.Name] = f
	}
	if len(skills) == 0 {
		for _, f := range found {
			skills = append(skills, f.Name)
		}
	}
	if len(skills) == 0 {
		return report, os.ErrNotExist
	}
	var plan []File
	for _, name := range skills {
		f, ok := byName[name]
		if !ok {
			return report, &MissingSkillError{Name: name, Root: root}
		}
		report.Skills = append(report.Skills, name)
		plan = append(plan, skillFiles(f.Name, f.Rel)...)
	}
	plan = append(plan, rulesForRoot(root, profileName)...)
	wrote, kept, err := Write(root, plan, false)
	report.Wrote, report.Kept = wrote, kept
	return report, err
}

// skillFiles is the eval half of one skill that is already on disk: the two sets if they are missing,
// and the fixture dir's note. SKILL.md is not in the list.
func skillFiles(name, rel string) []File {
	base := rel
	trigger := triggerFile(name)
	output := outputFile(name)
	return []File{
		{Path: filepath.ToSlash(filepath.Join(base, "evals", "evals.json")), Body: output},
		{Path: filepath.ToSlash(filepath.Join(base, "evals", store.TriggerFileName)), Body: trigger},
		{Path: filepath.ToSlash(filepath.Join(base, "evals", "files", "README.md")), Body: fixtureNote},
	}
}

// rulesForRoot writes the conventions file only when it is missing, and otherwise *merges* a profile
// into it: the tree's own fields are the operator's, and replacing them because a screen asked for a
// profile would be the tool deciding what their project means. A file that is not valid JSON is left
// alone — `validate` names it, and guessing at a half-written file is worse than saying so.
func rulesForRoot(root, profileName string) []File {
	if profileName == "" {
		return nil
	}
	path := filepath.Join(root, stage.FileName)
	b, err := os.ReadFile(path)
	if err != nil {
		return []File{RulesFile(profileName)}
	}
	var doc map[string]any
	if json.Unmarshal(b, &doc) != nil || doc == nil {
		return nil
	}
	if _, ok := doc["profile"]; ok {
		return nil
	}
	doc["profile"] = profileName
	out, _ := json.MarshalIndent(doc, "", "  ")
	return []File{{Path: stage.FileName, Body: string(out) + "\n", Replace: true}}
}

// MissingSkillError says a named skill is not in this folder, and which ones are.
type MissingSkillError struct {
	Name string
	Root string
}

func (e *MissingSkillError) Error() string {
	found := Detect(e.Root)
	names := make([]string, 0, len(found))
	for _, f := range found {
		names = append(names, f.Name)
	}
	if len(names) == 0 {
		return e.Name + ": this folder holds no skill (no directory with a SKILL.md) — `evalctl init` starts one"
	}
	return e.Name + ": no such skill in this folder (found: " + strings.Join(names, ", ") + ")"
}
