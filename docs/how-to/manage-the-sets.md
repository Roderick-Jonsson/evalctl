# Work the sets from the shell

The TUI is the loop; the CLI is what you reach for when you want the whole set at once — a table you
can sort, a search across both lanes, a bulk edit, a run started from a script. This page is the
patterns worth knowing; every flag is in the [CLI reference](../reference/cli.md).

## Survey the set

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
```

`skills` is the summary the TUI's chooser opens on — cases, assertions, fixtures, trigger rows and
health per skill — and `ls` is the same rows in one table you can filter. Both take `--json`.

## Write rows

```bash
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

A write is atomic and leaves the previous bytes in `.evalctl/backups/`; `rm` needs a matching
selection (refs, filters, or `--lane` with `--all`) and `--dry-run` prints the list first, so a
mistyped filter shows you the rows before it deletes them.

## Run the lanes

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

## Browse and provision

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

## Machine-readable output

Every read command takes `--json` where it makes sense (`ls`, `show`, `validate`, `stats`, `dupes`, `runs`, `profiles status`/`check`). `rm` without refs needs filters or `--lane` + `--all`; nothing is deleted without a matching selection and a `--dry-run` shows the list first.

Every verb answers `-h` with its own page — what it is for, the grammar, examples, and the flags that
verb actually parses — and `--help` adds the prose that would otherwise make it a wall. The layout is
a two-column list, flags grouped under headings that say what they are for, each carrying its allowed
values and its default or environment variable as a bracket. `evalctl help <verb>` is the `-h` page;
`evalctl help` alone is the index, in seven groups. `--no-input` turns off prompting everywhere: a
command that would have asked says which flag to pass instead.

## Shell completion

Shell completion is a script plus a callback: `evalctl completion bash|zsh|fish` prints the script, and the hidden `__complete` verb answers it — the command list with its one-line summaries, each verb's flags, the declared values of `--lane`/`--label`/`--split`/`--arms`/`--log`, the palette names, the `profiles` sub-verbs, and the tree's own skills for `--skill`. It reads nothing else and never fails, because an error in a completion is painted across the command line you were typing.

Exit codes are meant to be branched on — `0` done, `1` drift, `2` refused, `3` validation findings,
`130` Ctrl-C — and are listed once, in the [reference](../reference/cli.md#exit-codes).
