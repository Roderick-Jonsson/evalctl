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
		// The accent is the skill column's colour: the rendered sequence has to carry the index
		// the palette maps that accent to, not a leftover from the previous palette.
		if got := colSkill.Render("slide-builder"); !strings.Contains(got, p.ANSI256(p.Accent)) {
			t.Errorf("%s: the skill column is %q, which does not use accent %s (%s)",
				p.Name, got, p.Accent, p.ANSI256(p.Accent))
		}
		if got := statusBad.Render("fail"); !strings.Contains(got, p.ANSI256(p.Err)) {
			t.Errorf("%s: a failure is %q, which does not use err %s", p.Name, got, p.Err)
		}
		if got := box.Render("x"); !strings.Contains(got, p.ANSI256(p.Border)) {
			t.Errorf("%s: the frame is %q, which does not use border %s", p.Name, got, p.Border)
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
