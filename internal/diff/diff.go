// Package diff is the line diff the revision screens draw: what an agent added to a skill, what it
// removed, and what it left alone.
//
// It is its own package because two callers need the same answer — the proposal screen and
// `evalctl revise` — and a second implementation would eventually disagree with the first about what
// "changed" means.
package diff

import "strings"

// Kind is one line's fate.
type Kind int

const (
	Same Kind = iota
	Add
	Del
)

// Line is one line of the proposal, in reading order, with what happened to it.
type Line struct {
	Kind Kind
	Text string
}

// Lines returns the shortest edit script between two texts. A file that did not change comes back as
// its own unchanged lines, not as an empty list: the screen draws it, and "no edit" is a fact about
// the proposal rather than about the diff.
func Lines(before, after string) []Line {
	a, b := split(before), split(after)
	// lcs[i][j] is the length of the longest common subsequence of a[i:] and b[j:].
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
				continue
			}
			lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
		}
	}
	var out []Line
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, Line{Same, a[i]})
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, Line{Del, a[i]})
			i++
		default:
			out = append(out, Line{Add, b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, Line{Del, a[i]})
	}
	for ; j < len(b); j++ {
		out = append(out, Line{Add, b[j]})
	}
	return out
}

// Stat is how many lines the proposal adds and removes.
func Stat(lines []Line) (added, removed int) {
	for _, l := range lines {
		switch l.Kind {
		case Add:
			added++
		case Del:
			removed++
		}
	}
	return added, removed
}

// Changed is whether the proposal differs from the text it was written against.
func Changed(lines []Line) bool {
	for _, l := range lines {
		if l.Kind != Same {
			return true
		}
	}
	return false
}

func split(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
