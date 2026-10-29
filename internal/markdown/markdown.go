// Package markdown renders evalctl's markdown — the reports under evals/results and any note a
// run left behind — through glamour, so a terminal shows headings, tables and emphasis instead
// of raw asterisks. It is deterministic: the same text at the same width always renders the
// same bytes, and nothing here reads the environment (GLAMOUR_STYLE is deliberately ignored).
package markdown

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/glamour"
	glamansi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// MinWidth is the narrowest wrap the renderer accepts. Below it a table stops being a table and
// wrapping costs more than it saves, so the width is raised rather than honoured.
const MinWidth = 20

// DefaultWidth is the wrap width when the caller does not know one (a terminal that will not say).
const DefaultWidth = 100

// Indent is the document margin every rendered line starts with. The TUI's other panes indent
// by two, so a report reads as part of the same screen.
const Indent = 2

// Styles is glamour's dark theme with evalctl's palette and a tighter document frame: the
// yellow-on-purple h1 badge is replaced by a plain bold title, and headings step down through
// the blue/magenta the table columns already use.
func Styles() glamansi.StyleConfig {
	cfg := styles.DarkStyleConfig
	cfg.Document.Color = str("252")
	cfg.Document.Margin = uintp(Indent)

	cfg.H1 = glamansi.StyleBlock{
		StylePrimitive: glamansi.StylePrimitive{
			BlockSuffix: "\n",
			Color:       str("15"),
			Bold:        boolp(true),
		},
	}
	cfg.Heading.BlockSuffix = "\n"
	cfg.Heading.Color = str("12")
	cfg.Heading.Bold = boolp(true)
	cfg.H2 = glamansi.StyleBlock{StylePrimitive: glamansi.StylePrimitive{Prefix: "▍ ", Color: str("12"), Bold: boolp(true)}}
	cfg.H3 = glamansi.StyleBlock{StylePrimitive: glamansi.StylePrimitive{Prefix: "▸ ", Color: str("13"), Bold: boolp(true)}}
	cfg.H4 = glamansi.StyleBlock{StylePrimitive: glamansi.StylePrimitive{Prefix: "  ", Color: str("252")}}
	cfg.H5 = glamansi.StyleBlock{StylePrimitive: glamansi.StylePrimitive{Prefix: "  ", Color: str("245")}}
	cfg.H6 = glamansi.StyleBlock{StylePrimitive: glamansi.StylePrimitive{Prefix: "  ", Color: str("243"), Bold: boolp(false)}}

	cfg.Strong = glamansi.StylePrimitive{Bold: boolp(true), Color: str("15")}
	cfg.Emph = glamansi.StylePrimitive{Italic: boolp(true), Color: str("252")}
	cfg.HorizontalRule = glamansi.StylePrimitive{Color: str("237"), Format: "\n--------\n"}
	cfg.Item = glamansi.StylePrimitive{BlockPrefix: "• "}
	cfg.Link = glamansi.StylePrimitive{Color: str("12"), Underline: boolp(true)}
	cfg.LinkText = glamansi.StylePrimitive{Color: str("12"), Bold: boolp(true)}
	cfg.Code = glamansi.StyleBlock{StylePrimitive: glamansi.StylePrimitive{
		Prefix: " ", Suffix: " ", Color: str("3"), BackgroundColor: str("236"),
	}}
	cfg.CodeBlock.Color = str("245")
	cfg.CodeBlock.Margin = uintp(Indent)
	cfg.Table = glamansi.StyleTable{
		StyleBlock: glamansi.StyleBlock{StylePrimitive: glamansi.StylePrimitive{Color: str("250")}},
	}
	cfg.Table.RowSeparator = str("─")
	cfg.Table.ColumnSeparator = str("│")
	cfg.Table.CenterSeparator = str("┼")
	return cfg
}

// Render renders markdown wrapped at width. An error is glamour's (bad width, unparseable
// style): callers show the plain source instead, never a blank pane.
func Render(text string, width int) (string, error) {
	if width < MinWidth {
		width = MinWidth
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(Styles()),
		glamour.WithWordWrap(width),
		glamour.WithColorProfile(termenv.ANSI256),
		glamour.WithEmoji(),
	)
	if err != nil {
		return "", err
	}
	out, err := r.Render(text)
	if err != nil {
		return "", err
	}
	// glamour pads every block out to the wrap width. A pane (and a pipe) is better off without
	// that trailing whitespace: nothing in this style puts anything behind it.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for i, line := range lines {
		lines[i] = trimPad(line)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// trailingPad matches padding at the end of a rendered line: spaces, and the style/reset pairs
// glamour writes around every one of them (the padding is applied per token, so a padded line
// ends in a long tail of zero-width escapes once the spaces themselves are gone).
var trailingPad = regexp.MustCompile(`(?:[ ]+|\x1b\[[0-9;]*m)+$`)

func trimPad(line string) string {
	trimmed := trailingPad.ReplaceAllString(line, "")
	if trimmed == "" || !strings.Contains(trimmed, "\x1b") {
		return trimmed
	}
	// Whatever escape the tail was closing, the next line starts clean.
	return trimmed + "\x1b[0m"
}

// Lines renders markdown into the lines a scrollable pane holds. A render failure is not fatal:
// the reader gets the source's own lines, tab-expanded and indented like everything else.
func Lines(text string, width int) []string {
	if width < MinWidth {
		width = MinWidth
	}
	out, err := Render(text, width)
	if err != nil {
		return PlainLines(text)
	}
	lines := splitLines(out)
	for i, line := range lines {
		// A table wider than the pane is truncated rather than allowed to spill across it.
		if xansi.StringWidth(line) > width {
			lines[i] = xansi.Truncate(line, width, "…")
		}
	}
	return lines
}

// PlainLines is the no-rendering fallback: the source, tabs expanded, indented by Indent.
func PlainLines(text string) []string {
	lines := splitLines(strings.TrimRight(text, "\n"))
	for i, line := range lines {
		lines[i] = strings.Repeat(" ", Indent) + strings.ReplaceAll(line, "\t", "  ")
	}
	return lines
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func str(s string) *string { return &s }

func boolp(b bool) *bool { return &b }

func uintp(u uint) *uint { return &u }
