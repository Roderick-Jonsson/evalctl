package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/store"
)

// TestOpensTheRealTree is the smoke test the fixtures cannot be: it opens a copy of the repository's
// own eval tree — the real skills, the real trigger set, the real iterations — and draws every step
// of the flow for every skill.
//
// It copies the tree instead of opening it because opening a skill adopts its description into the
// ledger, and a test must not write into the repository it is testing.
func TestOpensTheRealTree(t *testing.T) {
	src := "../../.."
	if _, err := os.Stat(filepath.Join(src, "trigger", "trigger-evals.json")); err != nil {
		t.Skip("no eval tree above the tool")
	}
	root := copyTree(t, src)
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.KnownSkills()) == 0 {
		t.Skip("the tree has no skills")
	}
	m := newModel(s)
	m.width, m.height = 120, 34

	// The list first: one row per lane of work, and no frame wider than the window.
	if len(m.pick.rows) == 0 {
		t.Fatal("the work list is empty on a tree with skills")
	}
	fits := func(what string) {
		t.Helper()
		view := m.View()
		if got := len(strings.Split(view, "\n")); got != m.height {
			t.Fatalf("%s: %d rows in a %d-row frame", what, got, m.height)
		}
		for _, line := range strings.Split(view, "\n") {
			if w := len([]rune(line)); w > m.width {
				t.Fatalf("%s: a line is %d cells in a %d-cell frame", what, w, m.width)
			}
		}
	}
	fits("the work list")

	// Then every lane, both halves of a set, and the state a closed loop opens on.
	for _, r := range m.pick.rows {
		m.openWork(r)
		fits("work · " + r.skill + " · " + r.badge)
		if r.lane == store.LaneTrigger && m.flow != nil {
			for _, sec := range []int{secIteration, secGeneralization} {
				m.flow.sec, m.flow.step = sec, 1
				m.reload()
				fits(fmt.Sprintf("work · %s · %s", r.skill, secNames[sec]))
			}
		}
	}
}

// copyTree copies what the loop reads — the trigger set, each skill's case file and SKILL.md, and the
// two records of every trigger iteration — into a temp dir. Nothing else: a run's workspace is
// megabytes of agent output that no screen here opens.
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	copyFile(t, filepath.Join(src, "trigger", "trigger-evals.json"), filepath.Join(dst, "trigger", "trigger-evals.json"))
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasSuffix(e.Name(), "-workspace") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		for _, rel := range []string{filepath.Join("evals", "evals.json"), "SKILL.md"} {
			if _, err := os.Stat(filepath.Join(src, e.Name(), rel)); err == nil {
				copyFile(t, filepath.Join(src, e.Name(), rel), filepath.Join(dst, e.Name(), rel))
			}
		}
	}
	iters, _ := filepath.Glob(filepath.Join(src, "trigger-workspace", "trigger-*"))
	for _, dir := range iters {
		for _, rel := range []string{"meta.json", "trigger.json"} {
			if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
				copyFile(t, filepath.Join(dir, rel), filepath.Join(dst, "trigger-workspace", filepath.Base(dir), rel))
			}
		}
	}
	return dst
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(fmt.Errorf("%s: %w", to, err))
	}
}
