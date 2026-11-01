package tui

import (
	"fmt"
	"os"
	"strings"
)

// glyphSet is the marks the FRAME draws itself: the cursor, the rules, and the state marks of a live
// run. Every one has an ASCII twin, so a terminal without
// UTF-8 — a serial console, a CI log, a screen reader, $TERM=dumb — gets the same screen in plain
// characters instead of a line of replacement boxes.
//
// Icons are chrome and only chrome: a font icon on every row would cost a cell a line saying what
// the SKILL or SLUG column already says, and it would be the first thing to go missing on a
// terminal that has not got the font. The data columns stay words.
type glyphSet struct {
	Name     string
	Cursor   string // the row under the cursor, in the region that has the keys
	Block    string // the focused region's own mark, in front of its name
	Here     string // the step the flow is standing on, in the rail
	Running  string // a run in flight
	Done     string // a finished run, a step that is done, a pass
	Failed   string // a failed arm or query
	Seam     string // the divider between a list and the pane beside it
	Arrow    string // "this becomes that": the step screen's one action line
	Rule     string // the thin rules inside a screen
	Heavy    string // the rule under the bar, so the bar reads as a bar
	Ellipsis string // what a fitted line ends with
}

var (
	unicodeGlyphs = glyphSet{
		Name: "unicode", Cursor: "▶ ", Block: "▌", Here: "●", Running: "⟳", Done: "✓", Failed: "✗", Seam: "│",
		Arrow: "→", Rule: "─", Heavy: "━", Ellipsis: "…",
	}
	asciiGlyphs = glyphSet{
		Name: "ascii", Cursor: "> ", Block: "|", Here: "*", Running: "~", Done: "+", Failed: "x", Seam: "|",
		Arrow: ">", Rule: "-", Heavy: "=", Ellipsis: "~",
	}
)

// glyphs is the set the process draws with, resolved once at startup — the same kind of process
// state as the palette, and for the same reason: a hundred call sites read it out of a view and
// none of them needs to know where it came from.
var glyphs = unicodeGlyphs

func setName(s glyphSet) {
	glyphs = s
}

// GlyphNames lists the profiles, for the CLI's usage line and its error message.
func GlyphNames() []string { return []string{"auto", "unicode", "ascii"} }

// Glyphs resolves a profile name: "" and "auto" follow the locale, anything else has to be one of
// GlyphNames(). A typo is an error rather than a silent fallback — a mistyped profile that quietly
// draws something else is worse than a message listing the choices (the palette makes the same
// trade).
func Glyphs(name string, lang, ctype, all string) (glyphSet, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "auto":
		if utf8Locale(lang, ctype, all) {
			return unicodeGlyphs, nil
		}
		return asciiGlyphs, nil
	case "unicode":
		return unicodeGlyphs, nil
	case "ascii":
		return asciiGlyphs, nil
	}
	return glyphSet{}, fmt.Errorf("unknown glyph profile %q (known: %s)", name, strings.Join(GlyphNames(), ", "))
}

// utf8Locale reports whether the environment names a UTF-8 locale. LC_ALL wins over LC_CTYPE wins
// over LANG, and nothing set means a modern terminal (the overwhelmingly common case) rather than a
// dumb one: falling back to ASCII because a container cleared the environment would be wrong in the
// direction that looks broken.
func utf8Locale(lang, ctype, all string) bool {
	for _, v := range []string{all, ctype, lang} {
		if strings.TrimSpace(v) == "" {
			continue
		}
		v = strings.ToUpper(strings.ReplaceAll(v, "-", ""))
		return strings.Contains(v, "UTF8")
	}
	return true
}

// glyphProfileFromEnv resolves the profile from the environment, for a caller that has no flag.
func glyphProfileFromEnv() glyphSet {
	g, err := Glyphs(os.Getenv("EVALCTL_GLYPHS"), os.Getenv("LANG"), os.Getenv("LC_CTYPE"), os.Getenv("LC_ALL"))
	if err != nil {
		return unicodeGlyphs
	}
	return g
}

// ascii reports whether the process is drawing the fallback set. Views that print a word rather
// than a mark use it to stay in one register.
func ascii() bool { return glyphs.Name == asciiGlyphs.Name }
