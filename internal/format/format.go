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
