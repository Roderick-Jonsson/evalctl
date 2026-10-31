// Package format holds the two or three string conventions both faces of evalctl depend on. It
// exists so the TUI and the CLI cannot print the same fact two different ways — a count that reads
// "1 warning(s)" in one and "1 warning" in the other is the kind of drift a reader stops trusting.
package format

import "fmt"

// Plural renders a count with a noun that agrees with it. "3 row(s)" makes the reader parse the
// parenthesised suffix to learn whether they are looking at one thing or several.
func Plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// Pct renders a pass rate as a whole percentage of the rows that were judged. A set of five
// validation queries cannot support a decimal, and printing one invites a decision it does not
// support; zero judged rows is a dash, never 0%, because "not measured" and "measured at zero" are
// different facts.
func Pct(passed, judged int) string {
	if judged == 0 {
		return "—"
	}
	return fmt.Sprintf("%d%%", (passed*200+judged)/(2*judged))
}

// Pts renders a difference between two percentages ("+20%"), for the gap beside two rates.
func Pts(n int) string { return fmt.Sprintf("%d%%", n) }
