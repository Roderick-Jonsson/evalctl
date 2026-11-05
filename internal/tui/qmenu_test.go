package tui

import (
	"strings"
	"testing"

	"evalctl/internal/store"
)

// TestTheMenuCannotWalkIntoALockedView is the closed loop's cursor contract. The two shut views stay
// in the list — seeing what the loop will open is why the menu draws them, dim, with the reason in
// place of their numbers — but no move key puts the cursor on one: `enter` there could only refuse,
// and a row you can stand on is a row that looks open.
func TestTheMenuCannotWalkIntoALockedView(t *testing.T) {
	m, _ := gateFixture(t, true)
	if m.mode != screenQualityMenu || m.quality.vcursor != qSetup {
		t.Fatalf("lane opened at mode %v row %d, want the menu on the set", m.mode, m.quality.vcursor)
	}
	for _, key := range []string{"j", "down", "G", "end", "k", "up", "g", "home"} {
		press(m, key)
		if m.quality.vcursor != qSetup {
			t.Fatalf("after %q the cursor is on row %d, want it held on the set — the other two are locked",
				key, m.quality.vcursor)
		}
	}
	// The rows are still drawn, dim, with the fact that keeps them shut: the menu has to say what the
	// loop will open, it just may not walk there.
	body := stripMarks(strings.Join(m.qualityMenuBody(m.bodyHeight()), "\n"))
	for _, want := range []string{"SECTIONS", "initialization", "iteration", "testing quality",
		"locked until the loop opens"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the closed menu is missing %q:\n%s", want, body)
		}
	}
	// And the band does not promise the move: one open row means j k and 1 2 3 have nowhere to go.
	for _, h := range m.qualityMenuHints() {
		if h.key == "j k" || h.key == "1 2 3" {
			t.Fatalf("the band offers %q to a closed loop, which cannot move", h.key)
		}
	}
}

// TestTheOpenMenuWalksEveryRow is the other half: once the loop is open nothing is locked, so the
// cursor reaches all three — and it still stops at the ends instead of wrapping onto a row the
// operator did not ask for.
func TestTheOpenMenuWalksEveryRow(t *testing.T) {
	m, _ := qualityFixture(t)
	m.lane = store.LaneOutput
	m.openQualitySkill("dummy")
	if m.mode != screenQualityMenu || m.quality.vcursor != qDojo {
		t.Fatalf("menu at mode %v row %d, want the iteration — its newest run failed", m.mode, m.quality.vcursor)
	}
	steps := []struct {
		key  string
		want int
	}{
		{"j", qQuality}, {"j", qQuality}, // the last row is the end, not a wrap
		{"k", qDojo}, {"k", qSetup}, {"k", qSetup},
		{"G", qQuality}, {"g", qSetup}, {"end", qQuality}, {"home", qSetup},
	}
	for _, s := range steps {
		press(m, s.key)
		if m.quality.vcursor != s.want {
			t.Fatalf("after %q the cursor is on row %d, want %d", s.key, m.quality.vcursor, s.want)
		}
	}
	// With three rows open the move is offered again, or the closed-menu band would be the one the
	// operator learnt.
	var haveMove bool
	for _, h := range m.qualityMenuHints() {
		if h.key == "j k" {
			haveMove = true
		}
	}
	if !haveMove {
		t.Fatalf("the open menu does not band j k: %v", m.qualityMenuHints())
	}
}
