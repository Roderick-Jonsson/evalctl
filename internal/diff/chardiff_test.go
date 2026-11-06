package diff

import (
	"strings"
	"testing"
)

// The diff is only usable if it can be trusted twice over: it must rebuild both texts exactly (no
// character invented, none lost) and it must read as prose rather than confetti. The tests below pin
// both halves — the rebuild on every case, and the shape on the cases where a naive LCS is unreadable.

func TestSegmentsRebuildBothSides(t *testing.T) {
	cases := []struct{ before, after string }{
		{"", ""},
		{"Roll a die.", "Roll a die."},
		{"Roll a die.", "Roll two dice."},
		{"", "An entirely new description."},
		{"An old description.", ""},
		{"Use this when the user asks for a chart.", "Use this skill when the user asks for a chart, graph or plot."},
		{"Write a description for a skill that routes.", "Write descriptions for skills that route."},
		{"Zürich-based design work — posters, wayfinding.", "Zürich design work — posters and wayfinding."},
		{strings.Repeat("word ", 300), strings.Repeat("word ", 200) + "other " + strings.Repeat("word ", 99)},
	}
	for _, c := range cases {
		segs := Segments(c.before, c.after)
		gotBefore, gotAfter := Sides(segs)
		if gotBefore != c.before {
			t.Errorf("before side not rebuilt\n in: %q\nout: %q", c.before, gotBefore)
		}
		if gotAfter != c.after {
			t.Errorf("after side not rebuilt\n in: %q\nout: %q", c.after, gotAfter)
		}
	}
}

func TestIdenticalTextIsOneSameRun(t *testing.T) {
	segs := Segments("a description", "a description")
	if len(segs) != 1 || segs[0].Kind != Same || segs[0].Text != "a description" {
		t.Fatalf("identical text should be one Same run, got %+v", segs)
	}
	if SegsChanged(segs) {
		t.Error("identical text reported as changed")
	}
}

// A replaced word is replaced, not split into every character it happens to share with its
// neighbour: the shared prefix survives because it is a prefix, and the tail is one substitution.
func TestAChangedWordKeepsItsSharedStem(t *testing.T) {
	segs := Segments("description", "descriptions")
	before, after := Sides(segs)
	if before != "description" || after != "descriptions" {
		t.Fatalf("rebuilt wrongly: %q → %q", before, after)
	}
	added, removed := SegmentsStat(segs)
	if added != 1 || removed != 0 {
		t.Errorf("changing description → descriptions should add one character, got +%d −%d", added, removed)
	}
	for _, s := range segs {
		if s.Kind == Del && strings.Contains(s.Text, "descripti") {
			t.Errorf("the shared stem was deleted: %+v", segs)
		}
	}
}

// The cleanup rule: a single shared character between two changes is noise and is folded into the
// substitution, so the neighbourhood reads as one swap instead of three. With "abXcd" against
// "pqXrs" the shared X is the only witness of anything, and it tells the reader nothing.
func TestASingleSharedCharacterIsFoldedAway(t *testing.T) {
	segs := Segments("abXcd", "pqXrs")
	if len(segs) != 2 || segs[0].Kind != Del || segs[1].Kind != Add {
		t.Fatalf("want one removal and one addition, got %+v", segs)
	}
	if segs[0].Text != "abXcd" || segs[1].Text != "pqXrs" {
		t.Fatalf("the substitution lost text: %+v", segs)
	}
}

// A shared PREFIX is not an island: changing "description" to "descriptions" has to keep the word
// and show the letter that arrived, or every edit of a word is a replacement of it.
func TestASharedPrefixSurvives(t *testing.T) {
	segs := Segments("description", "descriptions")
	if b, a := Sides(segs); b != "description" || a != "descriptions" {
		t.Fatalf("rebuilt wrongly: %q → %q", b, a)
	}
	if len(segs) < 2 || segs[len(segs)-1].Kind != Add || segs[len(segs)-1].Text != "s" {
		t.Errorf("want the shared stem kept and one letter added, got %+v", segs)
	}
}

// A shared word between two changes stays: "the chart" → "a chart, graph" keeps "chart" and the
// reader sees one insertion on each side of it, which is what happened.
func TestASharedWordBetweenChangesSurvives(t *testing.T) {
	segs := Segments("the chart", "a chart, graph")
	var same []string
	for _, s := range segs {
		if s.Kind == Same {
			same = append(same, strings.TrimSpace(s.Text))
		}
	}
	if len(same) != 1 || same[0] != "chart" {
		t.Errorf("want the word \"chart\" shared once, got %+v", segs)
	}
}

// The regression that removed whole-region character refinement: a rewritten clause must move WORDS,
// never glue the tail of one word to the head of the next. The real case is a description rewritten
// from "dont use during scripting avtivity" to "do not use it during scripting to generate code".
func TestARewrittenClauseKeepsItsWordsWhole(t *testing.T) {
	segs := Segments("dont use during scripting avtivity", "do not use it during scripting to generate code")
	if b, a := Sides(segs); b != "dont use during scripting avtivity" || a != "do not use it during scripting to generate code" {
		t.Fatalf("rebuilt wrongly: %q → %q", b, a)
	}
	var dels, adds int
	for _, s := range segs {
		switch s.Kind {
		case Del:
			dels++
		case Add:
			adds++
		}
	}
	if dels == 0 || adds == 0 {
		t.Errorf("want both a removal and an addition, got %+v", segs)
	}
	// The shared words of the clause are context: they stay on screen between the change on the left
	// and the change on the right, which is the whole reason this is a token diff.
	foundContext := false
	for _, s := range segs {
		if s.Kind == Same && strings.Contains(s.Text, "during scripting") {
			foundContext = true
		}
	}
	if !foundContext {
		t.Errorf("\"during scripting\" did not survive as context: %+v", segs)
	}
	// The regression itself: the removed word is one word and the added clause is three words, each
	// printed whole. Before the fix these came out as "avtivity|to" and "generateit".
	if !hasRun(segs, Del, "avtivity") {
		t.Errorf("the removed word was mangled: %+v", segs)
	}
	if !hasRun(segs, Add, "to generate code") {
		t.Errorf("the added clause was mangled: %+v", segs)
	}
}

// hasRun reports whether one kind of change carries exactly that text, ignoring the whitespace the
// alignment attached to either end of it.
func hasRun(segs []Seg, kind Kind, want string) bool {
	for _, s := range segs {
		if s.Kind == kind && strings.TrimSpace(s.Text) == want {
			return true
		}
	}
	return false
}

// Whitespace is never folded into a change: two words that merely sit next to each other are not a
// substitution of the space between them.
func TestWhitespaceIsNotAnIsland(t *testing.T) {
	segs := Segments("alpha beta", "alpha gamma")
	if len(segs) < 2 || segs[0].Kind != Same || !strings.HasSuffix(segs[0].Text, " ") {
		t.Errorf("the space between the words was not kept as shared text: %+v", segs)
	}
	if b, a := Sides(segs); b != "alpha beta" || a != "alpha gamma" {
		t.Errorf("rebuilt wrongly: %q → %q", b, a)
	}
}

func TestSegmentsStatCountsCharacters(t *testing.T) {
	segs := Segments("a b", "a c")
	added, removed := SegmentsStat(segs)
	if added != 1 || removed != 1 {
		t.Fatalf("want +1 −1, got +%d −%d (%+v)", added, removed, segs)
	}
}

// A total rewrite of one run is not a character study: past the refinement cap the answer is the
// whole run removed and the whole run added, which is also the truth.
func TestAVastRewriteIsNotRefined(t *testing.T) {
	before := strings.Repeat("α", 600)
	after := strings.Repeat("β", 600)
	segs := refine(before, after)
	if len(segs) != 2 || segs[0].Kind != Del || segs[1].Kind != Add {
		t.Fatalf("want the capped pair, got %d segments", len(segs))
	}
	if b, a := Sides(segs); b != before || a != after {
		t.Fatal("a capped refinement lost text")
	}
}

func TestTokensKeepWordsWhole(t *testing.T) {
	got := tokens("well-known, isn't it")
	want := []string{"well-known", ",", " ", "isn't", " ", "it"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("tokens = %q, want %q", got, want)
	}
}

// A rewrite in place is one change, not a stutter. The aligner finds small islands inside a large
// rewrite — the shared letters of avtivity/activity, the "or" between two replaced clauses — and each
// one used to split the neighbourhood, so the pane printed: delete, insert, delete, insert. One
// removal and one addition is what actually happened, and it is what the reader needs.
func TestARewriteInPlaceIsOneChange(t *testing.T) {
	before := "Roll dice and report the faces that come up, dont use during scripting avtivity or odds calculation"
	after := "Roll dice and report the faces that come up, not for generating code, scripting, or probability calculations."
	segs := Segments(before, after)
	if b, a := Sides(segs); b != before || a != after {
		t.Fatalf("rebuilt wrongly:\n%q\n%q", b, a)
	}
	var kinds []Kind
	for _, s := range segs {
		kinds = append(kinds, s.Kind)
	}
	// same, del, add — one neighbourhood, printed old-then-new.
	if len(segs) != 3 || segs[0].Kind != Same || segs[1].Kind != Del || segs[2].Kind != Add {
		t.Fatalf("want one removal and one addition after the shared opening, got %v: %+v", kinds, segs)
	}
}
