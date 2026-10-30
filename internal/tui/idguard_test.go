package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"evalctl/internal/store"
)

// TestIDEditAsksBeforeItMoves: the editor is not a back way around the CLI's id guard. A number that
// earlier iterations cite is asked for in the same dialog a delete asks in, and a refusal leaves the
// row — and the typing — exactly as they were.
func TestIDEditAsksBeforeItMoves(t *testing.T) {
	s := testStore(t)
	// #1's results are on disk: moving the id would leave them pointing at a row that is no longer it.
	if err := os.MkdirAll(filepath.Join(s.Root, "trigger-workspace", "trigger-1", "q01-alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := sized(newModel(s, store.LaneTrigger), 140, 30)
	m.openEditor(store.Entry{Lane: store.LaneTrigger, Skill: "alpha", Index: 0}, false)
	m.editor.d.id = "9"
	m.editor.apply(m)

	if m.mode != screenConfirm || m.confirm == nil || m.confirm.kind != "id" {
		t.Fatalf("an id edit did not ask first (mode %v)", m.mode)
	}
	if got := ansi.Strip(m.View()); !strings.Contains(got, "q01-alpha") || !strings.Contains(got, "move trigger #1 -> #9") {
		t.Fatalf("the dialog must name the number and what cites it, got %q", got)
	}
	if s.Trigger.Queries[0].ID != 1 {
		t.Fatal("the row moved before the question was answered")
	}

	// Anything but y keeps the id and hands the form back.
	m.Update(key("esc"))
	if m.mode != screenEditor || s.Trigger.Queries[0].ID != 1 {
		t.Fatalf("esc did not put the row back (mode %v, id %d)", m.mode, s.Trigger.Queries[0].ID)
	}
	if !strings.Contains(ansi.Strip(m.statusFooter()), "id left where it was") {
		t.Fatalf("status = %q", ansi.Strip(m.statusFooter()))
	}

	m.editor.d.id = "9"
	m.editor.apply(m)
	m.Update(key("y"))
	if s.Trigger.Queries[0].ID != 9 {
		t.Fatalf("y did not move the id: %d", s.Trigger.Queries[0].ID)
	}
	if !m.dirty {
		t.Fatal("the moved id was not marked for writing")
	}
}

// TestNewRowAsksForACitedID: creating a row is a create, but taking a number that a deleted row's
// results still carry is the same lie from the other side, so that asks too. A number nothing cites
// does not.
func TestNewRowAsksForACitedID(t *testing.T) {
	s := testStore(t)
	if err := os.MkdirAll(filepath.Join(s.Root, "trigger-workspace", "trigger-1", "q07-alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := sized(newModel(s, store.LaneTrigger), 140, 30)

	m.openEditor(store.Entry{Lane: store.LaneTrigger, Skill: "alpha"}, true)
	m.editor.d.query = "a fresh query"
	m.editor.d.id = "6" // next free, nothing cites it
	m.editor.apply(m)
	if m.mode == screenConfirm {
		t.Fatal("a free id asked a question it did not need to")
	}

	m.openEditor(store.Entry{Lane: store.LaneTrigger, Skill: "alpha"}, true)
	m.editor.d.query = "a fresh query"
	m.editor.d.id = "7" // a gap in the file, still on disk
	m.editor.apply(m)
	if m.mode != screenConfirm || !m.confirm.ask.isNew {
		t.Fatalf("taking a cited id did not ask (mode %v)", m.mode)
	}
	if got := ansi.Strip(m.View()); !strings.Contains(got, "new trigger row takes #7") {
		t.Fatalf("dialog = %q", got)
	}
}
