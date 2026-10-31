package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"evalctl/internal/format"
	"evalctl/internal/runner"
)

// The string arithmetic every screen needs, in one place: a view that fits its own lines fits them
// the same way as the next one, and a test can hold one function instead of seven habits.

func plural(n int, noun string) string { return format.Plural(n, noun) }

// pluralOf is plural for the nouns that do not take an s — "2 train querys" is the kind of detail
// that makes a careful screen look careless.
func pluralOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// oneLine flattens a field that is allowed to be prose: a row is one line, and a newline inside a
// cell is a row that grew a second line and pushed the table off the frame.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// clipText is oneLine at a width: the body of a fact line, where the value came from a file.
func clipText(s string, n int) string { return ellipsis(oneLine(s), n) }

// ellipsis cuts to width cells and ends with the mark: a cut that says nothing reads as a value
// that happened to end there.
func ellipsis(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return glyphs.Ellipsis
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + glyphs.Ellipsis
}

// fitTo cuts or pads a string to exactly w cells, so a column never reflows.
func fitTo(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ellipsis(s, w)
	if n := lipgloss.Width(s); n < w {
		s += strings.Repeat(" ", w-n)
	}
	return s
}

// endsLine puts the left half at the left edge and the right half at the right edge of one row, and
// cuts the left one when the two would meet. Every bar in the frame is this function.
func endsLine(left, right string, w int) string {
	if w <= 0 {
		return ""
	}
	rw := lipgloss.Width(right)
	room := w - rw - 1
	if room < 0 {
		return ellipsis(right, w)
	}
	left = fitTo(left, room)
	if rw == 0 {
		return left
	}
	return left + " " + right
}

// wrapPlain folds prose on spaces to at most w cells. It is the only wrapper the step screens use:
// glamour is for reports, and a two-sentence rule does not need a markdown renderer.
func wrapPlain(text string, w int) []string {
	if w <= 0 {
		return []string{text}
	}
	var out []string
	for _, para := range strings.Split(text, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := words[0]
		for _, word := range words[1:] {
			if lipgloss.Width(line)+1+lipgloss.Width(word) <= w {
				line += " " + word
				continue
			}
			out = append(out, line)
			line = word
		}
		out = append(out, line)
	}
	return out
}

// fitWidth is the last line of defence for the frame: a view is a window, so a line wider than the
// window is a line that wraps and pushes the status row off the screen. It is applied once, in View,
// to catch the composition nobody measured — every screen still aims to fit itself.
func fitWidth(s string, w int) string {
	if w <= 0 || lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

// pct is a rate as the screens print it: a judged count, never an average of averages.
func pct(passed, judged int) string { return format.Pct(passed, judged) }

// gapOf is train minus validation: the number the generalization step exists to show. A description
// that learned the train set and nothing else has a large positive gap.
func gapOf(train, valid *runner.LabTally) string {
	if train == nil || valid == nil || train.Judged == 0 || valid.Judged == 0 {
		return "—"
	}
	d := pctInt(train.Passed, train.Judged) - pctInt(valid.Passed, valid.Judged)
	switch {
	case d > 0:
		return "+" + format.Pts(d)
	case d < 0:
		return "−" + format.Pts(-d)
	}
	return format.Pts(0)
}

// pctInt is the rate as a whole number, which is what every comparison in these screens is about:
// a 3% difference between two descriptions of five queries is noise, and printing it as 96.7 to
// 100.0 invites a decision the set cannot support.
func pctInt(passed, judged int) int {
	if judged == 0 {
		return 0
	}
	return (passed*200 + judged) / (2 * judged)
}
