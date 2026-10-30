package tui

import (
	"strings"
	"testing"

	"evalctl/internal/store"
)

// TestTheGridIsTheWholeFrame: the vertical split is one function of the height, and a view built from
// it is exactly as tall as the window at every size. This is the contract the four screens share —
// before it, each screen padded itself and the viewer got it wrong by two rows.
func TestTheGridIsTheWholeFrame(t *testing.T) {
	for _, h := range []int{1, 3, 5, 8, 12, 24, 30, 55} {
		if h >= gridHead+gridFoot+1 {
			if got := gridBody(h) + gridHead + gridFoot; got != h {
				t.Errorf("h=%d: body %d + head %d + foot %d = %d", h, gridBody(h), gridHead, gridFoot, got)
			}
		} else if gridBody(h) < 1 {
			t.Errorf("h=%d: body is %d rows; a body of zero is not a layout", h, gridBody(h))
		}

		view := fitView([]string{"a", "b", "c"}, []string{"x", "y"}, []string{"hint", "status"}, h)
		if got := len(strings.Split(view, "\n")); got != h {
			t.Errorf("h=%d: fitView returned %d rows: %q", h, got, view)
		}
		lines := strings.Split(view, "\n")
		// The status row is the one that must never be the row a short frame drops.
		if last := lines[len(lines)-1]; last != "status" {
			t.Errorf("h=%d: the last row is %q, not the status", h, last)
		}
	}
}

// TestFitRowsPadsAndCuts: a body that is short of the frame is padding, never a shorter view; one that
// is long is cut, never allowed to push the footer off the bottom.
func TestFitRowsPadsAndCuts(t *testing.T) {
	if got := fitRows([]string{"a"}, 3); len(got) != 3 || got[0] != "a" || got[1] != "" || got[2] != "" {
		t.Errorf("pad: %q", got)
	}
	if got := fitRows([]string{"a", "b", "c"}, 2); len(got) != 2 || got[1] != "b" {
		t.Errorf("cut: %q", got)
	}
	if got := fitRows([]string{"a"}, 0); got != nil {
		t.Errorf("zero rows: %q", got)
	}
}

// TestPaneWindowCountsWhatIsLeftAfterTheMarker: the marker costs a row, so it counts the record after
// itself. Off by one here is the difference between "… 3 more" and a record that seems to have lost a
// line every time you scroll.
func TestPaneWindowCountsWhatIsLeftAfterTheMarker(t *testing.T) {
	doc := make([]string, 10)
	for i := range doc {
		doc[i] = strings.Repeat("x", i+1)
	}
	// A 4-row window over 10 lines: 3 lines of record, then the marker naming the other 7 (the row
	// the marker takes is not a row of the record).
	got := paneWindow(doc, 0, 4, "v opens it whole")
	if len(got) != 4 {
		t.Fatalf("window is %d rows: %q", len(got), got)
	}
	if !strings.Contains(got[3], "7 more lines") || !strings.Contains(got[3], "v opens it whole") {
		t.Fatalf("marker = %q", got[3])
	}
	// The title and the marker read one answer: rows 1-3 are on screen, the marker says 7.
	if from, to := paneShown(len(doc), 0, 4); from != 1 || to != 3 {
		t.Errorf("paneShown = %d-%d, want 1-3", from, to)
	}
	if from, to := paneShown(len(doc), 10, 4); from != 7 || to != 10 {
		t.Errorf("paneShown at the end = %d-%d, want 7-10", from, to)
	}

	// Scrolled to the end: no marker, and the last lines of the record are on screen.
	got = paneWindow(doc, 6, 4, "v opens it whole")
	if len(got) != 4 || got[3] != doc[9] {
		t.Fatalf("end of the record: %q", got)
	}
	for _, l := range got {
		if strings.Contains(l, "more lines") {
			t.Fatalf("a window at the end still claims more: %q", got)
		}
	}
	// A document shorter than the window never gets a marker, and the window is the document.
	got = paneWindow(doc[:2], 0, 4, "v opens it whole")
	if len(got) != 2 {
		t.Fatalf("short document: %q", got)
	}
}

// TestTheResultsPaneDoesNotRepeatTheKeyMap: the pane used to carry a third copy of the verbs the
// footer already renders from one table — the copy that drifts, and the block a short frame cut
// first. It shows the iteration's facts instead, and a cut names the key that opens the run whole.
func TestTheResultsPaneDoesNotRepeatTheKeyMap(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneOutput), 220, 30)
	m.setTab(tabResults)
	if m.runsList == nil || len(m.runsList.infos) == 0 {
		t.Skip("the fixture has no iterations")
	}
	view := m.View()
	for _, gone := range []string{"enter the report it wrote", "re-read results/ for runs finished elsewhere"} {
		if strings.Contains(view, gone) {
			t.Errorf("the results pane still repeats the key map: %q\n%s", gone, view)
		}
	}
	for _, want := range []string{"lane", "cases", "runs", "judged", "PATH"} {
		if !strings.Contains(view, want) {
			t.Errorf("the results pane lost %q:\n%s", want, view)
		}
	}

	// Too short for the whole document: the path is what gives way, and the cut names v.
	m = sized(newModel(testStore(t), store.LaneOutput), 220, 12)
	m.setTab(tabResults)
	if view := m.View(); !strings.Contains(view, "v opens the report") {
		t.Errorf("the cut pane does not name the key that shows the run whole:\n%s", view)
	}
}
