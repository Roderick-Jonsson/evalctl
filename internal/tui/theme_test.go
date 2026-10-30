package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"evalctl/internal/theme"
)

// The style table has to follow the palette, or a theme is a flag that changes nothing. The test
// pins the colour profile first: without a terminal, Lip Gloss renders plain text and every
// assertion here would pass for free.
func TestStylesFollowPalette(t *testing.T) {
	was := theme.Current()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer func() {
		theme.Set(was)
		applyStyles(was)
		lipgloss.SetColorProfile(termenv.Ascii)
	}()

	for _, p := range theme.Palettes() {
		theme.Set(p)
		applyStyles(p)
		// The accent marks what takes keystrokes — the cursor — and no longer the skill column:
		// a screenful of identical blue bold was decoration, and it competed with the cursor for
		// the one cue that means "you type here".
		if got := cursorMark.Render("▸"); !strings.Contains(got, p.ANSI256(p.Accent)) {
			t.Errorf("%s: the cursor mark is %q, which does not use accent %s (%s)",
				p.Name, got, p.Accent, p.ANSI256(p.Accent))
		}
		if got := colSkill.Render("slide-builder"); !strings.Contains(got, p.ANSI256(p.Text)) {
			t.Errorf("%s: the skill column is %q, which does not use text %s", p.Name, got, p.Text)
		}
		if got := statusBad.Render("fail"); !strings.Contains(got, p.ANSI256(p.Err)) {
			t.Errorf("%s: a failure is %q, which does not use err %s", p.Name, got, p.Err)
		}
		// Amber has exactly one meaning left in this tool — a warning — so it is pinned here:
		// the badge is the only thing allowed to carry it.
		if got := badgeWarn.Render("2 warnings"); !strings.Contains(got, p.ANSI256(p.Warn)) {
			t.Errorf("%s: the warning badge is %q, which does not use warn %s", p.Name, got, p.Warn)
		}
	}
}

// A cursor row is one band, not a band with holes: Lip Gloss resets at the end of every styled cell,
// and the background has to be re-opened after each of those resets.
func TestSelectedRepaintsAfterResets(t *testing.T) {
	was := theme.Current()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer func() {
		theme.Set(was)
		applyStyles(was)
		lipgloss.SetColorProfile(termenv.Ascii)
	}()
	p, _ := theme.Lookup("midnight")
	theme.Set(p)
	applyStyles(p)

	line := colSkill.Render("slide-builder") + colYes.Render("✓")
	got := selected(line)
	if !strings.HasPrefix(got, selSeq) {
		t.Errorf("the painted row does not open with the selection background: %q", got)
	}
	// Every reset inside the row must be followed by the background again — except the closing one
	// that ends the row.
	for i := 0; i < len(got); {
		at := strings.Index(got[i:], escReset)
		if at < 0 {
			break
		}
		at += i
		rest := got[at+len(escReset):]
		if rest == "" {
			break
		}
		if !strings.HasPrefix(rest, selSeq) {
			t.Fatalf("after a reset at %d the background is not re-opened: %q", at, got)
		}
		i = at + len(escReset) + len(selSeq)
	}
}
