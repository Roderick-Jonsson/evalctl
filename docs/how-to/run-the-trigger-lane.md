# Run the trigger lane: tune a description

The trigger lane asks one question — *given this request, does the right skill load?* — and the
description in `SKILL.md` is the whole of what decides it. This page works one skill's loop in the
TUI: the door, the skill, the two sections, the iteration, the run.

## The loop, one question at a time

Every screen below comes from one sample project; `slide-builder` is that project's skill, and the
names in the screenshots (`SL-*.md`, `MC-…`) are that project's conventions in its own
`evalctl.json`. Nothing in the tool knows any of them.

It walks one loop and asks one question at a time: **which optimization**, **which skill**, **which
of the skill's two activities**, then that activity's own screens. There is no table to sort, no
column to focus and no second menu — what used to be four tabs is the rail at the top of every step,
which is also the resume point.

## Which skill

```
 trigger optimization > choose a skill                                                        evalctl v0.9.0 · 11:06
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
                                           │
▌ SKILLS                                   │   SLIDE-BUILDER
  ──────────────────────────────────────   │   ────────────────────────────────────────────────────────────────
  SKILL                TRAIN VALID   ITERS │   iteration · step 1 of 1, the loop
▶ slide-builder            2     2     0/5 │   set ready — nothing measured yet
                                           │
                                           │   The set
                                           │   train        2 queries · 1 trigger · 1 do not
                                           │   validation   2 queries · 1 trigger · 1 do not
                                           │   holdout      0 queries
                                           │
                                           │   The wordings
                                           │   measured     0 runs
                                           │   staged       none yet
                                           │   in SKILL.md  52 characters
                                           │
                                           │   Findings
                                           │   warning      no cases: the runner will have nothing to measure for
                                           │                this stage
                                           │
                                           │   → enter  open at iteration · the loop
                                           │
                                           │
                                           │
                                           │
                                           │
                                           │
                                           │
j k move · enter open the loop · esc lanes                                                                   ? keys
 1 skill with a description to tune                                                                           1 / 1
```

`TRAIN`/`VALID` are that skill's query rows, `ITERS` is where it stands against the five iterations
the guide asks for, and `STATE` is what the loop would do next with it — one phrase, read off the
trigger runs on disk rather than remembered.

The list is the disk, not a copy of it taken at startup. The tool stamps the tree once a second —
every path under the root with its size and mtime, pruned at the workspaces' iteration level and
skipping `.git` and `.evalctl`'s backups — and re-reads when the stamp moves. Add a stage skill in
another window and it is in the table a second later; finish a run in a second terminal and the
counts, the wordings and the `STATE` phrase follow it. Two states are held back on purpose, because
they are writes and not readings: a run in flight, whose runner is still writing into the store it
started with, and an open form or question, which holds the index of the row it is about to write.
Both re-read on the tick after they hand the keys back.

The table is the shape the eval tables had: a two-cell gutter where the cursor lives, fixed columns,
counts right-aligned so 1 and 47 end on one edge, a quiet header and no rule under it. Beside it,
the pane says what the row under the cursor is — the activity and step its loop opens in, what both
splits hold (queries, and how many should trigger), how many wordings its runs have measured,
whether a draft is staged, and that skill's validation findings.

`STATE` is the column that gives way first: the pane already says it in words. Below the width that
buys a pane, the row under the cursor is spelled out in a block under the table instead — same
facts, one column.

## The two sections

```
 trigger optimization > slide-builder                                                   evalctl v0.9.0 · 11:06
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  SECTIONS                         │   THE RUN                                                 6 agent calls
  ──────────────────────────────   │   runs per query ‹ 3 ›     dry run ‹ off ›
▶ iteration  0 of 5                │   ─────────────────────────────────────────────────────────────────────
                                   │ ▌ TRAIN SET                                 0 of 5 iterations measured
  generalization  gated            │   ─────────────────────────────────────────────────────────────────────
                                   │   ID    TRIGGER  QUERY                                 WHY
                                   │ ▶ 1     yes      build me slides from this outline     core ask
                                   │   2     no       research the operator pattern for me  near miss
                                   │
                                   │
                                   │
                                   │
                                   │
                                   │
                                   │
                                   │   ─────────────────────────────────────────────────────────────────────
                                   │   q1
                                   │     query
                                   │     build me slides from this outline
                                   │     why
                                   │     core ask
                                   │   ─────────────────────────────────────────────────────────────────────
                                   │   2 rows · 1 should trigger · 1 should not · 50% of the set (the
                                   │   guide asks ~60/40)
                                   │
j k rows · tab the run · n e d new·edit·delete · space trigger · enter the workbench · shift+tab sections   ? keys
 the wording may learn from these rows — nothing else shapes a revision                      sections · 1 / 2
```

The loop is two activities, not four steps in a row, because the two sets are used for opposite
things. **Iteration** works the train set — the only measurement a revision may learn from.
**Generalization** works the validation set — the instrument that judges a wording and must never
shape one. Each owns its query set and its own steps, so the rail inside a section only ever lists
the work that belongs to it, and `esc` goes up one level: a step back to its section, a section back
to the skills.

The pane beside the two is the split the section works, as a table: the train set beside iteration,
the validation set beside generalization, with the same columns and the same keys the step screen's
own set has. That is the moment a set wants fixing — a label that is wrong, a why that is missing —
and cards about it would be a paragraph where a table belongs. `tab` moves the keys between the two
halves: the half that has them is the accent, with a block in front of its name; the other is quiet,
and the status row says which one it is in words.

The pane reads in the order the work is decided. **THE RUN** leads — two knobs, the settings that
govern every row below them, drawn whether or not they have the keys, because what a run costs is
decided before it is started. **TRAIN SET** follows: the split as a table. Then, under the table,
the fields of the row under the cursor.

`tab` walks down that order: from the table into the run's block, and from one knob to the other;
the knob under the cursor takes the accent and the cursor mark, and the table above goes quiet, so
exactly one region is lit. `←`/`→` change it. Runs per query steps between 1 and 9 and is the set's
own number, so it is written into `trigger-evals.json` and every run reads it from there, this
session's or the CLI's. `space` flips the dry run, which prints the commands a run would issue and
writes nothing — the one setting that is not the file's, because it is what the next run is asked to
be; while it is on the status row says so. `enter` or `esc` hands the keys back to the table; every
change is already written, so there is nothing to save.

`enter` goes deeper from either side, and the two are not the same move: from the list it opens the
workbench at the step the disk is at, and from the pane it opens the step that owns the set on
screen. Below the width that buys a pane (about 65 columns, once the list has its own) the set is
managed one screen in, and the list carries its own counts.

## Iteration — four quadrants

The loop is a comparison, not a queue. Four things belong to one decision: **what failed**, **how
much of the set failed**, **what the wording says now**, and **what the last five revisions
bought**. They share one screen as four quadrants, because you look at them together — a
rung-by-rung screen hid three of them while you worked on the fourth.

Only the description is edited here, because a revision is an edit to `SKILL.md` and nothing else.
The set is drawn with the run's verdicts and read; the rows are written beside the two sections,
where the set is the thing on screen.

Each quadrant's name is closed by a rule, the way every other region in the app is drawn: four areas
sharing one screen need the same edge under their titles as a screen with one region, or the pane's
name and the first row of its content read as one paragraph.

```
 tl  FAILED QUERY      one failing row in full, and what the guide says to do about it
 tr  TRAIN SET         the queries and the last run's verdict beside each one — read, not edited
 bl  THE DESCRIPTION   SKILL.md's wording, editable in place — the answer is written here
 br  ITERATIONS        every measured wording, and the rate it scored
```
                                               │
FAILED QUERY · q3                            │ ▌ TRAIN SET
2 queries ──────────────────────────────────────────── │
────────────────────────────────────────────────────────────── Query     build the deck from the
retro      │     ID   TRIGGER RESULT  QUERY
            notes                              │ ▶    1   yes     pass    build me slides from this outline
                                               │      2   no      FAIL    research the operator pattern for me
Expected  to trigger                         │ Fired     0 of 3 runs                        │
                                               │
Verdict   too narrow                         │ Hint 1    Name the context this belongs to.  │ Hint 2
Be pushy: say it applies unnamed.  │ Hint 3    Describe the intent, not the tool. │   2 rows · 1
should trigger · 1 should not · 50% of the set
                                               │   (the guide asks ~60/40)
                                               │
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
                                               │
THE DESCRIPTION                   52 / 1024  │   ITERATIONS
1 of 5 ──────────────────────────────────────────── │
────────────────────────────────────────────────────────────── Use this skill when an outline has to
│    ITER        TRAIN  CHARS  NOTE become slides.                               │       1
50%     52  the description in SKILL.md when t…
                                               │   4 iterations to go before generalization
                                               │
                                               │
                                               │
                                               │
                                               │
                                               │
note  the description in SKILL.md when the … │
                                               │
enter run the train set · tab the description · R run again · f the failures · esc sections
? keys the wording may learn from these rows — nothing else shapes a revision
train set · failure 1 of 1
```

| quadrant | what it holds |
|---|---|
| **failed query** (top left) | the row under the cursor if it failed, otherwise the first failure, in three moves and nothing else: **the ask** in full under a `Query` label; **the evidence** as two labelled rows — `Expected  not to trigger` above `Fired  3 of 3 runs`; then the **verdict** and its hints. The expectation and the run are separate facts with their own labels, because `should not trigger · fired 3 of 3` made the reader work out which half was the wish — the same fix a table makes with a column called *is true*. The verdict (`too broad`) is the guide's reading of those numbers, not a measurement, so it opens the advice rather than trailing the evidence. The why, the skill that loaded instead and the full verdict sentence are one key away (`f`), and each hint is one clause, since the pane sits beside the field you are about to rewrite |
| **train set** (top right) | the queries the wording may learn from, as the last run left them: `RESULT` is `pass`, `FAIL`, or `—` when the candidate has not been run. It is drawn whole — a passing row is half of what says whether the wording works — but the cursor walks the failures only, because a pass is nothing to work on and the pane beside it would have nothing to say. It is read here and written on the screen that owns the set — the pane beside the two sections |
| **the description** (bottom left) | the wording itself, in a field with a caret, because a revision is an edit and not a screen you navigate to. The counter is the format's 1024-character ceiling |
| **iterations** (bottom right) | every run, its train rate, its length, and the note that says why it exists — the only place you can see whether five revisions moved anything |

### Typing

`tab` walks the three panes: the set, the description, the note. Only the last two type —
the set takes `j k` as row commands, and the description is the one thing this screen writes. A pane
that has the caret says so — the footer changes, and a `q` types a `q` instead of quitting. `esc` gives
the keys back to the set; a second `esc` leaves the section.

### The set is one pane: table above, the row's fields below

Every set draws the row under the
cursor in its fields as part of itself — `query`, `why`, `should the skill load?`, `split` — so there is
nothing between looking at a row and changing it, and no key that has to reveal the fields first. This
is the set on the screen that owns it: beside the two sections, and the validation set's own step. The
workbench's right column is the same table without the fields, because that screen is for the
description. `e` puts the caret in them: `tab` moves between them, `ctrl+s` saves through the store and
re-seeds the pane from the file, `esc` gives the keys back to the table and throws the draft away. With
the caret in the table, `j k` move and `q` quits — the fields are drawn quietly until they are asked
for. The `split` field is how a row moves between the train set, the validation set and the holdout: the
save says so out loud (**moved train → validation**), because that row stops being part of what this
screen measures. The id is left to the tool: it indexes the result directories.

### The loop in one key

`enter` on the set does the next thing and the footer names it: it stages
the wording you typed (if it changed) and measures it. `ctrl+s` stages without measuring, `R` measures
again on purpose, and `enter` on an unchanged draft just runs — a run is not a revision.

A revision does not touch `SKILL.md` by itself: it becomes the draft in the workspace
(`pending-description.json`), and the training run is what writes it into the file, because the file
is what the router reads and a wording nobody has measured should not be in it. There is no note
field: what a wording changed is computed from the text itself, against the wording before it.

Below 90 columns the four quadrants cannot be read, so the workbench stacks: the set with its
verdicts, then the description. The failures keep their full cards behind `f`.

## A run

There is no screen between the key and the spend. `enter` on the workbench (or `R`, which is the same
run against the same wording) starts the train run for the candidate being edited, and lands on the
live log; a validation batch from the ranking's `v` and the holdout from the same table go the same
way. What a run costs is decided before, on the section's pane — the runs per query and the dry
run are there, beside the rows they multiply — so the one thing left to read is the run itself:

```
trigger eval 11 · 10 quer(y/ies) × 3 run(s) · threshold 0.5 · profile
eval-video-course-creator-trigger
──────────────────────────────────────────────────────────────────────────────────── q01 ·
slide-builder · should_trigger=true · train          ✓ ✓ ✓ q02 · slide-builder ·
should_trigger=false · train         ✓ ✗ ✓
──────────────────────────────────────────────────────────────────────────────────── run q01 #1 →
skill loaded, evidence found
```

While it runs, one row per query carries a mark per attempt (`·` pending, `⟳` running, `✓` done, `✗`
failed) with the newest eight lines of the arm's own stream underneath — the frames `activity.log`
gets. `x` cancels the batch — the runner stops before it launches the next query and scores nothing, so
an aborted run leaves no iteration behind rather than a table of cancelled rows; the directory it did
create carries `aborted.json` and no `meta.json`, so the lab skips it and the next reader sees what
stopped and why. `v` opens the arm's log in full, and the live screen is the one place `esc` does not
leave: a half-read table of a run that is still spending money is worse than the wait. What the run
measured — the wording, the rate, the per-row verdicts — is in `trigger.json`, which the workbench
reads when it comes back.

## Where next

- [Rank the wordings and apply the winner](generalize-and-apply.md)
- [The same lane from the shell](../reference/cli.md)
