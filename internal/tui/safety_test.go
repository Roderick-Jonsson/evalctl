package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"evalctl/internal/store"
)

// TestDeleteConfirmDefaultsToCancel is the one that matters most: Enter is the key a tired hand
// presses, so Enter must cancel a delete. Only "y" may destroy a row.
func TestDeleteConfirmDefaultsToCancel(t *testing.T) {
	for _, k := range []string{"enter", "esc", "n", "q"} {
		m := sized(newModel(testStore(t), store.LaneTrigger), 120, 30)
		before := len(m.store.Trigger.Queries)
		m.Update(key("d"))
		if m.mode != screenConfirm {
			t.Fatalf("%q: delete did not ask first", k)
		}
		m.Update(key(k))
		if m.mode != screenList {
			t.Fatalf("%q: dialog stayed open (mode %v)", k, m.mode)
		}
		if n := len(m.store.Trigger.Queries); n != before {
			t.Fatalf("%q deleted a row: %d → %d", k, before, n)
		}
		if m.dirty {
			t.Fatalf("%q marked the model dirty", k)
		}
	}
}

// A mistyped key must not be read as consent: any other key leaves the dialog standing.
func TestDeleteConfirmIgnoresOtherKeys(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneTrigger), 120, 30)
	m.Update(key("d"))
	m.Update(key("p"))
	if m.mode != screenConfirm {
		t.Fatalf("an unrelated key dismissed the confirm screen")
	}
	if m.dirty || len(m.store.Trigger.Queries) != 4 {
		t.Fatal("an unrelated key changed the set")
	}
}

// TestRunRefusesWhileDirty: a run grades the files on disk, so starting one over unsaved edits
// would report a number that belongs to a different set. It refuses, and says why.
func TestRunRefusesWhileDirty(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneTrigger), 120, 30)
	m.Update(key(" ")) // select the focused row
	m.Update(key("d"))
	m.Update(key("y")) // delete it in memory: dirty, nothing on disk
	if !m.dirty {
		t.Fatal("the delete did not leave the model dirty")
	}
	m.Update(key("R"))
	if m.mode == screenRunLauncher {
		t.Fatal("R opened the run form with unsaved edits — the run would grade the disk, not the screen")
	}
	if !strings.Contains(m.View(), "unsaved changes") {
		t.Fatalf("no refusal was shown:\n%s", m.View())
	}
	m.Update(key("ctrl+s"))
	m.Update(key("R"))
	if m.mode != screenRunLauncher {
		t.Fatalf("R did not open the run form after saving (mode %v)", m.mode)
	}
}

// TestFooterNeverClips: the hint line drops bindings by importance, never by column. At every
// width the save key and the way to the key map survive, and no line exceeds the screen.
func TestFooterNeverClips(t *testing.T) {
	for _, w := range []int{60, 80, 100, 118, 160} {
		m := sized(newModel(testStore(t), store.LaneTrigger), w, 30)
		view := m.View()
		for _, line := range strings.Split(view, "\n") {
			if n := ansi.StringWidth(line); n > w {
				t.Errorf("%d cols: a line is %d wide: %q", w, n, line)
			}
		}
		hint := strings.Split(view, "\n")[len(strings.Split(view, "\n"))-3]
		for _, want := range []string{"ctrl+s save", "? keys"} {
			if !strings.Contains(hint, want) {
				t.Errorf("%d cols: the footer lost %q: %q", w, want, hint)
			}
		}
	}
}

// TestColumnHeadsAlignWithRows: the header and the rows are padded from one gutter, so a numeric
// value ends on the same cell as its head and a name starts on the same cell as its head.
func TestColumnHeadsAlignWithRows(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneOutput), 140, 30)
	lines := strings.Split(m.View(), "\n")
	if len(lines) < 5 {
		t.Fatalf("view has %d lines", len(lines))
	}
	head := []rune(lines[2])
	at := rowGutter
	for _, c := range m.columns() {
		if c.width == 0 {
			break
		}
		row := []rune(lines[3])
		if at+c.width > len(head) || at+c.width > len(row) {
			t.Fatalf("column %s runs past the line", c.name)
		}
		hs, rs := head[at:at+c.width], row[at:at+c.width]
		if c.right {
			if got, want := lastText(rs), lastText(hs); got != want {
				t.Errorf("column %s: value %q ends at %d, head %q ends at %d", c.name, string(rs), got, string(hs), want)
			}
		} else {
			if got, want := firstText(rs), firstText(hs); got != want {
				t.Errorf("column %s: value %q starts at %d, head %q starts at %d", c.name, string(rs), got, string(hs), want)
			}
		}
		at += c.width + 1
	}
}

func firstText(rs []rune) int {
	for i, r := range rs {
		if r != ' ' {
			return i
		}
	}
	return -1
}

func lastText(rs []rune) int {
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i] != ' ' {
			return i
		}
	}
	return -1
}

// TestHelpScrollsToTheEnd: the key map is longer than the screen, so it must scroll — the old
// static box hid its first section under a 30-row terminal and nothing could bring it back.
func TestHelpScrollsToTheEnd(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneTrigger), 100, 24)
	m.Update(key("?"))
	first := m.View()
	if !strings.Contains(first, "SKILL LIST") {
		t.Fatalf("help does not open at the top:\n%s", first)
	}
	if strings.Contains(first, "ids are never renumbered") {
		t.Fatalf("help fits on one screen, so this test proves nothing")
	}
	for i := 0; i < 40; i++ {
		m.Update(key("G"))
	}
	if !strings.Contains(m.View(), "ids are never renumbered") {
		t.Fatalf("G did not reach the end of the key map:\n%s", m.View())
	}
	m.Update(key("g"))
	if !strings.Contains(m.View(), "SKILL LIST") {
		t.Fatalf("g did not return to the top:\n%s", m.View())
	}
	// While the map is open it owns the keyboard: no stray letter may reach the list behind it.
	m.Update(key("d"))
	if m.mode == screenConfirm {
		t.Fatal("a key pressed inside help opened the delete dialog")
	}
	m.Update(key("esc"))
	if m.help || m.mode != screenList {
		t.Fatalf("esc left help=%v mode=%v", m.help, m.mode)
	}
}
