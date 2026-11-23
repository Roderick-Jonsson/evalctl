# CLI reference

Every verb, its grammar and its flags. The same pages answer `-h` in the terminal — this one
exists so the surface can be read top to bottom, and linked to from a sentence.

## Managing the sets

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

## Running the lanes

```bash
evalctl run --list                          # cases + prompts
evalctl run --dry-run                       # plan + the exact hermes commands, writes nothing
evalctl run --skill source-research         # one stage, next free iteration-N
evalctl run --skill learning-path --case 1,3
evalctl run --skill slide-builder --iteration 2 --resume   # add cases to an iteration
evalctl run --skill slide-builder --fill --case 3          # add case 3 to the newest open iteration
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

## Browsing and provisioning

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
evalctl profiles ensure --dry-run           # what create/env/soul/sync would do (sandboxes only)
evalctl profiles ensure                     # the four sandboxes; --real adds the real profile
evalctl profiles attach [PROFILE]          # name the tree this profile is measured from
evalctl profiles render                    # after editing the real SOUL.md: copy it to the arms
evalctl profiles sync eval-video-course-creator-with --skills source-research
evalctl profiles scrub                     # wipe sandbox memories between iterations

evalctl completion bash                    # or zsh, fish — print the script
eval "$(evalctl completion zsh)"           # install it in ~/.zshrc
```

## Output, help and exit codes

Every read command takes `--json` where it makes sense (`ls`, `show`, `validate`, `stats`,
`dupes`, `runs`, `profiles status`/`check`). `rm` without refs needs filters or `--lane` +
`--all`; nothing is deleted without a matching selection and a `--dry-run` shows the list first.

Every verb answers `-h` with its own page — what it is for, the grammar, examples, and the flags that
verb actually parses — and `--help` adds the prose that would otherwise make it a wall (what a
sandbox is, `promote`'s three modes, what the profiles link does). `evalctl help <verb>` is the `-h`
page (`evalctl help` alone is the index above, in seven groups). The page is a two-column layout, one
line per flag: flags are grouped under headings that say what they are for (`WHICH ROWS`,
`EXECUTION`, `GRADING` …), each carries its allowed values and its default or environment variable
as a bracket (`[possible values: trigger, output, both] [default: both]`, `[env: EVALCTL_ROOT]`),
the four global ones appear once at the foot rather than inside every list, headings and flag names
take their colour from the active theme, and the layout follows `COLUMNS` when the page is piped.
`--no-input` turns off prompting everywhere; a command that would have asked says which flag to pass
instead.

Shell completion is a script plus a callback: `evalctl completion bash|zsh|fish` prints the script,
and the hidden `__complete` verb answers it — the command list with its one-line summaries, each
verb's flags, the declared values of `--lane`/`--label`/`--split`/`--arms`/`--log`, the palette
names, the `profiles` sub-verbs, and the tree's own skills for `--skill`. It reads nothing else and
never fails, because an error in a completion is painted across the command line you were typing.

Exit codes are meant to be branched on: `0` done · `1` drift (`profiles check`) · `2` refused (bad
usage, unknown ref, nothing selected, a prompt with no terminal) · `3` validation findings · `130`
Ctrl-C.

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

- [The screens and keys](tui.md)
- [Output, help and exit codes](#output-help-and-exit-codes)
