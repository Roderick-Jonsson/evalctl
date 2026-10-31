package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
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
type docWriter struct {
	lines []string
	w     int
}

func (d *docWriter) blank() { d.lines = append(d.lines, "") }

func (d *docWriter) title(text string) {
	d.blank()
	for _, line := range wrapPlain(text, d.w) {
		d.lines = append(d.lines, "  "+titleStyle.Render(line))
	}
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
	for _, line := range wrapPlain(text, d.w) {
		d.lines = append(d.lines, "  "+dimStyle.Render(line))
	}
}

func (d *docWriter) rule() {
	d.lines = append(d.lines, "  "+dimStyle.Render(strings.Repeat(glyphs.Rule, min(64, max(10, d.w)))))
}

// fact is one labelled value. The label column is fixed, so four facts read as a table and not as
// four sentences.
func (d *docWriter) fact(label, value string) {
	d.lines = append(d.lines, fmt.Sprintf("  %-14s %s", label, value))
}

// note is an aside that belongs to the fact above it.
func (d *docWriter) note(text string) {
	for _, line := range wrapPlain(text, d.w-8) {
		d.lines = append(d.lines, "      "+dimStyle.Render(line))
	}
}

// para is the guide's own advice, where a step uses it.
func (d *docWriter) para(text string) {
	for _, line := range wrapPlain(text, d.w) {
		d.lines = append(d.lines, "  "+dimStyle.Render(line))
	}
}

// action is the last line before the key bar and the only line that says what to press. Every step
// has exactly one.
func (d *docWriter) action(key, text string) {
	d.blank()
	d.lines = append(d.lines, "  "+cursorMark.Render(glyphs.Arrow)+" "+titleStyle.Render(key)+"  "+text)
}
