package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig gives a profile the config.yaml a link lives in. The nesting matters: `skills.config`
// is a real key of the same section, and a reader that stops at the wrong line would report its
// children as external dirs.
func writeConfig(t *testing.T, m *Manager, name, body string) {
	t.Helper()
	writeFile(t, filepath.Join(m.Home(name), "config.yaml"), body)
}

// TestExternalDirsAreReadFromTheSkillsSection: both spellings Hermes writes have to parse, and a
// neighbouring `skills.config` key must not leak into the answer.
func TestExternalDirsAreReadFromTheSkillsSection(t *testing.T) {
	m, _ := testManager(t)
	writeConfig(t, m, m.Real, `model:
  default: m
skills:
  config:
    - key: myplugin.path
      description: data dir
  external_dirs:
    - ~/work/notes-evals
    - "/opt/shared/skills"
  create_dir: /opt/brain/skills
`)

	got := m.ExternalDirs(m.Real)
	if len(got) != 2 || got[0] != "~/work/notes-evals" || got[1] != "/opt/shared/skills" {
		t.Fatalf("external dirs = %v", got)
	}

	// The one-line spelling.
	writeConfig(t, m, m.Real, "skills:\n  external_dirs: [/a/b, \"/c d\"]\n")
	if got := m.ExternalDirs(m.Real); len(got) != 2 || got[0] != "/a/b" || got[1] != "/c d" {
		t.Fatalf("flow list = %v", got)
	}

	// No config, no link.
	writeConfig(t, m, m.Real, "model:\n  default: m\n")
	if got := m.ExternalDirs(m.Real); len(got) != 0 {
		t.Fatalf("a config with no skills section reported %v", got)
	}
}

// TestLinkedToResolvesTheWayHermesDoes: a configured path is expanded (`~`, relative to the Hermes
// home) before it is compared, so "already linked" is not a string match.
func TestLinkedToResolvesTheWayHermesDoes(t *testing.T) {
	m, _ := testManager(t)
	dir := filepath.Join(m.Root, "grouped")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, m, m.Real, "skills:\n  external_dirs:\n    - "+dir+"\n")
	if !m.LinkedTo(m.Real, dir) {
		t.Fatalf("%s is not seen as linked: %v", dir, m.ExternalDirs(m.Real))
	}
	if m.LinkedTo(m.Real, filepath.Join(m.Root, "other")) {
		t.Fatal("an unlinked folder reported as linked")
	}
}

// TestAMissingExternalDirIsAProblem: Hermes skips a configured folder that is not there without a
// word, which would read as "the skill did not load". `check` is where that is said out loud.
func TestAMissingExternalDirIsAProblem(t *testing.T) {
	m, _ := testManager(t)
	gone := filepath.Join(t.TempDir(), "moved-away")
	writeConfig(t, m, m.Real, "skills:\n  external_dirs:\n    - "+gone+"\n")

	if p := m.LinkProblem(m.Real); !strings.Contains(p, "is missing") {
		t.Fatalf("link problem = %q", p)
	}
	_, problems := m.Check([]string{m.Real})
	found := false
	for _, p := range problems {
		if strings.Contains(p, "external skill dir") {
			found = true
		}
	}
	if !found {
		t.Fatalf("check did not report the dangling link: %v", problems)
	}

	// A folder that is there is not a problem.
	writeConfig(t, m, m.Real, "skills:\n  external_dirs:\n    - "+m.Root+"\n")
	if p := m.LinkProblem(m.Real); p != "" {
		t.Fatalf("a live link reported %q", p)
	}
}

// TestLinkRefusesAFolderThatIsNotThere: a link to nothing is the failure mode the whole check exists
// for, so it is refused at the door rather than written and discovered later.
func TestLinkRefusesAFolderThatIsNotThere(t *testing.T) {
	m, _ := testManager(t)
	if _, err := m.Link(m.Real, filepath.Join(t.TempDir(), "nope"), false); err == nil ||
		!strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("link to a missing folder = %v", err)
	}
	// A dry run plans the call without touching hermes or the config.
	line, err := m.Link(m.Real, m.Root, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "hermes -p "+m.Real+" config set skills.external_dirs") {
		t.Fatalf("dry-run line = %q", line)
	}
}
