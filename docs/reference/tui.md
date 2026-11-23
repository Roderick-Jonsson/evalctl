# TUI reference: the frame and the keys

One frame for every screen: where the keys are, how the path reads, and what is deliberately not
in the TUI at all.

## The frame

The bar carries the path — `trigger optimization > skill > step` — and the tool with its build at the
right edge. The rail sits under it, and the loop's own numbers sit at the right end of the rail's row.
The output lane has no rail under the bar, in a view or on its menu: the three views are a choice, and
a choice has one screen. Inside a view the name is already the path's last segment, so a rail of its
siblings would be the menu drawn again above a screen with no key to any of them; what stands there
instead is the loop's numbers, the one reading no view counts for itself.

**Where the keys are.** A screen here is one or more *regions* — a list, a table, a pane, a field — and
exactly one of them has the keystrokes. It says so in two places and no more: the region that has them
is drawn in the accent with a block in front of its name, every other region's name is quiet, and the
cursor mark and the selected row exist only inside that region. A second bright thing is a second
answer to the only question the frame has to answer. The same fact is spelled out at the right edge of
the status row — `sections · 1 / 2`, `train set · failure 2 of 3`, `editor · q3`, `note`,
`assertions · case 2 of 2` — which is the form
of it that survives a screenshot, a colour-blind reader, and a terminal drawing neither the accent nor
the block. The rail's `●` is a different mark on purpose: it says which step of the flow you are in,
which stays true whichever region has the keys, and it is never the arrow the cursor uses. The key band and the status row are the last two rows of the window,
always; `esc` is one level up and the footer names the level. `--glyphs ascii` draws the same frame in
`> - + ~`, and a narrow window cuts the hints before it cuts the `? keys` cap.

## What the TUI does not do

Browsing the set, sorting and grouping columns, multi-select, delete-by-filter, renumbering, moving a
case between skills, and both promotions are **CLI verbs** (below). The TUI is the loop; the CLI is
what you reach for when you want to look at the whole set at once.

## Where next

- [The loop the frame draws](../how-to/run-the-trigger-lane.md)
- [Why there is no second bright thing](../explanation/deliberate-choices.md)
