# Contributing to evalctl

The tool is one binary with a dozen internal packages and a TUI that draws every screen itself;
the tests hold the arithmetic and the frame. Build it with `make`, and read the layout before
you move a file.

## The post-commit hook

`make hooks` copies `githooks/post-commit` into `.git/hooks/` (refusing to overwrite a hook it did
not write — merge by hand instead). It fires only when the commit touched Go sources, `go.mod`,
`go.sum` or the Makefile, so commits to the eval sets stay fast, and a failed rebuild only prints a
stale-binary warning — never a failed commit. It rebuilds **both** copies: the one on PATH and
`./bin/evalctl`, because both get run — refreshing only PATH is how `./bin/evalctl` ended up two
commits behind the screen someone was looking at. It is per-clone: run it once after cloning.

## The reference page is generated

`docs/reference/cli.md` is not written by hand: `make docs` renders it from the binary, by running
every verb's `--help` page through `internal/cli/reference.go` and re-laying it out as markdown, so a
flag cannot exist in the command and be missing from the page. `TestReferencePageIsCurrent` compares
the file with `ReferenceMarkdown()` and fails when they differ, which means `make test` — and so the
release workflow — refuses a stale reference. Change a flag: run `make docs`, commit both.

## Layout

```
evalctl/
  main.go
  internal/model/    JSON shapes, key order, unknown-key preservation
  internal/store/    discovery, load/save (atomic + backups), fixtures, validation, dupes, the
                     per-skill summary both faces open on
  internal/runner/   the loop: cases, staging, arms, trigger lane, scoring, reports, results
  internal/profile/  the four sandboxes: SOUL copy, env, skills sync, scrub, check, attach
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
the real commands and write nothing**. The profile port adds: the real profile's `SOUL.md` copied
byte-identically into both arms and never written back, tree hash blind to `evals/` and `.DS_Store`,
skills synced to exactly the wanted set, scrub never touching `SOUL.md`, `check` naming an arm whose
`SOUL.md` is not the contract or a sandbox that pins a working directory, and the profile layout
(`skills/<skill>`, the real profile resolved from the cwd, `.evalctl-tree`) opening as a tree.

`EVALCTL_PARITY_ROOT=/path/to/project go test ./internal/runner -run Rescore -v` re-scores a
Python-written trigger iteration from its own `trigger.json` records and compares every verdict;
it skips when that tree has no results.

## Where next

- [Why it exists](docs/explanation/why-it-exists.md)
("The docs map", "docs/README.md")
