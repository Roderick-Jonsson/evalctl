package tui

import "strings"

// A line diff for the proposal screen: what the reviser added, what it removed, and what it left
// alone. It is a plain LCS over lines — a skill is a few hundred lines, so the quadratic table is
// nothing, and the output is the same shape `git diff` shows minus the hunk headers.

// diffKind is one line's fate.
type diffKind int

const (
	diffSame diffKind = iota
	diffAdd
	diffDel
)

type diffLine struct {
	Kind diffKind
	Text string
}

// lineDiff returns the shortest edit script between two texts, in reading order. A file that did not
// change comes back as the same lines (not an empty list), because the screen draws it unchanged.
func lineDiff(before, after string) []diffLine {
	a := splitLines(before)
	b := splitLines(after)
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
	var out []diffLine
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, diffLine{diffSame, a[i]})
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, diffLine{diffDel, a[i]})
			i++
		default:
			out = append(out, diffLine{diffAdd, b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, diffLine{diffDel, a[i]})
	}
	for ; j < len(b); j++ {
		out = append(out, diffLine{diffAdd, b[j]})
	}
	return out
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// diffStat is "N added, M removed" for the screen's head.
func diffStat(lines []diffLine) (added, removed int) {
	for _, l := range lines {
		switch l.Kind {
		case diffAdd:
			added++
		case diffDel:
			removed++
		}
	}
	return added, removed
}

// changed is whether the proposal differs from the text it was written against at all.
func changed(lines []diffLine) bool {
	for _, l := range lines {
		if l.Kind != diffSame {
			return true
		}
	}
	return false
}
