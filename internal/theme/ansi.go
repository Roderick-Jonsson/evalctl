package theme

import (
	"math"
	"strconv"

	"github.com/charmbracelet/lipgloss"
)

// Not every terminal renders 24-bit colour. Apple Terminal — the one this tool is used in — answers
// with 256, so a #rrggbb value is not printed as written: it is approximated, and the approximation
// the libraries pick is not the closest one. termenv turns #E6EDF3 (a near-white) into xterm 195
// (#d7ffff), a visibly cyan white, and #9FB0C3 (a cool grey) into 146 (#afafd7), a blue-tinted one;
// the drift is exactly the hue shift a careful palette is trying to avoid.
//
// So the mapping is done here, in CIE Lab, and handed to Lip Gloss and Glamour as explicit indices
// (CompleteColor's 256 and 16 slots, glamour's numeric colour form). A truecolor terminal still gets
// the hex; a 256-colour terminal gets the nearest entry by perceptual distance; a 16-colour terminal
// gets the nearest system colour, with one rule — never black on a dark palette, because a border or
// a selection drawn in the background colour is not quiet, it is invisible.

// Color is the colour to draw a role with: the hex where the terminal can show it, the nearest
// xterm-256 index where it cannot, and the nearest ANSI-16 entry below that.
func (p Palette) Color(hex string) lipgloss.TerminalColor {
	return lipgloss.CompleteColor{TrueColor: hex, ANSI256: strconv.Itoa(p.nearest256(hex)), ANSI: strconv.Itoa(p.nearest16(hex))}
}

// ANSI256 is the xterm-256 index of a colour as a decimal string — the form glamour's style config
// takes, since its renderer is pinned to 256 colours.
func (p Palette) ANSI256(hex string) string { return strconv.Itoa(p.nearest256(hex)) }

// nearest256 picks the xterm entry that is closest to hex *and* keeps the contrast the palette
// promised: an index that is close in hue but twice as bright on the background would leave a
// terminal showing exactly the unreadable screen the palette was written to avoid. The 16 system
// colours are the ones a user's own theme redefines, so the fixed cube and grey ramp are preferred
// slightly — a 256-colour terminal then draws what the palette says, not what someone's profile
// happens to call blue.
func (p Palette) nearest256(hex string) int {
	want := Contrast(hex, p.BG)
	pick := func(lo, hi float64) (int, float64) {
		best, bestD := -1, math.Inf(1)
		for i := 0; i < 256; i++ {
			c := xterm(i)
			if got := Contrast(c, p.BG); got < lo || got > hi {
				continue
			}
			d := labDistance(lab(hex), lab(c))
			if i < 16 {
				d *= 1.15
			}
			if d < bestD {
				best, bestD = i, d
			}
		}
		return best, bestD
	}
	// ±20% of the promised contrast is invisible in use; ±25% upwards would not be.
	if best, _ := pick(want*0.8, want*1.25); best >= 0 {
		return best
	}
	if best, _ := pick(0, math.Inf(1)); best >= 0 {
		return best
	}
	return 231
}

// nearest16 picks the closest of the sixteen system colours. On a dark palette black is swapped for
// bright black: every role here is meant to be seen on that background.
func (p Palette) nearest16(hex string) int {
	target := lab(hex)
	best, bestD := 7, math.Inf(1)
	for i := 0; i < 16; i++ {
		if d := labDistance(target, lab(xterm(i))); d < bestD {
			best, bestD = i, d
		}
	}
	if best == 0 && !p.Light {
		return 8
	}
	return best
}

// xterm is the standard 256-colour table: 16 system entries, a 6×6×6 cube, then 24 greys.
func xterm(n int) string {
	system := [...]string{
		"#000000", "#990000", "#00a666", "#999900", "#0000b3", "#b300b3", "#00a6b3", "#bfbfbf",
		"#666666", "#e60000", "#00d933", "#e6e600", "#0000ff", "#e600e6", "#00e6e6", "#e6e6e6",
	}
	switch {
	case n < 16:
		return system[n]
	case n < 232:
		steps := [6]int{0, 95, 135, 175, 215, 255}
		n -= 16
		return rgbHex(steps[n/36], steps[(n/6)%6], steps[n%6])
	default:
		v := 8 + (n-232)*10
		return rgbHex(v, v, v)
	}
}

func rgbHex(r, g, b int) string {
	const digits = "0123456789abcdef"
	out := []byte{'#', 0, 0, 0, 0, 0, 0}
	for i, v := range []int{r, g, b} {
		out[1+i*2] = digits[v>>4]
		out[2+i*2] = digits[v&0xf]
	}
	return string(out)
}

// lab converts #rrggbb to CIE L*a*b* (D65). Perceptual distance is the point: two colours can be far
// apart in RGB and indistinguishable on screen, and a 256-entry palette is small enough that the
// right entry can simply be searched for.
func lab(hex string) [3]float64 {
	r, g, b, _ := rgb(hex)
	r, g, b = linear(r), linear(g), linear(b)
	x := 0.4124*r + 0.3576*g + 0.1805*b
	y := 0.2126*r + 0.7152*g + 0.0722*b
	z := 0.0193*r + 0.1192*g + 0.9505*b
	fx, fy, fz := labF(x/0.95047), labF(y/1.0), labF(z/1.08883)
	return [3]float64{116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)}
}

func labF(t float64) float64 {
	if t > 0.008856 {
		return math.Cbrt(t)
	}
	return 7.787*t + 16.0/116.0
}

func labDistance(a, b [3]float64) float64 {
	dl, da, db := a[0]-b[0], a[1]-b[1], a[2]-b[2]
	return math.Sqrt(dl*dl + da*da + db*db)
}
