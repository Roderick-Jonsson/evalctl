package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"evalctl/internal/store"
	"evalctl/internal/theme"
)

// The chrome has three promises, and each of them was asked for by name: the bar is the top edge
// with the build parked at its right end, the rule under it is what makes the bar a bar, and the
// footer is one band whose keys are the bright register. They are pinned here so a later layout
// change has to say so out loud.

func ansiModel(t *testing.T, w, h int) *Model {
	t.Helper()
	was, wasProfile := theme.Current(), lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	p, _ := theme.Lookup("midnight")
	theme.Set(p)
	applyStyles(p)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(wasProfile)
		theme.Set(was)
		applyStyles(was)
	})
	return sized(newModel(testStore(t), store.LaneOutput), w, h)
}

func TestTabBarIsTheTopEdge(t *testing.T) {
	m := ansiModel(t, 160, 30)
	lines := strings.Split(m.View(), "\n")

	bar := ansi.Strip(lines[0])
	if !strings.HasPrefix(bar, "all skills") {
		t.Errorf("the bar does not open with the scope: %q", bar)
	}
	if got, want := strings.TrimRight(bar, " "), "evalctl "+m.ver; !strings.HasSuffix(got, want) {
		t.Errorf("the bar does not park the build at its right edge: %q, want suffix %q", got, want)
	}
	// The view on screen is the FILLED chip, and the others are not filled: one fill per bar.
	if !strings.Contains(lines[0], tabActive.Render(" "+glyphs.Cases+" output cases 2 ")) {
		t.Errorf("the active tab is not the filled chip: %q", ansi.Strip(lines[0]))
	}
	for _, idle := range []string{"queries", "results"} {
		if strings.Contains(lines[0], tabActive.Render(idle)) {
			t.Errorf("%s is drawn as the active chip", idle)
		}
	}

	// The rule under the bar: the profile's heavy mark, at the full width of the window.
	rule := ansi.Strip(lines[1])
	if rule != strings.Repeat(glyphs.Heavy, 160) {
		t.Errorf("line 2 is not the full-width rule under the bar: %q", rule)
	}
	if lines[1] == rule {
		t.Error("the rule carries no colour: it would read as text instead of structure")
	}
}

func TestNarrowBarKeepsTheCounts(t *testing.T) {
	m := ansiModel(t, 80, 24)
	bar := ansi.Strip(strings.Split(m.View(), "\n")[0])
	for _, want := range []string{"cases 2", "queries 4"} {
		if !strings.Contains(bar, want) {
			t.Errorf("80 cols: the bar lost %q: %q", want, bar)
		}
	}
	// The icons are what a narrow window drops first — the words and the counts stay.
	if strings.Contains(bar, glyphs.Cases) {
		t.Errorf("80 cols: the bar kept its icons: %q", bar)
	}
}

func TestFooterIsOneBand(t *testing.T) {
	m := ansiModel(t, 140, 30)
	lines := strings.Split(m.View(), "\n")
	band, last := lines[len(lines)-2], lines[len(lines)-1]

	if !strings.HasPrefix(band, footSeq) {
		t.Errorf("the hint row does not open with the band's background: %q", ansi.Strip(band))
	}
	if got := ansi.StringWidth(band); got != 140 {
		t.Errorf("the band is %d cells on a 140-cell window", got)
	}
	if got := strings.TrimRight(ansi.Strip(band), " "); !strings.HasSuffix(got, "? keys") {
		t.Errorf("the help cap is not at the band's right edge: %q", got)
	}
	// The keys are bold and the actions are not: one register each, all the way across.
	if !strings.Contains(band, footKey.Render("space")) {
		t.Errorf("the keys are not in the bright register: %q", ansi.Strip(band))
	}
	if strings.Contains(band, footKey.Render("select")) {
		t.Error("the action word is styled as a key")
	}

	// The last row: the position is the final thing on it, whatever else it carries.
	if got := ansi.Strip(last); !strings.HasSuffix(got, m.position()) {
		t.Errorf("the last row does not end with the position: %q", got)
	}
	if got := ansi.Strip(last); !strings.Contains(got, "warning") {
		t.Errorf("the last row does not carry the tree's health: %q", got)
	}
}
