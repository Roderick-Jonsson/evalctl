package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"evalctl/internal/store"
)

// TestTabsWalkBothWays: a scope has three views and tab / shift+tab walk them in both directions,
// with each view keeping its own place — a tab that forgets where you were is a tab you stop using.
func TestTabsWalkBothWays(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneOutput), 140, 30)
	if m.tab != tabCases {
		t.Fatalf("a model built on the output lane opens on %v", m.tab)
	}
	var walk []viewTab
	for i := 0; i < 3; i++ {
		walk = append(walk, m.tab)
		m.Update(key("tab"))
	}
	if walk[0] != tabCases || walk[1] != tabQueries || walk[2] != tabResults {
		t.Fatalf("tab walk = %v", walk)
	}
	if m.tab != tabCases {
		t.Fatalf("three tabs did not wrap back to the first, got %v", m.tab)
	}
	m.Update(key("shift+tab"))
	if m.tab != tabResults {
		t.Fatalf("shift+tab from cases should wrap to results, got %v", m.tab)
	}

	// Each tab keeps its place.
	m.setTab(tabCases)
	m.cursor = 2
	m.setTab(tabResults)
	m.setTab(tabCases)
	if m.cursor != 2 {
		t.Fatalf("the cases tab forgot its cursor: %d", m.cursor)
	}
}

// TestResultsTabIsReadOnly: the results view offers the verbs it has and none it does not. A
// read-only table that still answers to select / edit / delete teaches the wrong key map.
func TestResultsTabIsReadOnly(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneOutput), 200, 30)
	m.setTab(tabResults)
	foot := ansi.Strip(m.hintFooter())
	for _, want := range []string{"enter report", "c chain", "r re-read", "tab next tab"} {
		if !strings.Contains(foot, want) {
			t.Fatalf("results footer is missing %q: %q", want, foot)
		}
	}
	for _, bad := range []string{"select", "edit", "delete", "new"} {
		if strings.Contains(foot, bad) {
			t.Fatalf("results footer advertises %q: %q", bad, foot)
		}
	}
	// The write keys do nothing here — not even open a confirmation.
	m.Update(key("d"))
	if m.mode == screenConfirm {
		t.Fatal("d opened a delete confirmation on the read-only results tab")
	}
	m.Update(key("n"))
	if m.mode == screenEditor {
		t.Fatal("n opened the editor on the read-only results tab")
	}
	if len(m.chosen) != 0 {
		t.Fatal("a selection was taken on the read-only results tab")
	}
	// A digit says why a column cannot be focused instead of failing silently.
	m.Update(key("2"))
	if !strings.Contains(ansi.Strip(m.statusFooter()), "no columns") {
		t.Fatalf("a digit on results said nothing useful: %q", ansi.Strip(m.statusFooter()))
	}
}

// TestEscSaysWhatItWillDo: the footer names the level esc takes, and esc takes exactly that one —
// the hint and the handler read the same answer.
func TestEscSaysWhatItWillDo(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneTrigger), 140, 30)
	if got := ansi.Strip(m.hintFooter()); !strings.Contains(got, "esc skills") {
		t.Fatalf("footer = %q", got)
	}
	m.Update(key("4")) // focus a column
	if got := ansi.Strip(m.hintFooter()); !strings.Contains(got, "esc clears") {
		t.Fatalf("footer after focusing a column = %q", got)
	}
	m.Update(key("0"))
	m.Update(key(" ")) // select a row
	if got := ansi.Strip(m.hintFooter()); !strings.Contains(got, "esc clear selection") {
		t.Fatalf("footer with a selection = %q", got)
	}
	m.Update(key("esc"))
	if len(m.chosen) != 0 || m.mode != screenList {
		t.Fatalf("esc with a selection should clear it and stay: chosen=%d mode=%v", len(m.chosen), m.mode)
	}
	m.Update(key("esc"))
	if m.mode != screenSkills {
		t.Fatalf("the second esc should leave for the front door, mode=%v", m.mode)
	}
}

// TestStatusDecaysOnTheNextMove: the status line reports what just happened, so moving on clears it.
// A message that outlives its action reads as if it were still true.
func TestStatusDecaysOnTheNextMove(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneTrigger), 140, 30)
	m.Update(key("4"))
	if !strings.Contains(ansi.Strip(m.statusFooter()), "column split") {
		t.Fatalf("status = %q", ansi.Strip(m.statusFooter()))
	}
	m.Update(key("j"))
	if got := ansi.Strip(m.statusFooter()); strings.Contains(got, "column split") {
		t.Fatalf("the status outlived the move: %q", got)
	}
}

// TestEditorAsksBeforeDiscarding: esc on an edited form is the one way in this app to lose typing,
// so it asks once — and any other key disarms it.
func TestEditorAsksBeforeDiscarding(t *testing.T) {
	s := testStore(t)
	m := sized(newModel(s, store.LaneTrigger), 120, 30)
	e, _ := m.focused()
	m.openEditor(e, false)
	m.editor.d.query = "typed but not applied"

	m.Update(key("esc"))
	if m.mode != screenEditor || m.editor == nil {
		t.Fatal("one esc threw the edit away")
	}
	if !strings.Contains(ansi.Strip(m.View()), "esc again discards") {
		t.Fatalf("the form does not say what the second esc will do:\n%s", ansi.Strip(m.View()))
	}
	m.Update(key("k")) // typing on: the arming is off again
	if m.editor.armed {
		t.Fatal("a keypress other than esc left the form armed")
	}
	m.Update(key("esc"))
	m.Update(key("esc"))
	if m.mode != screenList || m.editor != nil {
		t.Fatalf("two escs should cancel the edit: mode=%v", m.mode)
	}
	if m.store.Trigger.Queries[0].Query == "typed but not applied" {
		t.Fatal("cancelling wrote the draft")
	}
}

// TestCleanEditorLeavesOnOneEsc: nothing typed, nothing to ask about.
func TestCleanEditorLeavesOnOneEsc(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneTrigger), 120, 30)
	e, _ := m.focused()
	m.openEditor(e, false)
	m.Update(key("esc"))
	if m.mode != screenList || m.editor != nil {
		t.Fatalf("esc on an untouched form should leave at once: mode=%v", m.mode)
	}
}
