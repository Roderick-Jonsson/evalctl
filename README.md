# evalctl

The whole eval toolchain for this repo in one Go binary: it manages the two eval lanes, runs
them, and browses the results. No Python at runtime.

- **trigger lane** — `evals/trigger-evals.json` (does the right skill load?)
- **output lane** — `<skill>/evals/evals.json` plus the fixture dirs `<skill>/evals/files/<slug>/`

Built with Bubble Tea, Bubbles, Lip Gloss and Huh, and Glamour for the reports.

```bash
cd evalctl
make build                    # stamped: commit, build time, dirty flag
go build -o bin/evalctl .     # plain build: commit comes from the Go build info
./bin/evalctl                 # TUI, opens on the skill list
./bin/evalctl tui --skill slide-builder   # straight into one skill
./bin/evalctl version         # which build this is
```

`--root` picks the tree (default: walk up for the eval tree, or `$EVALCTL_ROOT`). Running it from
inside the repo just works.

## Version

The number lives in `internal/version/version.go` and is hand-edited: bump it when the runner, the
store rules, the profiles or the case format change, because an iteration measured by a different
harness is not comparable with the last one. It is also the first thing in the artifact:

- the TUI header carries `v0.5.0` (a `+` means the binary came from a dirty tree);
- `evals/results/iteration-N/meta.json` and `trigger-N/meta.json` record `provenance.tool`
  (version, commit, dirty, `built_at`);
- both `report.md` files name the build that generated them, and a run's start line prints it.

`make build` stamps the commit, the build time and the dirty flag. A plain `go build` still names
its commit through the Go build info — `evalctl version` says which of the three sources it used
(ldflags, build info, or nothing but the declared number), so a release binary and a scratch build
never look the same. `make check` runs gofmt and vet; `make test` the suite.

## Why it exists

The set is worked on one stage skill at a time, so the tool starts there: the first screen is the
four skills with what each one owns, and everything after it is scoped to the skill you picked.

The sets grew faster than they got pruned, and the loop that measured them lived in two Python
files that had to agree with the manager about slugs, splits and fixture dirs. So the rules live
in one place (`internal/store` for the sets, `internal/runner` for the loop) and the CLI and the
TUI both go through them.

The Python originals (`evals/run.py`, `evals/profiles.py`) are gone: this binary is the only
runner. `go test ./internal/runner -run Parity` still re-scores a trigger iteration recorded
before the port from its own `trigger.json` records and compares every verdict, so the port stays
checkable against what the old loop produced.

## TUI

It opens on the skill list — one line per stage skill, the thing you actually work on:

```
   SKILL                     CASES     ASR   FIXTURE     TRIGGER  HEALTH
   · all skills                 12    0/12    10·47f    32·17/15  2 warning(s)
   av-production                 3     0/3     3·10f      10·5/5  clean
   learning-path                 2     0/2     2·18f       1·1/0  1 warning(s)
   slide-builder                 5     0/5     5·19f      11·6/5  1 warning(s)
   source-research               2     0/2         —      10·5/5  clean
```

`CASES` is the cases the skill owns, `ASR` how many of them carry assertions, `FIXTURE` the dirs
and files they stage, `TRIGGER` the rows that test its description (`total · should/should-not`),
and `HEALTH` the validation findings that belong to this skill — its own cases and its own
queries. The first row is `· all skills`, the unscoped two-lane view. Every number is counted from
the tree in memory, so an unsaved edit shows up here before it is written.

`enter` opens a skill: its output cases (on the case lane — the thing a skill is worked on for),
with its trigger queries one `tab` away and both counts in the header. Nothing else is in the
list; `esc` goes back.

| key | action |
|---|---|
| `j k ↑ ↓` `enter` | skill list: move / open a skill (`· all skills` = every skill) |
| `esc` | back to the skill list (after it has cleared a column focus, the help, or a selection) |
| `tab` | switch lane inside the scope (output cases / trigger queries) |
| `j k ↑ ↓` `g G` `ctrl+d ctrl+u` | in a lane: move / top / bottom / half page |
| `space` | select row and advance — the trim gesture |
| `a` / `A` `esc` | select all visible / clear selection |
| `/` | search query, prompt, slug, why, assertion, skill (esc clears) |
| `f` `F` | cycle skill filter forward / back (unscoped view only — inside a skill the scope is the
skill and `esc` is how you change it) |
| `s` `l` | cycle split / label filter (trigger lane) |
| `u` | show only near-duplicate rows — the trim shortlist |
| `1`–`6` | focus a column of this lane (`0` / `esc` drops it) — see columns below |
| `o` `g` | with a column focused: sort by it (asc → desc → file order) · group by it |
| `e` `enter` | edit the focused row |
| `n` | new row in this lane |
| `d` `x` | delete selection (or the focused row); confirmation screen, `f` toggles deleting fixture dirs too |
| `v` | full text of the focused row |
| `ctrl+v` | the validation findings, scrollable |
| `ctrl+s` | write both lanes (atomic; previous bytes in `.evalctl/backups/`) |
| `R` | **run** what is selected (or the whole lane) — form, then `enter` |
| `c` | **chain / reference**: promote a run's output into the next stage's fixture, or into the skill's own `references/` — form, then `enter` (output lane) |
| `t` | **to trigger**: the focused case's prompt becomes a trigger query — written at once (output lane) |
| `L` | **results**: every iteration under `evals/results`, `enter` opens its `report.md` rendered (markdown, re-wrapped to the window), `a` its activity logs |
| `P` | **profiles**: check the four sandboxes, `u` runs `ensure` on what fails |
| `?` | help · `q` quit (asks once if there are unsaved changes or a run is going) |

The header carries the scope and both lanes of it, so the state is legible without a keystroke:

```
evalctl v0.6.0  ◆ av-production  [output cases 3]  trigger queries 10
/Users/…/presentation-skill  3 of 3  clean
```

`R` in a skill's view runs that skill's lane: the selection, or every row of *that skill* — the
list on screen is the filter, so the form says exactly what it will measure.

Nothing in the set files is written until `ctrl+s`. Deleting a row leaves its fixture dir alone
unless you toggle it in the confirmation screen, and every write first copies the old file to
`.evalctl/backups/`.

### Column functions

The table is the query surface, so its columns are operable. `1`–`6` focus one (the header
lights up, the footer says what it can do); the lane decides which:

```
trigger  id · skill · label · split · dupe · text
output   id · skill · slug · assert · dupe · text
```

Scoped to one skill the `skill` column is dropped (the header already says it) and the remaining
columns renumber with it — the number keys are read off the column list on screen, never a fixed
map.

| key | column function |
|---|---|
| `o` | sort by the focused column — ascending, descending, then back to file order |
| `g` | group by the focused column — a section header with a count each, rows keep their order |
| `/` | search only the focused column (`esc` clears the search and the scope) |
| `0` `esc` | drop the focus; the sort and the grouping stay until you cycle them off |

```
── skill alpha · 2 ─────────────────────────────────────────────
▸ [ ] 1    alpha    yes  train   -    research the operator pattern …
  [ ] 2    alpha    no   train   -    paste the three numbers here …
── skill beta · 2 ──────────────────────────────────────────────
```

Sorting and grouping are the view only. The row key (`lane|skill|index`) never changes, so a
selection, a delete, a chain or a run acts on the same rows in any order — the header's filter
line (`sort=skill↑ group=split`) is the record of what you are looking at. A column the other
lane does not have (`split`, `label`) is dropped on `tab` instead of being drawn empty.

### The run form

`R` opens a form prefilled from where you are: the selected rows, otherwise the whole lane (the
list already has filters, so it is the selection UI). Nothing is ever run for you — `enter` starts,
`esc` cancels, and with **dry run** on it only prints the exact `hermes` commands.

Rows, `j k` to move and `← →`/`space` to change:

| row | output lane | trigger lane |
|---|---|---|
| target | the selection, or every case in the lane (read-out) | same |
| iteration | `0` = next free, else the number you pick | same |
| arms | `both` / `with_skill only` / `without_skill only` | — |
| resume / force | skip finished arms; force reruns them | skip finished queries |
| verify | run the skill's own `scripts/ verify` per workspace | — |
| isolate | temp run root, result copied back (on by default) | n/a — always a temp root |
| dry run | print the commands, write nothing | same |
| model | `-m` for `hermes chat` | same |
| activity | `full` / `tools` / `off` — how much of the agent's stream is logged | same |
| budget | seconds per run (1200 output, 240 trigger) | same |
| split / runs / threshold / max turns | — | `all`/`train`/`validation`, N runs per query, pass threshold, `--max-turns` |

### The run table

A live table, one row per case and one mark per arm: `·` pending, `⟳` running, `✓` done, `✗` failed
(non-zero exit, or a write outside the workspace). Counters and the last eight lines of the
running arm's activity sit underneath. `esc` goes back to the list and **the run keeps going** —
the header shows `⟳ run in progress`; `R` returns to the table, `x` cancels (finished arms are
kept, `--resume` picks up), `v` opens the arm's full `activity.log` (it keeps following while the
arm runs), and `o` opens the report once it is written.

### Live activity

An arm is opaque for as long as it takes — a twenty-minute run used to show one `⟳` and nothing
else. The child already writes one JSON frame per event to `stream.jsonl`, so the runner tails
that file every 250 ms and folds each frame into a line:

```
+  12.4s  │ I'll start by checking the workspace and loading the skill.
+  13.1s  ▸ skill_view  slide-builder
+  13.1s  ✓ skill_view  0.0s
+  15.8s  ▸ terminal  go run . verify --root MC-kubernetes-operators
+  18.2s  ✓ terminal  2.4s
```

`▸` is a tool call with the one input that matters (the command, the path, the query), `✓`/`✗` its
result and cost, `│` the agent's own streamed prose — coalesced, because the stream is one frame
per token. Every line carries `+seconds` since the arm started.

The same lines are written to `activity.log` next to `stream.jsonl`, so a finished run can be read
back: `L` → `a` shows every arm's log in one pane. The file is the evidence and the log is the
narrative; nothing about how the arm launches changes, and a log that cannot be written never
fails an arm.

`activity` in the run form (or `--log full|tools|off`) decides how much gets through: `full` adds
the prose, `tools` keeps only the calls, `off` writes no log and emits nothing.

## CLI

Managing the sets:

```bash
evalctl skills                              # the set by skill: cases, assertions, fixtures, trigger rows, health
evalctl skills --json
evalctl ls --skill slide-builder            # one skill's rows, both lanes
evalctl ls --lane trigger --skill slide-builder --label no --split train
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
evalctl trigger                                           # full set, 3 runs per query
```

Browsing and provisioning:

```bash
evalctl runs                                # every iteration, with pass rates and report state
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
arm defaults to `with_skill`, the run may be `2` or `iteration-2`). In the TUI it is `c`: the form
opens on the newest output iteration with the focused case already picked and the *next* stage as
the target, and it previews the copy — every file that would land in the fixture, and everything
deliberately left behind. What lands in the new fixture is **only** the `MC-[topic]/` workspace:
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

The TUI form is the same operation, not a second implementation: it fills a `PromoteRequest` and
the store does the rest. One difference from every other edit — it writes the case file itself
instead of waiting for `ctrl+s`, because the fixture bytes are already on disk; it therefore
refuses to run while there are unsaved edits in memory.

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

In the TUI it is the same form: `c`, then `destination` (`←`/`→`) from *next stage's fixture* to
*this skill's references/*. The case rows (slug, id, prompt) give way to `file`, `as` and
`workspace`, and the preview names a taken path before `enter` is pressed.

### Promoting a prompt into the trigger lane

A case's prompt is a request a real user typed, so it is also a fair question for the other lane:
*saying this, does the right skill load?* `promote --to-trigger` lifts the text into
`evals/trigger-evals.json` unchanged:

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

In the TUI it is `t` on an output case: no form, since the row is entirely derived; it writes
`trigger-evals.json` at once and refuses while a run is going (the run is reading that file) or
edits are unsaved (the write would carry them along).

## What a run writes

```
evals/results/iteration-N/meta.json                 contract + SOUL + skill-tree + fixture hashes
evals/results/iteration-N/<slug>/prompt.txt
evals/results/iteration-N/<slug>/<arm>/             the run root, fixture staged
  stream.jsonl activity.log timing.json changes.json grading.json stderr.txt
evals/results/iteration-N/benchmark.json report.md feedback.json

evals/results/trigger-N/meta.json                   query-file hash + every description verbatim
evals/results/trigger-N/q01-<skill>/query.txt
evals/results/trigger-N/q01-<skill>/run-1/stream.jsonl activity.log trigger.json stderr.txt
evals/results/trigger-N/trigger.json                per-run records + the score
evals/results/trigger-N/report.md
```

`grading.json` carries the mechanical checks (containment, root guard, the skills the run actually
loaded, the skill's own `verify` output) and the final response. Grading itself stays manual until
`assertions` exist: read `expected_output` next to what each arm produced and write the verdict into
`feedback.json`.

## The rules it enforces

Errors — the tree would be rejected or could not be staged:

- trigger `id` is a positive integer, unique; `split` ∈ {train, validation}; `query` non-empty
- trigger `skill` must be a stage skill with an `evals/evals.json`
- **each split must carry both labels** — deleting the last negative of a split is the classic
  trim accident, so it is reported as an error rather than silently saved-and-forgotten
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
  ids, so a gap is honest. `renumber` exists, asks first, and says what it breaks.
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
  is copied into `evals/results/...`. Inside the repo the baseline arm can see the project and the
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
  internal/tui/      skill list, scoped list, editor (huh), delete confirmation, viewer, run form,
                     run table, results
```

## Tests

`go test ./...` covers byte-identical round trips, unknown-key preservation, fixture move on slug
change, fixture survival on delete, the split-balance rule, dupe grouping, and the TUI's
select → delete → save flow. The skill-first port adds: assertion round trips (written only when
present, a comma is content, an empty one is an error), the per-skill summary counts (including a
trigger row whose skill is not in the tree), the list opening on the aggregate row, `enter`
scoping both lanes to one skill, `esc` coming back to the list it left, the skill column dropping
when scoped, and a new case taking its skill from the scope. The column functions add: sort keys with a stable tie-break, group
order and counts (numeric columns numerically, an empty cell last), a search scoped to one
column with a full-text fallback when the lane lacks it, and in the TUI that a number key, `o`,
`g` and `/` move rows without losing the cursor or the selection. The runner adds: slug and
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
evidence, and `evals/results/` is gitignored. `.evalctl/references.json` is the one other file the
tool keeps there: which run each promoted reference came from, and the hash of the bytes it
landed as.
