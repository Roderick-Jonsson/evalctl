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

	styleOn  = lipgloss.NewStyle()
	styleOff = lipgloss.NewStyle()
	// styleCur is the row under the cursor: a background, so the row's own foregrounds survive.
	styleCur = lipgloss.NewStyle()
	// The gutter's two marks. Accent means "keystrokes go here", and it is the only thing on a
	// row that carries it — green/amber/red stay reserved for state.
	cursorMark = lipgloss.NewStyle()
	selMark    = lipgloss.NewStyle()
	box        = lipgloss.NewStyle()

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
	// badgeWarn has no width on purpose: colDupe is a 4-cell column style, and wrapping a
	// sentence in it folds the header one character per line.
	badgeWarn = lipgloss.NewStyle()

	// The skill-list screen's own columns.
	colCases = lipgloss.NewStyle()
	colAsr   = lipgloss.NewStyle()
	colNone  = lipgloss.NewStyle()

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
	colSkill = lipgloss.NewStyle().Foreground(p.Color(p.Text))
	colID = lipgloss.NewStyle().Foreground(p.Color(p.Muted))
	colSlug = lipgloss.NewStyle().Foreground(p.Color(p.Text))
	// A yes/no label is a fact about the row, not a pass or a failure: a "no" row is a valid
	// negative test. Text and Muted keep green and red for what they actually mean.
	colYes = lipgloss.NewStyle().Foreground(p.Color(p.Text))
	colNo = lipgloss.NewStyle().Foreground(p.Color(p.Muted))
	colSplit = lipgloss.NewStyle().Foreground(p.Color(p.Muted))
	// A near-duplicate IS a warning — the one thing in this table that earns amber.
	colDupe = lipgloss.NewStyle().Foreground(p.Color(p.Warn))

	styleOn = lipgloss.NewStyle().Foreground(p.Color(p.OK))
	styleOff = lipgloss.NewStyle().Foreground(p.Color(p.Muted))
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
	tabActive = lipgloss.NewStyle().Bold(true).Foreground(p.Color(p.Accent))
	tabIdle = lipgloss.NewStyle().Foreground(p.Color(p.Muted))
	styleGroup = lipgloss.NewStyle().Bold(true).Foreground(p.Color(p.Text))
	badgeWarn = lipgloss.NewStyle().Foreground(p.Color(p.Warn))

	colCases = lipgloss.NewStyle().Foreground(p.Color(p.Text))
	colAsr = lipgloss.NewStyle().Foreground(p.Color(p.OK))
	colNone = lipgloss.NewStyle().Foreground(p.Color(p.Faint))

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

// escReset is what Lip Gloss (and every other renderer here) writes at the end of a style.
const escReset = "\x1b[0m"
