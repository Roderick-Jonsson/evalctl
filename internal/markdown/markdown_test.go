package markdown

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
)

const sample = `# Eval iteration 7

Generated 2026-01-01 · 2 case(s) · **pass rate 50%**

## Timing

| Arm | Cases | Mean duration (s) |
|---|---|---|
| with_skill | 2 | 143.6 |
| without_skill | 2 | 12.0 |

### arm notes

- run: exit 0, ` + "`143.6s`" + `
- containment: ok
`

// A report is rendered, not printed: the syntax is gone and the structure is still there.
func TestRenderDropsSyntaxKeepsStructure(t *testing.T) {
	out, err := Render(sample, 80)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	plain := xansi.Strip(out)
	for _, gone := range []string{"# Eval", "**pass rate", "|---", "`143.6s`"} {
		if strings.Contains(plain, gone) {
			t.Errorf("markdown syntax survived rendering: %q\n%s", gone, plain)
		}
	}
	for _, kept := range []string{"Eval iteration 7", "pass rate 50%", "Timing", "with_skill", "containment: ok"} {
		if !strings.Contains(plain, kept) {
			t.Errorf("rendered report lost %q\n%s", kept, plain)
		}
	}
	// A table becomes columns and rules, not a row of pipes and dashes.
	if !strings.Contains(plain, "│") || !strings.Contains(plain, "─") {
		t.Errorf("the table was not drawn as a table:\n%s", plain)
	}
	if !strings.Contains(out, "\x1b[") {
		t.Error("the rendered report carries no styling at all")
	}
}

// The pane never gets a line it cannot show: a table wider than the window is cut, not spilled.
func TestRenderFitsTheWidth(t *testing.T) {
	wide := "| Arm | Cases | Failed runs | Mean duration (s) | Mean tokens |\n" +
		"|---|---|---|---|---|\n| with_skill | 12 | 0 | 143.6 | 548,607 |\n"
	for _, width := range []int{20, 40, 80, 120} {
		for _, line := range Lines(sample+"\n"+wide, width) {
			if w := xansi.StringWidth(line); w > width {
				t.Errorf("width %d: line is %d cells: %q", width, w, xansi.Strip(line))
			}
		}
	}
}

// Trailing padding is stripped, escapes and all: a padded report line otherwise ends in a long
// tail of zero-width style sequences.
func TestRenderTrimsPadding(t *testing.T) {
	out, err := Render(sample, 80)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasSuffix(line, " ") {
			t.Errorf("line ends in a space: %q", xansi.Strip(line))
		}
		if strings.HasSuffix(line, "\x1b[38;5;252m") {
			t.Errorf("line ends in an unterminated style: %q", xansi.Strip(line))
		}
	}
}

// Same text, same width, same bytes — a report re-rendered on a resize must not wobble.
func TestRenderIsDeterministic(t *testing.T) {
	first, err := Render(sample, 100)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	second, err := Render(sample, 100)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if first != second {
		t.Error("two renders of the same text at the same width differ")
	}
}

// A plain-text pane is still the fallback, and it indents like every other pane.
func TestPlainLinesIndentsAndExpandsTabs(t *testing.T) {
	lines := PlainLines("first\n\tsecond\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %q", lines)
	}
	if lines[0] != "  first" || lines[1] != "    second" {
		t.Errorf("plain lines = %q", lines)
	}
}

// Markdown that already is a report is not the only input: an empty document renders to nothing
// rather than an error, so a pane can show "(empty)".
func TestRenderEmptyDocument(t *testing.T) {
	out, err := Render("", 80)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.TrimSpace(xansi.Strip(out)) != "" {
		t.Errorf("empty document rendered as %q", xansi.Strip(out))
	}
}
