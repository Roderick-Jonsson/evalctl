# evalctl

The whole eval toolchain for this repo in one Go binary: it manages the two eval lanes, runs
them, and browses the results. No Python at runtime.

- **trigger lane** — `evals/trigger-evals.json` (does the right skill load?)
- **output lane** — `<skill>/evals/evals.json` plus the fixture dirs `<skill>/evals/files/<slug>/`

Built with Bubble Tea, Bubbles, Lip Gloss and Huh.

```bash
cd evalctl
go build -o bin/evalctl .      # or: go install .
./bin/evalctl                  # TUI, opens on the trigger lane
```

`--root` picks the tree (default: walk up for the eval tree, or `$EVALCTL_ROOT`). Running it from
inside the repo just works.

## Why it exists

The sets grew faster than they got pruned, and the loop that measured them lived in two Python
files that had to agree with the manager about slugs, splits and fixture dirs. So the rules live
in one place (`internal/store` for the sets, `internal/runner` for the loop) and the CLI and the
TUI both go through them.

The Python originals (`evals/run.py`, `evals/profiles.py`) are gone: this binary is the only
runner. `go test ./internal/runner -run Parity` still re-scores a trigger iteration recorded
before the port from its own `trigger.json` records and compares every verdict, so the port stays
checkable against what the old loop produced.

## TUI

| key | action |
|---|---|
| `tab` | switch lane (trigger queries / output cases) |
| `j k ↑ ↓` `g G` `ctrl+d ctrl+u` | move / top / bottom / half page |
| `space` | select row and advance — the trim gesture |
| `a` / `A` `esc` | select all visible / clear selection |
| `/` | search query, prompt, slug, why, skill (esc clears) |
| `f` `F` | cycle skill filter forward / back |
| `s` `l` | cycle split / label filter (trigger lane) |
| `u` | show only near-duplicate rows — the trim shortlist |
| `e` `enter` | edit the focused row |
| `n` | new row in this lane |
| `d` `x` | delete selection (or the focused row); confirmation screen, `f` toggles deleting fixture dirs too |
| `v` | full text of the focused row |
| `ctrl+v` | the validation findings, scrollable |
| `ctrl+s` | write both lanes (atomic; previous bytes in `.evalctl/backups/`) |
| `R` | **run** what is selected (or the whole lane) — form, then `enter` |
| `L` | **results**: every iteration under `evals/results`, `enter` opens its `report.md` |
| `P` | **profiles**: check the four sandboxes, `u` runs `ensure` on what fails |
| `?` | help · `q` quit (asks once if there are unsaved changes or a run is going) |

Nothing in the set files is written until `ctrl+s`. Deleting a row leaves its fixture dir alone
unless you toggle it in the confirmation screen, and every write first copies the old file to
`.evalctl/backups/`.

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
| budget | seconds per run (1200 output, 240 trigger) | same |
| split / runs / threshold / max turns | — | `all`/`train`/`validation`, N runs per query, pass threshold, `--max-turns` |

### The run table

A live table, one row per case and one mark per arm: `·` pending, `⟳` running, `✓` done, `✗` failed
(non-zero exit, or a write outside the workspace). Counters and the last eight log lines sit
underneath. `esc` goes back to the list and **the run keeps going** — the header shows `⟳ run in
progress`; `R` returns to the table, `x` cancels (finished arms are kept, `--resume` picks up),
and `o` opens the report once it is written.

## CLI

Managing the sets:

```bash
evalctl ls                                  # both lanes, aligned
evalctl ls --lane trigger --skill slide-builder --label no --split train
evalctl ls --dupes                           # rows with a near-twin
evalctl show t12                            # refs: t12, trigger:12, o:slide-builder:3
evalctl stats                               # counts per skill / label / split + dupe groups
evalctl dupes --threshold 0.6                # candidate duplicates, grouped, with refs
evalctl validate                            # what the runner would reject, plus fixture check

evalctl add --lane trigger --skill source-research --label no --split train \
            --query - --why "near-miss: extraction only, no workspace"   # "-" reads stdin
evalctl add --lane output --skill slide-builder --slug sb-4-x \
            --prompt "..." --expected "..." --files "MC-x/pages/01.md,MC-x/pages/02.md"
evalctl edit t12 --split validation --label yes
evalctl edit o:slide-builder:3 --slug sb-3-dense-item-renamed   # moves the fixture dir
evalctl mv o:slide-builder:3 --to-skill av-production           # moves case + fixture
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
evalctl report 3                            # report.md of iteration-3
evalctl report trigger-2                    # or of the trigger lane
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

## What a run writes

```
evals/results/iteration-N/meta.json                 contract + SOUL + skill-tree hashes
evals/results/iteration-N/<slug>/prompt.txt
evals/results/iteration-N/<slug>/<arm>/             the run root, fixture staged
  stream.jsonl timing.json changes.json grading.json stderr.txt
evals/results/iteration-N/benchmark.json report.md feedback.json

evals/results/trigger-N/meta.json                   query-file hash + every description verbatim
evals/results/trigger-N/q01-<skill>/query.txt
evals/results/trigger-N/q01-<skill>/run-1/stream.jsonl trigger.json stderr.txt
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
- every entry in `files` exists inside `<skill>/evals/files/<slug>/` and cannot escape it

Warnings — runs, but the measurement is unsound: empty `expected_output`, a fixture dir that
exists while `files` is empty, an empty case file, a split far from the ~60/40 the guide asks
for, two identical query strings.

## Deliberate choices

- **Ids never shift on delete.** Results are keyed `q<id>-<skill>` and iteration reports quote
  ids, so a gap is honest. `renumber` exists, asks first, and says what it breaks.
- **Unknown JSON keys survive.** The format grows (`assertions` is the documented next field);
  keys this tool does not know are carried through a round trip untouched. Saving an unchanged
  file is byte-identical — there is a test for that.
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

## Layout

```
evalctl/
  main.go
  internal/model/    JSON shapes, key order, unknown-key preservation
  internal/store/    discovery, load/save (atomic + backups), fixtures, validation, dupes
  internal/runner/   the loop: cases, staging, arms, trigger lane, scoring, reports, results
  internal/profile/  the four profiles: contract render, env, skills sync, scrub, cwd pin, check
  internal/cli/      ls, show, add, edit, rm, mv, dupes, stats, validate, renumber, run, trigger,
                     runs, report, profiles
  internal/tui/      list, editor (huh), delete confirmation, viewer, run form, run table, results
```

## Tests

`go test ./...` covers byte-identical round trips, unknown-key preservation, fixture move on slug
change, fixture survival on delete, the split-balance rule, dupe grouping, and the TUI's
select → delete → save flow. The runner adds: slug and duplicate-slug refusal, staging escape
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

Every write to the sets copies the previous file to `.evalctl/backups/<timestamp>--<path>.json` (200
per file, oldest pruned). To undo a trim, copy the backup back over the file. Runs are never
backed up — they are their own evidence, and `evals/results/` is gitignored.
