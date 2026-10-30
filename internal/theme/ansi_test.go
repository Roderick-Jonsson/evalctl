package theme

import (
	"strconv"
	"testing"
)

// A 256-colour terminal is the common case here (Apple Terminal has no truecolor), so the mapping
// from a palette's hex to an xterm index is the colour a user actually sees. These are the checks
// that keep it faithful instead of merely approximate.
func TestAnsi256IsNearest(t *testing.T) {
	// The value termenv would have chosen for this one is 195 (#d7ffff), a cyan white; 255
	// (#eeeeee) is the grey it should be. The difference is visible on every line of body text.
	cases := map[string]int{
		"#E6EDF3": 255,
		"#FFFFFF": 231,
		"#A8A8A8": 248,
		"#30363D": 237,
		"#1F2328": 235,
	}
	dark, _ := Lookup("midnight")
	for hex, want := range cases {
		if got := dark.nearest256(hex); got != want {
			t.Errorf("nearest256(%s) = %d (%s), want %d (%s)", hex, got, xterm(got), want, xterm(want))
		}
	}
}

// Whatever index is chosen has to be perceptually close to the colour the palette asked for, or the
// palette's contrast numbers stop describing the screen.
func TestAnsi256StaysClose(t *testing.T) {
	for _, p := range Palettes() {
		for _, r := range p.Roles() {
			idx, err := strconv.Atoi(p.ANSI256(r.Hex))
			if err != nil || idx < 0 || idx > 255 {
				t.Fatalf("%s/%s: ANSI256(%s) = %q, not an index", p.Name, r.Name, r.Hex, p.ANSI256(r.Hex))
			}
			// 16 on the Lab scale is roughly "just noticeable at a glance"; a handful of the
			// daylight colours land above it because the cube has no dark saturated greens, and
			// the test says so rather than pretending otherwise.
			if d := labDistance(lab(r.Hex), lab(xterm(idx))); d > 16 {
				t.Errorf("%s/%s: %s maps to %d (%s), %.1f Lab away", p.Name, r.Name, r.Hex, idx, xterm(idx), d)
			}
			// Contrast must survive the approximation: the 256 entry is what is drawn, so it is
			// what the readability claim has to hold for.
			if got := Contrast(xterm(idx), p.BG); got < 0.85*Contrast(r.Hex, p.BG) {
				t.Errorf("%s/%s: %s is %.2f:1 on the background but its 256 entry %s is %.2f:1",
					p.Name, r.Name, r.Hex, Contrast(r.Hex, p.BG), xterm(idx), got)
			}
		}
	}
}

// Sixteen-colour terminals get the nearest system colour, and never black on a dark palette.
func TestAnsi16NeverBlackOnDark(t *testing.T) {
	dark, _ := Lookup("midnight")
	for _, hex := range []string{dark.Text, dark.Border, dark.Sel, "#000000", "#101010"} {
		if got := dark.nearest16(hex); got == 0 {
			t.Errorf("%s resolved to black on a dark palette", hex)
		}
	}
	// On a light palette black is the correct answer for body text; it must not be pushed to grey.
	light, _ := Lookup("daylight")
	if got := light.nearest16(light.Text); got != 0 {
		t.Errorf("daylight text %s resolved to %d on a light palette, want black", light.Text, got)
	}
}
