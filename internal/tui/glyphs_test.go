package tui

import (
	"strings"
	"testing"

	"evalctl/internal/store"
)

// TestASCIIProfileIsPureASCII: the fallback exists so a terminal without UTF-8 draws the same screen
// in plain characters. One non-ASCII glyph left in the set is one row of replacement boxes.
func TestASCIIProfileIsPureASCII(t *testing.T) {
	fields := map[string]string{
		"cursor": asciiGlyphs.Cursor, "selected": asciiGlyphs.Selected, "running": asciiGlyphs.Running,
		"done": asciiGlyphs.Done, "failed": asciiGlyphs.Failed, "sort-up": asciiGlyphs.SortUp,
		"sort-down": asciiGlyphs.SortDown, "arrow": asciiGlyphs.Arrow, "shift": asciiGlyphs.Shift,
		"seam": asciiGlyphs.Seam, "rule": asciiGlyphs.Rule, "ellipsis": asciiGlyphs.Ellipsis,
		"heavy": asciiGlyphs.Heavy, "cases": asciiGlyphs.Cases, "queries": asciiGlyphs.Queries,
		"results": asciiGlyphs.Results,
	}
	for name, g := range fields {
		if g == "" {
			t.Errorf("the ascii profile has no %s", name)
		}
		for _, r := range g {
			if r > 0x7e || r < 0x20 {
				t.Errorf("ascii.%s = %q, which is not ASCII", name, g)
			}
		}
	}
	// And the seam keeps the frame's three cells: every width the layout computes is arithmetic on
	// that constant, so a wider or narrower separator would shift the table.
	if got := len([]rune(" " + asciiGlyphs.Seam + " ")); got != len([]rune(" │ ")) {
		t.Errorf("the ascii seam is %d cells, the frame budgets %d", got, len([]rune(" │ ")))
	}
}

// TestGlyphProfileResolution: auto follows the locale, a name is a name, and a typo is an error rather
// than a silent fallback — a mistyped profile that quietly draws something else is worse than a
// message listing the choices.
func TestGlyphProfileResolution(t *testing.T) {
	cases := []struct {
		name, lang, ctype, all string
		want                   string
		bad                    bool
	}{
		{name: "", lang: "en_US.UTF-8", want: "unicode"},
		{name: "auto", lang: "de_CH.UTF-8", want: "unicode"},
		{name: "auto", lang: "C", want: "ascii"},
		{name: "auto", lang: "en_US.ISO-8859-1", want: "ascii"},
		// LC_ALL wins over LANG.
		{name: "auto", lang: "C", all: "en_US.UTF-8", want: "unicode"},
		// Nothing set at all is a modern terminal, not a dumb one.
		{name: "auto", want: "unicode"},
		{name: "ascii", lang: "en_US.UTF-8", want: "ascii"},
		{name: "unicode", lang: "C", want: "unicode"},
		{name: " Ascii ", want: "ascii"},
		{name: "utf8", bad: true},
	}
	for _, c := range cases {
		got, err := Glyphs(c.name, c.lang, c.ctype, c.all)
		if c.bad {
			if err == nil {
				t.Errorf("%q: expected an error, got %q", c.name, got.Name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.name, err)
			continue
		}
		if got.Name != c.want {
			t.Errorf("%q (LANG=%q LC_ALL=%q) = %q, want %q", c.name, c.lang, c.all, got.Name, c.want)
		}
	}
}

// TestASCIIProfileDrawsNoBoxDrawing: the profile is not a switch that changes one glyph — the rules,
// the seam and the marks all follow it, or the fallback is a fallback for a third of the frame.
func TestASCIIProfileDrawsNoBoxDrawing(t *testing.T) {
	defer setName(unicodeGlyphs)
	setName(asciiGlyphs)

	m := sized(newModel(testStore(t), store.LaneTrigger), 200, 30)
	views := []struct{ name, view string }{{"lane", m.View()}}
	// The seam kept its three cells, so the pane is still where the layout put it.
	if m.frame().pane > 0 && !strings.Contains(views[0].view, " | ") {
		t.Errorf("the ascii seam is not where the divider was:\n%s", views[0].view)
	}
	m.openSkills()
	views = append(views, struct{ name, view string }{"skills", m.View()})
	m.help = true
	views = append(views, struct{ name, view string }{"help", m.View()})

	for _, v := range views {
		for _, mark := range []string{"▸", "▪", "│", "─", "━", "⟳", "…", "▤", "◎", "✓"} {
			if strings.Contains(v.view, mark) {
				t.Errorf("%s: the ascii profile still draws %q:\n%s", v.name, mark, v.view)
			}
		}
	}
}
