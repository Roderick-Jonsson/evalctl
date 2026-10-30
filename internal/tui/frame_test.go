package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"evalctl/internal/store"
)

// TestFrameIsAnchoredToTheTerminal: the view is exactly as tall as the window and the status line
// is its last row, so a short list does not leave the footer floating in the middle of the screen.
// Both screens go through the same frame.
func TestFrameIsAnchoredToTheTerminal(t *testing.T) {
	for _, h := range []int{8, 12, 24, 30, 55} {
		m := sized(newModel(testStore(t), store.LaneTrigger), 140, h)
		if got := len(strings.Split(m.View(), "\n")); got != h {
			t.Errorf("lane %d rows: view is %d rows", h, got)
		}
		m.openSkills()
		view := m.View()
		lines := strings.Split(view, "\n")
		if len(lines) != h {
			t.Errorf("skills %d rows: view is %d rows", h, len(lines))
		}
		last := lines[len(lines)-1]
		if !strings.Contains(last, "/") {
			t.Errorf("skills %d rows: last row is not the position: %q", h, last)
		}
	}
}

// TestSkillsDetailCarriesTheFindings: the front door spells out the row under the cursor — the
// counts its line compresses, and the findings HEALTH collapses into a number — and follows the
// cursor rather than the scroll.
func TestSkillsDetailCarriesTheFindings(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneOutput), 140, 40)
	m.openSkills()
	if len(m.issues) == 0 {
		t.Fatal("the fixture store has no findings to show")
	}
	want := m.issues[0].Msg
	view := m.View()
	if !strings.Contains(view, want) {
		t.Fatalf("no finding is spelled out on the front door (want %q):\n%s", want, view)
	}
	if !strings.Contains(view, "── all skills ") {
		t.Fatalf("the block does not name the row it belongs to:\n%s", view)
	}
	m.Update(key("j")) // the first real skill
	name := m.skills.rows[m.skills.cursor].sum.Skill
	if !strings.Contains(m.View(), "── "+name+" ") {
		t.Fatalf("the block did not follow the cursor to %q:\n%s", name, m.View())
	}
}

// TestSkillsDetailNeverEatsTheFrame: on a short or narrow terminal the detail block gives way, and
// the key line and status stay on the screen. The block is content; the frame is the window.
func TestSkillsDetailNeverEatsTheFrame(t *testing.T) {
	for _, sz := range [][2]int{{140, 10}, {140, 12}, {60, 30}} {
		m := sized(newModel(testStore(t), store.LaneOutput), sz[0], sz[1])
		m.openSkills()
		lines := strings.Split(m.View(), "\n")
		if len(lines) != sz[1] {
			t.Errorf("%dx%d: view is %d rows", sz[0], sz[1], len(lines))
		}
		if !strings.Contains(lines[len(lines)-2], "ctrl+s save") {
			t.Errorf("%dx%d: the key line is not the second-to-last row", sz[0], sz[1])
		}
		for _, l := range lines {
			if n := ansi.StringWidth(l); n > sz[0] {
				t.Errorf("%dx%d: a line is %d wide: %q", sz[0], sz[1], n, l)
			}
		}
	}
}

// TestPaneOnlyWhileWidthIsSpare: the text column keeps a readable measure and the pane takes what
// is left over — it is not drawn at a width where it would be paid for out of the table.
func TestPaneOnlyWhileWidthIsSpare(t *testing.T) {
	narrow := sized(newModel(testStore(t), store.LaneTrigger), 120, 30)
	if f := narrow.frame(); f.pane != 0 {
		t.Fatalf("120 cols: a pane of %d was reserved", f.pane)
	}
	if strings.Contains(narrow.View(), paneSep) {
		t.Fatalf("120 cols: a divider is drawn with no pane behind it:\n%s", narrow.View())
	}

	wide := sized(newModel(testStore(t), store.LaneTrigger), 200, 30)
	f := wide.frame()
	if f.pane < paneMin || f.pane > paneMax {
		t.Fatalf("200 cols: pane = %d, want %d..%d", f.pane, paneMin, paneMax)
	}
	if f.text > textMax {
		t.Fatalf("200 cols: text column = %d, past the %d-cell measure", f.text, textMax)
	}
	if f.table+f.pane+len([]rune(paneSep)) > 199 {
		t.Fatalf("200 cols: frame asks for %d cells", f.table+f.pane+3)
	}
	// The six-column output lane reaches the pane later than the five-column trigger lane.
	out := sized(newModel(testStore(t), store.LaneOutput), 200, 30)
	if of := out.frame(); of.table <= f.table && of.pane >= f.pane && of.text >= f.text {
		t.Fatalf("output lane did not pay for its extra column: trigger %+v, output %+v", f, of)
	}
}

// TestPaneCarriesTheWholeRecord: nothing a row holds is left to the v view. The pane is the same
// document v opens — text, expected output, assertions, the declared files, the fixture dir on
// disk — read through a narrow window.
func TestPaneCarriesTheWholeRecord(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneOutput), 200, 40)
	view := m.View()
	for _, want := range []string{"PROMPT", "EXPECTED OUTPUT", "a thing", "ASSERTIONS", "FIXTURE"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the pane is missing %q:\n%s", want, view)
		}
	}
	// And it is the same document the v view lays out, not a second account of the row.
	e, _ := m.focused()
	v := newViewer(m.store, e, 200, 40)
	joined := ansi.Strip(strings.Join(v.lines, "\n"))
	for _, want := range []string{"PROMPT", "EXPECTED OUTPUT", "ASSERTIONS", "FIXTURE"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the v view is missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "\x1b[") {
		t.Fatal("the v view kept raw escapes")
	}
}

// TestPaneNamesTheWayToReadTheRest: when the record does not fit the frame, the pane says how much
// is hidden and which key opens it, rather than ending in a bare ellipsis.
func TestPaneNamesTheWayToReadTheRest(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneOutput), 200, 10)
	if got := m.frame(); got.pane == 0 {
		t.Fatalf("no pane at 200 cols: %+v", got)
	}
	if !strings.Contains(m.View(), "v opens it whole") {
		t.Fatalf("a cut pane does not name v:\n%s", m.View())
	}
}

// TestPaneReadsTheRowTheTableCannotShow: the pane carries the row's text in full and the why it was
// written for, neither of which fits in a column; it follows the cursor, not the scroll offset.
func TestPaneReadsTheRowTheTableCannotShow(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneTrigger), 200, 30)
	view := m.View()
	if !strings.Contains(view, "clear") {
		t.Fatalf("pane does not carry the row's why:\n%s", view)
	}
	// One line, wrapped: the pane's copy of the query is not cut at the table's measure.
	m.Update(key("j"))
	after := m.View()
	if !strings.Contains(after, "near miss") {
		t.Fatalf("pane did not follow the cursor:\n%s", after)
	}
	if strings.Contains(after, "· clear") {
		t.Fatal("pane still shows the previous row's why")
	}
}

// TestPaneStopsAtTheFrame: a long pane (a big assertion list) is a window, not a document — it ends
// at the last row it has instead of pushing the footer off the screen.
func TestPaneStopsAtTheFrame(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneOutput), 200, 12)
	e, _ := m.focused()
	m.store.Skill("alpha").Output.Evals[0].Prompt = strings.Repeat("long prompt sentence. ", 40)
	m.refresh()
	_ = e
	lines := strings.Split(m.View(), "\n")
	if len(lines) != 12 {
		t.Fatalf("view is %d rows on a 12-row terminal", len(lines))
	}
	if !strings.Contains(lines[len(lines)-1], "/") {
		t.Fatalf("last row is not the position: %q", lines[len(lines)-1])
	}
	// The pane's own cut marker is the only line that ends with the divider and a line count.
	cut := false
	for _, l := range lines {
		if strings.Contains(l, "│ … ") && strings.Contains(l, "v opens it whole") {
			cut = true
		}
	}
	if !cut {
		t.Fatal("the cut pane does not say it was cut, or where to read the rest")
	}
}
