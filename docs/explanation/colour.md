# Colour: roles, contrast, and what a terminal can draw

Views name a role, never a colour. Three palettes ship, each held to measured contrast and each
an exact xterm-256 entry, because the terminal's own ANSI palette is not automatically readable.

## Colour

Views never name a colour, they name a role — `Text`, `Muted`, `Faint`, `Accent`, `Accent2`, `OK`,
`Warn`, `Err`, `Border`, `Line`, `Sel`, `CodeBG`/`CodeFG` — and `internal/theme` decides what each
role is. `Line` is the frame's dividers (the rule under the bar, the seams between table cells): brighter
than a box edge, because it is structure you are meant to find, and well below the text it divides.
One register, both surfaces: a column head is `Faint` in the TUI's tables and in every printed one, and
the quiet words — ids, hints, splits — take the same role in both, so `evalctl ls` and the workbench
agree on what a head looks like. `evalctl theme NAME` draws a sample screen with the frame's own focus
marks in it, because a palette has to be judged on its cue and not on a swatch.

Three palettes ship:

| palette | for | held to |
|---|---|---|
| `midnight` | a dark terminal (the default) | body text ≥ 7:1, every other role ≥ 4.5:1, borders 1.4–3:1, dividers 2.2–5:1 |
| `high-contrast` | a bright room, a projector, tired eyes | nothing below 8.8:1 |
| `daylight` | a light terminal | the same floors, with the hues darkened instead of brightened |

The palette is chosen by asking the terminal for its background colour (OSC 11) and falling back to
dark; `--theme NAME` or `$EVALCTL_THEME` overrides it, and `evalctl theme` lists the palettes with
each colour's measured contrast on its own background:

```bash
evalctl theme                     # the palettes, the roles, the contrast ratios
evalctl theme high-contrast       # a sample screen drawn in one of them
export EVALCTL_THEME=daylight     # make it stick
```

Two things this was written against. First, a terminal's own ANSI palette is not automatically
readable: on a black background Apple Terminal's blue is `#0000B3` (1.7:1) and its bright blue
`#0000FF` (2.4:1), which is what a view gets for asking for "12". Second, most terminals here are
256-colour, not truecolor, so a hex value is approximated — `internal/theme` maps it to the
perceptually nearest xterm entry in CIE Lab and holds the mapping to the palette's contrast promise,
instead of taking the default converter's guess (which turns a near-white into a cyan one). Each
palette colour is an exact xterm-256 entry, so what a 256-colour terminal draws is what the table
above claims.

The tests hold the palettes to their numbers (`internal/theme/theme_test.go`) and check that the
three status colours stay apart under simulated protanopia and deuteranopia — colour is never the
only cue, since `✓`/`✗`, bold, the cursor arrow, and the block in front of the focused region's name
carry the same information.

## Where next

- [Glyphs: `unicode`, `ascii`, `auto`](../reference/environment.md)
- [Colour is never the only cue](deliberate-choices.md)
