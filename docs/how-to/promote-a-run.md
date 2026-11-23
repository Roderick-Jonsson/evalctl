# Promote a run's output

One stage's eval output is the next stage's input, and a good output is also the example the
skill should read next time. Three promotions, one verb, three modes.

## Into the next stage's fixture

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

## A run's output as the skill's own reference

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

## Promoting a prompt into the trigger lane

A case's prompt is a request a real user typed, so it is also a fair question for the other lane:
*saying this, does the right skill load?* `promote --to-trigger` lifts the text into the target
skill's `evals/trigger-evals.json` unchanged:

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

## Where next

- [The `promote` grammar, flag by flag](../reference/cli.md#evalctl-promote)
- [Fixtures: what a case stages](../reference/runs.md)
