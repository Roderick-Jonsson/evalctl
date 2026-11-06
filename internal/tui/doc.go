package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"evalctl/internal/markdown"
)

// The documents the loop reads: the train failures, a report, an activity log.
//
// They are all the same screen — a title, a body, a scroll position — because they are all things
// you read rather than act on, and a reader that behaves differently per document is a reader you
// relearn three times. The ranking is the one document that is also a decision, and it says so with
// its own act and hints; the reader keeps the frame, the scroll and the way out.

type docState struct {
	scope string // what the bar calls it: "failures", "report", "activity"
	title string
	lines []string
	top   int
	view  int // the body height of the last render, for the scroll arithmetic
	// src is the document's markdown source, when it has one: report.md is written as markdown and
	// is read as markdown, so the reader never asks anyone to read `## Output` and `| cell |`.
	// lines are that source rendered at width; a resize re-renders rather than leaving a report
	// laid out for the window the reader used to have.
	src   string
	width int // the pane width lines were rendered at; 0 until the first draw
	// A document is usually read and nothing else. Two exceptions earn a field each: the ranking is
	// a table you walk and act on (act, hints, table), and a record opened from it returns to it
	// (back) rather than to the screen the table itself returns to.
	act   func(key string) tea.Cmd
	hints []hint
	table bool
	back  *docState
}

// md renders src through glamour at the pane's width. It is idempotent, so a document is rendered
// once per width and not once per frame: scrolling a report is reading, not re-typesetting.
func (d *docState) md(w int) {
	if d.src == "" || w <= 0 || w == d.width {
		return
	}
	d.width = w
	// One column short of the frame, like `evalctl report`: a table that fills the pane exactly is
	// a table whose last cell touches the edge.
	d.lines = markdown.Lines(d.src, max(markdown.MinWidth, w-1))
}

func (d *docState) body(h int) []string {
	d.view = h
	if d.top > len(d.lines)-h {
		d.top = max(0, len(d.lines)-h)
	}
	head := []string{"", "  " + titleStyle.Render(d.title),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, 40))}
	return fitRows(append(head, d.lines[d.top:]...), h)
}

func (d *docState) update(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "j", "down":
		d.top = min(d.top+1, max(0, len(d.lines)-1))
	case "k", "up":
		d.top = max(0, d.top-1)
	case "ctrl+d", " ":
		d.top = min(d.top+d.view/2, max(0, len(d.lines)-1))
	case "ctrl+u":
		d.top = max(0, d.top-d.view/2)
	case "g", "home":
		d.top = 0
	case "G", "end":
		d.top = max(0, len(d.lines)-d.view)
	}
	return nil
}

func (d *docState) position() string {
	if len(d.lines) == 0 {
		return ""
	}
	end := min(len(d.lines), d.top+d.view)
	return fmt.Sprintf("%d-%d / %d", d.top+1, end, len(d.lines))
}

// ---------------------------------------------------------------- the builder

// docWriter is the shape every step screen's prose takes: a title in the step's own words, the one
// rule the step exists for, a rule, labelled facts, and a single action line. Keeping it in one type
// is what makes the four steps read as one flow rather than four opinions about where a label goes.
//
// Its width is the width of the column it is drawn into, indents included — the two spaces in front
// of every line are part of the budget, not a detail a caller adds afterwards. A writer that wraps
// to w and then indents by two produces lines the pane cuts mid-word, which is exactly the kind of
// quiet overflow the frame's last-line trim then hides.
type docWriter struct {
	lines []string
	w     int // the column's width, indents included
	label int // fact label column; 0 means 14
}

// body is the width available to prose once the two-space indent is spent.
func (d *docWriter) body() int {
	if d.w <= 4 {
		return max(1, d.w)
	}
	return d.w - 2
}

func (d *docWriter) labelW() int {
	if d.label > 0 {
		return d.label
	}
	return 14
}

func (d *docWriter) blank() { d.lines = append(d.lines, "") }

func (d *docWriter) title(text string) {
	d.blank()
	for _, line := range wrapPlain(text, d.body()) {
		d.lines = append(d.lines, "  "+titleStyle.Render(line))
	}
}

// head is a quadrant's name, in the register every region uses: the workbench's four areas are
// four keyboard areas, and a document title in one of them would read like a screen that has no
// keys at all.
func (d *docWriter) head(name string, focused bool) {
	d.blank()
	d.lines = append(d.lines, regionHead(d.w, ellipsis(name, max(4, d.body()-2)), "", focused))
}

// card is a block's own heading: the failure card's first line, one step louder than its facts.
func (d *docWriter) card(text string) {
	d.lines = append(d.lines, "", "  "+titleStyle.Render(text))
}

// lead is the rule for this step, dim and short: two lines, never a paragraph.
func (d *docWriter) lead(text string) {
	if text == "" {
		return
	}
	for _, line := range wrapPlain(text, d.body()) {
		d.lines = append(d.lines, "  "+dimStyle.Render(line))
	}
}

func (d *docWriter) rule() {
	d.lines = append(d.lines, "  "+dimStyle.Render(strings.Repeat(glyphs.Rule, min(64, max(10, d.body())))))
}

// fact is one labelled value. The label column is fixed, so four facts read as a table and not as
// four sentences. A value too long for the line is cut with the ellipsis mark rather than left to
// the frame's trim: the trim is width-invisible, the mark is not.
func (d *docWriter) fact(label, value string) {
	lw := d.labelW()
	room := max(10, d.body()-lw-1)
	lines := wrapPlain(value, room)
	d.lines = append(d.lines, fmt.Sprintf("  %-*s %s", lw, label, lines[0]))
	for _, line := range lines[1:] {
		d.lines = append(d.lines, strings.Repeat(" ", lw+3)+line)
	}
}

// note is an aside that belongs to the fact above it.
func (d *docWriter) note(text string) {
	for _, line := range wrapPlain(text, max(8, d.body()-6)) {
		d.lines = append(d.lines, "      "+dimStyle.Render(line))
	}
}

// bullet is one line of advice in a list: a dash and the sentence, wrapped under its own indent.
func (d *docWriter) bullet(text string) {
	for i, line := range wrapPlain(text, max(12, d.body()-4)) {
		if i == 0 {
			d.lines = append(d.lines, "  - "+dimStyle.Render(line))
			continue
		}
		d.lines = append(d.lines, "    "+dimStyle.Render(line))
	}
}

// hint is one numbered line of advice: the number in the amber the frame keeps for what to look at
// next, the sentence in the body colour so it reads as an instruction and not as small print.
func (d *docWriter) hint(n int, text string) {
	d.labelled(fmt.Sprintf("Hint %d", n), hintLabel, text)
}

// paneLabelW is the workbench's label column: wide enough for "Expected", so the failure pane's two
// facts, the query and the numbered hints all start their text on the same edge.
const paneLabelW = 8

// paneGap is that column plus the two spaces that separate it from the value. Callers that mix styles
// on one line build it themselves; labelled() is the same line with the whole value in the body colour.
func (d *docWriter) paneGap(label string, st lipgloss.Style) string {
	return "  " + st.Render(padCell(label, paneLabelW, false)) + "  "
}

// labelled is one line of the workbench's panes: a label in the label column, the text in the body
// colour, wrapped with a hanging indent so the labels stay a column and a wrapped line starts under
// the first word of the thing it belongs to. The label carries the weight and the colour, the text
// carries the meaning — which is why the text is not bold: a bold label beside bold prose is one solid
// block, and the eye has nowhere to land.
func (d *docWriter) labelled(label string, st lipgloss.Style, text string) {
	gap := d.paneGap(label, st)
	for i, line := range wrapPlain(text, max(10, d.body()-lipgloss.Width(gap))) {
		if i == 0 {
			d.lines = append(d.lines, gap+colText.Render(line))
			continue
		}
		d.lines = append(d.lines, strings.Repeat(" ", lipgloss.Width(gap))+colText.Render(line))
	}
}

// para is the guide's own advice, where a step uses it.
func (d *docWriter) para(text string) {
	for _, line := range wrapPlain(text, d.body()) {
		d.lines = append(d.lines, "  "+dimStyle.Render(line))
	}
}

// source is a field's text as the file holds it: the line breaks are the author's, each line wraps
// to the column, and a blank line stays a blank line. para is for prose the screen wrote — a rule,
// an aside — and this is for prose the case wrote, so it is said in the body colour and not in the
// dim one.
func (d *docWriter) source(text string) {
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			d.lines = append(d.lines, "")
			continue
		}
		for _, wrapped := range wrapPlain(line, d.body()) {
			d.lines = append(d.lines, "  "+colText.Render(wrapped))
		}
	}
}

// action is the last line before the key bar and the only line that says what to press. Every step
// has exactly one. A long action wraps under its own indent, because a line the frame trims would
// lose the end of the sentence that says what happens next.
func (d *docWriter) action(key, text string) {
	d.blank()
	indent := "  " + glyphs.Arrow + " " + key + "  "
	for i, line := range wrapPlain(text, max(12, d.body()-lipgloss.Width(indent))) {
		if i == 0 {
			d.lines = append(d.lines, "  "+cursorMark.Render(glyphs.Arrow)+" "+titleStyle.Render(key)+"  "+line)
			continue
		}
		d.lines = append(d.lines, strings.Repeat(" ", lipgloss.Width(indent))+line)
	}
}
