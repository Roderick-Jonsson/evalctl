package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"evalctl/internal/theme"
)

// The TUI's whole style table. Every entry is a role in the active palette (see internal/theme),
// never a literal colour: switching the palette rebuilds this table and nothing else in a view
// changes. The table is package state rather than a field of the model because a hundred call
// sites read it straight out of a view, and none of them needs to know where its colour came from.
var (
	// Columns of the row table. The skill is the primary identifier and carries the accent; the
	// slug is the human-readable name and stays neutral text, so a screenful of rows has one
	// coloured column instead of three competing ones.
	colSkill = lipgloss.NewStyle()
	colID    = lipgloss.NewStyle()
	colSlug  = lipgloss.NewStyle()
	colYes   = lipgloss.NewStyle()
	colNo    = lipgloss.NewStyle()
	colSplit = lipgloss.NewStyle()
	colDupe  = lipgloss.NewStyle()
	// colText is the row's substance — the query or the prompt, the widest column on the screen.
	// It is a style and not "no style" on purpose: left unstyled it took the terminal's default
	// foreground, so the one column you actually read to decide what to cut was the only column
	// the palette did not reach (measured: 2,733 of a 220-column lane screen's non-space cells).
	colText = lipgloss.NewStyle()

	styleOn = lipgloss.NewStyle()
	// styleCur is the row under the cursor: a background, so the row's own foregrounds survive.
	styleCur = lipgloss.NewStyle()
	// The gutter's two marks. Accent means "keystrokes go here", and it is the only thing on a
	// row that carries it — green/amber/red stay reserved for state.
	cursorMark = lipgloss.NewStyle()
	selMark    = lipgloss.NewStyle()

	statusOk   = lipgloss.NewStyle()
	statusBad  = lipgloss.NewStyle()
	titleStyle = lipgloss.NewStyle()
	// dimStyle is a colour and not Faint(true): SGR 2 is ignored by some terminals (Apple
	// Terminal among them), and a hint that is styled with nothing at all is a hint at full
	// brightness.
	dimStyle = lipgloss.NewStyle()
	// activityTool colours the tool-call lines of a live activity log.
	activityTool = lipgloss.NewStyle()
	// Header states: focus, sort and group are shown by styling the column's own header cell,
	// which keeps the header aligned with the rows below it.
	headerFocus = lipgloss.NewStyle()
	headerSort  = lipgloss.NewStyle()
	headerGroup = lipgloss.NewStyle()
	// The two lanes of the current scope, drawn in the header as a switch.
	tabActive  = lipgloss.NewStyle()
	tabIdle    = lipgloss.NewStyle()
	styleGroup = lipgloss.NewStyle()
	// The tab bar is the top edge of every list screen: the scope on the left, its views as chips,
	// the build in hand on the right, and a heavier rule under all of it. The rule is the border
	// role because it is structure — the same reason the pane's divider is — and the bar uses
	// exactly one fill (the active chip), so "you are here" is the only bright thing on the row.
	tabSep  = lipgloss.NewStyle()
	tabRule = lipgloss.NewStyle()
	// tabScope is the skill the tabs belong to. It is a title and not a tab: accent, bold, no
	// fill, so it never reads as another thing to switch to.
	tabScope = lipgloss.NewStyle()
	// The build in hand, parked at the right edge of the bar: a filled name and a quiet number.
	brandName = lipgloss.NewStyle()
	brandVer  = lipgloss.NewStyle()

	// The pane's divider and its title. The divider is structure (border role) and stays a line
	// whether or not the pane has the keys; the title is the loud half, and it is the only thing
	// that turns accent when the keystrokes move into the pane.
	seamLine  = lipgloss.NewStyle()
	paneTitle = lipgloss.NewStyle()
	// paneSeam is the divider while the PANE has the keys. Accent means "the keystrokes go
	// here", and there are exactly two places they can go — the table (cursor mark, focused
	// column head) and the pane (this seam). Nothing else may take the accent.
	paneSeam = lipgloss.NewStyle()

	// The footer is one band across the bottom: a background surface with its keys bold and its
	// actions soft, and a help chip at the right edge. footSeq is that background as a raw escape,
	// re-applied after every styled cell (see banded).
	footBar  = lipgloss.NewStyle()
	footKey  = lipgloss.NewStyle()
	footAct  = lipgloss.NewStyle()
	helpChip = lipgloss.NewStyle()
	// statValue is a number or a path in the footer: present, never loud.
	statValue = lipgloss.NewStyle()
	footSeq   string

	// badgeWarn has no width on purpose: colDupe is a 4-cell column style, and wrapping a
	// sentence in it folds the header one character per line.
	badgeWarn = lipgloss.NewStyle()

	// The skill-list screen's own columns.
	colCases = lipgloss.NewStyle()
	colAsr   = lipgloss.NewStyle()

	// promoNote is colDupe without its fixed width: the table's column style would reflow a
	// sentence into four-character fragments inside the form's box.
	promoNote = lipgloss.NewStyle()
	// selSeq is the cursor row's background as an escape sequence, so it can be re-applied over an
	// already-styled line (see selected).
	selSeq string
)

func init() { applyStyles(theme.Current()) }

// applyStyles rebuilds the table from a palette. It is called once at startup and again by Run
// when the caller resolved a different theme, so nothing has to be threaded through the model.
func applyStyles(p theme.Palette) {
	// Column styles carry colour only. Padding is done by the table from the column's own width
	// and alignment, so no style can re-wrap a cell (or a badge) on its own.
	colSkill = lipgloss.NewStyle().Bold(true).Foreground(p.Color(p.Text))
	colID = lipgloss.NewStyle().Foreground(p.Color(p.Faint))
	colSlug = lipgloss.NewStyle().Bold(true).Foreground(p.Color(p.Text))
	// A yes/no label is a fact about the row, not a pass or a failure: a "no" row is a valid
	// negative test. Text and Muted keep green and red for what they actually mean.
	colYes = lipgloss.NewStyle().Foreground(p.Color(p.Text))
	colNo = lipgloss.NewStyle().Foreground(p.Color(p.Muted))
	colSplit = lipgloss.NewStyle().Foreground(p.Color(p.Muted))
	// The prose column carries the palette's body colour: it is the column the screen exists for.
	colText = lipgloss.NewStyle().Foreground(p.Color(p.Text))
	// A near-duplicate IS a warning — the one thing in this table that earns amber.
	colDupe = lipgloss.NewStyle().Foreground(p.Color(p.Warn))

	styleOn = lipgloss.NewStyle().Foreground(p.Color(p.OK))
	styleCur = lipgloss.NewStyle().Background(p.Color(p.Sel))
	selSeq = escapeOf(styleCur)
	cursorMark = lipgloss.NewStyle().Foreground(p.Color(p.Accent)).Bold(true)
	selMark = lipgloss.NewStyle().Foreground(p.Color(p.Accent))

	statusOk = lipgloss.NewStyle().Foreground(p.Color(p.OK))
	statusBad = lipgloss.NewStyle().Foreground(p.Color(p.Err))
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(p.Color(p.Text))
	dimStyle = lipgloss.NewStyle().Foreground(p.Color(p.Faint))
	// A tool-call line is activity, not a warning: cyan (accent2) is this palette's structural
	// hue, and amber is kept for the one thing it means in this tool — a dupe or a validation
	// warning.
	activityTool = lipgloss.NewStyle().Foreground(p.Color(p.Accent2))

	headerFocus = lipgloss.NewStyle().Bold(true).Underline(true).Foreground(p.Color(p.Accent))
	headerSort = lipgloss.NewStyle().Underline(true).Foreground(p.Color(p.Text))
	headerGroup = lipgloss.NewStyle().Bold(true).Foreground(p.Color(p.Text))
	tabActive = lipgloss.NewStyle().Bold(true).Foreground(p.Color(p.Text)).Background(p.Color(p.Sel))
	tabIdle = lipgloss.NewStyle().Foreground(p.Color(p.Muted))
	tabSep = lipgloss.NewStyle().Foreground(p.Color(p.Line))
	tabRule = lipgloss.NewStyle().Foreground(p.Color(p.Line))
	tabScope = lipgloss.NewStyle().Bold(true).Foreground(p.Color(p.Accent))
	// The brand is the one cell on the screen that is filled with the accent instead of text on
	// it: a name, not a cue, so it takes the accent for identity and never for focus.
	brandName = lipgloss.NewStyle().Bold(true).Foreground(p.Color(p.BG)).Background(p.Color(p.Accent))
	brandVer = lipgloss.NewStyle().Foreground(p.Color(p.Faint))
	// The divider is a line, not a label: border role when the table has the keys, accent when the
	// pane does — the one cue that says which of the two the keystrokes are going into.
	seamLine = lipgloss.NewStyle().Foreground(p.Color(p.Line))
	paneTitle = lipgloss.NewStyle().Bold(true).Foreground(p.Color(p.Text))
	footBar = lipgloss.NewStyle().Background(p.Color(p.Sel))
	footSeq = escapeOf(footBar)
	footKey = lipgloss.NewStyle().Bold(true).Foreground(p.Color(p.Text))
	footAct = lipgloss.NewStyle().Foreground(p.Color(p.Muted))
	// The help chip is inverted so it reads as a key cap at the edge of the band: fill from the
	// faint role, text from the palette's own background — the two roles that are furthest apart.
	helpChip = lipgloss.NewStyle().Bold(true).Foreground(p.Color(p.BG)).Background(p.Color(p.Faint))
	statValue = lipgloss.NewStyle().Foreground(p.Color(p.Muted))
	// The pane's seam only becomes the accent while the pane has the keys: a frame that colours
	// both halves of a split at once has not said where you are.
	paneSeam = lipgloss.NewStyle().Foreground(p.Color(p.Accent))
	styleGroup = lipgloss.NewStyle().Bold(true).Foreground(p.Color(p.Text))
	badgeWarn = lipgloss.NewStyle().Foreground(p.Color(p.Warn))

	colCases = lipgloss.NewStyle().Foreground(p.Color(p.Text))
	colAsr = lipgloss.NewStyle().Foreground(p.Color(p.OK))

	promoNote = lipgloss.NewStyle().Foreground(p.Color(p.Warn))
}

// escapeOf is the opening escape a style writes, without any text or reset: "x" rendered by the
// style gives the sequence to re-apply on its own.
func escapeOf(st lipgloss.Style) string {
	out := st.Render("x")
	if i := strings.IndexByte(out, 'x'); i > 0 {
		return out[:i]
	}
	return ""
}

// selected paints a finished row. Lip Gloss writes a reset at the end of every styled cell, so
// wrapping a whole line in a background leaves the band on the gaps between columns and nowhere
// else — the cursor row is then invisible in the one place it matters. Re-opening the background
// after each reset paints the row as one band and keeps the cell foregrounds.
func selected(line string) string {
	if selSeq == "" {
		return line
	}
	return selSeq + strings.ReplaceAll(line, escReset, escReset+selSeq) + escReset
}

// banded paints a whole ROW with a background — the cursor row and the footer band are the same
// problem: Lip Gloss closes every styled cell with a reset, so wrapping a finished line in a
// background leaves the fill on the gaps and nowhere else. The line is padded to w first, so the
// band ends at the window's edge and not where the text happened to stop.
func banded(seq, line string, w int) string {
	if seq == "" {
		return line
	}
	if n := lipgloss.Width(line); n < w {
		line += strings.Repeat(" ", w-n)
	}
	return seq + strings.ReplaceAll(line, escReset, escReset+seq) + escReset
}

// escReset is what Lip Gloss (and every other renderer here) writes at the end of a style.
const escReset = "\x1b[0m"
