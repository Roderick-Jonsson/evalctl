package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The two files the output loop cannot be worked without, opened where they are written.
//
// An assertion is a sentence the app can hold in a text area; SKILL.md is a whole document, and a
// prompt is prose beside the fixtures it stages. Those belong in the editor the operator already
// uses, so the TUI hands the file over, suspends its own screen, and re-reads the tree when the
// editor exits — not a third text field, and not a tool that decides what a skill body may look
// like.
//
// Bubble Tea's ExecProcess is what makes that safe: the terminal is restored before the child
// starts and reclaimed afterwards, so a full-screen editor draws in a terminal it owns.

// editorDoneMsg is the editor exiting. The screen behind it is re-read whatever the exit code was:
// a file saved with a warning is still a file that changed, and a tree that did not change re-reads
// to exactly what it showed.
type editorDoneMsg struct {
	scope string
	err   error
}

// editorCommand resolves the editor: $EDITOR first because that is the one a shell profile sets,
// then $VISUAL, then vi, which is on every POSIX box. A value with arguments is split on spaces so
// `code -w` and `nvim -u NONE` work the way they do in a shell.
func editorCommand(path string) ([]string, error) {
	for _, env := range []string{"EDITOR", "VISUAL"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return append(strings.Fields(v), path), nil
		}
	}
	if _, err := exec.LookPath("vi"); err != nil {
		return nil, err
	}
	return []string{"vi", path}, nil
}

// editFile hands one file to the editor. It is the only place the app leaves its own frame.
func (m *Model) editFile(path, what string) tea.Cmd {
	if !isFileOnDisk(path) {
		m.setStatus("nothing at "+path+" yet", true)
		return nil
	}
	argv, err := editorCommand(path)
	if err != nil {
		m.setStatus("no editor: set $EDITOR (or $VISUAL) and press the key again", true)
		return nil
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	scope := what
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return editorDoneMsg{scope: scope, err: err}
	})
}

// editCaseFile is `S` in setup: the case file itself, for the fields the pane only reads — the
// prompt, the expected output, the fixtures. The assertions have their own editor in the pane,
// because they are the list the screen judges against and the one field that is written while
// looking at verdicts.
func (m *Model) editCaseFile() tea.Cmd {
	sk := m.store.Skill(m.skill)
	if sk == nil {
		m.setStatus("unknown skill "+m.skill, true)
		return nil
	}
	m.setStatus("editing "+sk.FilePath+" — the pane re-reads it when the editor exits", false)
	return m.editFile(sk.FilePath, "evals.json")
}

// editSkillFile is `E` in the dojo: the skill's own text, which is what a revision changes.
func (m *Model) editSkillFile() tea.Cmd {
	sk := m.store.Skill(m.skill)
	if sk == nil {
		m.setStatus("unknown skill "+m.skill, true)
		return nil
	}
	path := filepath.Join(sk.Dir, "SKILL.md")
	m.setStatus("editing "+path+" — close the editor and the loop re-reads it", false)
	return m.editFile(path, "SKILL.md")
}

// isFileOnDisk is the check before handing a path to an editor: vim will happily open a buffer for a
// file that does not exist, and saving it would invent a skill the tree never had.
func isFileOnDisk(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
