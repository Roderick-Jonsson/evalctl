package profile

// The profile a tree is measured in has a second job: it is where a person opens the skills by hand,
// in the desktop, outside any eval run. For that it needs the skills — and the honest way to give
// them to it is the link Hermes already supports, not a copy.
//
//   - Hermes scans `skills.external_dirs` (a list under `skills:` in the profile's config.yaml)
//     beside its own `~/.hermes/skills/`. The skills appear in the index, `skills_list`, `skill_view`
//     and `/skill-name` exactly like installed ones.
//   - Nothing is copied, so the profile cannot go stale: an edit in the project folder is the edit
//     the profile sees. That is the point — the skills a person works with are the skills evalctl
//     measures.
//   - The link is written by Hermes's own config writer (`hermes -p <name> config set`), because a
//     hand-edited config.yaml is one duplicate key away from a profile that silently loses settings.
//   - A configured folder that does not exist is skipped without an error, so the link is verified
//     on write and re-checked by `profiles check`. A moved project folder must not look like
//     "the skill did not load".
//
// Eval sandboxes never get a link: they are copies with the evals stripped, and an arm that could
// read its own answers, or write to the tree, would not be measuring the skill.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ExternalDirs is the profile's skills.external_dirs: the folders Hermes scans beside its own
// skills/. Unreadable or absent config means no link.
func (m *Manager) ExternalDirs(name string) []string {
	b, err := os.ReadFile(filepath.Join(m.Home(name), "config.yaml"))
	if err != nil {
		return nil
	}
	return listUnder(string(b), "skills", "external_dirs")
}

// LinkedTo reports whether the profile scans dir.
func (m *Manager) LinkedTo(name, dir string) bool {
	want := resolved(dir)
	for _, have := range m.ExternalDirs(name) {
		if resolved(expandHomePath(have, m.Hermes)) == want {
			return true
		}
	}
	return false
}

// All lists the live profiles on this machine, by name: the default profile when the Hermes home is
// one, then the named ones. Eval sandboxes are left out — they exist for the length of a run.
func (m *Manager) All() []string {
	var out []string
	if info, err := os.Stat(filepath.Join(m.Hermes, "SOUL.md")); err == nil && !info.IsDir() {
		out = append(out, DefaultReal)
	}
	entries, err := os.ReadDir(filepath.Join(m.Hermes, "profiles"))
	if err != nil {
		return out
	}
	var named []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || SandboxShape(name) {
			continue
		}
		if m.Live(name) {
			named = append(named, name)
		}
	}
	sort.Strings(named)
	return append(out, named...)
}

// CreateIfMissing makes the profile when it is missing, and says whether it had to. A tree may name a
// profile nobody has created yet; the first run then has somewhere to measure instead of a refusal.
//
// It is not `Ensure`: that brings a profile to the state a *sandbox* needs (skills synced, memories
// wiped). This one creates the identity and nothing else — Hermes writes the SOUL.md and the model
// default, which is exactly what a profile made by hand would start from.
func (m *Manager) CreateIfMissing(name string, dry bool) (line string, created bool, err error) {
	if name == "" {
		return "", false, nil
	}
	if m.Live(name) {
		return "profile " + name + ": exists", false, nil
	}
	line, err = m.Create(name, dry)
	return line, true, err
}

// Link points a profile at a folder to scan for skills. It is the lab half of the split: the profile
// works with the tree's skills by hand, while evalctl keeps measuring throwaway copies.
func (m *Manager) Link(name, dir string, dry bool) (string, error) {
	if name == "" {
		return "", fmt.Errorf("link: no profile named")
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("link %s: %s is not a directory — there is nothing to scan", name, dir)
	}
	if m.LinkedTo(name, dir) {
		return fmt.Sprintf("link %s: already scans %s", name, dir), nil
	}
	value := "[" + strconv.Quote(dir) + "]"
	msg := fmt.Sprintf("link %s ← %s (skills.external_dirs — no copy)", name, dir)
	if dry {
		return msg + fmt.Sprintf(" [hermes -p %s config set skills.external_dirs %s]", name, value), nil
	}
	if _, err := os.Stat(m.Home(name)); err != nil {
		return "", fmt.Errorf("link %s: no profile at %s", name, m.Home(name))
	}
	out, err := exec.Command(m.Bin, "-p", name, "config", "set", "skills.external_dirs", value).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("hermes config set skills.external_dirs failed:\n%s", strings.TrimSpace(string(out)))
	}
	// Hermes's config writer is the authority; a value it stored as one scalar is skipped by the
	// loader, and the symptom would be a profile that looks linked and has no skills.
	if !m.LinkedTo(name, dir) {
		return "", fmt.Errorf("link %s: hermes reported success but %s does not list %s — check %s",
			name, filepath.Join(m.Home(name), "config.yaml"), dir, "skills.external_dirs")
	}
	return msg, nil
}

// Unlink removes the profile's external_dirs, returning it to its own skills/ only.
func (m *Manager) Unlink(name string, dry bool) (string, error) {
	dirs := m.ExternalDirs(name)
	if len(dirs) == 0 {
		return "unlink " + name + ": no external_dirs set", nil
	}
	msg := fmt.Sprintf("unlink %s: drops %s", name, strings.Join(dirs, ", "))
	if dry {
		return msg + " [hermes -p " + name + " config unset skills.external_dirs]", nil
	}
	out, err := exec.Command(m.Bin, "-p", name, "config", "unset", "skills.external_dirs").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("hermes config unset skills.external_dirs failed:\n%s", strings.TrimSpace(string(out)))
	}
	if left := m.ExternalDirs(name); len(left) > 0 {
		return "", fmt.Errorf("unlink %s: hermes reported success but %s still lists %s", name,
			filepath.Join(m.Home(name), "config.yaml"), strings.Join(left, ", "))
	}
	return msg, nil
}

// LinkProblem is the one drift a link can suffer: the folder it names is gone, and Hermes skips a
// missing external dir without a word.
func (m *Manager) LinkProblem(name string) string {
	for _, dir := range m.ExternalDirs(name) {
		path := expandHomePath(dir, m.Hermes)
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return fmt.Sprintf("%s: external skill dir %s is missing — Hermes skips it silently; "+
				"restore the folder, or `evalctl profiles unlink %s`", name, dir, name)
		}
	}
	return ""
}

// expandHomePath resolves a configured path the way Hermes does: `~` and ${VAR}, relative to the
// Hermes home.
func expandHomePath(entry, hermes string) string {
	p := os.ExpandEnv(entry)
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(hermes, p)
	}
	return p
}

// listUnder reads one block list out of a YAML file: a top-level `section:`, the `key:` below it,
// and the `- item` lines that belong to that key. It handles both spellings Hermes writes — a block
// list and a one-line flow list — and stops at the next key, so `skills.config` above or below it
// cannot leak into the answer.
func listUnder(text, section, key string) []string {
	var out []string
	inSection, inKey := false, false
	secIndent, keyIndent := 0, 0
	for _, raw := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
		isItem := strings.HasPrefix(trimmed, "- ") || trimmed == "-"

		if inKey {
			if isItem && indent > keyIndent {
				out = append(out, unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))))
				continue
			}
			// Any other line at or above the key ends the list.
			inKey = false
		}
		if inSection {
			if indent <= secIndent {
				inSection = false
			} else if strings.HasPrefix(trimmed, key+":") {
				rest := strings.TrimSpace(strings.TrimPrefix(trimmed, key+":"))
				if rest == "" {
					inKey, keyIndent = true, indent
					continue
				}
				out = append(out, flowItems(rest)...)
				continue
			}
		}
		if !inSection && indent == 0 && strings.HasPrefix(trimmed, section+":") {
			inSection, secIndent = true, indent
		}
	}
	return out
}

// flowItems splits `[/a, "/b"]`, the one-line spelling of a list.
func flowItems(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := unquote(strings.TrimSpace(part)); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
