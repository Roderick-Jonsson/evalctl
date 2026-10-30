package theme

import (
	"math"
	"strings"
	"testing"
)

// The palettes are a readability promise, so they are tested like one: every colour a view can
// print is checked against the background the palette says it is for. A palette edit that makes
// text harder to read fails here rather than in a terminal, at night, on a slide.
func TestPalettesAreReadable(t *testing.T) {
	for _, p := range Palettes() {
		t.Run(p.Name, func(t *testing.T) {
			for _, r := range p.Roles() {
				if !strings.HasPrefix(r.Hex, "#") || len(r.Hex) != 7 {
					t.Errorf("%s: role %s is %q, not #rrggbb", p.Name, r.Name, r.Hex)
				}
			}
			// Body text earns the 7:1 bar; every role that carries meaning earns the 4.5:1 floor.
			for _, c := range []struct {
				role, hex string
				min       float64
			}{
				{"text", p.Text, 7.0},
				{"muted", p.Muted, 4.5},
				{"faint", p.Faint, 4.5},
				{"accent", p.Accent, 4.5},
				{"accent2", p.Accent2, 4.5},
				{"ok", p.OK, 4.5},
				{"warn", p.Warn, 4.5},
				{"err", p.Err, 4.5},
				{"code", p.CodeFG, 4.5},
			} {
				if got := Contrast(c.hex, p.BG); got < c.min {
					t.Errorf("%s: %s %s is %.2f:1 on %s, want %.1f:1", p.Name, c.role, c.hex, got, p.BG, c.min)
				}
			}
			// A frame has to be visible without competing with the text it frames.
			if got := Contrast(p.Border, p.BG); got < 1.3 || got > 3.2 {
				t.Errorf("%s: border %s is %.2f:1 on %s, want 1.3–3.2:1", p.Name, p.Border, got, p.BG)
			}
			// The dividers are the one piece of structure you are meant to FIND — the tabs end
			// where the body starts, and two panes are two panes — so they are brighter than a
			// box edge and still well below the text they divide.
			if got := Contrast(p.Line, p.BG); got < 2.2 || got > 5.0 {
				t.Errorf("%s: line %s is %.2f:1 on %s, want 2.2–5:1", p.Name, p.Line, got, p.BG)
			}
			// The row under the cursor has to be findable: a selection you cannot see is a lost
			// cursor row.
			if got := Contrast(p.Sel, p.BG); got < 1.25 {
				t.Errorf("%s: selection %s is %.2f:1 on %s, want at least 1.25:1", p.Name, p.Sel, got, p.BG)
			}
			if got := Contrast(p.CodeFG, p.CodeBG); got < 4.5 {
				t.Errorf("%s: code %s on %s is %.2f:1, want 4.5:1", p.Name, p.CodeFG, p.CodeBG, got)
			}
		})
	}
}

// Colour is never the only cue — yes/no keep their glyphs, headers their underline — but the three
// status colours are read side by side in one column, so they must not collapse into each other for
// a reader with red-green colour blindness. The bar is 10 on the Lab scale (a just-noticeable
// difference is about 2.3, so this is five steps clear): enough that a deuteranope sees three
// colours and not one brown. It is not the maximum achievable — a palette tuned for this alone
// ends up pale green against pure red — and the glyphs carry the rest.
func TestStatusColoursStayApart(t *testing.T) {
	const want = 10.0
	for _, p := range Palettes() {
		t.Run(p.Name, func(t *testing.T) {
			trio := map[string]string{"ok": p.OK, "warn": p.Warn, "err": p.Err}
			for _, kind := range []string{"prot", "deut"} {
				for _, pair := range [][2]string{{"ok", "warn"}, {"ok", "err"}, {"warn", "err"}} {
					a, b := simulate(trio[pair[0]], kind), simulate(trio[pair[1]], kind)
					if d := labDistance(lab(a), lab(b)); d < want {
						t.Errorf("%s/%s: %s and %s are %.1f Lab apart as seen with %s, want %.0f",
							p.Name, kind, pair[0], pair[1], d, kind, want)
					}
				}
			}
		})
	}
}

// Resolve never guesses: a name it does not know is an error that lists the choices, because a
// typo that silently draws some other palette is worse than a message.
func TestResolve(t *testing.T) {
	for _, name := range Names() {
		got, err := Resolve(name)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", name, err)
		}
		if got.Name != name {
			t.Errorf("Resolve(%q) gave %q", name, got.Name)
		}
	}
	// Spelling should not matter, since the name is typed by hand at a shell.
	for _, name := range []string{"High Contrast", "high-contrast", "HIGH-CONTRAST"} {
		if got, err := Resolve(name); err != nil || got.Name != "high-contrast" {
			t.Errorf("Resolve(%q) = %q, %v", name, got.Name, err)
		}
	}
	if _, err := Resolve("solarized"); err == nil {
		t.Error("an unknown theme resolved without an error")
	} else if !strings.Contains(err.Error(), "midnight") {
		t.Errorf("the error does not name the choices: %v", err)
	}
}

// The automatic choice is the whole point of the feature: a dark background gets the dark palette.
func TestForBackground(t *testing.T) {
	if got := ForBackground(true); got.Light {
		t.Errorf("a dark terminal got the light palette %q", got.Name)
	}
	if got := ForBackground(false); !got.Light {
		t.Errorf("a light terminal got the dark palette %q", got.Name)
	}
}

// Set/Current is process state; a resolver must not be able to leave it empty.
func TestSetCurrent(t *testing.T) {
	was := Current()
	defer Set(was)
	p, _ := Lookup("daylight")
	Set(p)
	if Current().Name != "daylight" {
		t.Errorf("Current() = %q after Set", Current().Name)
	}
}

// ---------------------------------------------------------------- colour vision

// simulate renders a colour as a red-blind (prot) or green-blind (deut) reader sees it, through the
// standard LMS projection. It is a test-only approximation: enough to catch two hues that differ
// only in the red-green axis, which is exactly the mistake a status palette makes.
func simulate(hex, kind string) string {
	l, m, s := lms(hex)
	switch kind {
	case "prot":
		l = 2.02344*m - 2.52581*s
	case "deut":
		m = 0.494207*l + 1.24827*s
	}
	return fromLMS(l, m, s)
}

func lms(hex string) (l, m, s float64) {
	r, g, b, _ := rgb(hex)
	r, g, b = linear(r), linear(g), linear(b)
	return 0.31399*r + 0.63951*g + 0.04649*b,
		0.15537*r + 0.75789*g + 0.08673*b,
		0.01775*r + 0.10945*g + 0.87262*b
}

func fromLMS(l, m, s float64) string {
	r := 4.07674*l - 3.30771*m + 0.23097*s
	g := -1.26844*l + 2.60976*m - 0.34132*s
	b := -0.00420*l - 0.70342*m + 1.70686*s
	const digits = "0123456789abcdef"
	out := make([]byte, 7)
	out[0] = '#'
	for i, v := range []float64{r, g, b} {
		v = math.Max(0, math.Min(1, v))
		// linear back to sRGB
		if v <= 0.0031308 {
			v *= 12.92
		} else {
			v = 1.055*math.Pow(v, 1/2.4) - 0.055
		}
		n := int(math.Round(v * 255))
		out[1+i*2] = digits[n>>4]
		out[2+i*2] = digits[n&0xf]
	}
	return string(out)
}
