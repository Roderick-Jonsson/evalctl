package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"evalctl/internal/store"
	"evalctl/internal/theme"
)

// longRow is a model whose cursor row carries a record longer than the pane, so the pane has
// something to scroll.
func longRow(t *testing.T, w, h int) *Model {
	t.Helper()
	m := sized(newModel(testStore(t), store.LaneOutput), w, h)
	m.store.Skill("alpha").Output.Evals[0].Prompt = strings.Repeat("long prompt sentence. ", 40)
	m.refresh()
	if m.frame().pane == 0 {
		t.Fatalf("%d cols: no pane to focus", w)
	}
	return m
}

// TestEnterGivesThePaneTheKeysAndEscTakesThemBack: the pane is a component you can go INTO. enter
// hands it the keyboard, esc hands it back, and the seam between the table and the pane is what says
// which of the two is taking keystrokes.
func TestEnterGivesThePaneTheKeysAndEscTakesThemBack(t *testing.T) {
	was, wasProfile := theme.Current(), lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer func() {
		lipgloss.SetColorProfile(wasProfile)
		theme.Set(was)
		applyStyles(was)
	}()
	p, _ := theme.Lookup("midnight")
	theme.Set(p)
	applyStyles(p)

	m := sized(newModel(testStore(t), store.LaneOutput), 200, 30)
	if m.paneFocus {
		t.Fatal("the table has the keys on arrival")
	}
	if got := m.columnHead(m.frame()); strings.Contains(got, p.ANSI256(p.Accent)) {
		t.Fatalf("the seam is accent while the table has the keys: %q", got)
	}

	m.Update(key("enter"))
	if !m.paneFocus {
		t.Fatal("enter did not move the focus into the pane")
	}
	if m.mode != screenList {
		t.Fatalf("enter opened %v instead of focusing the pane", m.mode)
	}
	head := m.columnHead(m.frame())
	if !strings.Contains(head, p.ANSI256(p.Accent)) {
		t.Fatalf("the seam does not take the accent while the pane has the keys: %q", head)
	}
	if !strings.Contains(m.View(), "esc table") {
		t.Fatalf("the footer does not name the way back:\n%s", m.View())
	}

	m.Update(key("esc"))
	if m.paneFocus {
		t.Fatal("esc did not hand the keys back")
	}
	if m.mode != screenList {
		t.Fatalf("esc left the list (mode %v)", m.mode)
	}
}

// TestPaneScrollsWithoutMovingTheList: the window is the pane's, the cursor is the list's. j inside
// the pane moves the record, not the selection, and the pane's title says where in the record it is
// — a scrolled window that does not say so reads as a record that got shorter.
func TestPaneScrollsWithoutMovingTheList(t *testing.T) {
	m := longRow(t, 200, 12)
	m.Update(key("enter"))
	cursor := m.cursor
	// The record overflows, so the marker takes one of the seven rows: six rows of record on screen.
	if !strings.Contains(m.View(), "1–6/") {
		t.Fatalf("the pane does not say where the window is:\n%s", m.View())
	}

	m.Update(key("j"))
	m.Update(key("j"))
	if m.cursor != cursor {
		t.Fatalf("the list moved: cursor %d → %d", cursor, m.cursor)
	}
	if m.paneTop != 2 {
		t.Fatalf("paneTop = %d after two j", m.paneTop)
	}
	if !strings.Contains(m.View(), "3–8/") {
		t.Fatalf("the pane did not scroll:\n%s", m.View())
	}

	// g goes back to the top of the record, and the list still has not moved.
	m.Update(key("g"))
	if m.paneTop != 0 || m.cursor != cursor {
		t.Fatalf("g: paneTop %d, cursor %d", m.paneTop, m.cursor)
	}

	// G runs to the end and clamps there: the last screen is a full screen, not a two-line tail.
	m.Update(key("G"))
	doc := len(m.paneDoc(m.rows[m.cursor], m.frame().pane))
	if want := doc - m.listHeight(); m.paneTop != want {
		t.Fatalf("G: paneTop = %d, want %d", m.paneTop, want)
	}
}

// TestThePaneDoesNotEditTheRowBehindIt: while the pane has the keys, the list's verbs do nothing —
// and say why. A key that silently does nothing inside a component reads as a broken component.
func TestThePaneDoesNotEditTheRowBehindIt(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneOutput), 200, 30)
	before := len(m.store.Skill("alpha").Output.Evals)
	m.Update(key("enter"))
	for _, k := range []string{"d", "x", " ", "a"} {
		m.Update(key(k))
		if m.mode != screenList {
			t.Fatalf("%q opened %v from inside the pane", k, m.mode)
		}
		if len(m.chosen) != 0 {
			t.Fatalf("%q selected a row from inside the pane", k)
		}
	}
	if n := len(m.store.Skill("alpha").Output.Evals); n != before {
		t.Fatalf("%d case(s) vanished: %d → %d", before, before, n)
	}
	if !strings.Contains(m.status, "the pane has the keys") {
		t.Fatalf("the last ignored key said %q", m.status)
	}
	// esc is still one level up, and it still names the level it takes.
	if m.escLabel() != "table" {
		t.Fatalf("esc says %q while the pane has the keys", m.escLabel())
	}
}

// TestNarrowTerminalSaysThereIsNoPane: where the frame cannot buy a pane there is nothing to focus,
// and enter says which key does the job instead of quietly opening the editor.
func TestNarrowTerminalSaysThereIsNoPane(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneOutput), 80, 30)
	if m.frame().pane != 0 {
		t.Skip("this width still has a pane")
	}
	m.Update(key("enter"))
	if m.paneFocus {
		t.Fatal("the pane took the keys with no pane on screen")
	}
	if m.mode != screenList {
		t.Fatalf("enter opened %v with no pane to open", m.mode)
	}
	if !strings.Contains(m.status, "no detail pane at this width") {
		t.Fatalf("enter said %q", m.status)
	}
}

// TestSwitchingTabsLeavesThePane: a tab is a new body, so the pane the keys were in is not on screen
// any more — a focus that survives it points at something the user cannot see.
func TestSwitchingTabsLeavesThePane(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneOutput), 200, 30)
	m.Update(key("enter"))
	if !m.paneFocus {
		t.Fatal("enter did not focus the pane")
	}
	m.cycleTab(1)
	if m.paneFocus {
		t.Fatal("the pane kept the keys across a tab switch")
	}
}
