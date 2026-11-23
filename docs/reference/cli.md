# CLI reference

Every verb, its grammar and its flags. This page is generated from the binary — `make docs` writes
it from the same pages `evalctl <verb> -h` prints — so a flag cannot exist in the command and be
missing here, and nothing here can outlive the flag it describes.

Each verb answers `-h` with its own page and `--help` with that page plus its details; both are
below, in full. The four global flags are at the foot of this page, once, rather than in every list.

Three things are true of almost every verb, so they are stated once rather than repeated per table:
a read verb takes `--json`; every write to the sets is atomic and leaves the previous bytes in
`.evalctl/backups/`; and `--no-input` turns prompting off everywhere, with a command that would have
asked naming the flag to pass instead. Patterns rather than grammar — sorting a set, a bulk trim, a
run from a script — live in [Work the sets from the shell](../how-to/manage-the-sets.md).

## Contents

**Start here** — [`evalctl init`](#evalctl-init) · [`evalctl adopt`](#evalctl-adopt) · `evalctl` · [`evalctl help [verb]`](#evalctl-help-verb)

**Read the sets** — [`evalctl skills`](#evalctl-skills) · [`evalctl ls`](#evalctl-ls) · [`evalctl show <ref>`](#evalctl-show-ref) · [`evalctl dupes`](#evalctl-dupes) · [`evalctl stats`](#evalctl-stats)

**Write the sets** — [`evalctl add --lane L`](#evalctl-add---lane-l) · [`evalctl edit <ref>`](#evalctl-edit-ref) · [`evalctl rm <ref...>`](#evalctl-rm-ref) · [`evalctl mv <ref>`](#evalctl-mv-ref) · [`evalctl promote`](#evalctl-promote)

**Keep them honest** — [`evalctl validate`](#evalctl-validate) · [`evalctl renumber`](#evalctl-renumber)

**Measure** — [`evalctl lab --skill S`](#evalctl-lab---skill-s) · [`evalctl trigger`](#evalctl-trigger) · [`evalctl run`](#evalctl-run) · [`evalctl grade --skill S`](#evalctl-grade---skill-s) · [`evalctl revise --skill S`](#evalctl-revise---skill-s) · [`evalctl describe --skill S`](#evalctl-describe---skill-s)

**Browse** — [`evalctl runs`](#evalctl-runs) · [`evalctl report <run>`](#evalctl-report-run)

**Profiles, colour and shell** — [`evalctl profiles [cmd]`](#evalctl-profiles-cmd) · [`evalctl theme [name]`](#evalctl-theme-name) · [`evalctl completion SH`](#evalctl-completion-sh)

## Start here

### evalctl init

start a project: evalctl.json and one skill holding both eval sets

**Usage**

```bash
evalctl init [--skill NAME] [--dir DIR] [--profile P] [--force]
```

**Examples**

```bash
evalctl init --skill slide-builder        # in this folder
evalctl init --dir ~/code/evals --skill x --profile default
```

#### Flags

| flag | what it does |
|---|---|
| `--dir <DIR>` | where to create the tree [default: .] |
| `--force` | overwrite files that already exist |
| `--profile <PROFILE>` | the Hermes profile this tree is measured in (default: whoever runs it) |
| `--skill <SKILL>` | the skill to create (default: the directory's name) |

### evalctl adopt

declare a folder that already holds skills: add the sets each skill is missing

**Usage**

```bash
evalctl adopt [--dir DIR] [--skills a,b] [--profile P] [--link]
```

**Examples**

```bash
evalctl adopt --dir .                     # never replaces a file that is already there
evalctl adopt --profile default --link    # and make that profile see this folder
```

#### Flags

| flag | what it does |
|---|---|
| `--dir <DIR>` | the folder holding the skills [default: .] |
| `--link` | also create that profile if it is missing, and link it to this folder |
| `--profile <PROFILE>` | the Hermes profile this tree is measured in |
| `--skills <SKILLS>` | only these skills (default: every skill the folder holds) |

### evalctl help [verb]

manage and run a skill's eval sets: trigger queries and output cases.

## Read the sets

### evalctl skills

the set by skill: cases, fixtures, trigger rows, health

**Usage**

```bash
evalctl skills [--json]
```

**Examples**

```bash
evalctl skills
evalctl skills --json | jq '.[] | select(.errors > 0)'
```

#### Flags

| flag | what it does |
|---|---|
| `--json` | machine-readable output |

### evalctl ls

list rows from either lane, as one table

**Usage**

```bash
evalctl ls [--lane trigger|output|both] [--skill S] [flags]
```

**Examples**

```bash
evalctl ls --lane trigger --label no --split train
evalctl ls --skill slide-builder --json
evalctl ls --dupes --sort skill,id:desc
```

#### Which rows

| flag | what it does |
|---|---|
| `--lane <LANE>` | which lanes to list [possible values: trigger, output, both] [default: both] |
| `--skill <SKILL>` | only this stage skill |
| `--label <LABEL>` | trigger rows only: should the skill load? [possible values: yes, no] |
| `--split <SPLIT>` | trigger rows only: which side of the split [possible values: train, validation] |
| `--search <SEARCH>` | case-insensitive substring of query/prompt/slug/why |
| `--in <IN>` | scope --search to one column: id, skill, slug, text, why … |
| `--dupes` | only rows with a near-duplicate twin |

#### Order and shape

| flag | what it does |
|---|---|
| `--sort <SORT>` | order by a column: skill, id:desc, or skill,id:desc |
| `--group <GROUP>` | draw the table in sections by a column: skill, split, dupe … |
| `--width <WIDTH>` | wrap width for the text column [default: 120] |
| `--json` | machine-readable output |

### evalctl show <ref>

one row in full: the prompt, the expected output, the assertions, the fixture

**Usage**

```bash
evalctl show <ref> [--lane trigger|output] [--json]
```

**Examples**

```bash
evalctl show t12                          # a trigger query
evalctl show o:slide-builder:3             # an output case, by skill
```

#### Flags

| flag | what it does |
|---|---|
| `--json` | machine-readable output |
| `--lane <LANE>` | the ref's lane (needed for a bare numeric ref) [possible values: trigger, output] |

### evalctl dupes

near-duplicate rows, grouped — the shortlist for a trim

**Usage**

```bash
evalctl dupes [--lane trigger|output|both] [--threshold 0.60] [--json]
```

**Examples**

```bash
evalctl dupes
evalctl dupes --lane trigger --threshold 0.75
```

#### Flags

| flag | what it does |
|---|---|
| `--json` | machine-readable output |
| `--lane <LANE>` | which lanes to scan [possible values: trigger, output, both] [default: both] |
| `--threshold <THRESHOLD>` | word-overlap above which two rows are the same test [default: 0.6] |

### evalctl stats

counts per skill, label and split, with the duplicate totals

**Usage**

```bash
evalctl stats [--json]
```

**Examples**

```bash
evalctl stats
evalctl stats --json | jq .trigger.by_skill
```

#### Flags

| flag | what it does |
|---|---|
| `--json` | machine-readable output |

## Write the sets

### evalctl add --lane L

create a row in either lane

**Usage**

```bash
evalctl add --lane trigger --skill S --query <text|-> --label yes|no --split S [flags]
evalctl add --lane output --skill S --slug NAME [flags]
```

**Examples**

```bash
evalctl add --lane trigger --skill source-research --label no --split train --query -
evalctl add --lane output --skill slide-builder --slug sb-4-x --prompt "..." --expected "..."
```

#### The row

| flag | what it does |
|---|---|
| `--lane <LANE>` | which lane the row belongs to (required) [possible values: trigger, output] |
| `--skill <SKILL>` | stage skill under test (required) |
| `--id <ID>` | row id (default: next free id) |

#### Trigger lane

| flag | what it does |
|---|---|
| `--query <QUERY>` | trigger: the user prompt being tested (- for stdin) |
| `--why <WHY>` | trigger: why the row exists (read when it fails) |
| `--label <LABEL>` | trigger: should the skill load? [possible values: yes, no] |
| `--split <SPLIT>` | trigger: which side of the split [possible values: train, validation] |

#### Output lane

| flag | what it does |
|---|---|
| `--slug <SLUG>` | output: case slug, also its fixture dir name |
| `--prompt <PROMPT>` | output: the case prompt (- for stdin) |
| `--expected <EXPECTED>` | output: expected_output (- for stdin) |
| `--files <FILES>` | output: comma-separated fixture files, relative to the slug dir |
| `--assertions <ASSERTIONS>` | output: newline- or ;-separated assertions, the grading list |

### evalctl edit <ref>

change only the flags you pass

**Usage**

```bash
evalctl edit <ref> [--field value ...] [--id N [--yes]] [--dry-run]
```

**Examples**

```bash
evalctl edit t12 --split validation
evalctl edit o:slide-builder:3 --slug sb-3-dense-item-renamed   # moves the fixture dir
evalctl edit o:slide-builder:3 --id 9                            # says what cites #3 first
```

#### The row

| flag | what it does |
|---|---|
| `--lane <LANE>` | the ref's lane (needed for a bare numeric ref) [possible values: trigger, output] |
| `--id <ID>` | new id (it is cited by earlier iterations, so this change asks first) |
| `--skill <SKILL>` | new skill |

#### Trigger fields

| flag | what it does |
|---|---|
| `--query <QUERY>` | trigger: new query (- for stdin) |
| `--why <WHY>` | trigger: new why |
| `--label <LABEL>` | trigger: should the skill load? [possible values: yes, no] |
| `--split <SPLIT>` | trigger: new split [possible values: train, validation] |

#### Output fields

| flag | what it does |
|---|---|
| `--prompt <PROMPT>` | output: new prompt (- for stdin) |
| `--expected <EXPECTED>` | output: new expected_output (- for stdin) |
| `--files <FILES>` | output: new fixture file list, comma-separated ('' to clear) |
| `--assertions <ASSERTIONS>` | output: new assertion list, one per line or ';'-separated ('' to clear) |
| `--slug <SLUG>` | output: new slug (moves the fixture dir) |

#### Writing

| flag | what it does |
|---|---|
| `--dry-run` | show the change without writing |
| `--yes` | with --id: move it without asking |

### evalctl rm <ref...>

delete rows by ref, by filter, or by lane

**Usage**

```bash
evalctl rm <ref...> [--with-fixture] [--dry-run] [--yes]
evalctl rm [--lane L] [--skill S] [--label yes|no] [--split …] [--search Q] [--dupes]
evalctl rm --all --lane trigger|output
```

**Examples**

```bash
evalctl rm t6 t7 t9 --yes
evalctl rm --lane trigger --dupes --dry-run    # the list, nothing written
evalctl rm o:slide-builder:2 --with-fixture --yes
```

#### The selection

| flag | what it does |
|---|---|
| `--lane <LANE>` | which lane to delete from (needed for bare numeric refs) [possible values: trigger, output] |
| `--all` | bulk: every row in the lane(s) — needs --lane |
| `--skill <SKILL>` | bulk: only rows of this skill |
| `--label <LABEL>` | bulk trigger: should the skill load? [possible values: yes, no] |
| `--split <SPLIT>` | bulk trigger: which side of the split [possible values: train, validation] |
| `--search <SEARCH>` | bulk: substring filter |
| `--dupes` | bulk: only near-duplicate rows |

#### Writing

| flag | what it does |
|---|---|
| `--with-fixture` | output: also delete the fixture dirs |
| `--dry-run` | show what would go, write nothing |
| `--yes` | do not ask |

### evalctl mv <ref>

move an output case, and its fixture, to another stage skill

**Usage**

```bash
evalctl mv <ref> --to-skill S [--lane output] [--dry-run]
```

**Examples**

```bash
evalctl mv o:slide-builder:3 --to-skill av-production
```

#### Flags

| flag | what it does |
|---|---|
| `--lane <LANE>` | the ref's lane [possible values: trigger, output] |
| `--to-skill <TO_SKILL>` | destination stage skill (required) |

### evalctl promote

a run's output becomes a case, a reference, or a trigger row

**Usage**

```bash
evalctl promote --from <run>/<case>[/<arm>] --skill S --slug NAME [flags]
evalctl promote --to-references --from <run>/<case>[/<arm>] [--skill S] [flags]
evalctl promote --to-trigger --from <case-ref> [--skill S] [flags]
```

**Examples**

```bash
evalctl promote --from 2/src-4-agentskills/with_skill --skill learning-path --slug lp-4-x
evalctl promote --to-references --from 3/lp-4-agentskills --dry-run
evalctl promote --to-trigger --from o:slide-builder:3 --label no --skill learning-path
```

#### The source

| flag | what it does |
|---|---|
| `--from <FROM>` | the run to promote: <run>/<case-slug>[/<arm>], or a case ref with --to-trigger |
| `--workspace <WORKSPACE>` | --to-references: which workspace dir to take when the arm wrote several |

#### The target

| flag | what it does |
|---|---|
| `--skill <SKILL>` | stage skill the output becomes the fixture of (required); --to-references: the skill whose references/ receives it (default: the case's own); --to-trigger: the skill under test (default: the case's own) |
| `--slug <SLUG>` | new case slug, also its fixture dir (output mode, required, must be new) |
| `--id <ID>` | case id / trigger id (default: next free) |
| `--prompt <PROMPT>` | the case prompt (- for stdin) |
| `--expected <EXPECTED>` | expected_output (- for stdin) |

#### References mode

| flag | what it does |
|---|---|
| `--to-references` | promote the run's own stage output into the skill's references/ (a worked example) |
| `--file <FILE>` | --to-references: workspace-relative path to take (repeatable, comma-separated) |
| `--as <AS>` | --to-references: destination inside references/ (rename, or a dir for several files) |
| `--all` | --to-references: take the whole workspace, not just the skill's own outputs |
| `--force` | --to-references: replace an existing reference (old bytes in .evalctl/backups/) |

#### Trigger mode

| flag | what it does |
|---|---|
| `--to-trigger` | promote a case's prompt into a trigger query instead of a fixture |
| `--label <LABEL>` | --to-trigger: should the skill load? [possible values: yes, no] |
| `--split <SPLIT>` | --to-trigger: which side of the split [possible values: train, validation] |
| `--why <WHY>` | --to-trigger: why the row exists (default: names the source case) |

#### Writing

| flag | what it does |
|---|---|
| `--dry-run` | print the plan, write nothing |

**Details**

Turn a finished run's output into the next skill's fixture: the workspace dirs the tree declares are copied into <skill>/evals/files/<new-slug>/, the target skill's own outputs are left behind, and a case carrying the provenance is appended. <run> is 2, iteration-2 or source-research@2 (the skill's workspace the run lives in), <arm> defaults to with_skill.

```bash
evalctl promote --to-references --from <run>/<case>[/<arm>] [--skill S] [--file <path>]... [--as <name|dir/>] [--all] [--workspace <dir>] [--force] [--dry-run]
```

Turn a run's own output into a worked example in the skill's references/ — the files its own evalctl.json declares as its outputs. That is the file an agent reads to learn the shape of a run: promote it, adapt it by hand, and the next run sees it. --skill defaults to the case's own skill; --file narrows to one path, --all takes the workspace; --as renames (or names a dir for several files). An existing reference is never replaced without --force, and every promotion is logged in .evalctl/references.json.

```bash
evalctl promote --to-trigger --from <case-ref> [--skill S] [--split train|validation] [--label yes|no] [--why <text|->] [--id N] [--dry-run]
```

Turn a case's prompt into a trigger query: same text, the stage under test, and a why naming the case it came from. <case-ref> is o:source-research:3, source-research:3 or a bare id that only one skill uses. --skill defaults to the case's own skill; pass a sibling with --label no to make a real negative.

## Keep them honest

### evalctl validate

check the tree against the runner's rules

**Usage**

```bash
evalctl validate [--strict] [--json]
```

**Examples**

```bash
evalctl validate                          # findings, exit 3 when there are errors
evalctl validate --strict                 # warnings count as findings too
```

#### Flags

| flag | what it does |
|---|---|
| `--json` | machine-readable output |
| `--strict` | also fail on warnings |

### evalctl renumber

close id gaps; it asks first, because every report cites those numbers

**Usage**

```bash
evalctl renumber [--lane trigger|output|both] [--yes] [--dry-run]
```

**Examples**

```bash
evalctl renumber --lane trigger
```

#### Flags

| flag | what it does |
|---|---|
| `--lane <LANE>` | which lanes to renumber [possible values: trigger, output, both] [default: both] |
| `--yes` | do not ask |

## Measure

### evalctl lab --skill S

where a skill's optimization loop stands, and the guide's next move

**Usage**

```bash
evalctl lab --skill S [--lane trigger|output] [--json]
```

**Examples**

```bash
evalctl lab --skill slide-builder
evalctl lab --skill dummy --lane output --json
```

#### Flags

| flag | what it does |
|---|---|
| `--json` | machine-readable output |
| `--lane <LANE>` | which optimization the loop runs [possible values: trigger, output] [default: trigger] |
| `--skill <SKILL>` | the stage skill whose description is being optimized |

### evalctl trigger

trigger lane: does the right skill load for each query, N runs per query

**Usage**

```bash
evalctl trigger [--skill S|all] [--query IDS] [--split all|train|validation|holdout]
```

**Examples**

```bash
evalctl trigger --list
evalctl trigger --split train --runs 1     # a cheap probe of two queries
evalctl trigger                            # the loop set: train + validation
```

#### Flags

| flag | what it does |
|---|---|
| `--arms <ARMS>` | which arms to run [possible values: with_skill, without_skill] |
| `--budget <BUDGET>` | wall-clock seconds per run (default 1200 output, 240 trigger) |
| `--case <CASE>` | comma-separated case ids, default all |
| `--dry-run` | print the plan and the commands, change nothing |
| `--fill` | output lane: write into the newest iteration while it is still open, instead of starting a new number |
| `--force` | with --resume: rerun anyway |
| `--grade` | output lane: grade the new iteration with the judge after running [default: true] |
| `--isolate` | run in a temp root and copy the result back (default); --isolate=false runs inside the results dir, for debugging only |
| `--iteration <ITERATION>` | iteration number (default: next free) |
| `--keep` | keep the sandbox profiles when the run ends (they are removed by default) |
| `--lane <LANE>` | which lane to run [possible values: output, trigger] [default: output] |
| `--list` | list the selected cases and exit |
| `--log <LOG>` | live agent activity on stderr [possible values: full, tools, off] [default: full] |
| `--max-turns <MAX_TURNS>` | trigger lane: --max-turns per run (default 8) |
| `--model <MODEL>` | pass -m MODEL to hermes chat |
| `--no-grade` | output lane: run the arms and stop — no verdicts, no iteration in the loop |
| `--no-verify` | skip the skill's scripts/ verify checks |
| `--query <QUERY>` | trigger lane: comma-separated query ids, default all |
| `--resume` | skip arms/queries that already finished |
| `--runs <RUNS>` | trigger lane: runs per query (default from the file, else 3) |
| `--skill <SKILL>` | all (default) or comma-separated skill names |
| `--split <SPLIT>` | trigger lane: which side of the split [possible values: all, train, validation] [default: all] |
| `--threshold <THRESHOLD>` | trigger lane: pass threshold on the trigger rate |

**Details**

Sandboxes are created by the run that needs them and removed when it ends: one that outlives its run carries that run's caches and sessions into the next measurement. --keep holds them for inspection; "profiles reap" clears what a killed run left behind, and "profiles ensure" provisions them on purpose. ensure/sync/render/scrub leave the real profile alone unless --real is passed: those verbs write.

--log full (default) streams each arm's tool calls and prose to stderr while it runs, and writes the same lines to activity.log beside stream.jsonl; tools drops the prose, off writes nothing.

Refs: 12 --lane trigger | t12 | trigger:12 | o:slide-builder:3 | slide-builder:3. Every write to the sets is atomic and leaves a copy in .evalctl/backups/.

### evalctl run

output lane: run every selected case with and without the skill, then grade it

**Usage**

```bash
evalctl run [--skill S|all] [--case IDS] [flags]
```

**Examples**

```bash
evalctl run --list                         # the cases and their prompts, nothing runs
evalctl run --dry-run                      # the plan and the exact hermes commands
evalctl run --skill slide-builder --iteration 2 --resume
```

#### Which rows

| flag | what it does |
|---|---|
| `--skill <SKILL>` | all (default) or comma-separated skill names |
| `--case <CASE>` | comma-separated case ids, default all |
| `--query <QUERY>` | trigger lane: comma-separated query ids, default all |
| `--split <SPLIT>` | trigger lane: which side of the split [possible values: all, train, validation] [default: all] |
| `--iteration <ITERATION>` | iteration number (default: next free) |
| `--lane <LANE>` | which lane to run [possible values: output, trigger] [default: output] |

#### Execution

| flag | what it does |
|---|---|
| `--arms <ARMS>` | which arms to run [possible values: with_skill, without_skill] |
| `--model <MODEL>` | pass -m MODEL to hermes chat |
| `--budget <BUDGET>` | wall-clock seconds per run (default 1200 output, 240 trigger) |
| `--max-turns <MAX_TURNS>` | trigger lane: --max-turns per run (default 8) |
| `--runs <RUNS>` | trigger lane: runs per query (default from the file, else 3) |
| `--isolate` | run in a temp root and copy the result back (default); --isolate=false runs inside the results dir, for debugging only |
| `--no-verify` | skip the skill's scripts/ verify checks |
| `--keep` | keep the sandbox profiles when the run ends (they are removed by default) |

#### Resuming

| flag | what it does |
|---|---|
| `--resume` | skip arms/queries that already finished |
| `--force` | with --resume: rerun anyway |
| `--fill` | output lane: write into the newest iteration while it is still open, instead of starting a new number |

#### Grading

| flag | what it does |
|---|---|
| `--grade` | output lane: grade the new iteration with the judge after running [default: true] |
| `--no-grade` | output lane: run the arms and stop — no verdicts, no iteration in the loop |
| `--threshold <THRESHOLD>` | trigger lane: pass threshold on the trigger rate |

#### What it prints

| flag | what it does |
|---|---|
| `--list` | list the selected cases and exit |
| `--dry-run` | print the plan and the commands, change nothing |
| `--log <LOG>` | live agent activity on stderr [possible values: full, tools, off] [default: full] |

**Details**

Sandboxes are created by the run that needs them and removed when it ends: one that outlives its run carries that run's caches and sessions into the next measurement. --keep holds them for inspection; "profiles reap" clears what a killed run left behind, and "profiles ensure" provisions them on purpose. ensure/sync/render/scrub leave the real profile alone unless --real is passed: those verbs write.

--log full (default) streams each arm's tool calls and prose to stderr while it runs, and writes the same lines to activity.log beside stream.jsonl; tools drops the prose, off writes nothing.

Refs: 12 --lane trigger | t12 | trigger:12 | o:slide-builder:3 | slide-builder:3. Every write to the sets is atomic and leaves a copy in .evalctl/backups/.

### evalctl grade --skill S

output lane: the judge grades every arm's assertions for an iteration

**Usage**

```bash
evalctl grade --skill S [--iteration N] [--case IDS] [--force]
```

**Examples**

```bash
evalctl grade --skill dummy --iteration 2 --force
```

#### Which iteration

| flag | what it does |
|---|---|
| `--skill <SKILL>` | the stage skill whose iteration gets graded |
| `--iteration <ITERATION>` | iteration number (default: the newest on disk) |
| `--case <CASE>` | comma-separated case ids, default all in that iteration |

#### The call

| flag | what it does |
|---|---|
| `--model <MODEL>` | pass -m MODEL to the judge call |
| `--budget <BUDGET>` | wall-clock seconds per judge call (default 180) |
| `--keep` | keep the judge's sandbox profile when the pass ends (removed by default) |
| `--force` | re-grade arms that already carry verdicts |

#### Output

| flag | what it does |
|---|---|
| `--dry-run` | print the judge commands, write nothing |
| `--json` | machine-readable output |

### evalctl revise --skill S

propose a revision to one case from its verdicts (the output lane's own step)

**Usage**

```bash
evalctl revise --skill S [--case N] [--iteration N] [--apply]
```

**Examples**

```bash
evalctl revise --skill slide-builder --case 3 --dry-run
```

#### Flags

| flag | what it does |
|---|---|
| `--apply` | write the proposal into SKILL.md (backed up first) |
| `--budget <BUDGET>` | wall-clock seconds for the call (default 300) |
| `--dry-run` | print the prompt and the command, write nothing |
| `--iteration <ITERATION>` | iteration whose evidence the revision answers (default: newest) |
| `--keep` | keep the reviser's sandbox profile when the call ends (removed by default) |
| `--model <MODEL>` | pass -m MODEL to the revision call |
| `--skill <SKILL>` | the stage skill to revise |

### evalctl describe --skill S

ask a model for a new description from the train failures, and print the diff

**Usage**

```bash
evalctl describe --skill S [--candidate N] [--dry-run|--write]
```

**Examples**

```bash
evalctl describe --skill source-research --dry-run   # the prompt and the provider
evalctl describe --skill source-research --write     # the diff, written and backed up
```

#### The call

| flag | what it does |
|---|---|
| `--skill <SKILL>` | the skill whose description is rewritten |
| `--candidate <CANDIDATE>` | the measured wording to revise (default: the newest measured one) |
| `--model <MODEL>` | override the model for this call ($EVALCTL_DESCRIBE_MODEL) |
| `--budget <BUDGET>` | wall-clock seconds for the call (default 180) |

#### Output

| flag | what it does |
|---|---|
| `--dry-run` | print the prompt and the provider, write nothing |
| `--write` | record the proposal as the ledger's next candidate (SKILL.md is untouched) |
| `--json` | the proposal as JSON |

## Browse

### evalctl runs

iterations per workspace, with pass rates and report state

**Usage**

```bash
evalctl runs [--lane output|trigger] [--skill S] [--json]
```

**Examples**

```bash
evalctl runs
evalctl runs --lane trigger --json
```

#### Flags

| flag | what it does |
|---|---|
| `--json` | machine-readable output |
| `--lane <LANE>` | only one lane [possible values: output, trigger] |
| `--skill <SKILL>` | only one skill's iterations: its output runs and the trigger runs that measured it |

### evalctl report <run>

an iteration's report.md, rendered — the source when piped

**Usage**

```bash
evalctl report <run> [--lane output|trigger] [--raw] [--width N]
```

**Examples**

```bash
evalctl report 3
evalctl report trigger-2 --raw
evalctl report 3 | head -20                # a pipe gets the markdown, not the ANSI
```

#### Flags

| flag | what it does |
|---|---|
| `--lane <LANE>` | the ref's lane [possible values: output, trigger] |
| `--raw` | print the markdown source instead of rendering it |
| `--width <WIDTH>` | render at this wrap width, even into a pipe |

## Profiles, colour and shell

### evalctl profiles [cmd]

the Hermes profiles the lanes measure in: status and provisioning

**Usage**

```bash
evalctl profiles [status] [--json]
evalctl profiles check|ensure [--dry-run] [--real] [--json]
evalctl profiles sync [PROFILE] [--skills a,b] [--real]
evalctl profiles render|scrub|reap [--real] [--dry-run]
evalctl profiles attach|link|unlink [PROFILE] [--dry-run]
```

**Examples**

```bash
evalctl profiles                       # the status table
evalctl profiles check                 # exits 1 if anything drifted
evalctl profiles ensure --dry-run      # what create/env/soul/sync would do
```

#### Flags

| flag | what it does |
|---|---|
| `--dry-run` | print the plan, change nothing |
| `--json` | machine-readable output (status, check) |
| `--real` | ensure/sync/render/scrub: also write into the real profile (default: sandboxes only) |
| `--skills <SKILLS>` | sync: comma-separated skill names |

**Details**

"profiles link [PROFILE]" makes a profile scan this project folder via skills.external_dirs, so the profile you open in the desktop holds the same skills evalctl measures — by reference, with nothing copied. Eval sandboxes never get a link: they are throwaway copies with the evals stripped.

### evalctl theme [name]

the colour schemes, each with its roles' contrast on its own background

**Usage**

```bash
evalctl theme [NAME]
```

**Examples**

```bash
evalctl theme                          # every palette, with contrast ratios
evalctl theme daylight                 # a sample screen in that palette
export EVALCTL_THEME=high-contrast     # keep it
```

### evalctl completion SH

print a shell completion script: bash, zsh or fish

**Usage**

```bash
evalctl completion bash|zsh|fish
```

**Examples**

```bash
eval "$(evalctl completion bash)"        # in ~/.bashrc
eval "$(evalctl completion zsh)"         # in ~/.zshrc
evalctl completion fish > ~/.config/fish/completions/evalctl.fish
```

## Global flags

| flag | what it does |
|---|---|
| `--root <DIR>` | the eval tree, or a profile naming one (default: nearest dir holding it) [env: EVALCTL_ROOT] |
| `--theme <NAME>` | the colour scheme [possible values: auto, midnight, high-contrast, daylight] [env: EVALCTL_THEME] |
| `--no-input` | never prompt; a command that would have asked names the flag to pass  |
| `--help` | this page  |

## Notes

Sandboxes are created by the run that needs them and removed when it ends: one that outlives its run carries that run's caches and sessions into the next measurement. --keep holds them for inspection; "profiles reap" clears what a killed run left behind, and "profiles ensure" provisions them on purpose. ensure/sync/render/scrub leave the real profile alone unless --real is passed: those verbs write.

Refs: 12 --lane trigger | t12 | trigger:12 | o:slide-builder:3 | slide-builder:3. Every write to the sets is atomic and leaves a copy in .evalctl/backups/.

The rest — the profiles link, --log levels, what a sandbox is made of — is in docs/reference/cli.md, or `evalctl run --help` / `evalctl profiles --help`.

## Exit codes

0 done · 1 drift (profiles check) · 2 refused (bad usage, unknown ref, nothing selected, a prompt with no terminal) · 3 validation findings · 130 Ctrl-C

## Where next

- [Work the sets from the shell](../how-to/manage-the-sets.md) — the patterns, with the examples
  worth copying.
- [Your first eval](../tutorials/first-eval.md) — the whole loop once, from an empty folder.
- [The map of these docs](../README.md).
- `evalctl help` — the command index, in seven groups, in the terminal.
