package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testContract = "# video-course-creator — stage contract\n\n## Workspace\nA stage writes inside MC-[topic]/.\n"

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testManager(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "contract.md"), testContract)
	writeFile(t, filepath.Join(root, "slide-builder", "SKILL.md"), "---\nname: slide-builder\n---\n")
	writeFile(t, filepath.Join(root, "slide-builder", "references", "style.md"), "style\n")
	writeFile(t, filepath.Join(root, "slide-builder", "evals", "evals.json"), "{}")
	writeFile(t, filepath.Join(root, "slide-builder", "references", ".DS_Store"), "noise")
	return &Manager{
		Root:            root,
		Hermes:          filepath.Join(t.TempDir(), "hermes"),
		Skills:          []string{"slide-builder"},
		MasterclassRoot: filepath.Join(t.TempDir(), "masterclasses"),
		Bin:             "hermes",
	}, root
}

func TestRenderStampsOnceAndLeavesBothArmsIdentical(t *testing.T) {
	m, _ := testManager(t)
	for _, name := range []string{With, Without} {
		if _, err := m.Render(name, false); err != nil {
			t.Fatal(err)
		}
	}
	with, _ := os.ReadFile(m.Soul(With))
	without, _ := os.ReadFile(m.Soul(Without))
	if string(with) != string(without) {
		t.Fatalf("arms differ:\n%s\n---\n%s", with, without)
	}
	if n := strings.Count(string(with), "<!-- stage-contract "); n != 1 {
		t.Fatalf("want exactly one stamp, got %d", n)
	}
	if n := strings.Count(string(with), contractHead); n != 1 {
		t.Fatalf("want exactly one contract heading, got %d", n)
	}
	// Re-rendering replaces the block instead of appending a second copy.
	if _, err := m.Render(With, false); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(m.Soul(With))
	if string(again) != string(with) {
		t.Fatal("render is not idempotent")
	}
}

func TestRenderKeepsThePersonaAboveTheContract(t *testing.T) {
	m, _ := testManager(t)
	writeFile(t, m.Soul(With), "You are Hermes, built by Nous Research.\n\nBe concise.\n")
	if _, err := m.Render(With, false); err != nil {
		t.Fatal(err)
	}
	text, _ := os.ReadFile(m.Soul(With))
	if !strings.HasPrefix(string(text), "You are Hermes, built by Nous Research.\n\nBe concise.") {
		t.Fatalf("persona was cut wrong:\n%s", text)
	}
	if !strings.Contains(string(text), "A stage writes inside MC-") {
		t.Fatalf("contract body missing:\n%s", text)
	}
	// A second render must not stack the persona or the block.
	before := string(text)
	if _, err := m.Render(With, false); err != nil {
		t.Fatal(err)
	}
	text, _ = os.ReadFile(m.Soul(With))
	if string(text) != before {
		t.Fatalf("persona re-render drifted:\n%s", text)
	}
}

func TestTreeHashIgnoresTestsAndEditorNoise(t *testing.T) {
	m, root := testManager(t)
	first, err := m.TreeHash("slide-builder")
	if err != nil {
		t.Fatal(err)
	}
	// evals/ and .DS_Store are not part of the installed skill.
	writeFile(t, filepath.Join(root, "slide-builder", "evals", "evals.json"), `{"changed":true}`)
	writeFile(t, filepath.Join(root, "slide-builder", ".DS_Store"), "junk")
	unchanged, _ := m.TreeHash("slide-builder")
	if unchanged != first {
		t.Fatal("hash moved on files that are never installed")
	}
	writeFile(t, filepath.Join(root, "slide-builder", "SKILL.md"), "---\nname: slide-builder\n---\n# changed\n")
	moved, _ := m.TreeHash("slide-builder")
	if moved == first {
		t.Fatal("hash ignored a change to the installed SKILL.md")
	}
}

func TestSyncSkillsInstallsExactlyTheWantedSet(t *testing.T) {
	m, _ := testManager(t)
	m.Skills = []string{"slide-builder", "source-research"}
	writeFile(t, filepath.Join(m.Root, "source-research", "SKILL.md"), "---\nname: source-research\n---\n")

	if _, err := m.SyncSkills(With, []string{"slide-builder"}, false); err != nil {
		t.Fatal(err)
	}
	if got := m.InstalledSkills(With); len(got) != 1 || got[0] != "slide-builder" {
		t.Fatalf("installed = %v", got)
	}
	if _, err := m.SyncSkills(With, []string{"source-research"}, false); err != nil {
		t.Fatal(err)
	}
	if got := m.InstalledSkills(With); len(got) != 1 || got[0] != "source-research" {
		t.Fatalf("the previous case's skill was left behind: %v", got)
	}
	if _, err := m.SyncSkills(With, []string{"not-a-skill"}, false); err == nil {
		t.Fatal("expected an unknown-skill refusal")
	}
	// The installed copy must not carry the evals or the editor noise.
	if _, err := os.Stat(filepath.Join(m.SkillsDir(Without), "slide-builder", "evals")); err == nil {
		t.Fatal("evals/ was installed into a sandbox")
	}
}

func TestScrubWipesMemoriesButNeverSoul(t *testing.T) {
	m, _ := testManager(t)
	if _, err := m.Render(With, false); err != nil {
		t.Fatal(err)
	}
	mem := filepath.Join(m.Home(With), "memories")
	writeFile(t, filepath.Join(mem, "notes.md"), "a lesson")
	writeFile(t, filepath.Join(mem, "MEMORY.md"), "index")
	line, err := m.Scrub(With, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "notes.md") {
		t.Fatalf("scrub did not name what it wiped: %q", line)
	}
	if entries, _ := os.ReadDir(mem); len(entries) != 0 {
		t.Fatalf("memories survived: %v", entries)
	}
	if _, err := os.Stat(m.Soul(With)); err != nil {
		t.Fatal("scrub touched SOUL.md")
	}
	if line, _ := m.Scrub(Real, false); !strings.Contains(line, "refused") {
		t.Fatalf("scrub must refuse the real profile: %q", line)
	}
}

// TestCheckNamesEveryDeviation builds the intended state on disk instead of calling `ensure`:
// ensure shells out to the real `hermes` binary, and a unit test must never touch a real profile.
func TestCheckNamesEveryDeviation(t *testing.T) {
	m, _ := testManager(t)
	m.MasterclassRoot = t.TempDir() // must exist, and only the real profile pins it
	for _, name := range []string{Real, With, Without, Trigger} {
		skills := m.Plan(name)
		if name == With {
			skills = []string{"slide-builder"}
		}
		if _, err := m.Render(name, false); err != nil {
			t.Fatal(err)
		}
		if _, err := m.SyncSkills(name, skills, false); err != nil {
			t.Fatal(err)
		}
		config := "\nmodel:\n  default: test-model\n"
		if name == Real {
			config += "\nterminal:\n  cwd: " + m.MasterclassRoot + "\n"
		}
		writeFile(t, filepath.Join(m.Home(name), "config.yaml"), config)
		writeFile(t, filepath.Join(m.Home(name), ".env"), "OLLAMA_API_KEY=x\n")
	}
	rows, problems := m.Check([]string{Real, With, Without, Trigger})
	if len(problems) != 0 {
		t.Fatalf("an intended-state tree should be clean, got %v", problems)
	}
	if len(rows) != 4 {
		t.Fatalf("rows = %d", len(rows))
	}

	// A stale stamp is the classic drift: contract.md changed, SOUL.md did not.
	writeFile(t, filepath.Join(m.Root, "contract.md"), testContract+"\n## New rule\nDon't.\n")
	_, problems = m.Check([]string{Real, With, Without, Trigger})
	if !strings.Contains(strings.Join(problems, "\n"), "stale contract") {
		t.Fatalf("stale contract not reported: %v", problems)
	}

	// A sandbox that pins a working directory breaks the --in rule.
	writeFile(t, filepath.Join(m.Home(Trigger), "config.yaml"),
		"\nmodel:\n  default: test-model\n\nterminal:\n  cwd: /tmp/somewhere\n")
	_, problems = m.Check([]string{Trigger})
	if !strings.Contains(strings.Join(problems, "\n"), "must not pin a working directory") {
		t.Fatalf("pinned sandbox not reported: %v", problems)
	}
}

// fakeHermesBin stands in for `hermes` on the two profile verbs: create makes a live profile (an
// identity marker), delete removes it and leaves the tombstone Hermes leaves beside profiles/.
func fakeHermesBin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fake-hermes")
	script := `#!/bin/sh
eval "last=\${$#}"
case "$1 $2" in
  "profile create")
    mkdir -p "$HERMES_ROOT/profiles/$3"
    printf 'description: fake sandbox\n' > "$HERMES_ROOT/profiles/$3/profile.yaml"
    rm -f "$HERMES_ROOT/profiles/.deleted/$3"
    ;;
  "profile delete")
    rm -rf "$HERMES_ROOT/profiles/$last"
    mkdir -p "$HERMES_ROOT/profiles/.deleted"
    : > "$HERMES_ROOT/profiles/.deleted/$last"
    ;;
esac
exit 0
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// TestLiveAsksForAnIdentityMarker: a directory is a profile only when it carries one of Hermes's
// identity markers and has not been tombstoned. A crashed create leaves the first state and a
// finished delete leaves the second, and both would be misread by os.Stat alone.
func TestLiveAsksForAnIdentityMarker(t *testing.T) {
	m, _ := testManager(t)
	if m.Live(With) {
		t.Fatal("a profile that was never created reads as live")
	}
	if err := os.MkdirAll(m.Home(With), 0o755); err != nil {
		t.Fatal(err)
	}
	if m.Live(With) {
		t.Fatal("an empty directory reads as a profile — that is the shell a crashed create leaves")
	}
	writeFile(t, filepath.Join(m.Home(With), "profile.yaml"), "description: x\n")
	if !m.Live(With) {
		t.Fatal("a directory carrying profile.yaml does not read as live")
	}
	writeFile(t, m.Tombstone(With), "deleted\n")
	if m.Live(With) {
		t.Fatal("a tombstoned directory still reads as live")
	}
}

// TestRemoveOnlyEverDeletesASandbox: the teardown path must not be able to touch the real profile,
// and it must go through `hermes profile delete` rather than removing the directory itself.
func TestRemoveOnlyEverDeletesASandbox(t *testing.T) {
	m, _ := testManager(t)
	t.Setenv("HERMES_ROOT", m.Hermes)
	m.Bin = fakeHermesBin(t)
	writeFile(t, filepath.Join(m.Home(Real), "profile.yaml"), "description: real\n")

	for _, name := range []string{Real, "default", "someone-elses-profile"} {
		if _, err := m.Remove(name, false); err == nil {
			t.Fatalf("remove %s was allowed", name)
		}
	}
	if _, err := os.Stat(m.Home(Real)); err != nil {
		t.Fatal("the refused remove deleted the real profile")
	}
	if line, err := m.Remove(With, false); err != nil || !strings.Contains(line, "not provisioned") {
		t.Fatalf("removing a sandbox that is not there = %q, %v", line, err)
	}
}

// TestRemoveTombstonesAndCreateReplaces: the pair is one cycle. Delete leaves Hermes's tombstone
// behind, and create has to clear it — asking Live, not os.Stat, is what makes the second run of a
// session possible at all.
func TestRemoveTombstonesAndCreateReplaces(t *testing.T) {
	m, _ := testManager(t)
	t.Setenv("HERMES_ROOT", m.Hermes)
	m.Bin = fakeHermesBin(t)
	if _, err := m.Create(With, false); err != nil {
		t.Fatal(err)
	}
	if !m.Live(With) {
		t.Fatal("create did not produce a live profile")
	}
	if line, err := m.Remove(With, false); err != nil || line != "remove "+With {
		t.Fatalf("remove = %q, %v", line, err)
	}
	if m.Live(With) {
		t.Fatal("the profile is still live after remove")
	}
	if _, err := os.Stat(m.Tombstone(With)); err != nil {
		t.Fatalf("remove left no tombstone — the delete did not go through hermes: %v", err)
	}
	if _, err := m.Create(With, false); err != nil {
		t.Fatalf("create after remove: %v", err)
	}
	if !m.Live(With) {
		t.Fatal("the recreated profile is not live")
	}
}

// TestAMissingSandboxIsNotADeviation: with the cleanup default, `check` must be green between runs.
// Only the real profile's absence is a problem.
func TestAMissingSandboxIsNotADeviation(t *testing.T) {
	m, _ := testManager(t)
	if err := os.MkdirAll(m.MasterclassRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(m.Home(Real), "config.yaml"),
		"\nmodel:\n  default: test-model\n\nterminal:\n  cwd: "+m.MasterclassRoot+"\n")
	writeFile(t, filepath.Join(m.Home(Real), ".env"), "OLLAMA_API_KEY=x\n")
	if _, err := m.Render(Real, false); err != nil {
		t.Fatal(err)
	}
	rows, problems := m.Check([]string{Real, With, Without, Trigger, Revise})
	for _, p := range problems {
		if strings.Contains(p, "missing") {
			t.Fatalf("a removed sandbox is reported as a problem: %v", problems)
		}
	}
	if len(rows) != 5 || rows[1].Exists {
		t.Fatalf("rows = %+v", rows)
	}
}
