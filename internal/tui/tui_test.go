package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"evalctl/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	root := t.TempDir()
	write := func(path, body string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "trigger", "trigger-evals.json"), `{
  "runs": 3,
  "queries": [
    {"id": 1, "skill": "alpha", "should_trigger": true, "split": "train", "why": "clear",
     "query": "research the operator pattern and write the facts with links"},
    {"id": 2, "skill": "alpha", "should_trigger": false, "split": "train", "why": "near miss",
     "query": "paste the three numbers here in chat, no files"},
    {"id": 3, "skill": "beta", "should_trigger": true, "split": "validation", "why": "v pos",
     "query": "turn the research into a teaching order"},
    {"id": 4, "skill": "beta", "should_trigger": false, "split": "validation", "why": "v neg",
     "query": "render slide two as an image please"}
  ]
}
`)
	write(filepath.Join(root, "alpha", "evals", "evals.json"), `{
  "skill_name": "alpha",
  "evals": [{"id": 1, "slug": "a-1", "prompt": "do the alpha thing", "expected_output": "a thing", "files": []}]
}
`)
	write(filepath.Join(root, "alpha", "evals", "files", "a-1", "in.md"), "x\n")
	write(filepath.Join(root, "beta", "evals", "evals.json"), `{
  "skill_name": "beta",
  "evals": [{"id": 1, "slug": "b-1", "prompt": "do the beta thing", "expected_output": "another thing", "files": []}]
}
`)
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func key(s string) tea.KeyMsg {
	switch s {
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	if strings.HasPrefix(s, "alt+") {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.TrimPrefix(s, "alt+")), Alt: true}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func sized(m *Model, w, h int) *Model {
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

func TestListViewRenders(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneTrigger), 120, 30)
	view := m.View()
	for _, want := range []string{"evalctl", "all skills", "trigger queries 4", "4 of 4", "TRIG", "SPLIT",
		"research the operator pattern"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	// The header draws both lanes of the scope, so the other lane is a tab you can see, not a key
	// you have to know; switching to it must bring its columns with it.
	if !strings.Contains(view, "output cases 2") {
		t.Fatalf("header does not name the other lane:\n%s", view)
	}
	// tab walks the scope's three views in workflow order: queries → cases → results → queries.
	m.Update(key("tab"))
	after := m.View()
	if !strings.Contains(ansi.Strip(after), "output cases 2") || !strings.Contains(after, "PROMPT") {
		t.Fatalf("tab did not walk forward to the cases lane:\n%s", after)
	}
	// The view on screen is the FILLED chip: the tab bar says where you are by fill, and tab
	// has to move the fill with the body.
	if !strings.Contains(after, tabActive.Render(" "+glyphs.Cases+" output cases 2 ")) {
		t.Fatalf("the cases tab is not the filled one after tab:\n%s", after)
	}
	m.Update(key("tab"))
	view = m.View()
	if !strings.Contains(view, tabActive.Render(" "+glyphs.Results+" results")) {
		t.Fatalf("tab did not reach the results view:\n%s", view)
	}
	// And one more step wraps to the first tab, the queries lane.
	m.Update(key("tab"))
	if !strings.Contains(m.View(), tabActive.Render(" "+glyphs.Queries+" trigger queries 4")) {
		t.Fatalf("tab did not wrap back to the queries view:\n%s", m.View())
	}
}

// TestHeaderNamesTheBuild: the header carries the version, so a screenshot or a session is always
// traceable to a build without running `evalctl version` in another pane.
func TestHeaderNamesTheBuild(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneTrigger), 120, 30)
	if !strings.HasPrefix(m.ver, "v") {
		t.Fatalf("model version = %q", m.ver)
	}
	if !strings.Contains(m.View(), "evalctl ") || !strings.Contains(ansi.Strip(m.View()), "evalctl "+m.ver) {
		t.Fatalf("header does not name the build:\n%s", m.View())
	}
}

func TestFilterAndSelectAll(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneTrigger), 120, 30)
	m.filter.Label = "no"
	m.refresh()
	if len(m.rows) != 2 {
		t.Fatalf("label filter kept %d rows, want 2", len(m.rows))
	}
	m.Update(key("u"))
	if len(m.rows) != 0 || !m.onlyDup {
		t.Fatalf("dupes filter should hide every distinct row, got %d", len(m.rows))
	}
	m.Update(key("u"))
	m.filter.Label = ""
	m.refresh()
	m.Update(key("a"))
	if len(m.chosen) != 4 {
		t.Fatalf("select-all chose %d rows, want 4", len(m.chosen))
	}
	m.Update(key("A"))
	if len(m.chosen) != 0 {
		t.Fatalf("clear left %d chosen", len(m.chosen))
	}
}

func TestDeleteSelectionThenSave(t *testing.T) {
	s := testStore(t)
	m := sized(newModel(s, store.LaneTrigger), 120, 30)
	m.Update(key("down")) // move to #2
	m.Update(key(" "))    // select #2, advance to #3
	m.Update(key("d"))    // ask to delete the selection
	if m.mode != screenConfirm {
		t.Fatalf("d did not open the confirm screen (mode %v)", m.mode)
	}
	if !strings.Contains(m.View(), "delete 1 row") {
		t.Fatalf("confirm screen does not state the count:\n%s", m.View())
	}
	m.Update(key("y"))
	if len(m.store.Trigger.Queries) != 3 {
		t.Fatalf("store has %d queries, want 3", len(m.store.Trigger.Queries))
	}
	if !m.dirty {
		t.Fatal("delete did not mark the model dirty")
	}
	m.Update(key("ctrl+s"))
	if m.dirty {
		t.Fatal("save did not clear the dirty flag")
	}
	raw, err := os.ReadFile(s.TriggerPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"id": 2`) {
		t.Fatalf("deleted row is still on disk:\n%s", raw)
	}
	if !strings.Contains(string(raw), `"id": 3`) {
		t.Fatalf("survivor missing from disk:\n%s", raw)
	}
}

func TestCancelDeleteTouchesNothing(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneTrigger), 120, 30)
	m.Update(key("d")) // focused row, no selection
	if m.mode != screenConfirm {
		t.Fatal("delete did not ask for confirmation")
	}
	m.Update(key("esc"))
	if m.mode != screenList || m.dirty {
		t.Fatalf("cancel left mode=%v dirty=%v", m.mode, m.dirty)
	}
	if len(m.store.Trigger.Queries) != 4 {
		t.Fatal("cancel removed a row anyway")
	}
}

func TestEditorAppliesEditAndKeepsExtras(t *testing.T) {
	s := testStore(t)
	m := sized(newModel(s, store.LaneTrigger), 120, 30)
	e, _ := m.focused()
	m.openEditor(e, false)
	if m.mode != screenEditor || m.editor == nil {
		t.Fatal("editor did not open")
	}
	m.editor.d.query = "research the operator pattern and cite every claim"
	m.editor.d.label = "should not trigger"
	m.editor.d.split = "validation"
	m.editor.apply(m)
	if m.mode != screenList {
		t.Fatalf("apply left the editor open (err %q)", m.editor.errMsg)
	}
	q := s.Trigger.Queries[0]
	if q.Query != "research the operator pattern and cite every claim" {
		t.Fatalf("query not saved: %q", q.Query)
	}
	if q.ShouldTrigger || q.Split != "validation" {
		t.Fatalf("label/split not saved: %+v", q)
	}
	if !m.dirty {
		t.Fatal("edit did not mark dirty")
	}
}

func TestEditorRejectsBadSlugWithoutLosingText(t *testing.T) {
	s := testStore(t)
	m := sized(newModel(s, store.LaneOutput), 120, 30)
	e, _ := m.focused()
	m.openEditor(e, false)
	m.editor.d.slug = "Not A Slug"
	m.editor.d.prompt = "typed text worth keeping"
	m.editor.apply(m)
	if m.mode != screenEditor {
		t.Fatal("bad slug was accepted")
	}
	if m.editor.d.prompt != "typed text worth keeping" {
		t.Fatal("apply wiped the prompt on error")
	}
	if s.Skill("alpha").Output.Evals[0].Slug != "a-1" {
		t.Fatal("store changed despite the error")
	}
}
