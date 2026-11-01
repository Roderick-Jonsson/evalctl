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

- the TUI header's right corner carries `v0.8.0+ · 11:06` — the version, a `+` when the tool's own
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
already are.

```
 what to optimize                                                                             evalctl v0.8.0 · 11:06
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
                                     │ 
▌ OPTIMIZATIONS                      │   TRIGGER OPTIMIZATION
  ────────────────────────────────   │   ────────────────────────────────────────────────────────────────
▶ trigger optimization  built        │   built · the description loop
                                     │   A skill only helps if it gets loaded, and the description is the whole of
  output optimization  not built     │   the input — an agent decides from that one paragraph. This lane measures
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
cursor; `enter` opens the trigger loop. `output` is the second optimization the tool will grow, and the
door says plainly that it is not built rather than opening a "coming soon" screen.

### Which skill

```
 trigger optimization > choose a skill                                                        evalctl v0.8.0 · 11:06
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
 trigger optimization > slide-builder                                                   evalctl v0.8.0 · 11:06
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
                                   │
▌ SECTIONS                         │   TRAIN SET                                   0 of 5 iterations measured
  ──────────────────────────────   │   ─────────────────────────────────────────────────────────────────────
▶ iteration  0 of 5                │   ID    TRIGGER  QUERY                                 WHY
                                   │   1     yes      build me slides from this outline     core ask
  generalization  gated            │   2     no       research the operator pattern for me  near miss
                                   │
                                   │
                                   │
                                   │
                                   │
                                   │
                                   │
                                   │
                                   │
                                   │
                                   │
                                   │
                                   │
                                   │
                                   │
                                   │
                                   │   ─────────────────────────────────────────────────────────────────────
                                   │   2 rows · 1 should trigger · 1 should not · 50% of the set (the
                                   │   guide asks ~60/40)
                                   │
j k move · tab the set · enter open · esc skills                                                       ? keys
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

```
 tl  FAILED QUERY      one failing row in full, and what the guide says to do about it
 tr  TRAIN SET         the queries and the last run's verdict beside each one — read, not edited
 bl  THE DESCRIPTION   SKILL.md's wording, editable in place — the answer is written here
 br  ITERATIONS        every measured wording, and the rate it scored
```

```
 trigger optimization > slide-builder > iteration                                             evalctl v0.8.0 · 11:06
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
 iteration 1 of 5 · 1 of 2 pass (50%)
                                               │ 
  FAILED QUERY · q2                            │ ▌ TRAIN SET                                              2 queries 
  ──────────────────────────────────────────── │     ID   TRIGGER RESULT  QUERY                                   
  should not trigger · fired 0 of 3 runs       │ ▶    1   yes     pass    build me slides from this outline       
  verdict        unstable — failed to hold in  │      2   no      FAIL    research the operator pattern for me    
                 3 of 3 runs                   │ 
                                               │ 
  What to adapt                                │ 
  - The wording is ambiguous here: it fires    │ 
    on some runs and not others.               │ 
  - Make the line between yes and no           │   2 rows · 1 should trigger · 1 should not · 50% of the set
    explicit — say what makes this a yes.      │   (the guide asks ~60/40)
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
                                               │ 
  THE DESCRIPTION                   52 / 1024  │   ITERATIONS                                                1 of 5 
  Use this skill when an outline has to        │    ITER        TRAIN  CHARS  NOTE
  become slides.                               │       1          50%     52  the description in SKILL.md when t…
                                               │   4 iterations to go before generalization
                                               │ 
                                               │ 
                                               │ 
                                               │ 
                                               │ 
                                               │ 
                                               │ 
  note  the description in SKILL.md when the … │ 
                                               │ 
enter run the train set · tab the description · R run again · f the failures · esc sections              ? keys 
 the wording may learn from these rows — nothing else shapes a revision                            train set · row 1 of 2 
```

| quadrant | what it holds |
|---|---|
| **failed query** (top left) | the row under the cursor if it failed, otherwise the first failure: the ask in full, why it is in the set, what loaded instead, the verdict (too narrow, too broad, unstable) and **what to adapt** — the guide's advice for that kind of failure. It is deterministic, so the advice is checkable against the page it came from |
| **train set** (top right) | the queries the wording may learn from, as the last run left them: `RESULT` is `pass`, `FAIL`, or `—` when the candidate has not been run, and the cursor walks the rows so the failure card can follow it. It is read here and written on the screen that owns the set — the pane beside the two sections |
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

### Generalization — the validation set

```
 ✓ 1 validation set ─── ● 2 validate ─── 3 results
```

**Step 1 — the validation set.** The same table with the other job: these rows judge the wording and
are never used to write one. `e` and `n` open the same editor under the table, and its `split` field is
the way a row that turned out not to be held-back becomes a train row instead.

**Step 2 — validate.** One run per measured wording, in the order the loop wrote them:

```
 trigger optimization > slide-builder > generalization > validate                             evalctl v0.8.0 · 11:06
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
 ✓ 1 validation set ─── ● 2 validate ─── 3 results                                                 2 of 2 validated 

  Every measured wording has been validated
  Nothing is left to run against the validation set. The result list ranks the wordings by it, and there is
  where one gets applied.
  ────────────────────────────────────────────────────────────────
  iterations     2 measured wordings

  → enter  open the results



















enter open the results · esc sections                                                                        ? keys 
 validation is run once per iteration, and it is the only thing that ranks them                         step 2 of 3 
```

Each candidate is written into `SKILL.md` in turn (that is what the router reads), the validation
queries are run against it, and the next candidate follows — one batch, one progress screen. `SKILL.md`
is left holding the last candidate measured; nothing is applied until you choose one.

**Step 3 — results.** The ranking, in the draft's own table:

```
 trigger optimization > slide-builder > generalization > results                              evalctl v0.8.0 · 11:06
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
 ✓ 1 validation set ─── ✓ 2 validate ─── ● 3 results                                          2 iterations measured 

▌ RESULTS                                                                                              ranked by the validation set 
  ────────────────────────────────────────────────────────────────────────────────────────────────
  ITERATION  TRAIN         VALIDATION   GAP    CHARS   NOTE
▶ 1          1/2 50%       2/2 100%     −50%   52      the description in SKILL.md when the loop started ✓ in file
  2          2/2 100%      1/2 50%      +50%   91      named the deck, not the file


















  ────────────────────────────────────────────────────────────────────────────────────────────────
  ✓ best by validation  #1 · validation 100% · train 50%
  validation is the ranking; train beside it is how much of that rate the wording learned rather than earned
j k move · enter what it failed · a apply · h holdout · esc sections                                         ? keys 
 validation is run once per iteration, and it is the only thing that ranks them                         step 3 of 3 
```

`✓ best by validation` is the guide's selection rule computed, with a tie going to the earlier wording
— the one that got there with less text and fewer revisions. `enter` opens one iteration in full, `a`
applies it to `SKILL.md` (frontmatter only, backed up, re-read through the parser, recorded in the
ledger), and `h` then runs the fresh queries once, apart from every rate the loop tuned on.

### The gate

Five measured iterations is the guide's bar — below it a validation rate is a coin and the difference
between two wordings is noise. Generalization's run and results steps stay locked until then, and
`ctrl+g` is the stated exception: it opens them and the report says the rate is unproven. The
validation set itself can be written at any time; it is the run that waits.

### A run

Nothing is spent without a screen that says what it will cost:

```
  training run · candidate #4
  scope          10 train queries of slide-builder, 3 runs each = 30 agent calls
  budget         4m per run · profile eval-vcc-trigger
  writes         trigger-workspace/trigger-11/
  measures       candidate #4 — it is written into slide-builder/SKILL.md first
  dry run        off
  → enter  start it
```

`←`/`→` change the runs per query and `d` makes it a dry run: the exact commands, nothing written.
While it runs, one row per query carries a mark per attempt (`·` pending, `⟳` running, `✓` done, `✗`
failed) with the newest eight lines of the arm's own stream underneath — the frames `activity.log`
gets. `x` cancels and keeps what finished, `v` opens the arm's log in full, and the live screen is the
one place `esc` does not leave: a half-read table of a run that is still spending money is worse than
the wait.

### The frame

The bar carries the path — `trigger optimization > skill > step` — and the tool with its build at the
right edge. The rail sits under it, and the loop's own numbers sit at the right end of the rail's row.

**Where the keys are.** A screen here is one or more *regions* — a list, a table, a pane, a field — and
exactly one of them has the keystrokes. It says so in two places and no more: the region that has them
is drawn in the accent with a block in front of its name, every other region's name is quiet, and the
cursor mark and the selected row exist only inside that region. A second bright thing is a second
answer to the only question the frame has to answer. The same fact is spelled out at the right edge of
the status row — `sections · 1 / 2`, `train set · row 3 of 6`, `editor · q3`, `note` — which is the form
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
<skill>-workspace/iteration-N/<slug>/<arm>/             the arm dir
  outputs/                                          the run root: fixture staged, then what it wrote
  stream.jsonl activity.log timing.json changes.json grading.json stderr.txt
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
loaded, the skill's own `verify` output) and the final response. Grading itself stays manual until
`assertions` exist: read `expected_output` next to what each arm produced and write the verdict into
`feedback.json`.

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
