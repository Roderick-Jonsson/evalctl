package tui

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The one question in the app — asked on the status row, not on a screen.
//
// Deleting a query, applying a wording: both change something a measurement depends on, so both ask
// first, in the same words, with the same two keys. It used to be a screen, which made the thing being
// decided disappear at the moment of deciding; now the question takes the status row and the work
// stays where it was, under the eye that is about to answer.
type askState struct {
	title string
	yes   string // what y does, in words: "delete it", "apply it"
	then  func(m *Model) tea.Cmd
}

// line is the question as one row: what is being decided. The two keys are the key band's, where
// every other key is named — a question that repeats them is the same fact in two places, and the
// row it costs is the room the question needed.
func (a *askState) line(w int) string {
	return " " + statusBad.Render("?") + " " + ellipsis(a.title, max(12, w-4))
}

func (a *askState) hints() []hint {
	return []hint{{"y", a.yes}, {"n", "cancel"}}
}

// update answers it. Only y and n (and esc, q, which cancel) mean anything while a question stands:
// a question that swallows a stray key is worse than one that ignores it.
func (a *askState) update(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "Y", "enter":
		then := a.then
		m.ask = nil
		if then != nil {
			return then(m)
		}
	case "n", "N", "esc", "q":
		m.ask = nil
		m.setStatus("nothing happened", false)
	}
	return nil
}

// ask is the one place a question is made, so every one of them has the same two keys and the same
// shape. It changes nothing about the screen: the work under it is the context for the answer.
func (m *Model) askNow(title, yes string, then func(*Model) tea.Cmd) {
	m.ask = &askState{title: title, yes: yes, then: then}
	m.status = ""
}

// readLog reads a stream's own text into lines for the document reader: an arm's activity log. Its
// lines are already the layout the agent produced, so they are indented and nothing else — a report
// is markdown and goes through docState.md instead. A
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
