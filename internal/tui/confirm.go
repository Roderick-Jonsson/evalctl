package tui

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The one question in the app.
//
// Deleting a query, applying a wording: both change something a measurement depends on, so both ask
// first, in the same dialog, with the same two keys. A confirmation that looks different per action
// is a confirmation you read twice before you trust it.

type askState struct {
	scope string
	title string
	lines []string
	yes   string // what y does, in words: "delete it", "apply it"
	then  func(m *Model) tea.Cmd
}

func (a *askState) body(h int) []string {
	lines := []string{"", "  " + titleStyle.Render(a.title), ""}
	for _, line := range a.lines {
		lines = append(lines, "  "+line)
	}
	lines = append(lines, "",
		"  "+cursorMark.Render(glyphs.Arrow)+" "+footKey.Render("y")+"  "+a.yes,
		"  "+dimStyle.Render("   n or esc  nothing happens"))
	return fitRows(lines, h)
}

func (a *askState) hints() []hint {
	return []hint{{"y", a.yes}, {"n", "cancel"}}
}

func (a *askState) update(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "Y", "enter":
		then := a.then
		m.ask, m.mode = nil, screenFlow
		if then != nil {
			return then(m)
		}
	case "n", "N", "esc", "q":
		m.ask, m.mode = nil, screenFlow
		m.setStatus("nothing happened", false)
	}
	return nil
}

// readLog reads a text file into lines for the document reader: an activity log, a report. A
// missing file is a message and not a crash — the run that wrote it may have been cancelled before
// it wrote anything.
func readLog(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.TrimRight(string(b), "\n")
	if text == "" {
		return []string{"  " + dimStyle.Render("(empty)")}, nil
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return lines, nil
}
