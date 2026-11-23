# Generalize on the validation set, then apply the winner

The validation set is the instrument that judges a wording and must never shape one. This page
ranks every wording the loop measured against it, and puts the pick into `SKILL.md`.

## Generalization — validate, rank, apply

```
 ✓ 1 validation set ─── ● 2 ranking
```

**Step 1 — the validation set.** The same table with the other job: these rows judge the wording and
are never used to write one. `e` and `n` open the same editor under the table, and its `split` field is
the way a row that turned out not to be held-back becomes a train row instead.

**Step 2 — the ranking.** One table is the whole of the section, and it has no rail above it: the head
says what ranks the rows and the line under them says what the table still owes, so a rail would be the
flow restated at the top of a screen that is all table — the same row the workbench takes. `1` goes back
to the validation set. The table holds every iteration the loop measured, the train rate it was written
against, and the validation rate it earned — or a dash, where the validation set has not been run
against that wording yet. It used to be two screens, a batch that listed what was about to be run and a
result list that listed what had been, and the same rows were drawn on both with a keypress between
them.

```
 trigger optimization > slide-builder > generalization > ranking                              evalctl v0.9.0 · 11:06
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

▌ RANKING                                                                        ranked by the validation set
  ────────────────────────────────────────────────────────────────────────────────────────────────
  ITERATION  TRAIN         VALIDATION       GAP    CHARS   NOTE
  1          1/2 50%       2/2 100%         −50%   52      the description in SKILL.md when the loop …  in file Recommended
▶ 2          2/2 100%      —                —      91      the description with the deck named
  ────────────────────────────────────────────────────────────────────────────────────────────────
  → v  validate 1 iteration · 2 queries × 3 runs = 6 agent calls
j k move · v validate the rest · 1 validation set · enter apply · f failures · esc sections          ? keys
 1 of 2 ranked                                                                                    step 2 of 2
```

That tail is the whole of what sits under the table: the run it still owes, with what the run costs,
and a legend only when a cell uses the mark it explains. The pick is not restated there — it is the
`Recommended` tag on its own row, in amber, at the right edge; the columns are named by their own head;
and the corner says how much of the table is ranked at all. A screen that is all table does not read
itself out loud underneath.

**A wording with no rate is still a row.** The dash is a fact the table exists to show, and a row that
vanished until it was measured would hide how much of the table is unranked. `v` runs the validation
set for exactly those iterations, in the order the loop wrote them: the wording is written into
`SKILL.md` in turn (that is what the router reads), the validation queries are run against it, and the
next candidate follows — one batch, one progress screen, and the line that offers it states what it
costs. When the batch lands the same table carries the new percentages, still and only this table,
with the cursor on what the new rates recommend. `SKILL.md` is left holding the last candidate
measured; nothing is applied until you pick one.

**A rate over part of the set is marked `of N`.** A batch you cancelled, or one whose queries all died
at launch, leaves a rate over the queries that happened to land — and `1/1 100%` beside `3/3 100%` is a
batch that stopped competing with one that finished. The cell says how many of the set's validation
queries it actually judged, the table says what that mark means, and the recommendation never picks
such a row: `v` runs the whole set for it again. `x` on the run screen stops the batch at the candidate
it is on — the queue behind it is dropped rather than walked through, and an aborted batch writes no
measurement, so a stopped batch is never read as an iteration.

**A wording nothing has run yet is named, not drawn.** A staged draft is not an iteration: it has no
row, and the line under the table names it, says that no run has measured it, and points at the
workbench's run, which is what measures it. There is one staging slot, so a second draft replaces the
first rather than leaving a wording nobody can measure.

**The recommendation is a tag on the row, and a line under the table.** `Recommended`, in amber, at the
right edge of the row the rule picked — a word instead of the green tick it used to be, because a tick
in the note column read as the rate's own pass mark and was the first thing on the screen to be skipped.
The line under the rule says the same pick with its numbers. It is the guide's selection rule computed —
the best validation rate, a tie going to the earlier wording, the one that got there with less text and
fewer revisions — and it is where the cursor opens, because walking to the winner by hand would be the
reader repeating the arithmetic. The pick stays yours: `j k` walk the table and `enter` (or `a`) applies
the row under the cursor to `SKILL.md` — frontmatter only, backed up, re-read through the parser,
and the numbers that earned it printed as the commit message git can keep — after the loop's one
confirmation. `f`
opens that iteration in full, and `h` then runs the fresh queries once, apart from every rate the loop
tuned on. With nothing validated yet the table has no rate to rank, and it says so instead of
recommending a row.

## The gate

Five measured iterations is the guide's bar — below it a validation rate is a coin and the difference
between two wordings is noise. Generalization's ranking stays locked until then, and `ctrl+g` is the
stated exception: it opens it and the status row says the rate is unproven. The validation set itself
can be written at any time; it is the run that waits.

## Where next

- [The output lane: does the work come out right?](run-the-output-lane.md)
- [Why the gate exists](../explanation/deliberate-choices.md)
