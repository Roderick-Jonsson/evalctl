package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The documents the loop reads: the train failures, a report, an activity log.
//
// They are all the same screen — a title, a body, a scroll position — because they are all things
// you read rather than act on, and a reader that behaves differently per document is a reader you
// relearn three times.

type docState struct {
	scope string // what the bar calls it: "failures", "report", "activity"
	title string
	lines []string
	top   int
	view  int // the body height of the last render, for the scroll arithmetic
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
// next, the sentence in the body colour so it reads as an instruction and not as small print. The
// indent hangs, so a hint that wraps starts its second line under its own first word and the numbers
// stay a column down the pane.
func (d *docWriter) hint(n int, text string) {
	label := fmt.Sprintf("Hint %d", n)
	gap := "  " + hintLabel.Render(label) + "  "
	for i, line := range wrapPlain(text, max(10, d.body()-lipgloss.Width(label)-2)) {
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
