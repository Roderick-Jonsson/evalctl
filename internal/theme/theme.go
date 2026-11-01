// Package theme is evalctl's colour: the palettes it ships, the role each colour plays, and the
// choice of which palette to draw with.
//
// Two rules decide everything here.
//
// A palette names roles, not hues. Views ask for Border, Muted or Err — never for "yellow" — so a
// scheme can be swapped without a view knowing, and so nothing in a view can pick a colour that
// the palette did not choose for it.
//
// A colour must be readable on its own background. Each shipped palette is checked against the
// background it is designed for (see theme_test.go): body text at 7:1 or better, every secondary
// and status role at 4.5:1 or better, borders at 1.4–3:1 so a frame reads as a frame and not as
// text, and the frame's dividers (Line) at 2.2–5:1 — those you are meant to find, so they are
// brighter than a box edge and still quieter than the text they separate. That bar is why evalctl
// no longer uses the terminal's own ANSI red or blue: on a black
// background a palette's ANSI-8 colour is often near-invisible (Apple Terminal's blue #0000B3 is
// 1.7:1, its magenta-adjacent bright blue #0000FF is 2.4:1), and a status glyph you cannot see is
// a status glyph that does not exist.
//
// Colour is also never the only cue: a should-trigger row says the word "yes" or "no" (the label is
// a fact about the row, not a verdict, so it carries no pass/fail glyph), region names keep their
// bold, group rows keep their heading text, the cursor keeps its mark, and the focused region carries
// a block in front of its name. That is what makes a palette survive a colour-blind reader and a
// 16-colour terminal.
package theme

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

// Palette is one colour scheme. Every field is a #rrggbb string: the format both Lip Gloss and
// Glamour accept, and the one a terminal without truecolor converts to the nearest xterm-256
// entry by itself.
type Palette struct {
	Name  string
	Light bool   // the kind of background it is designed for
	BG    string // that background, so a palette carries what it was checked against

	Text    string // body text: the default foreground, the table's values
	Muted   string // second tier: ids, separators, counts, out-of-focus state
	Faint   string // third tier: placeholders, hints, anything you look away from
	Accent  string // the pointer: the cursor, the region that has the keys, the skill column
	Accent2 string // structural labels: group headers, sub-headings
	OK      string // yes, pass, healthy
	Warn    string // dupes, warnings, tool calls, run notes
	Err     string // no, fail, errors
	Border  string // boxes, rules, table separators
	Line    string // the frame's dividers: the tab bar's rule and the pane's seam
	Sel     string // background of the row under the cursor
	CodeBG  string // inline code and code blocks
	CodeFG  string
}

// Role is one named colour of a palette, for previews and for the readability check.
type Role struct{ Name, Hex string }

// Roles lists a palette's colours in the order a reader should meet them: text first, decoration
// last.
func (p Palette) Roles() []Role {
	return []Role{
		{"text", p.Text}, {"muted", p.Muted}, {"faint", p.Faint},
		{"accent", p.Accent}, {"accent2", p.Accent2},
		{"ok", p.OK}, {"warn", p.Warn}, {"err", p.Err},
		{"border", p.Border}, {"line", p.Line}, {"sel", p.Sel}, {"code", p.CodeFG},
	}
}

// midnight is the default for a dark terminal. Low-chroma text at 17.8:1 with quiet greys beneath
// it, and four hues that stay apart under protanopia and deuteranopia as well as they do in
// normal vision — the status trio (green/amber/salmon) was picked for that separation, not for
// fashion.
var midnight = Palette{
	Name: "midnight", Light: false, BG: "#000000",
	Text: "#EEEEEE", Muted: "#BCBCBC", Faint: "#8A8A8A",
	Accent: "#87AFFF", Accent2: "#5FD7D7",
	OK: "#5FD787", Warn: "#FFAF00", Err: "#FF8787",
	Border: "#3A3A3A", Line: "#6B6B6B", Sel: "#444444",
	CodeBG: "#262626", CodeFG: "#AFD7FF",
}

// highContrast is for a bright room, a projected screen, or eyes that want the maximum: nothing
// below 8.8:1, status colours at 13–16:1, and a border heavy enough to be seen at all.
var highContrast = Palette{
	Name: "high-contrast", Light: false, BG: "#000000",
	Text: "#FFFFFF", Muted: "#B2B2B2", Faint: "#A8A8A8",
	Accent: "#87D7FF", Accent2: "#FFAFFF",
	OK: "#5FFF87", Warn: "#FFD75F", Err: "#FF8787",
	Border: "#585858", Line: "#757575", Sel: "#444444",
	CodeBG: "#262626", CodeFG: "#AFFFFF",
}

// daylight is for a light terminal, or a dark-mode terminal whose background is not what the
// detection expected. The hues are darkened rather than brightened: on white, saturation costs
// contrast, so these are the same roles at 5–8:1 with the chroma turned down.
var daylight = Palette{
	Name: "daylight", Light: true, BG: "#FFFFFF",
	Text: "#1C1C1C", Muted: "#626262", Faint: "#767676",
	Accent: "#005FAF", Accent2: "#8700AF",
	OK: "#008700", Warn: "#875F00", Err: "#870000",
	Border: "#D0D0D0", Line: "#9C9C9C", Sel: "#C6C6C6",
	CodeBG: "#E4E4E4", CodeFG: "#005F87",
}

// Palettes is the shipped set, in the order `evalctl theme` lists them: the dark default first,
// because that is what most terminals are.
func Palettes() []Palette { return []Palette{midnight, highContrast, daylight} }

// Lookup finds a palette by name (case- and space-insensitive, so "high contrast" works too).
func Lookup(name string) (Palette, bool) {
	want := normalise(name)
	for _, p := range Palettes() {
		if normalise(p.Name) == want {
			return p, true
		}
	}
	return Palette{}, false
}

// Names lists the palette names for an error message or a picker.
func Names() []string {
	out := make([]string, 0, len(Palettes()))
	for _, p := range Palettes() {
		out = append(out, p.Name)
	}
	return out
}

// Resolve turns a theme name into a palette. The empty name and "auto" mean: detect. Anything
// else has to be one of Names(); a typo is an error rather than a silent fallback, because a
// mistyped theme that quietly draws something else is worse than a message listing the choices.
func Resolve(name string) (Palette, error) {
	name = strings.TrimSpace(name)
	if name == "" || normalise(name) == "auto" {
		return ForBackground(DarkBackground()), nil
	}
	p, ok := Lookup(name)
	if !ok {
		return Palette{}, fmt.Errorf("unknown theme %q (known: %s, auto)", name, strings.Join(Names(), ", "))
	}
	return p, nil
}

// ForBackground picks the palette for a background: midnight on a dark one, daylight on a light
// one. It is the whole of the automatic choice, and it is exported so the choice can be inspected
// (and tested) without a terminal.
func ForBackground(dark bool) Palette {
	if dark {
		return midnight
	}
	return daylight
}

// DarkBackground reports whether the terminal is dark. It asks the terminal for its background
// colour (OSC 11) and falls back to the usual environment hints; a pipe, a file or a cron job has
// no terminal to ask and gets the dark default, since that is where colour is least likely to be
// read at all.
func DarkBackground() bool {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return true
	}
	if v := os.Getenv("COLORFGBG"); v != "" {
		// "15;0" — the last number is the background: 0–6 and 8 are dark, 7 and 9–15 are light.
		if parts := strings.Split(v, ";"); len(parts) > 1 {
			if n, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
				return n < 7 || n == 8
			}
		}
	}
	// Ask the terminal itself (OSC 11). This is the only answer that reflects what is actually on
	// screen, and it is asked with a short deadline of our own: termenv's five-second one turns an
	// unanswering terminal (a tmux without passthrough, an IDE's console) into a five-second hang.
	if r, g, b, ok := queryBackground(200 * time.Millisecond); ok {
		// A grey background is judged on its luminance: #808080 is 0.22 and still reads dark.
		return 0.2126*r+0.7152*g+0.0722*b < 0.5
	}
	return true
}

// current is the palette the process draws with. It is process state, not view state, because the
// style tables in the TUI and the CLI are built once from it — and because the markdown renderer
// is a third consumer that has no model to ask.
var current = midnight

// Current returns the palette in effect.
func Current() Palette { return current }

// Set makes p the palette in effect. Callers set it once, at startup, from Resolve.
func Set(p Palette) { current = p }

// Contrast is the WCAG 2 relative contrast ratio between two #rrggbb colours: 1 means the same
// colour, 21 means black on white. The palettes here are checked with it, and `evalctl theme`
// prints it, so the claim "this is readable" is a number and not an opinion.
func Contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(hex string) float64 {
	r, g, b, ok := rgb(hex)
	if !ok {
		return 0
	}
	return 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
}

func linear(v float64) float64 {
	v /= 255
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

// rgb parses #rrggbb (the only format a Palette holds) and reports whether it could.
func rgb(hex string) (r, g, b float64, ok bool) {
	h := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(h) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return float64(v >> 16 & 0xff), float64(v >> 8 & 0xff), float64(v & 0xff), true
}

// normalise folds a palette name for comparison: "high contrast", "High-Contrast" and
// "highcontrast" all mean the same palette.
func normalise(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), " ", ""), "-", "")
}
