package diff

import (
	"strings"
	"unicode"
)

// A character-level diff, for the one text in this tool that is a paragraph rather than a file.
//
// The line diff next door answers "which lines of SKILL.md changed", and a description has no lines:
// it is one sentence-shaped block of 100 to 1000 characters, and the question is which WORDS inside
// it moved. So this is the word-level diff every diff reader converges on — align the words, then
// refine each replaced run down to the characters that actually differ, because "the description
// names a context" becoming "the description names its context" is one word, and showing it as two
// different sentences is the same as showing nothing.
//
// The refinement is a plain LCS inside each replaced run, with one cleanup rule that turns it from
// confetti into prose: a shared run shorter than two characters, sitting between two changes and
// carrying no whitespace, is not evidence of anything — "for" against "from" shares an "r" and a
// reader does not want to be told. It is folded back into the change, so the pair reads as one
// substitution.

// Seg is one run of characters: text both versions share, text only the old one has, or text only
// the new one has. Concatenating the Same and Del runs gives the old text, Same and Add the new.
type Seg struct {
	Kind Kind
	Text string
}

// refineCap bounds the rune-level LCS. A rewrite of a 1000-character description is a cell per pair,
// and past a quarter of a million of them the answer is not worth the allocation: an unrelated
// replacement is shown as the whole run removed and the whole run added, which is also the truth.
const refineCap = 250_000

// Segments is the character-level diff between two texts.
//
// It is three rules, in the order they matter.
//
// The token pass aligns whole words, so the shared words of a rewritten clause stay on the screen as
// context. A change that separates two changes by nothing but whitespace joins them, because the
// weight of a matched space is not evidence of anything and a sentence that alternates struck-out
// word, word, struck-out word reads as a stutter. And a single word replaced by a single similar word
// is then refined to the characters that differ, which is where "description" against "descriptions"
// has to show one letter rather than the whole word twice.
//
// What it deliberately does not do is diff two unrelated words character by character. "avtivity"
// against "writing" shares "iti" and nothing else; the alignment said the word was replaced, and a
// reader wants the words that left and the words that arrived, not the coincidence in the middle.
func Segments(before, after string) []Seg {
	if before == after {
		if before == "" {
			return nil
		}
		return []Seg{{Kind: Same, Text: before}}
	}
	ops := tokenOps(tokens(before), tokens(after))
	var out []Seg
	for i := 0; i < len(ops); {
		if ops[i].Kind == Same {
			var b strings.Builder
			for ; i < len(ops) && ops[i].Kind == Same; i++ {
				b.WriteString(ops[i].Text)
			}
			out = append(out, Seg{Kind: Same, Text: b.String()})
			continue
		}
		dels, adds, next := changeRun(ops, i)
		i = next
		// A word for a word, and close enough that their shared letters mean something.
		if len(dels) == 1 && len(adds) == 1 && similar(dels[0], adds[0]) {
			out = append(out, refine(dels[0], adds[0])...)
			continue
		}
		// Everything else keeps its words whole, the old text first and the new one second.
		if s := strings.Join(dels, ""); s != "" {
			out = append(out, Seg{Kind: Del, Text: s})
		}
		if s := strings.Join(adds, ""); s != "" {
			out = append(out, Seg{Kind: Add, Text: s})
		}
	}
	return mergeNeighbourhoods(normalize(out))
}

// mergeNeighbourhoods joins changes separated only by punctuation or whitespace, and prints the old
// side before the new one.
//
// The aligner matches a comma across a long insertion because a matched token is free credit, and the
// result is one rewrite split into an insertion, a comma and a deletion — read in that order the
// sentence says the opposite of what happened: "…a throw, dont use during scripting avtivity or odds
// calculationfor working out…". A comma between two changes is not context a reader wants; it belongs
// to both sides, and taking it into both keeps the two texts exact while letting the neighbourhood
// read as one change.
func mergeNeighbourhoods(segs []Seg) []Seg {
	out := make([]Seg, 0, len(segs))
	var dels, adds strings.Builder
	flush := func() {
		if dels.Len() > 0 {
			out = append(out, Seg{Kind: Del, Text: dels.String()})
			dels.Reset()
		}
		if adds.Len() > 0 {
			out = append(out, Seg{Kind: Add, Text: adds.String()})
			adds.Reset()
		}
	}
	for i := 0; i < len(segs); i++ {
		s := segs[i]
		switch s.Kind {
		case Same:
			if dels.Len() > 0 || adds.Len() > 0 {
				// A comma or a space never separates two changes; neither does a short word the
				// aligner happened to find in the middle of two large rewrites, which is what "or"
				// was when "…avtivity or odds calculation" became "…roller or answering probability
				// questions": the reader gets more from one substitution than from a shared "or"
				// stranded between two halves of it.
				runes := len([]rune(s.Text))
				big := dels.Len()+adds.Len() >= bigChange
				if insubstantial(s.Text) ||
					(big && runes <= shortSame && regionSize(segs, i+1) >= bigChange) {
					dels.WriteString(s.Text)
					adds.WriteString(s.Text)
					continue
				}
			}
			flush()
			out = append(out, s)
		case Del:
			dels.WriteString(s.Text)
		case Add:
			adds.WriteString(s.Text)
		}
	}
	flush()
	return normalize(out)
}

// Two thresholds, and they are about size rather than about words: a change of a few characters keeps
// every shared word it has, because those are the words the reader is comparing; a change of a
// paragraph keeps none of the short ones, because at that size a shared "or" is the aligner finding a
// coincidence, not the two texts agreeing.
const (
	bigChange = 24
	shortSame = 12
)

// regionSize is how much changing text follows this point before the next piece of real context: the
// changes and the short shared runs between them, which are the ones a merge would take in. It has to
// be the whole region and not the next block, because the blocks of one rewrite are small once the
// aligner has split it — asking only the next block whether it is big lets the split stand.
func regionSize(segs []Seg, from int) int {
	size := 0
	for i := from; i < len(segs); i++ {
		if segs[i].Kind == Same && !insubstantial(segs[i].Text) && len([]rune(segs[i].Text)) > shortSame {
			break
		}
		size += len(segs[i].Text)
	}
	return size
}

// insubstantial is a shared run that carries no meaning of its own: a comma, a dash, a space. A run
// with a letter or a digit in it is context, and context splits one change from the next.
func insubstantial(s string) bool {
	if len([]rune(s)) >= 4 {
		return false
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// changeRun gathers one change from the token script: the tokens that left and the tokens that
// arrived, with the whitespace-only shared runs between them taken in on both sides. It stops at
// shared text that says something, which is the context the reader gets to keep, and returns the
// index it stopped at.
func changeRun(ops []Seg, i int) (dels, adds []string, next int) {
	j := i
	for j < len(ops) {
		s := ops[j]
		switch s.Kind {
		case Del:
			dels = append(dels, s.Text)
		case Add:
			adds = append(adds, s.Text)
		case Same:
			if strings.TrimSpace(s.Text) != "" || j+1 >= len(ops) || ops[j+1].Kind == Same {
				return dels, adds, j
			}
			// A space between two changes is the seam between them, not a fact about the text.
			dels = append(dels, s.Text)
			adds = append(adds, s.Text)
		}
		j++
	}
	return dels, adds, j
}

// similar reports whether two words are close enough that their shared characters are worth showing.
// The line is at half the longer word: "description"/"descriptions" is one letter, "use"/"uses" is
// one, and "avtivity"/"writing" is a coincidence that would be rendered as context it is not.
func similar(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return false
	}
	long := max(len(ra), len(rb))
	if long <= 2 {
		return a == b
	}
	lcs := lcsTable(len(ra), len(rb), func(i, j int) bool { return ra[i] == rb[j] }, nil)
	return lcs[0][0]*2 >= long
}

// SegmentsStat is how many characters the change adds and removes. Whitespace counts: a description
// over its ceiling is over its ceiling because of the spaces too.
func SegmentsStat(segs []Seg) (added, removed int) {
	for _, s := range segs {
		switch s.Kind {
		case Add:
			added += len([]rune(s.Text))
		case Del:
			removed += len([]rune(s.Text))
		}
	}
	return added, removed
}

// Sides rebuilds both texts from a diff: the old one and the new one. It is what makes a diff
// checkable — a rendering that cannot be run back into its two inputs is a rendering that lost text.
func Sides(segs []Seg) (before, after string) {
	var b, a strings.Builder
	for _, s := range segs {
		switch s.Kind {
		case Same:
			b.WriteString(s.Text)
			a.WriteString(s.Text)
		case Del:
			b.WriteString(s.Text)
		case Add:
			a.WriteString(s.Text)
		}
	}
	return b.String(), a.String()
}

// SegsChanged reports whether anything moved.
func SegsChanged(segs []Seg) bool {
	for _, s := range segs {
		if s.Kind != Same {
			return true
		}
	}
	return false
}

// refine is the character-level diff of one replaced run.
func refine(before, after string) []Seg {
	switch {
	case before == "":
		return []Seg{{Kind: Add, Text: after}}
	case after == "":
		return []Seg{{Kind: Del, Text: before}}
	}
	a, b := []rune(before), []rune(after)
	if len(a)*len(b) > refineCap {
		return []Seg{{Kind: Del, Text: before}, {Kind: Add, Text: after}}
	}
	return coalesceChanges(absorbIslands(normalize(runeOps(a, b))))
}

// coalesceChanges turns one replaced neighbourhood into "all of the old text, then all of the new":
// the run `-be +g -a +amma` says the same thing as `-beta +gamma` and takes twice the room to say
// it. The shared text between two such runs is the only thing that survives as context.
func coalesceChanges(segs []Seg) []Seg {
	out := make([]Seg, 0, len(segs))
	var dels, adds strings.Builder
	flush := func() {
		if dels.Len() > 0 {
			out = append(out, Seg{Kind: Del, Text: dels.String()})
			dels.Reset()
		}
		if adds.Len() > 0 {
			out = append(out, Seg{Kind: Add, Text: adds.String()})
			adds.Reset()
		}
	}
	for _, s := range segs {
		switch s.Kind {
		case Same:
			flush()
			out = append(out, s)
		case Del:
			dels.WriteString(s.Text)
		case Add:
			adds.WriteString(s.Text)
		}
	}
	flush()
	return normalize(out)
}

// absorbIslands folds a one-character shared run back into the change around it. The rule is
// deliberately narrow: it must be shorter than two runes, contain no whitespace, and have a change
// on each side. A leading or trailing shared character is a real prefix or suffix and reads well
// ("description" → "descriptions" keeps the word and shows the s); a shared "r" in the middle of
// "for" → "from" is noise, and turning it into one substitution deletes the noise without lying
// about the text.
func absorbIslands(segs []Seg) []Seg {
	out := make([]Seg, 0, len(segs)+4)
	for i, s := range segs {
		if s.Kind == Same && len([]rune(s.Text)) < 2 && !strings.ContainsFunc(s.Text, unicode.IsSpace) &&
			i > 0 && i < len(segs)-1 {
			out = append(out, Seg{Kind: Del, Text: s.Text}, Seg{Kind: Add, Text: s.Text})
			continue
		}
		out = append(out, s)
	}
	return normalize(out)
}

// normalize merges neighbours of one kind and drops the empty ones, so the same diff reached by two
// paths renders identically.
func normalize(segs []Seg) []Seg {
	out := make([]Seg, 0, len(segs))
	for _, s := range segs {
		if s.Text == "" {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Kind == s.Kind {
			out[n-1].Text += s.Text
			continue
		}
		out = append(out, s)
	}
	return out
}

// ---------------------------------------------------------------- display

// cell is one rune and the kind it belongs to: the diff is wrapped as cells and regrouped per line,
// because a break in the middle of a run has to keep that run's kind on both sides.
type cell struct {
	kind Kind
	r    rune
}

// WrapSegs lays a diff out in lines of at most width cells, breaking at spaces where it can and
// inside a word only when the word is longer than the line. A paragraph diff on one 400-cell line is
// a line nobody reads, and a break that drops the style turns the last word of every row into
// unmarked text.
func WrapSegs(segs []Seg, width int) [][]Seg {
	var cells []cell
	for _, s := range segs {
		for _, r := range s.Text {
			if r == '\n' {
				// A description is one paragraph, but a caller's text may not be: a newline ends a
				// line and is not drawn.
				cells = append(cells, cell{kind: -1})
				continue
			}
			cells = append(cells, cell{kind: s.Kind, r: r})
		}
	}
	if width <= 0 {
		width = len(cells)
	}
	if len(cells) == 0 {
		return [][]Seg{{}}
	}
	var lines [][]Seg
	for i := 0; i < len(cells); {
		if cells[i].kind == -1 { // a forced break
			lines = append(lines, nil)
			i++
			continue
		}
		end := i + width
		if end > len(cells) {
			end = len(cells)
		}
		// Break at the last space that fits, so words stay whole.
		for j := end; j > i; j-- {
			if cells[j-1].kind == -1 || unicode.IsSpace(cells[j-1].r) {
				end = j
				break
			}
		}
		if end == i { // a single cell wider than the line (a forced break)
			end = i + 1
		}
		lines = append(lines, groupCells(cells[i:end]))
		i = end
		// A break at a space leaves the space at the end of the line; the next line starts at the
		// word, not with the indent the space was providing.
		for i < len(cells) && unicode.IsSpace(cells[i].r) {
			i++
		}
	}
	return lines
}

// groupCells rebuilds the runs of one wrapped line.
func groupCells(cells []cell) []Seg {
	var out []Seg
	for _, c := range cells {
		if c.kind == -1 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Kind == c.kind {
			out[n-1].Text += string(c.r)
			continue
		}
		out = append(out, Seg{Kind: c.kind, Text: string(c.r)})
	}
	return out
}

// tokens cuts text into the units a description is read in: words (with their internal hyphen and
// apostrophe), runs of whitespace, and one punctuation mark per token. Punctuation single-character
// on purpose — a comma that moves from one side of a clause to the other is a change to that comma,
// not to the phrase it is glued to.
func tokens(s string) []string {
	var out []string
	var cur []rune
	kind := -1 // -1 nothing, 0 punctuation, 1 space, 2 word
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = nil
		}
	}
	for _, r := range s {
		k := 0
		switch {
		case unicode.IsSpace(r):
			k = 1
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' || r == '’' || r == '-' || r == '_':
			k = 2
		}
		if k == 0 {
			flush()
			out = append(out, string(r))
			kind = -1
			continue
		}
		if k != kind {
			flush()
			kind = k
		}
		cur = append(cur, r)
	}
	flush()
	return out
}

// tokenOps is the shortest edit script over tokens, with matches weighted.
func tokenOps(a, b []string) []Seg {
	lcs := lcsTable(len(a), len(b), func(i, j int) bool { return a[i] == b[j] }, func(i int) int {
		return tokenWeight(a[i])
	})
	var out []Seg
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, Seg{Kind: Same, Text: a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, Seg{Kind: Del, Text: a[i]})
			i++
		default:
			out = append(out, Seg{Kind: Add, Text: b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, Seg{Kind: Del, Text: a[i]})
	}
	for ; j < len(b); j++ {
		out = append(out, Seg{Kind: Add, Text: b[j]})
	}
	return out
}

// tokenWeight is what a matched token is worth.
//
// The weights are the difference between a readable diff and a positional one, and that is not an
// exaggeration: an unweighted LCS counts a matched space exactly as it counts a matched word, and a
// paragraph rewritten in place is full of spaces. The aligner then keeps every space and pairs
// whatever single word happens to sit between two of them — "dont use during scripting avtivity"
// against "a quick throw of one" came back as five word-for-word substitutions, each rendered as one
// word struck out and another one beside it. Weighting a word a thousand times a space makes the
// aligner spend its matches on the words that carry meaning, and the same paragraph then reads as one
// clause leaving and one clause arriving.
func tokenWeight(s string) int {
	switch {
	case strings.TrimSpace(s) == "":
		return 1
	case startsWord(s):
		return 1000
	}
	return 10
}

// startsWord reports whether a token begins with a letter or a digit: the units a reader aligns on.
func startsWord(s string) bool {
	for _, r := range s {
		return unicode.IsLetter(r) || unicode.IsDigit(r)
	}
	return false
}

// runeOps is the same script one level down: every character is worth the same, because inside a
// single word or a single pair of words there is nothing to prefer.
func runeOps(a, b []rune) []Seg {
	lcs := lcsTable(len(a), len(b), func(i, j int) bool { return a[i] == b[j] }, nil)
	var out []Seg
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, Seg{Kind: Same, Text: string(a[i])})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, Seg{Kind: Del, Text: string(a[i])})
			i++
		default:
			out = append(out, Seg{Kind: Add, Text: string(b[j])})
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, Seg{Kind: Del, Text: string(a[i])})
	}
	for ; j < len(b); j++ {
		out = append(out, Seg{Kind: Add, Text: string(b[j])})
	}
	return out
}

// lcsTable is the suffix table both alignments walk: lcs[i][j] is the best total weight of a common
// subsequence of a[i:] and b[j:]. Indexing from the end is what makes the traceback greedy from the
// front, which is the order a reader expects the changes in. A nil weight counts every element as one,
// which is what a character-level table wants.
func lcsTable(n, m int, eq func(i, j int) bool, weight func(i int) int) [][]int {
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		w := 1
		if weight != nil {
			w = weight(i)
		}
		for j := m - 1; j >= 0; j-- {
			if eq(i, j) {
				lcs[i][j] = lcs[i+1][j+1] + w
				continue
			}
			if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	return lcs
}
