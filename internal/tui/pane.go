package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// The detail pane is a component and not a column of text: it owns a window onto one document, it
// takes the keyboard when it is asked for, and it hands it back with one esc. Everything about it
// that is a fact about the FILE lives in paneDoc (app.go); what lives here is where the window is
// and what the keys do to it.
//
// There are exactly two places the keystrokes can be — the table and the pane — and the seam between
// them says which one. That is the whole focus model: one cursor, two readings of it.

// paneKeys are the pane's verbs. The footer and ? both render them, so the pane cannot advertise a
// key it does not take.
func (m *Model) paneKeys() []hint {
	return []hint{
		{key: "j k", act: "scroll", rank: 1, help: "scroll the record under the cursor — the list does not move"},
		{key: "g G", act: "ends", rank: 9, help: "top / end of the record"},
		{key: "ctrl+d", act: "half", rank: 8, help: "half a page down (ctrl+u up)"},
		{key: "e", act: "edit", rank: 2, help: "edit the row the pane is showing"},
		{key: "v", act: "whole", rank: 3, help: "the whole record at the width of the window"},
		{key: "esc", act: "table", rank: 0, help: "the table has the keys again"},
	}
}

// openPane moves the focus into the detail pane. Where there is no pane — a terminal too narrow for
// the frame to buy one — it says which key does the same job rather than opening the editor under a
// key that no longer means that.
func (m *Model) openPane() tea.Cmd {
	if _, ok := m.focused(); !ok {
		m.setStatus("no row under the cursor", true)
		return nil
	}
	if m.frame().pane == 0 {
		m.setStatus("no detail pane at this width — e edits the row, v opens it whole", true)
		return nil
	}
	m.paneFocus = true
	m.paneTop = 0
	// One focus at a time: a focused column and a focused pane are two answers to "where do my keys
	// go", and the screen can only draw one of them.
	m.focusCol = -1
	m.status = ""
	return nil
}

// leavePane hands the keys back to the table.
func (m *Model) leavePane() {
	if !m.paneFocus {
		return
	}
	m.paneFocus = false
	m.paneTop = 0
	m.setStatus("the table has the keys", false)
}

// updatePane routes the pane's own verbs. Anything it does not own answers with what it does own:
// a key that silently does nothing inside a component reads as a broken component.
func (m *Model) updatePane(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m.escUp()
	case "j", "down":
		m.paneScroll(1)
	case "k", "up":
		m.paneScroll(-1)
	case "ctrl+d", "pgdown":
		m.paneScroll(m.listHeight() / 2)
	case "ctrl+u", "pgup":
		m.paneScroll(-m.listHeight() / 2)
	case "g", "home":
		m.paneTop = 0
	case "G", "end":
		m.paneScroll(1 << 20)
	case "e":
		e, ok := m.focused()
		if !ok {
			return m, nil
		}
		m.paneFocus = false
		return m, m.openEditor(e, false)
	case "v":
		e, ok := m.focused()
		if !ok {
			return m, nil
		}
		m.viewer = newViewer(m.store, e, m.width, m.height)
		m.mode = screenViewer
	case "enter":
		m.leavePane()
	default:
		m.setStatus(fmt.Sprintf("the pane has the keys — j k scroll · e edit · v whole · esc %s", m.escLabel()), true)
	}
	return m, nil
}

// paneWindow is the ONE window over a pane document: it slices the document to h rows at offset top
// and names what is left after the window, with the key that opens the whole of it. Both panes (a
// row's record, an iteration's facts) go through it, so the two tabs cannot disagree about what a
// cut looks like or how it counts.
//
// The marker costs a row of the record, so the count is what remains AFTER the marker — a count that
// includes the line it displaced reads as one more line than there is.
func paneWindow(doc []string, top, h int, whole string) []string {
	if h <= 0 {
		return nil
	}
	top = min(max(0, top), max(0, len(doc)-h))
	end := min(len(doc), top+h)
	lines := append([]string(nil), doc[top:end]...)
	if end < len(doc) {
		hidden := len(doc) - top - (h - 1)
		if hidden < 1 {
			hidden = 1
		}
		marker := fmt.Sprintf("%s %d more lines", glyphs.Ellipsis, hidden)
		if whole != "" {
			marker += " · " + whole
		}
		lines = lines[:min(len(lines), h-1)]
		lines = append(lines, dimStyle.Render(marker))
	}
	return lines
}

// paneShown is the record's rows that are actually on screen: the window, less the row the cut marker
// takes when there is more below it. The title and the marker read this same answer — a title that
// claims one row more than the marker leaves room for is the kind of off-by-one a reader notices
// exactly once.
func paneShown(total, top, h int) (from, to int) {
	top = min(max(0, top), max(0, total-h))
	end := min(total, top+h)
	if end < total {
		return top + 1, top + h - 1
	}
	return top + 1, end
}

// paneScroll moves the window inside the record, clamped to it: a record shorter than the pane never
// scrolls, and the last screen of a long one is a full screen, not a two-line tail.
func (m *Model) paneScroll(d int) {
	e, ok := m.focused()
	if !ok {
		return
	}
	f := m.frame()
	if f.pane == 0 {
		return
	}
	maxTop := max(0, len(m.paneDoc(e, f.pane))-m.listHeight())
	m.paneTop = min(max(0, m.paneTop+d), maxTop)
}
