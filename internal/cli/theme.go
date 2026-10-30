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
	styleDim    = lipgloss.NewStyle()
	styleHead   = lipgloss.NewStyle()
	styleYes    = lipgloss.NewStyle()
	styleNo     = lipgloss.NewStyle()
	styleDupe   = lipgloss.NewStyle()
	styleChange = lipgloss.NewStyle()
	styleErr    = lipgloss.NewStyle()
	styleWarn   = lipgloss.NewStyle()
	styleTitle  = lipgloss.NewStyle()
	styleGroup  = lipgloss.NewStyle()
)

func init() { applyTheme(theme.Current()) }

// applyTheme rebuilds the table from a palette.
func applyTheme(p theme.Palette) {
	// Dim is a colour, not Faint(true): SGR 2 is dropped by some terminals, and a hint that is
	// styled with nothing is a hint at full brightness.
	styleDim = lipgloss.NewStyle().Foreground(p.Color(p.Muted))
	styleHead = lipgloss.NewStyle().Bold(true).Underline(true).Foreground(p.Color(p.Text))
	styleYes = lipgloss.NewStyle().Foreground(p.Color(p.OK))
	styleNo = lipgloss.NewStyle().Foreground(p.Color(p.Err))
	styleDupe = lipgloss.NewStyle().Foreground(p.Color(p.Warn))
	styleChange = lipgloss.NewStyle().Foreground(p.Color(p.OK))
	styleErr = lipgloss.NewStyle().Foreground(p.Color(p.Err))
	styleWarn = lipgloss.NewStyle().Foreground(p.Color(p.Warn))
	styleTitle = lipgloss.NewStyle().Bold(true).Foreground(p.Color(p.Text))
	styleGroup = lipgloss.NewStyle().Foreground(p.Color(p.Accent2)).Bold(true)
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
	if len(args) > 0 && args[0] == "list" {
		args = args[1:]
	}
	if len(args) > 0 {
		if err := resolveTheme(args[0]); err != nil {
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
	id := lipgloss.NewStyle().Foreground(p.Color(p.Faint))
	slug := lipgloss.NewStyle().Foreground(p.Color(p.Text)).Width(24)
	split := lipgloss.NewStyle().Foreground(p.Color(p.Muted)).Width(11)
	yes := lipgloss.NewStyle().Foreground(p.Color(p.OK)).Width(4)
	no := lipgloss.NewStyle().Foreground(p.Color(p.Err)).Width(4)
	dupe := lipgloss.NewStyle().Foreground(p.Color(p.Warn)).Width(4)
	frame := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Color(p.Border))

	fmt.Fprintln(w, styleHead.Render(fmt.Sprintf("%-20s %-4s %-24s %-11s %-4s %-4s",
		"SKILL", "ID", "SLUG", "SPLIT", "ASR", "DUP")))
	fmt.Fprintln(w, styleGroup.Render("▾ slide-builder"))
	fmt.Fprintf(w, "%s %s %s %s %s %s\n", skill.Render("slide-builder"), id.Render("3"),
		slug.Render("dense-argument-slide"), split.Render("train"), yes.Render("✓"), dupe.Render(""))
	fmt.Fprintf(w, "%s %s %s %s %s %s\n", skill.Render("slide-builder"), id.Render("4"),
		slug.Render("no-bullet-wall"), split.Render("holdout"), no.Render("✗"), dupe.Render("dup"))
	fmt.Fprintf(w, "%s %s %s %s %s %s\n", skill.Render("slide-critique"), id.Render("12"),
		slug.Render("contrast-check"), split.Render("train"), yes.Render("✓"), dupe.Render(""))
	fmt.Fprintln(w, frame.Render("run 7 · 6/9 passed · report.md"))
	fmt.Fprintf(w, "%s  %s  %s\n", styleWarn.Render("warn  tool calls"), styleErr.Render("err  failures"),
		styleDim.Render("muted  ids, splits, hints"))
	fmt.Fprintf(w, "%s\n", styleGroup.Render("▸ group header"))
}
