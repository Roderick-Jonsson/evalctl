package tui

import "strings"

// The vertical grid. Every screen that is not a form is the same three bands — the rows that say
// what you are looking at, the body, and the two rows at the bottom that say what your keys do and
// what just happened — and this file is the ONLY place that arithmetic lives.
//
// It is a file rather than a habit because it drifted: the list, the front door and the help were
// height-anchored while the viewer reserved six rows of chrome and spent four, so its footer floated
// mid-screen on the screen you read longest. A screen that computes its own bands will eventually
// compute a different frame, and the difference shows up as a status line that walks up and down.

const (
	// gridHead is the rows above a list's body: the identity/state line, the tab strip's line, and
	// the column head (or the pane's title, which shares that row).
	gridHead = 3
	// gridFoot is the key line and the status line. Two rows, always, on every screen: the status is
	// the one thing that must not move, and the key line sits directly above it.
	gridFoot = 2
)

// gridBody is how many rows a list screen's body gets on an h-row terminal, never less than one: a
// body of zero rows is not a layout, it is a bug that draws the footer twice.
func gridBody(h int) int { return max(1, h-gridHead-gridFoot) }

// fitRows is a component's content in exactly n rows: short is padding, long is a cut. No screen
// returns a string of its own natural height — the frame is the window, not the content.
func fitRows(lines []string, n int) []string {
	if n <= 0 {
		return nil
	}
	out := make([]string, n)
	copy(out, lines)
	return out
}

// fitView is the whole contract in one call: the head rows, the body padded to whatever is left, the
// foot rows, and the exact height of the window. It never returns more than h rows; when the screen
// is too short for its own chrome the rows that survive are the bottom ones, because the status line
// is what a 4-row terminal needs most.
//
// An UNSIZED model (before the first WindowSizeMsg) has no frame to fit into, so it renders at the
// content's natural height instead of collapsing to its chrome — the placeholder is View()'s job,
// not this one's, and a probe that reads the view early should see the view.
func fitView(head, body, foot []string, h int) string {
	if h <= 0 {
		lines := make([]string, 0, len(head)+len(body)+len(foot))
		lines = append(lines, head...)
		lines = append(lines, body...)
		lines = append(lines, foot...)
		return strings.Join(lines, "\n")
	}
	n := max(0, h-len(head)-len(foot))
	lines := make([]string, 0, h)
	lines = append(lines, head...)
	lines = append(lines, fitRows(body, n)...)
	lines = append(lines, foot...)
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	return strings.Join(lines, "\n")
}
