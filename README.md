# evalctl

The whole eval toolchain for this repo in one Go binary: it manages the two eval lanes, runs
them, and browses the results. No Python at runtime.

- **trigger lane** — `trigger/trigger-evals.json` (does the right skill load?)
- **output lane** — `<skill>/evals/evals.json` plus the fixture dirs `<skill>/evals/files/<slug>/`

Built with Bubble Tea, Bubbles, Lip Gloss and Huh, and Glamour for the reports.

```bash
cd evalctl
make                          # same as `make install`: the binary on PATH is the default goal
make install                  # stamped build, put on PATH — then `evalctl` from anywhere in the tree
make hooks                    # post-commit hook: reinstalls whenever a commit touches the tool
make build                    # stamped: commit, build time, dirty flag → ./bin/evalctl
go build -o bin/evalctl .     # plain build: commit comes from the Go build info
./bin/evalctl                 # TUI: the door, then the loop, step by step
```

Once installed (`$(go env GOPATH)/bin/evalctl`, which `make install` warns you about if it is not
on `PATH`), no path into this directory is needed:

```bash
evalctl                       # TUI: what to optimize, then which skill
evalctl tui --skill slide-builder   # straight into one skill's loop
evalctl tui --glyphs ascii          # unicode marks and rules, or the ASCII fallback
evalctl version               # which build this is
make uninstall                # take it back off PATH
```

`make hooks` copies `githooks/post-commit` into `.git/hooks/` (refusing to overwrite a hook it did
not write — merge by hand instead). It fires only when the commit touched Go sources, `go.mod`,
`go.sum` or the Makefile, so commits to the eval sets stay fast, and a failed rebuild only prints a
stale-binary warning — never a failed commit. It rebuilds **both** copies: the one on PATH and
`./bin/evalctl`, because both get run — refreshing only PATH is how `./bin/evalctl` ended up two
commits behind the screen someone was looking at. It is per-clone: run it once after cloning.

`--root` picks the tree (default: walk up for the eval tree, or `$EVALCTL_ROOT`). Running it from
inside the repo just works, installed or not.

`--glyphs auto|unicode|ascii` (`$EVALCTL_GLYPHS` as the default) picks the marks the frame draws
itself: the cursor, the block in front of the region that has the keys, the rail's marks for a step that
is done, current or locked, and a run in flight. `auto` follows the locale. The ASCII profile draws the same frame in `> | - + x` for a
terminal without UTF-8 — same widths, same columns, and a test that no frame mark escapes it.

## Version

The number lives in `internal/version/version.go` and is hand-edited: bump it when the runner, the
store rules, the profiles or the case format change, because an iteration measured by a different
harness is not comparable with the last one. It is also the first thing in the artifact:

- the TUI header's right corner carries `v0.9.0+ · 11:06` — the version, a `+` when the tool's own
  sources were dirty at build time, and then the build's own clock, local time, read from the ldflags
  stamp. It is there because the binary somebody is looking at and the binary somebody built are not
  always the same one: a stale `bin/evalctl` looks exactly like a fresh one until the corner says
  when it was made. An unstamped `go build` shows no clock rather than the commit's time, which is
  the one number in this tool that would make a stale binary look fresh;
- `<skill>-workspace/iteration-N/meta.json` and `trigger-workspace/trigger-N/meta.json` record `provenance.tool`
  (version, commit, dirty, `built_at`);
- both `report.md` files name the build that generated them, and a run's start line prints it.

`make build` stamps the commit, the build time and the dirty flag; `make install` stamps it the same
way and drops the binary in `$GOBIN`. A plain `go build` still names
its commit through the Go build info — `evalctl version` says which of the three sources it used
(ldflags, build info, or nothing but the declared number), so a release binary and a scratch build
never look the same. `make check` runs gofmt and vet; `make test` the suite.

## Why it exists

The set is worked on one stage skill at a time and the work is one loop — measure a wording, revise
it, generalize it — so the TUI is that loop and nothing else: the door, the skill, its two sections.

The sets grew faster than they got pruned, and the loop that measured them lived in two Python
files that had to agree with the manager about slugs, splits and fixture dirs. So the rules live
in one place (`internal/store` for the sets, `internal/runner` for the loop) and the CLI and the
TUI both go through them.

The Python originals (`evals/run.py`, `evals/profiles.py`) are gone: this binary is the only
runner. `go test ./internal/runner -run Parity` still re-scores a trigger iteration recorded
before the port from its own `trigger.json` records and compares every verdict, so the port stays
checkable against what the old loop produced.

## TUI

It walks one loop and asks one question at a time: **which optimization**, **which skill**, **which
of the skill's two activities**, then that activity's own screens. There is no table to sort, no column
to focus and no second menu — what used to be four tabs is the rail at the top of every step, which is
also the resume point.

The top row is a path, not a sentence: `trigger optimization > slide-builder > iteration > iterate`. A
middot between three words reads as prose — so the segments are separated with `>`, the last one is
the one you are looking at, and the numbers moved to the right end of the rail's row, where the steps
already are. The iteration screen leaves that row empty, because the workbench states its own totals —
the iterations table counts the loop and rates the last run — and a second counter above the table read
as a rival to it. Below the width where the table is stacked away, the row comes back: there it is the
only place the loop's count is. Generalization's ranking takes the row out too — its head and its
line about what it still owes are the whole flow of that screen — so it opens one row higher, the
body where the rail was.

```
 what to optimize                                                                             evalctl v0.9.0 · 11:06
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
                                     │ 
▌ OPTIMIZATIONS                      │   TRIGGER OPTIMIZATION
  ────────────────────────────────   │   ────────────────────────────────────────────────────────────────
▶ trigger optimization  built        │   built · the description loop
                                     │   A skill only helps if it gets loaded, and the description is the whole of
  output optimization  built         │   the input — an agent decides from that one paragraph. This lane measures
                                     │   the wording, revises it on what the train set failed, and generalizes on a
                                     │   split no revision could read.
                                     │ 
                                     │   The two activities
                                     │   iteration      the train set: run it, read the failures, revise the wording
                                     │   generalization the validation set: run every wording against it, then rank
                                     │ 
                                     │   It writes
                                     │   the set        trigger/trigger-evals.json
                                     │   wordings       trigger/descriptions/<skill>.json
                                     │   the winner     SKILL.md — the frontmatter key, nothing else
                                     │ 
                                     │   → enter  pick the skill whose wording gets tuned
                                     │ 
                                     │ 
                                     │ 
                                     │ 
                                     │ 
                                     │ 
                                     │ 
                                     │ 
                                     │ 
enter open · q quit                                                                                          ? keys 
                                                                                                              1 / 2 
```

The two rows are the whole choice, and the pane beside them explains the one under the cursor: what
the lane asks, what it does step by step, and what it leaves on disk. `j`/`k` move the pane with the
cursor; `enter` opens the skill chooser for that lane. The output lane's chooser and loop are the
same shape as the trigger lane's, with its own columns and its own two screens (below).

### Which skill

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
                                           │   The ledger
                                           │   wordings     0 candidates
                                           │   applied      none yet
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

`TRAIN`/`VALID` are the query rows that name the skill, `ITERS` is where it stands against the five
iterations the guide asks for, and `STATE` is what the loop would do next with it — one phrase, read
off the ledger and the iterations on disk rather than remembered.

The list is the disk, not a copy of it taken at startup. The tool stamps the tree once a second —
every path under the root with its size and mtime, pruned at the workspaces' iteration level and
skipping `.git` and `.evalctl`'s backups — and re-reads when the stamp moves. Add a stage skill in
another window and it is in the table a second later; finish a run in a second terminal and the
counts, the ledger and the `STATE` phrase follow it. Two states are held back on purpose, because
they are writes and not readings: a run in flight, whose runner is still writing into the store it
started with, and an open form or question, which holds the index of the row it is about to write.
Both re-read on the tick after they hand the keys back.

The table is the shape the eval tables had: a two-cell gutter where the cursor lives, fixed columns,
counts right-aligned so 1 and 47 end on one edge, a quiet header and no rule under it. Beside it, the
pane says what the row under the cursor is — the activity and step its loop opens in, what both splits
hold (queries, and how many should trigger), how many wordings the ledger carries, which one
`SKILL.md` was given, and that skill's validation findings.

`STATE` is the column that gives way first: the pane already says it in words. Below the width that
buys a pane, the row under the cursor is spelled out in a block under the table instead — same facts,
one column.

### The two sections

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
**Generalization** works the validation set — the instrument that judges a wording and must never shape
one. Each owns its query set and its own steps, so the rail inside a section only ever lists the work
that belongs to it, and `esc` goes up one level: a step back to its section, a section back to the
skills.

The pane beside the two is the split the section works, as a table: the train set beside iteration, the
validation set beside generalization, with the same columns and the same keys the step screen's own set
has. That is the moment a set wants fixing — a label that is wrong, a why that is missing — and cards
about it would be a paragraph where a table belongs. `tab` moves the keys between the two
halves: the half that has them is the accent, with a block in front of its name; the other is quiet, and
the status row says which one it is in words.

The pane reads in the order the work is decided. **THE RUN** leads — two knobs, the settings that
govern every row below them, drawn whether or not they have the keys, because what a run costs is
decided before it is started. **TRAIN SET** follows: the split as a table. Then, under the table, the
fields of the row under the cursor. `tab` walks down that order: from the table into the run's block,
and from one knob to the other; the knob under the cursor takes the accent and the cursor mark, and
the table above goes quiet, so exactly one region is lit. `←`/`→` change it. Runs per query steps
between 1 and 9 and is the set's own number, so it is written into `trigger-evals.json` and every run
reads it from there, this session's or the CLI's. `space` flips the dry run, which prints the commands
a run would issue and writes nothing — the one setting that is not the file's, because it is what the
next run is asked to be; while it is on the status row says so. `enter` or `esc` hands the keys back to
the table; every change is already written, so there is nothing to save.

`enter` goes deeper from either side, and the two are not the same move: from the list it opens the
workbench at the step the disk is at, and from the pane it opens the step that owns the set on screen.
Below the width that buys a pane (about 65 columns, once the list has its own) the set is managed one
screen in, and the list carries its own counts.

### Iteration — four quadrants

The loop is a comparison, not a queue. Four things belong to one decision: **what failed**, **how much
of the set failed**, **what the wording says now**, and **what the last five revisions bought**. They
share one screen as four quadrants, because you look at them together — a rung-by-rung screen hid
three of them while you worked on the fourth.

Only the description is edited here, because a revision is an edit to `SKILL.md` and nothing else. The
set is drawn with the run's verdicts and read; the rows are written beside the two sections, where the
set is the thing on screen.

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
  FAILED QUERY · q3                            │ ▌ TRAIN SET                                              2 queries 
  ──────────────────────────────────────────── │   ────────────────────────────────────────────────────────────── 
  Query     build the deck from the retro      │     ID   TRIGGER RESULT  QUERY                                   
            notes                              │ ▶    1   yes     pass    build me slides from this outline       
                                               │      2   no      FAIL    research the operator pattern for me    
  Expected  to trigger                         │ 
  Fired     0 of 3 runs                        │ 
                                               │ 
  Verdict   too narrow                         │ 
  Hint 1    Name the context this belongs to.  │ 
  Hint 2    Be pushy: say it applies unnamed.  │ 
  Hint 3    Describe the intent, not the tool. │   2 rows · 1 should trigger · 1 should not · 50% of the set
                                               │   (the guide asks ~60/40)
                                               │ 
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
                                               │ 
  THE DESCRIPTION                   52 / 1024  │   ITERATIONS                                                1 of 5
  ──────────────────────────────────────────── │   ──────────────────────────────────────────────────────────────
  Use this skill when an outline has to        │    ITER        TRAIN  CHARS  NOTE
  become slides.                               │       1          50%     52  the description in SKILL.md when t…
                                               │   4 iterations to go before generalization
                                               │
                                               │
                                               │
                                               │
                                               │
                                               │
  note  the description in SKILL.md when the … │
                                               │
enter run the train set · tab the description · R run again · f the failures · esc sections              ? keys 
 the wording may learn from these rows — nothing else shapes a revision                            train set · failure 1 of 1 
```

| quadrant | what it holds |
|---|---|
| **failed query** (top left) | the row under the cursor if it failed, otherwise the first failure, in three moves and nothing else: **the ask** in full under a `Query` label; **the evidence** as two labelled rows — `Expected  not to trigger` above `Fired  3 of 3 runs`; then the **verdict** and its hints. The expectation and the run are separate facts with their own labels, because `should not trigger · fired 3 of 3` made the reader work out which half was the wish — the same fix a table makes with a column called *is true*. The verdict (`too broad`) is the guide's reading of those numbers, not a measurement, so it opens the advice rather than trailing the evidence. The why, the skill that loaded instead and the full verdict sentence are one key away (`f`), and each hint is one clause, since the pane sits beside the field you are about to rewrite |
| **train set** (top right) | the queries the wording may learn from, as the last run left them: `RESULT` is `pass`, `FAIL`, or `—` when the candidate has not been run. It is drawn whole — a passing row is half of what says whether the wording works — but the cursor walks the failures only, because a pass is nothing to work on and the pane beside it would have nothing to say. It is read here and written on the screen that owns the set — the pane beside the two sections |
| **the description** (bottom left) | the wording itself, in a field with a caret, because a revision is an edit and not a screen you navigate to. The counter is the format's 1024-character ceiling |
| **iterations** (bottom right) | every run, its train rate, its length, and the note that says why it exists — the only place you can see whether five revisions moved anything |

**Typing.** `tab` walks the three panes: the set, the description, the note. Only the last two type —
the set takes `j k` as row commands, and the description is the one thing this screen writes. A pane
that has the caret says so — the footer changes, and a `q` types a `q` instead of quitting. `esc` gives
the keys back to the set; a second `esc` leaves the section.

**The set is one pane: table above, the row's fields below.** Every set draws the row under the
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

**The loop in one key.** `enter` on the set does the next thing and the footer names it: it saves the
wording you typed as the next candidate (if it changed) and measures it. `ctrl+s` saves without
measuring, `R` measures again on purpose, and `enter` on an unchanged draft just runs — a run is not a
revision, and the ledger keeps a new candidate only when the text differs.

A revision does not touch `SKILL.md` by itself: it becomes the next candidate in the ledger, and the
training run is what writes it into the file, because the file is what the router reads and a wording
nobody has measured should not be in it. The note beside the description is the ledger's `note` field
— the one place to say why this wording exists, for whoever reads the run in a month.

Below 90 columns the four quadrants cannot be read, so the workbench stacks: the set with its
verdicts, then the description. The failures keep their full cards behind `f`.

### Generalization — validate, rank, apply

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
measurement, so nothing stopped enters the ledger as an iteration.

**A wording nothing has run yet is named, not drawn.** A revision saved and then revised again before
any run leaves a candidate the ledger holds and no train run measured. It is not an iteration, so it has
no row; the line under the table names it, says that no train run has measured it, and points at the
workbench's run, which is what measures it.

**The recommendation is a tag on the row, and a line under the table.** `Recommended`, in amber, at the
right edge of the row the rule picked — a word instead of the green tick it used to be, because a tick
in the note column read as the rate's own pass mark and was the first thing on the screen to be skipped.
The line under the rule says the same pick with its numbers. It is the guide's selection rule computed —
the best validation rate, a tie going to the earlier wording, the one that got there with less text and
fewer revisions — and it is where the cursor opens, because walking to the winner by hand would be the
reader repeating the arithmetic. The pick stays yours: `j k` walk the table and `enter` (or `a`) applies
the row under the cursor to `SKILL.md` — frontmatter only, backed up, re-read through the parser,
recorded in the ledger beside the measurement that earned it — after the loop's one confirmation. `f`
opens that iteration in full, and `h` then runs the fresh queries once, apart from every rate the loop
tuned on. With nothing validated yet the table has no rate to rank, and it says so instead of
recommending a row.

### The gate

Five measured iterations is the guide's bar — below it a validation rate is a coin and the difference
between two wordings is noise. Generalization's ranking stays locked until then, and `ctrl+g` is the
stated exception: it opens it and the status row says the rate is unproven. The validation set itself
can be written at any time; it is the run that waits.

### A run

There is no screen between the key and the spend. `enter` on the workbench (or `R`, which is the same
run against the same wording) starts the train run for the candidate being edited, and lands on the
live log; a validation batch from the ranking's `v` and the holdout from the same table go the same
way. What a run costs is decided before, on the section's pane — the runs per query and the dry
run are there, beside the rows they multiply — so the one thing left to read is the run itself:

```
  trigger eval 11 · 10 quer(y/ies) × 3 run(s) · threshold 0.5 · profile eval-vcc-trigger
  ────────────────────────────────────────────────────────────────────────────────────
  q01 · slide-builder · should_trigger=true · train          ✓ ✓ ✓
  q02 · slide-builder · should_trigger=false · train         ✓ ✗ ✓
  ────────────────────────────────────────────────────────────────────────────────────
  run q01 #1 → skill loaded, evidence found
```

While it runs, one row per query carries a mark per attempt (`·` pending, `⟳` running, `✓` done, `✗`
failed) with the newest eight lines of the arm's own stream underneath — the frames `activity.log`
gets. `x` cancels the batch — the runner stops before it launches the next query and scores nothing, so
an aborted run leaves no iteration behind rather than a table of cancelled rows; the directory it did
create carries `aborted.json` and no `meta.json`, so the lab skips it and the next reader sees what
stopped and why. `v` opens the arm's log in full, and the live screen is the one place `esc` does not
leave: a half-read table of a run that is still spending money is worse than the wait. What the run
measured — the candidate, the rate, the per-row verdicts — is in the ledger the workbench reads when it
comes back.

### The output lane

The other optimization, and the other half of the question: with the skill loaded, does the work come
out right? It is agentskills.io's *Evaluating skill output quality* loop, computed from disk. Every
case in `<skill>/evals/evals.json` runs twice — with the skill and without it — into
`<skill>-workspace/iteration-N/<slug>/with_skill|without_skill/`, and the judge grades each arm's
declared assertions PASS or FAIL with the evidence it read.

The skill chooser is the same table in this lane's terms: how many cases the skill declares, what the
last iteration scored on each arm, and how many iterations it has measured.

```
 output optimization > choose a skill                                                         evalctl v0.9.0 · 11:06
▌ SKILLS
  SKILL                CASES     WITH  WITHOUT   ITERS
▶ dummy                    2     100%     100%       1
  learning-path            2        —        —       2
```

`enter` opens the loop at the section the disk is asking for: **setup** when nothing is declared yet,
the **dojo** when the newest iteration has failures to read, **quality** when the loop is waiting on a
grade or on a rerun. The three sections are on one rail — `1` `2` `3`, `tab` for the next and
`shift+tab` for the previous — because a section reachable only by a key nobody can see is a section
nobody finds. `esc` is one level up in the lane's own ladder: a section back to setup, then the skills.

**1 · setup — declare the case.** The left column is the cases, one slug and one declaration mark each
(`✓` prompt and assertions, `○` prompt alone, `✗` nothing yet), and nothing else: the rates and the
verdicts belong to the sections that read them. The pane beside it shows **the case as its own record
writes it** — `prompt`, `expected_output`, `files` (the fixtures the run root is given, or `none — the
run root starts empty`), `assertions` — the fields keeping the file's names and the file's order,
because the pane is meant to be read as the case and not as a summary of it.

```
 output optimization > dummy > setup                                                         evalctl v0.9.0 · 11:06
 ● 1 setup ─── 2 dojo ─── 3 quality                          3 cases · 6 assertions · iteration #2 · best #2
                       │
▌ CASES                │   dummy-1-one-die                                                       #1
  ──────────────────   │   ─────────────────────────────────────────────────────────────────
▶ ✓ dummy-1-one-die    │   with 3/3 · without 2/3
  ○ dummy-2-three-dice │
  ✓ dummy-3-no-writes  │   prompt
                       │   Throw three twenty-sided dice and give me the faces and the total.
                       │
                       │   expected_output
                       │   Three faces, each an integer from 1 to 20, and their sum as the total.
                       │
                       │   files
                       │   none — the run root starts empty
                       │
                       │   assertions — 1 · ✓ with · ✗ without
                       │   ✓ ✗  The total given is the sum of the faces
                       │        without · the arm wrote out.txt
```

`e` writes that list where it is drawn: the field takes the assertion block's place in the pane, one
assertion per line, the way `evalctl edit --assertions` takes them, so the record above it stays on
screen while the sentence it is about is typed. `ctrl+s` writes `<skill>/evals/evals.json` (backed up
first), `esc` discards, and enter is a line in the field rather than a submit. The save says one more
thing: the verdicts on screen were read against the *old* list, so `g` re-grades (it asks first —
the run already judged this iteration once). An assertion added
after grading shows a dash for both arms until it is judged; clearing the field leaves the case with no
`assertions` key at all, the shape a case is in before its list is known. `S` hands the case file whole
to `$EDITOR` — a prompt and a fixture list are prose and paths, not a text area — and the pane re-reads
it when the editor exits.

**Facts ride in the assertion's own sentence.** `assertions` is a list of strings in the format, and the
judge reads them in that order, so what an expectation is checked against is written where the
expectation is: `every Sub-Topic appears exactly once — facts: ST-1 … ST-9, as in PS.md`. A second array
of facts would have to be kept in step by position, which is the one join this tool already distrusts.

**2 · dojo — work one iteration.** The left rail is the iteration's **failed** assertions, one row per
case and arm; the passing ones are counted under it (`+ 4 passed in both arms`), because an assertion
both arms pass measures the model and not the procedure. The pane works the one under the cursor: the
sentence the judge graded, the judge's words for it, the end of that arm's execution transcript, and the
human note. `[` and `]` walk the iterations without leaving the section; `v` (or `enter`) opens the whole
`activity.log`; `n` writes the note into the iteration's `feedback.json` (backed up first — it is a file
in the workspace, not in the tree, and it is backed up like one); `E` hands `SKILL.md` to `$EDITOR`,
because the revision is a document and a text field is not an editor.

```
 output optimization > dummy > dojo                                                          evalctl v0.9.0 · 11:06
 1 setup ─── ● 2 dojo ─── 3 quality                          3 cases · 6 assertions · iteration #2 · best #2
                             │
▌ FAILED                  1  │   dummy-2-three-dice                                      #2 · without ✗
  ────────────────────────   │   ─────────────────────────────────────────────────────────────────
▶ without dummy-2-three-dice │
                             │   assertion
  + 4 passed in both arms    │   The total given is the sum of the faces
                             │
                             │   evidence
                             │   the arm's out.txt totals 30, the reply says 27
                             │
                             │   transcript
                             │   rolled 3d20 → 17 9 4
                             │   wrote out.txt
                             │   … v reads the whole log
                             │
                             │   note
                             │   the total is the one that drifts — check the second roll
```

That is the loop's middle, and it is the part that used to be a sentence in the guide: the failures and
the note were read into a pane that had no way to answer them. `n` is the answer, and it lands in the
same `feedback.json` the pane quotes.

**3 · quality — judge the trend.** Every iteration's two rates, the difference between them, how much
of the set ran, and how much of the grading list the judge saw. `enter` opens that iteration on the
dojo — the question a table row raises is what happened in it, and the answer is its evidence, not its
own report (`r` opens `report.md`).

```
 output optimization > dummy > quality                                                       evalctl v0.9.0 · 11:06
 1 setup ─── 2 dojo ─── ● 3 quality                          3 cases · 6 assertions · iteration #2 · best #2

▌ ITERATIONS                                                       best = highest with-skill rate, complete only
    ITER    WITH WITHOUT Δ w−w/o   RAN   JUDGED  WHEN
▶     #1     89%     56%     +33   3/3      6/6  2026-11-01 12:00
      #2    100%     89%     +11   3/3      6/6  2026-11-01 14:20  best

  Δ = with − without, this iteration · RAN = cases run of 3 · JUDGED = assertions judged of 6
```

A rate is a percentage and nothing beside it: `89% 5/6` used to share one cell, and it reads as
arithmetic that does not hold — a rate that averages the cases is not the pooled assertions, so the two
numbers invite a division that lies about one of them. `JUDGED` carries the completeness fact in its own
column, `RAN` the set coverage, and both column names that cannot say their own basis have it spelled
out under the table: a delta between the arms and a delta against the last iteration are different
facts. A partial grade never ranks, the same rule the trigger lab enforces, and the best is the highest
with-skill rate among **complete** iterations — ties go to the later one.

The guide's stop condition is a fact here too: when the last two complete iterations tie, the table
says so, and the lab's `next:` line says "stop, or sharpen the assertions".

**The run grades itself.** `R` runs every case on both arms and then hands the arms to the judge —
one spend, two passes — so there is no step between the outputs and the verdicts to wait for. The live
screen says which pass it is in (`OUTPUT RUN · ITERATION 4`, then `GRADING · ITERATION 4`, then
`RUN + GRADED · ITERATION 4`), and the arm cells keep what the agent did while a `judging` state beside
them names the judge's pass: the same arm is not run twice. Verdicts go into each arm's `grading.json`,
then the benchmark and the report are re-aggregated.

`g` is what is left of the separate step, and it is two recoveries in one key: an iteration whose arms
carry no verdicts — a run made with `--no-grade`, by a build older than the fold, or one whose grade
failed — is graded by it, and an iteration that already has verdicts is re-graded by it **after asking**,
because that is the one thing an assertion edit leaves behind: the verdicts on screen describe the old
list. `evalctl run --no-grade` is the same trade from the shell. A finished run or grade lands on the
dojo, because the next move after a spend is to read what failed.

### The frame

The bar carries the path — `trigger optimization > skill > step` — and the tool with its build at the
right edge. The rail sits under it, and the loop's own numbers sit at the right end of the rail's row.
The output lane draws the same rail with its three sections, `1 setup ─── 2 dojo ─── 3 quality`, and the loop's numbers at its right end.

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

### What the TUI does not do

Browsing the set, sorting and grouping columns, multi-select, delete-by-filter, renumbering, moving a
case between skills, and both promotions are **CLI verbs** (below). The TUI is the loop; the CLI is
what you reach for when you want to look at the whole set at once.

## CLI

Managing the sets:

```bash
evalctl skills                              # the set by skill: cases, assertions, fixtures, trigger rows, health
evalctl skills --json
evalctl ls --skill slide-builder            # one skill's rows, both lanes
evalctl ls --lane trigger --skill slide-builder --label no --split train
evalctl lab --skill slide-builder           # the description loop: candidates, rates, next move
evalctl ls --dupes                           # rows with a near-twin
evalctl ls --sort skill,id:desc              # any column of the lane, id:desc mixes directions
evalctl ls --group split                     # sections with a count each (sort defaults to it)
evalctl ls --search dense --in slug          # search one column only
evalctl show t12                            # refs: t12, trigger:12, o:slide-builder:3
evalctl show o:slide-builder:3              # prompt, expected_output, assertions, files, fixture
evalctl stats                               # counts per skill / label / split + dupe groups
evalctl dupes --threshold 0.6                # candidate duplicates, grouped, with refs
evalctl validate                            # what the runner would reject, plus fixture check

evalctl add --lane trigger --skill source-research --label no --split train \
            --query - --why "near-miss: extraction only, no workspace"   # "-" reads stdin
evalctl add --lane output --skill slide-builder --slug sb-4-x \
            --prompt "..." --expected "..." --files "MC-x/pages/01.md,MC-x/pages/02.md" \
            --assertions "SL.md links every page
the speaker script is prose, not bullets"
evalctl edit t12 --split validation --label yes
evalctl edit o:slide-builder:3 --slug sb-3-dense-item-renamed   # moves the fixture dir
evalctl edit o:slide-builder:3 --id 9 --yes                     # lists what cites #3, then moves it
evalctl edit o:slide-builder:3 --assertions "one page per top level item
each page cites its Data Points"                                # replaces the assertion list
evalctl mv o:slide-builder:3 --to-skill av-production           # moves case + fixture
evalctl promote --from 2/src-4-agentskills/with_skill --skill learning-path \
            --slug lp-4-agentskills --prompt "..." --expected "..."   # run output → next stage's fixture
evalctl promote --from 2/src-4-agentskills --skill learning-path --slug lp-4-x \
            --prompt "..." --dry-run                              # what would be staged, nothing written
evalctl promote --to-references --from 3/lp-4-agentskills --dry-run  # the run's own output → references/
evalctl promote --to-references --from 3/lp-4-agentskills --file LP-writing-agent-skills.md --as LP-example.md
evalctl promote --to-references --from 3/lp-4-agentskills --as iteration-3/ --force  # replace what is there
evalctl promote --to-trigger --from o:slide-builder:3             # that case's prompt as a trigger query
evalctl promote --to-trigger --from slide-builder:3 --label no --skill learning-path \
            --dry-run                                             # a ready-made negative for a sibling
evalctl rm t6 t7 t9 --yes                     # or: rm --lane trigger --dupes --yes
evalctl rm o:slide-builder:2 --with-fixture --yes
evalctl renumber --lane trigger              # closes id gaps, refuses to unless you confirm
```

Running the lanes:

```bash
evalctl run --list                          # cases + prompts
evalctl run --dry-run                       # plan + the exact hermes commands, writes nothing
evalctl run --skill source-research         # one stage, next free iteration-N
evalctl run --skill learning-path --case 1,3
evalctl run --skill slide-builder --iteration 2 --resume   # add cases to an iteration
evalctl run --arms without_skill            # one side of the comparison only
evalctl run --no-grade                      # arms only: no verdicts, so nothing enters the loop

evalctl grade --skill dummy                 # finish an iteration whose arms carry no verdicts
evalctl grade --skill dummy --iteration 2 --force   # re-grade after editing an assertion
evalctl grade --skill dummy --dry-run       # the judge commands, nothing written
evalctl lab --skill dummy --lane output     # the output loop: rates, delta, the next move

evalctl trigger --list                                    # the query set, its labels and split
evalctl trigger --split train                             # iterate here only
evalctl trigger --query 1,6 --runs 1                      # cheap probe of two queries
evalctl trigger                                           # the loop set: train + validation, 3 runs each
evalctl trigger --split holdout                           # the fresh set, measured once — never part of a run above
```

Browsing and provisioning:

```bash
evalctl runs                                # every iteration, with pass rates and report state
evalctl runs --skill slide-builder          # that skill's output runs plus the trigger runs that measured it
evalctl runs --lane trigger --json
evalctl report 3                            # report.md of iteration-3, rendered as markdown
evalctl report trigger-2                    # or of the trigger lane
evalctl report 3 --raw                      # the markdown source, exactly as written
evalctl report 3 | head -20                 # a pipe gets the source, not the ANSI
evalctl profiles                            # status table of the four profiles
evalctl profiles check                      # exits 1 if anything drifted
evalctl profiles ensure --dry-run           # what create/render/sync/pin would do
evalctl profiles ensure
evalctl profiles render                    # after editing contract.md: re-stamp every SOUL.md
evalctl profiles sync eval-vcc-with --skills source-research
evalctl profiles scrub                     # wipe sandbox memories between iterations
```

Every read command takes `--json` where it makes sense (`ls`, `show`, `validate`, `stats`,
`dupes`, `runs`, `profiles status`/`check`). `rm` without refs needs filters or `--lane` +
`--all`; nothing is deleted without a matching selection and a `--dry-run` shows the list first.

### Promoting a run's output

One stage's eval output is the next stage's input — that is the pipeline, so the tool carries it
across instead of asking you to `cp` and hand-edit JSON:

```bash
evalctl promote --from <run>/<case-slug>[/<arm>] --skill <skill> --slug <new-slug> \
                --prompt "..." [--expected "..."] [--id N] [--dry-run]
```

`--from` names one arm dir of one case of one iteration (`2/src-4-agentskills/with_skill`; the
arm defaults to `with_skill`, the run may be `2` or `iteration-2`). `--dry-run` previews the copy —
every file that would land in the fixture, and everything deliberately left behind. What lands in the new fixture is **only** the `MC-[topic]/` workspace:
the arm dir's own artifacts (`stream.jsonl`, `grading.json`, …) and `_archive/` stay behind, and so
does the target stage's own output — the glob table in `internal/stage` (`LP-*.md`/`PO-Rev*-*.md`
for learning-path, `SL-*.md`/`pages/` for slide-builder, …) is what gets dropped, because a fixture
must hold the case's inputs, never its answers. The patterns, not fixed names, are what identify a
stage's output: documents carry their workspace's topic slug (`PS-writing-agent-skills.md`).
Everything else it produced — `PS-*.md`, `sources/` — rides along, so citations resolve.

A promotion never overwrites a fixture dir and never reuses a slug: the ancestry is recorded in
the case as `promoted_from` (`run`, `case`, `arm`, `fixture_sha256`), an unknown key the format
preserves, so it survives an `edit` and shows up in `evalctl show`. `evalctl run --list` prints it,
and `meta.json` records the fixture hash each run actually staged — which is how you tell later
whether a run was graded on a good input or a bad one, and whether the fixture was edited after
the fact. `--prompt` is required (the case has nothing to derive it from); `--expected` is optional
and only draws the usual validate warning.

It writes the case file itself instead of going through an editor, because the fixture bytes are
already on disk; it therefore refuses while a run is going or while an unsaved edit is in memory (the
write would carry it along).

### A run's output as the skill's own reference

A first iteration mostly tells you what the output looks like. This promotion takes that output
back into the skill that produced it, as an example the agent reads on its next run:

```bash
evalctl promote --to-references --from <run>/<case>[/<arm>] [--skill S] \
                [--file <path>]... [--as <name|dir/>] [--all] [--workspace MC-x] \
                [--force] [--dry-run]
```

It is the mirror image of the chain above. The chain drops the producing stage's own outputs
because a fixture must hold inputs; this one carries exactly those (`stage.Outputs`: `PS-*.md` and
`sources/` for source-research, `LP-*.md` and the `PO-Rev*-*.md` files for learning-path) into
`<skill>/references/` — where `source-research/references/PS.md` already sits for the same reason,
with `SKILL.md` telling the agent to take its shape and never its content.

- `--skill` defaults to the case's own skill. `--file` takes one workspace-relative path (a dir
  recurses), `--all` the whole `MC-*/` workspace minus the runner's artifacts, and `--workspace`
  picks one when an arm wrote several.
- Each file lands named as it is in the workspace (`PS-writing-agent-skills.md`, `sources/x.md`); `--as` renames a
  single file, or names a directory — which must end in `/` when several files go in.
- Nothing is overwritten without `--force`; the replaced bytes go to `.evalctl/backups/`. Every
  promotion is recorded in `.evalctl/references.json` (path, run, case, arm, sha256, time), so an
  example can be traced back to the iteration it came from.
- The copy is the run's bytes, unadapted — that part stays yours. Promote, edit the file, then
  name it in `SKILL.md`: the tool prints the line to paste, because a reference nothing names is a
  file the agent never opens.

`--dry-run` names a taken path, and `--as` on a directory must end in `/` when several files go in.

### Promoting a prompt into the trigger lane

A case's prompt is a request a real user typed, so it is also a fair question for the other lane:
*saying this, does the right skill load?* `promote --to-trigger` lifts the text into
`trigger/trigger-evals.json` unchanged:

```bash
evalctl promote --to-trigger --from <case-ref> [--skill S] [--split train|validation] \
                [--label yes|no] [--why <text|->] [--id N] [--dry-run]
```

`--from` takes a case ref (`o:slide-builder:3`, `slide-builder:3`, or a bare `3` when only one skill owns that id), never a
run path — the prompt lives in the case file, not in a results dir. Defaults: the case's own skill
under test, `train` (a row you have not measured yet belongs where description edits may learn from
it), `should_trigger: true`, the next free trigger id, and a `why` naming the case the text came
from. Naming a *sibling* skill with `--label no` is the other useful promotion: a research request
is a real negative for slide-builder, and the `why` says so.

An identical query is refused rather than added twice — the lane measures whether the right skill
loads, and two rows of the same text measure it once. The row records `promoted_from` (`skill`,
`case`, `slug`), the same unknown key the fixture promotion writes, so `evalctl show t41` says
where it came from and the row survives any `edit` untouched.

It writes `trigger-evals.json` at once, and refuses while a run is going (the run is reading that
file).

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

## What a run writes

```
<skill>-workspace/iteration-N/meta.json                 contract + SOUL + skill-tree + fixture hashes
<skill>-workspace/iteration-N/<slug>/prompt.txt
<skill>-workspace/iteration-N/<slug>/judge-prompt.txt    what the judge was asked, verbatim
<skill>-workspace/iteration-N/<slug>/<arm>/             the arm dir
  outputs/                                          the run root: fixture staged, then what it wrote
  stream.jsonl activity.log timing.json changes.json grading.json stderr.txt
  judge-stream.jsonl judge-stderr.txt                    the grading call, after `grade`
<skill>-workspace/iteration-N/benchmark.json report.md feedback.json

trigger-workspace/trigger-N/meta.json                   query-file hash + every description verbatim
trigger-workspace/trigger-N/q01-<skill>/query.txt
trigger-workspace/trigger-N/q01-<skill>/run-1/stream.jsonl activity.log trigger.json stderr.txt
trigger-workspace/trigger-N/trigger.json                per-run records + the score
trigger-workspace/trigger-N/report.md

trigger/descriptions/<skill>.json                       the loop's candidates, their notes, the applied one
```

The ledger is the only description state the tool owns, and it is committed with the set for the same
reason a query carries a `why`: a wording nobody can explain is one the next change undoes. Its
`applied.sha256` is what makes drift detectable — if `SKILL.md` no longer hashes to it, the file is
ahead of the last measurement.

`grading.json` carries the mechanical checks (containment, root guard, the skills the run actually
loaded, the skill's own `verify` output), the final response, and — once `evalctl grade` has run —
one verdict per declared assertion (`text`, `passed`, `evidence`) plus the arm's summary. The judge is
a Hermes call in the baseline profile (no skill installed, so the wording under test cannot bias it)
whose prompt never names the arm; it reads `outputs/` and quotes what it saw. `benchmark.json` carries
each arm's pass rate (mean and stddev over cases) beside its time and tokens, plus the quality delta;
`report.md` adds the per-assertion verdicts and the guide's pattern analysis. `feedback.json` stays
the human's own note per case, and the lab counts a non-empty one as review done.

## The rules it enforces

Errors — the tree would be rejected or could not be staged:

- trigger `id` is a positive integer, unique; `split` ∈ {train, validation, holdout}; `query` non-empty
- trigger `skill` must be a stage skill with an `evals/evals.json`
- **each split must carry both labels** — deleting the last negative of a split is the classic
  trim accident, so it is reported as an error rather than silently saved-and-forgotten; the
  holdout is exempt, because it is a handful of fresh queries rather than a measuring instrument
- output `slug` matches `^[a-z0-9][a-z0-9-]*$` and is unique **across skills**
- output `id` unique inside its skill; `prompt` non-empty
- an assertion is a non-empty string (an empty one can never be graded, so it is an error, not a
  blank that saves)
- every entry in `files` exists inside `<skill>/evals/files/<slug>/` and cannot escape it

Warnings — runs, but the measurement is unsound: empty `expected_output`, a fixture dir that
exists while `files` is empty, an empty case file, a split far from the ~60/40 the guide asks
for, two identical query strings.

## Deliberate choices

- **Ids never shift on delete.** Results are keyed `q<id>-<skill>` and iteration reports quote
  ids, so a gap is honest. `renumber` exists, asks first, and says what it breaks. `edit --id`
  is the same bargain at one row: it lists the result dirs and reports that still cite the old
  number and asks before the number moves (`--yes` when you mean it, `--dry-run` to only look).
  `add` and `promote` warn when the id they are handed is one history still cites — a gap in the
  file is not a gap on disk — but they never refuse: a create destroys nothing.
- **Assertions are a first-class field, not a blob.** `assertions` is a list of strings on the
  case (the agentskills.io grading unit), written only when the case has any: the guide adds them
  after a first graded iteration, and `"assertions": []` in every case would claim the opposite.
  `ASR` counts them, `v` lists them, `e` edits them one per line, and a search reads them — so
  "which case checks the manifest?" is a search, not a grep. Grading them is still manual; the
  runner records no verdict.
- **Unknown JSON keys survive.** The format grows; keys this tool does not know are carried
  through a round trip untouched. Saving an unchanged file is byte-identical — there is a test for
  that.
- **Fixture dirs are separate from cases.** Deleting a case does not delete the files it staged
  unless asked, because the fixture is the expensive part.
- **Duplicate detection is word-overlap (Jaccard), not embeddings.** It catches "test the same
  intent twice with different words" on sets of this size, offline, with no model.
- **Isolation is the default.** Each arm runs in a fresh temp root (`<tmp>/work`), then the result
  is copied into its arm dir's `outputs/`. Inside the repo the baseline arm can see the project and the
  other arm's output, and then reasons about being in an ablation. `--isolate=false` exists for
  debugging only.
- **The arms run sequentially, and a run holds `.evalctl/run.lock`.** An arm writes `skills/` and
  `memories/` inside its sandbox, so two concurrent runs would grade a half-synced profile. A
  second `evalctl run` refuses instead of racing.
- **The parent grades; the child is never asked.** Containment, the production-root fingerprint,
  the load evidence and the skill's own `verify` are all read from disk and the process exit —
  a child's self-report is not evidence.
- **Two containment checks per arm**: everything written sits under `MC-*/` or `_archive/`, and the
  production masterclasses root is fingerprinted before and after every arm, so a run that writes
  outside its run root fails instead of scoring clean.
- **A launch that died is not a miss.** A trigger run that emitted no tool call is reported as
  unmeasured with its stderr and excluded from the rate.
- **Trigger runs are stopped the moment the skill loads.** A positive stops on its own skill, a
  negative on *any* stage skill (in a four-skill bundle the first load is the routing decision);
  negatives also run with a 3-turn cap. Without this, one negative query cost ~160s instead of ~5s.
- **No grading, no assertions.** `grading.json` is evidence, not a verdict.
- **A reference is shipped, not measured.** Promoting into `<skill>/references/` changes the tree
  every later run installs (and the tree hash `meta.json` records), so it takes the runner's lock
  and refuses while an arm is going — the same reason a fixture promotion does.

## Layout

```
evalctl/
  main.go
  internal/model/    JSON shapes, key order, unknown-key preservation
  internal/store/    discovery, load/save (atomic + backups), fixtures, validation, dupes, the
                     per-skill summary both faces open on
  internal/runner/   the loop: cases, staging, arms, trigger lane, scoring, reports, results
  internal/profile/  the four profiles: contract render, env, skills sync, scrub, cwd pin, check
  internal/cli/      ls, show, add, edit, rm, mv, promote, dupes, stats, validate, renumber, run,
                     trigger, runs, report, profiles
  internal/tui/      the loop: door, skill chooser, the two sections and their steps, the query form,
                     the revision, the run screen, the documents, the frame
```

## Tests

`go test ./...` covers byte-identical round trips, unknown-key preservation, fixture move on slug
change, fixture survival on delete, the split-balance rule and dupe grouping. The loop's own tests
hold the arithmetic: a candidate measured by two runs (train, then validation) is one iteration with
both halves; the best iteration is the best validation, not the last one; the gate counts iterations
and ctrl+g records that it was forced; `space` writes the label it flipped; `a` puts the winner into
`SKILL.md`; and every screen draws exactly the height of the window at three widths. A smoke test
opens a copy of the project's own tree and draws every step of every skill, so a real set with a
shape the fixtures do not have still has to render. The skill-first port adds: assertion round trips (written only when
present, a comma is content, an empty one is an error), the per-skill summary counts (including a
trigger row whose skill is not in the tree), the list opening on the aggregate row, `enter`
scoping both lanes to one skill, `esc` coming back to the list it left, the skill column dropping
when scoped, and a new case taking its skill from the scope. The column functions (CLI) add: sort keys with a stable tie-break, group
order and counts (numeric columns numerically, an empty cell last), a search scoped to one
column with a full-text fallback when the lane lacks it. The runner adds: slug and
duplicate-slug refusal, staging escape
refusal, prompt-only staging, containment, `changes.json`, the benchmark delta, the trigger pass
rule (above/below threshold, unmeasured excluded), load evidence (prose is not a load), the folded
frontmatter description, next-iteration numbering, and a **dry run of both lanes that must print
the real commands and write nothing**. The profile port adds: SOUL rendered byte-identical for both
arms and idempotent on re-render, persona preserved, tree hash blind to `evals/` and `.DS_Store`,
skills synced to exactly the wanted set, scrub never touching `SOUL.md`, and `check` naming a stale
contract or a pinned sandbox.

`EVALCTL_PARITY_ROOT=/path/to/project go test ./internal/runner -run Rescore -v` re-scores a
Python-written trigger iteration from its own `trigger.json` records and compares every verdict;
it skips when that tree has no results.

## Backups

Every write to the sets copies the previous file to `.evalctl/backups/<timestamp>--<flattened path>`
(200 per file, oldest pruned); a reference replaced with `--force` is backed up the same way. To
undo a trim, copy the backup back over the file. Runs are never backed up — they are their own
evidence, and the `*-workspace/` dirs are gitignored. `.evalctl/references.json` is the one other file the
tool keeps there: which run each promoted reference came from, and the hash of the bytes it
landed as.
