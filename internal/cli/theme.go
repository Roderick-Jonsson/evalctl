package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"evalctl/internal/theme"
)

// The CLI's style table, built from the active palette (see internal/theme). Like the TUI's it is
// package state, because a printed report has no model to carry styles around in.
var (
	styleDim     = lipgloss.NewStyle()
	styleHead    = lipgloss.NewStyle()
	styleYes     = lipgloss.NewStyle()
	styleNo      = lipgloss.NewStyle()
	styleDupe    = lipgloss.NewStyle()
	styleChange  = lipgloss.NewStyle()
	styleErr     = lipgloss.NewStyle()
	styleWarn    = lipgloss.NewStyle()
	styleTitle   = lipgloss.NewStyle()
	styleGroup   = lipgloss.NewStyle()
	styleSection = lipgloss.NewStyle()
	styleFlag    = lipgloss.NewStyle()
)

func init() { applyTheme(theme.Current()) }

// applyTheme rebuilds the table from a palette.
func applyTheme(p theme.Palette) {
	// Dim is a colour, not Faint(true): SGR 2 is dropped by some terminals, and a hint that is
	// styled with nothing is a hint at full brightness. It is the same role the TUI's dim text
	// takes, so the two surfaces' quiet words are the same shade of quiet.
	styleDim = lipgloss.NewStyle().Foreground(p.Color(p.Faint))
	// A column head is the quiet register in both surfaces — the TUI's tables and these share the
	// role, not just the idea, so `evalctl ls` and the workbench agree on what a head looks like.
	styleHead = lipgloss.NewStyle().Foreground(p.Color(p.Faint))
	// yes/no is a property of a row (should the skill load?), not a verdict: a "no" row is a valid
	// negative test. Both read as text, so green stays "passed" and red stays "failed".
	styleYes = lipgloss.NewStyle().Foreground(p.Color(p.Text))
	styleNo = lipgloss.NewStyle().Foreground(p.Color(p.Muted))
	styleDupe = lipgloss.NewStyle().Foreground(p.Color(p.Warn))
	styleChange = lipgloss.NewStyle().Foreground(p.Color(p.OK))
	styleErr = lipgloss.NewStyle().Foreground(p.Color(p.Err))
	styleWarn = lipgloss.NewStyle().Foreground(p.Color(p.Warn))
	styleTitle = lipgloss.NewStyle().Bold(true).Foreground(p.Color(p.Text))
	styleGroup = lipgloss.NewStyle().Foreground(p.Color(p.Accent2)).Bold(true)
	// A help page's headings are the same register as a table's group label: bold, secondary
	// accent, upper case. Lip Gloss renders no escapes without a TTY, so a piped page is plain.
	styleSection = lipgloss.NewStyle().Bold(true).Foreground(p.Color(p.Accent2))
	// Flag names take the primary accent: the three-colour scheme uv and rg use — bright headings,
	// one colour for what you can type, body text everywhere else. Green is not available to them:
	// in this palette it already means "passed".
	styleFlag = lipgloss.NewStyle().Foreground(p.Color(p.Accent))
}

// takeTheme pulls `--theme NAME` / `--theme=NAME` out of the arguments wherever it appears, and
// returns the rest. Colour is a property of the whole process, not of one subcommand, so it is
// handled once here instead of being registered on a dozen flag sets.
func takeTheme(args []string) (string, []string) {
	out := make([]string, 0, len(args))
	name := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--theme":
			if i+1 < len(args) {
				name = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--theme="):
			name = strings.TrimPrefix(a, "--theme=")
		default:
			out = append(out, a)
		}
	}
	return name, out
}

// resolveTheme sets the process palette from the flag, then $EVALCTL_THEME, then detection.
func resolveTheme(flagName string) error {
	name := strings.TrimSpace(flagName)
	if name == "" {
		name = os.Getenv("EVALCTL_THEME")
	}
	p, err := theme.Resolve(name)
	if err != nil {
		return err
	}
	theme.Set(p)
	applyTheme(p)
	return nil
}

// runTheme is the colour command: with no argument it lists the palettes it ships and what each
// colour is worth on its own background; with a name it draws a sample screen in that palette and
// says how to keep it.
func runTheme(args []string) int {
	fs, _ := newFS("theme")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	// `theme list` and `theme NAME` are the same command; "list" is spelled out for symmetry with
	// `profiles`, and the name is a positional either way, so a global flag before the verb
	// (`evalctl --root X theme daylight`) is parsed instead of being read as a palette.
	rest := fs.Args()
	if len(rest) > 0 && rest[0] == "list" {
		rest = rest[1:]
	}
	if len(rest) > 1 {
		return fail(usageErr{fmt.Errorf("unexpected argument %q — theme takes one name", rest[1])})
	}
	if len(rest) > 0 {
		if err := resolveTheme(rest[0]); err != nil {
			return fail(err)
		}
		active := theme.Current()
		fmt.Printf("%s\n\n", styleTitle.Render("theme "+active.Name))
		themeSample(os.Stdout, active)
		fmt.Printf("\n%s\n", styleDim.Render("keep it: export EVALCTL_THEME="+active.Name+
			"  ·  or pass --theme "+active.Name+" to any command"))
		return 0
	}
	active := theme.Current()
	fmt.Printf("%s %s\n\n", styleTitle.Render("themes"),
		styleDim.Render("(auto picks by background; set with --theme or $EVALCTL_THEME)"))
	for _, p := range theme.Palettes() {
		mark := " "
		if p.Name == active.Name {
			mark = "*"
		}
		kind := "dark"
		if p.Light {
			kind = "light"
		}
		fmt.Printf("%s %s %s\n", styleDim.Render(mark), styleTitle.Render(p.Name), styleDim.Render("on "+kind+" "+p.BG))
		for _, r := range p.Roles() {
			swatch := lipgloss.NewStyle().Foreground(p.Color(r.Hex)).Render("████")
			fmt.Printf("      %s %-8s %s %s\n", swatch, r.Name, r.Hex,
				styleDim.Render(fmt.Sprintf("%5.1f:1", theme.Contrast(r.Hex, p.BG))))
		}
		fmt.Println()
	}
	return 0
}

// themeSample draws the rows, statuses and frame a palette has to carry, so a scheme can be judged
// on the thing it is for and not on a swatch alone.
func themeSample(w io.Writer, p theme.Palette) {
	skill := lipgloss.NewStyle().Foreground(p.Color(p.Accent)).Bold(true).Width(20)
	id := lipgloss.NewStyle().Foreground(p.Color(p.Faint)).Width(4)
	slug := lipgloss.NewStyle().Foreground(p.Color(p.Text)).Width(24)
	split := lipgloss.NewStyle().Foreground(p.Color(p.Muted)).Width(11)
	yes := lipgloss.NewStyle().Foreground(p.Color(p.OK)).Width(4)
	no := lipgloss.NewStyle().Foreground(p.Color(p.Err)).Width(4)
	dupe := lipgloss.NewStyle().Foreground(p.Color(p.Warn)).Width(4)
	frame := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Color(p.Border)).
		MarginLeft(2)
	// The frame's own focus marks, drawn as the screens draw them: the block and the accent on the
	// region that has the keys, the arrow on the row the cursor is on, and that row on the selection
	// colour. A palette judged on a table without them has not been judged on the screen's cue.
	focus := lipgloss.NewStyle().Foreground(p.Color(p.Accent))
	quiet := lipgloss.NewStyle().Bold(true).Foreground(p.Color(p.Faint))
	cursor := lipgloss.NewStyle().Bold(true).Foreground(p.Color(p.Accent))

	// One row, one shape: the cursor's two cells, then the columns every row below is measured
	// against. The band is painted over the finished line, so the cells keep their own colours.
	row := func(mark, name, idn, slugName, spl, verdict, dup string) string {
		return mark + skill.Render(name) + " " + id.Render(idn) + " " + slug.Render(slugName) + " " +
			split.Render(spl) + " " + verdict + " " + dupe.Render(dup)
	}

	fmt.Fprintf(w, "%s   %s\n", focus.Render("▌ TRAIN SET"), quiet.Render("ITERATIONS"))
	fmt.Fprintln(w, styleHead.Render(fmt.Sprintf("  %-20s %-4s %-24s %-11s %-4s %-4s",
		"SKILL", "ID", "SLUG", "SPLIT", "ASR", "DUP")))
	fmt.Fprintln(w, styleGroup.Render("▾ slide-builder"))
	banded := bandRow(lipgloss.NewStyle().Background(p.Color(p.Sel)))
	fmt.Fprintln(w, banded(row(cursor.Render("▶ "), "slide-builder", "3", "dense-argument-slide",
		"train", yes.Render("✓"), "")))
	fmt.Fprintln(w, row("  ", "slide-builder", "4", "no-bullet-wall", "holdout", no.Render("✗"), "dup"))
	fmt.Fprintln(w, row("  ", "slide-critique", "12", "contrast-check", "train", yes.Render("✓"), ""))
	fmt.Fprintln(w, frame.Render("run 7 · 6/9 passed · report.md"))
	fmt.Fprintf(w, "%s  %s  %s\n", styleWarn.Render("warn  tool calls"), styleErr.Render("err  failures"),
		styleDim.Render("muted  ids, splits, hints"))
	fmt.Fprintf(w, "%s\n", styleGroup.Render("▸ group header"))
}

// bandRow paints a finished row with a background, re-opening the fill after every reset Lip Gloss
// writes at the end of a styled cell: wrapping the line alone leaves the fill on the gaps between
// columns and nowhere else. It is the CLI's copy of the TUI's `selected`, for the one row in the
// sample that has the cursor on it.
func bandRow(st lipgloss.Style) func(string) string {
	seq := ""
	out := st.Render("x")
	if i := strings.IndexByte(out, 'x'); i > 0 {
		seq = out[:i]
	}
	if seq == "" {
		return func(line string) string { return line }
	}
	return func(line string) string {
		return seq + strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+seq) + "\x1b[0m"
	}
}
