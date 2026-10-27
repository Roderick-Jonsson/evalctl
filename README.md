# evalctl

A CRUD manager for this repo's eval sets. It edits the two lanes and nothing else: it never
launches a run, never grades a transcript and never touches `evals/results/`.

- **trigger lane** — `evals/trigger-evals.json` (does the right skill load?)
- **output lane** — `<skill>/evals/evals.json` plus the fixture dirs `<skill>/evals/files/<slug>/`

Built with Bubble Tea, Bubbles, Lip Gloss and Huh.

```bash
cd evalctl
go build -o bin/evalctl .      # or: go install .
./bin/evalctl                  # TUI, opens on the trigger lane
```

`--root` picks the tree (default: walk up for `evals/run.py`, or `$EVALCTL_ROOT`). Running it
from inside the repo just works.

## Why it exists

The sets grow faster than they get pruned. Trimming by hand means opening two JSON files,
remembering which id is which, deleting a fixture dir in a third place, and hoping the
split still satisfies `run.py`. So the rules live in one place (`internal/store`) and both the
CLI and the TUI go through them.

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
| `?` | help · `q` quit (asks once if there are unsaved changes) |

Nothing is written until `ctrl+s`. Deleting a row leaves its fixture dir alone unless you
toggle it in the confirmation screen, and every write first copies the old file to
`.evalctl/backups/`.

## CLI

```bash
evalctl ls                                  # both lanes, aligned
evalctl ls --lane trigger --skill slide-builder --label no --split train
evalctl ls --dupes                           # rows with a near-twin
evalctl show t12                            # refs: t12, trigger:12, o:slide-builder:3
evalctl stats                               # counts per skill / label / split + dupe groups
evalctl dupes --threshold 0.6                # candidate duplicates, grouped, with refs
evalctl validate                            # what evals/run.py would reject, plus fixture check

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

Every command takes `--json` where it makes sense (`ls`, `show`, `validate`, `stats`, `dupes`).
`rm` without refs needs filters or `--lane` + `--all`; nothing is deleted without a matching
selection and a `--dry-run` shows the list first.

## The rules it enforces (all from `evals/run.py`)

Errors — the tree would be rejected or could not be staged:

- trigger `id` is a positive integer, unique; `split` ∈ {train, validation}; `query` non-empty
- trigger `skill` must be a stage skill with an `evals/evals.json`
- **each split must carry both labels** — deleting the last negative of a split is the classic
  trim accident, so it is reported as an error rather than silently saved-and-forgotten
- output `slug` matches `^[a-z0-9][a-z0-9-]*$` and is unique **across skills**
- output `id` unique inside its skill; `prompt` non-empty
- every entry in `files` exists inside `<skill>/evals/files/<slug>/`

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
- **No run, no grade, no results.** `evals/run.py` stays the only thing that launches anything.

## Layout

```
evalctl/
  main.go
  internal/model/    JSON shapes, key order, unknown-key preservation
  internal/store/    discovery, load/save (atomic + backups), fixtures, validation, dupes
  internal/cli/      ls, show, add, edit, rm, mv, dupes, stats, validate, renumber
  internal/tui/      list, editor (huh), delete confirmation, viewer
```

`go test ./...` covers byte-identical round trips, unknown-key preservation, fixture move on
slug change, fixture survival on delete, the split-balance rule, dupe grouping, and the TUI's
select → delete → save flow.

## Backups

Every write copies the previous file to `.evalctl/backups/<timestamp>--<path>.json` (200 per
file, oldest pruned). To undo a trim, copy the backup back over the file.
