package diff

import (
	"strings"
	"testing"
)

// TestHunksCutAtTheChanges: the window is the change plus context, and two changes close together
// are one hunk — the property that keeps a small edit from reading as a whole-file rewrite.
func TestHunksCutAtTheChanges(t *testing.T) {
	before := strings.Join([]string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}, "\n")
	// Change "c" and "j": far apart, so two hunks; each carries two lines of context.
	after := strings.Replace(strings.Replace(before, "c", "C", 1), "j", "J", 1)
	hunks := Hunks(Lines(before+"\n", after+"\n"), 2)
	if len(hunks) != 2 {
		t.Fatalf("hunks = %d, want 2:\n%+v", len(hunks), hunks)
	}
	if hunks[0].OldStart != 1 || hunks[0].OldCount != 5 || hunks[0].Changes != 2 {
		t.Fatalf("first hunk = %+v, want lines 1..5 with one add and one delete", hunks[0])
	}
	if hunks[1].NewStart != 8 {
		t.Fatalf("second hunk starts at %d, want 8", hunks[1].NewStart)
	}
	// Neighbours merge: "b" and "c" changed with two lines of context is one window.
	merged := Hunks(Lines("a\nb\nc\nd\n", "a\nB\nC\nd\n"), 2)
	if len(merged) != 1 {
		t.Fatalf("nearby changes = %d hunks, want 1", len(merged))
	}
	if none := Hunks(Lines("a\nb\n", "a\nb\n"), 3); none != nil {
		t.Fatalf("an unchanged file has hunks: %+v", none)
	}
}

// TestApplyTakesTheSelectedSide: a partial accept has to be a real edit — the selected hunks' new
// text with the unselected hunks' old text, and nothing of the ones rejected.
func TestApplyTakesTheSelectedSide(t *testing.T) {
	before := "a\nb\nc\nd\ne\nf\ng\nh\n"
	after := "a\nB\nc\nd\ne\nf\nG\nh\n"
	lines := Lines(before, after)
	hunks := Hunks(lines, 1)
	if len(hunks) != 2 {
		t.Fatalf("hunks = %d, want 2", len(hunks))
	}
	all := Apply(lines, hunks, func(int) bool { return true })
	if all != after {
		t.Fatalf("apply all = %q, want %q", all, after)
	}
	none := Apply(lines, hunks, func(int) bool { return false })
	if none != before {
		t.Fatalf("apply none = %q, want the original %q", none, before)
	}
	first := Apply(lines, hunks, func(i int) bool { return i == 0 })
	if first != "a\nB\nc\nd\ne\nf\ng\nh\n" {
		t.Fatalf("apply first = %q", first)
	}
	if added, removed := StatOf(hunks, func(i int) bool { return i == 0 }); added != 1 || removed != 1 {
		t.Fatalf("stat of the first hunk = +%d −%d", added, removed)
	}
}
