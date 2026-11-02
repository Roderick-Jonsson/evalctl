# The eval harness

This file documents the harness itself: the case format, the two lanes, the profiles, and where a
run writes its results. It lives with the tool, not inside a skill — a run must never be told it is
being evaluated, and a skill installed into an arm must not carry the rules it is graded by.

## Workspace structure

The layout is the one agentskills.io's *Evaluating skill output quality* specifies: a skill's cases
live in `<skill>/evals/`, and its results live in a workspace folder **beside** the skill directory.

```
source-research/
├── SKILL.md
└── evals/
    ├── evals.json
    └── files/<slug>/                 the case's fixture, mirroring a run root
source-research-workspace/            gitignored
└── iteration-N/
    ├── <slug>/
    │   ├── prompt.txt
    │   ├── with_skill/
    │   │   ├── outputs/              the tree the run produced (MC-[topic]/ …)
    │   │   ├── stream.jsonl  stderr.txt  activity.log
    │   │   └── timing.json  changes.json  grading.json
    │   └── without_skill/…
    └── benchmark.json  report.md  feedback.json  meta.json
trigger/trigger-evals.json            the one cross-skill case file
trigger-workspace/trigger-N/…         its results: no skill owns the bundle-level lane
```

Two consequences worth naming, because they are rules, not accidents:

- **Iteration numbers are per workspace.** `learning-path@iteration-3` and
  `slide-builder@iteration-3` are different runs. A bare number still works while it is unique; once
  two workspaces hold it, `evalctl` refuses to guess and asks for `<skill>@N`. `evalctl runs` lists
  the workspace in its own column.
- **The produced tree is `outputs/`.** Everything the agent wrote is under the arm's `outputs/`
  dir; the harness's own evidence (stream, timing, changes, grading) sits beside it, and the judge's
  own call (`judge-stream.jsonl`, `judge-stderr.txt`) with it. `changes.json`, containment checks,
  `promote` and `references` all read `outputs/`, so "what the run made" is always exactly one
  directory.

One `evals/evals.json` per stage skill, in the agentskills.io format: every case is
`id`, `slug`, `prompt`, `expected_output`, `files`, and — once a run has shown what good looks
like — `assertions`. `slug` is the case's identity and names
its fixture dir `<skill>/evals/files/<slug>/`; it is declared, never derived, so `files` can
be relative to it and the two can never disagree. `id` is the other half: a per-skill counter
(`max+1`) that the system issues, which reports and trigger result dirs then cite, so an id edit
asks first and names the artifacts it would strand — and a create handed a number that history
still cites says so without refusing. `files` may be empty — a case that stages
nothing (a pure prompt, e.g. web research the skill must fetch itself) keeps `files: []` and
has no dir on disk. The key itself always stays present so the standard's pre-flight check
passes.

`assertions` is a list of strings: one verifiable statement each ("the manifest lists one entry
per slide"), graded PASS or FAIL with evidence. The key is written only when the case has any —
per the guide they are added after the first graded iteration, so a case without them is a case
whose shape is not known yet, not a case with an empty list. `evalctl` treats them as part of the
format rather than a stray key: `evalctl skills` counts them per skill (`ASR 2/3`), the case view
lists them, `evalctl edit … --assertions` replaces them (one per line), and a search reads them.
The output workbench shows the whole list with both arms' verdicts and writes it in place: `e`
opens it as one field under the case table, `ctrl+s` saves, `esc` discards — the same splitter as
the CLI, so a list typed in the TUI and one typed in a shell land in the file identically.
`evalctl grade` has the judge grade them and writes one verdict per assertion into each arm's
`grading.json` (see *Grading and iterating*). A run alone leaves `assertion_results` empty and says
in its note that the arm is ungraded.

> The four stage skills are still drafts. The runner, the profiles and the cases are ready; the
> first real iteration should wait until a skill is out of draft, otherwise the delta measures a
> sketch.

## The harness rules (deliberately NOT in the contract)

A run must never be told it is being evaluated, so none of this lives in `contract.md` /
`SOUL.md` — it lives here:

- An eval run gets an explicit run root; the run root contains the normal workspace structure,
  unmodified, and the run never touches any other root.
- A single stage is tested by pre-seeding its expected inputs (e.g. PS.md) into the run root —
  artifact presence is the staging mechanism.
- The previous stage's *real* output can be seeded instead of a hand-made artifact, and
  `evalctl promote` is that copy: it takes one arm dir out of a skill's workspace, keeps the
  `MC-[topic]/` workspace, drops the arm dir's own runner artifacts and drops the target stage's
  own outputs (the table in `internal/stage`), so the fixture holds inputs, never answers. It
  always writes a **new** slug — a promotion that overwrote `lp-1-outline` would void every earlier
  learning-path iteration and lose the hand-made reference input.
- Isolation is by convention plus verification: the harness trusts files on disk, never a child's
  self-report, and writes the report itself.
- Trigger evals (does the right skill load?) never touch workspaces.

## The trigger lane (descriptions)

`trigger/trigger-evals.json` holds the second eval set: realistic user prompts labelled with the
skill they are testing and whether that skill's `SKILL.md` should load. It exists because a skill
that never triggers is a dead file — the `description` in frontmatter carries the entire burden of
activation, and it is only improvable against measurements (agentskills.io, *Optimizing skill
descriptions*).

Every row is `id`, `skill`, `query`, `should_trigger`, `split`, and an optional `why` (the reason
the row is in the set, for whoever reads a failure). The label is about **one** skill: a near-miss
that should fire the *next* stage is a legitimate should-not-trigger row for this one, and with all
four skills installed the run's loaded set shows exactly that.

- **Runs.** Model behavior is nondeterministic, so every query runs N times (file default 3) and the
  fraction of runs that opened the skill is its trigger rate.
- **Pass rule.** Should-trigger passes when its rate is above the threshold (file default 0.5);
  should-not-trigger passes when below it. Nothing else counts — an agent that *says* it is using a
  skill without opening it has not triggered it, and prose is never evidence.
- **Splits.** `train` guides description edits, `validation` only judges them. ~60/40, both halves
  carrying both labels, ids and split fixed forever: change them and iterations stop being
  comparable. The number to compare between iterations is the **validation** pass rate.
- **Early stop.** A should-trigger query stops the moment *its* skill is opened; a should-not-trigger
  query stops the moment *any* stage skill is opened, because in a four-skill bundle the first skill
  opened is the routing decision. Everything after that is the wrong stage doing a workspace's worth
  of work — it costs minutes and measures nothing. Negatives also run with a 3-turn cap (positives
  keep 8) for the same reason. Without this, one negative query took ~160s instead of ~5s.
- **No workspace.** A trigger run gets an empty temp root and stages nothing; the guide's rule is
  that trigger evals never touch workspaces, and here they cannot.
- **Not a trigger.** A launch that dies before its first tool call (bad CLI flag, missing key) emits
  only the init and result frames. It is reported as unmeasured with its stderr and excluded from
  the rate, never scored as "did not trigger".

```bash
evalctl trigger --list                    # the set, its labels and split
evalctl trigger --split train             # iterate here only
evalctl trigger --query 1,6 --runs 1      # cheap probe
evalctl trigger                           # full set × 3 runs
```

`trigger-workspace/trigger-N/report.md` carries the pass rate overall / train / validation, a per-skill
table, the descriptions under test with char counts and the guide's phrasing advisories, every
failure with the loaded set it produced, and the full query table. `trigger.json` keeps the raw
per-run records; `meta.json` hashes the query file and records each description verbatim, so an
iteration is reproducible after the descriptions change. A filtered or resumed invocation still
scores every query that has data on disk in that iteration, so `--query`/`--split` slices accumulate
into one comparable report instead of overwriting it with a fragment.

A description edit does not touch `contract.md` (the contract is stage rules, not trigger text), so
`evalctl profiles render` is not part of this loop — a description edit does not touch the
contract — but re-sync the skills, which the lane does itself before the first run.

## Profiles

Four profiles, all rendered from the one contract. `evalctl profiles check` is the
authority on their state: it fails when a `SOUL.md` holds a stale contract, when the two arms'
`SOUL.md` differ, when `eval-vcc-without` holds any skill, when an eval sandbox kept memories,
or when a sandbox pins a working directory (only the real profile pins one).

| Profile | Skills | Notes |
|---|---|---|
| `video-course-creator` | all four | the real one; `--no-skills` plus the four stage skills, no gateway of its own; working directory pinned to the production root (`terminal.cwd`) |
| `eval-vcc-with` | exactly the skill under test (re-synced per case) | with-skill arm |
| `eval-vcc-without` | none | baseline arm |
| `eval-vcc-trigger` | all four | trigger lane — one skill installed cannot test misselection |

Why separate profiles instead of one: a same-profile baseline is dishonest. Every session's system
prompt lists the profile's skills and carries that profile's memories, so a "without" run inside
the with-profile still sees the skill and still inherits whatever the agent wrote there. The
baseline has to inherit the *contract* though, or the delta measures the contract instead of the
procedure — which is why both `SOUL.md` files are rendered from the same file and hashed into
`meta.json`.

`eval-vcc-with` carries one skill at a time, so a case can only ever be helped by the skill it
tests. Skills are installed without `evals/`, `.git` and `.DS_Store`: the installed skill must run,
not test.

## Cases

| Skill | # | Tests |
|---|---|---|
| source-research | 1 | new Subject Matter from raw snapshots: workspace, `sources/`, PS.md with Citations + Sub-Topic labels |
| source-research | 2 | boundary: same topic, fresh start → archive to `_archive/YYYY-MM--MC-[topic]/`, then recreate |
| source-research | 3 | boundary: same subject area, different goal → qualified slug, existing workspace untouched |
| source-research | 5 | descriptors: new Subject Matter → `PS-<topic>.md` opening with `Subject Matter` + `Type: Primary Sources`, never a bare `PS.md` |
| learning-path | 1 | PS.md → LP.md + PO-RevA.md + PO-RevB.md, Subject line carried through |
| learning-path | 2 | boundary: notes only, no PS.md → must point to source-research, build nothing |
| learning-path | 3 | sorting unit is Sub-Topic, not Source: source-grouped PS.md must be re-sequenced pedagogically |
| learning-path | 5 | descriptors: `LP-<topic>.md` + `PO-RevA-<topic>.md` + `PO-RevB-<topic>.md`, each opening with `Subject Matter` then its `Type` |
| slide-builder | 1 | PO-RevB.md → SL.md + pages/ with unedited content, Citations, prose speaker scripts |
| slide-builder | 2 | boundary: PS.md only, no PO-RevB.md → must point to learning-path, build nothing |
| slide-builder | 3 | style edge: one dense outline item → visual anchor, content preserved, detail in the script |
| slide-builder | 5 | descriptors: `SL-<topic>.md` whose Slidev headmatter carries `subjectMatter` and `type: Slide Layout` |
| av-production | 1 | two scripted slides → AV/ images, audio, manifest whose durations match, video = manifest sum |
| av-production | 2 | re-render rule: one reworded script touches only that slide's outputs |
| av-production | 3 | boundary: a page without a speaker script → no invented narration, missing script reported |

## Fixtures

`<skill>/evals/files/<slug>/` mirrors a run root (the workspace folder as the agent should
find it), where `<slug>` is the case's declared `slug`. Its contents are copied into an empty
run root before a run; each `files` entry is a path **inside** that dir (`raw/page.md`, not
`evals/files/<slug>/raw/page.md`), so the slug is the only locator and an entry that points
outside the fixture dir is refused. Prompts say "here" / "pages/" / "raw/" because the run is
launched with that run root as its working directory.
A case with `files: []` stages nothing at all: its run root starts empty and no dir needs to
exist on disk (so a pure-prompt case never needs a placeholder file).
Results are keyed by the same slug: `<skill>-workspace/iteration-N/<slug>/<arm>/`, which makes the
slug unique per iteration — the runner refuses a duplicate. A run that selects several stages writes
one iteration into each skill's workspace, so an iteration never mixes two stages' cases.

## Running — iteration N

`evalctl` does the loop (`internal/runner`): provision the arms, stage the fixture, run both arms in
isolation, capture timing and mechanical checks, aggregate, report. `internal/profile` owns the
profiles. There is no second runner: the Python originals (`run.py`, `profiles.py`) are gone, and
their rules now live only in `evalctl`.

```bash
evalctl profiles check                      # profiles in their intended state
evalctl profiles ensure                     # create/render/sync/pin whatever is missing
evalctl run --list                          # cases + prompts (+ where a fixture was promoted from)
evalctl run --dry-run                       # plan + exact hermes commands, writes nothing
evalctl run --skill source-research         # one stage, next free iteration-N
evalctl run --skill learning-path --case 1,3
evalctl run --skill slide-builder --iteration 2 --resume    # add cases to an iteration
evalctl grade --skill source-research       # judge the newest iteration (one call per arm)
evalctl run --skill source-research --grade # run, then grade in one command
evalctl lab --skill source-research --lane output   # iterations, rates, delta, the next move
evalctl runs                                # iterations on disk, with pass rates
evalctl runs --skill slide-builder          # that skill's output runs + the trigger runs that measured it
```

Chaining a stage onto a real upstream output — one command, then the run above:

```bash
evalctl promote --from source-research@2/src-4-agentskills/with_skill --skill learning-path \
                --slug lp-4-agentskills --prompt "the MC-writing-agent-skills workspace here has the PS.md …" \
                --expected "LP.md, PO-RevA.md, PO-RevB.md"
```

`--from` is `<run>/<case-slug>[/<arm>]` with `<run>` written `source-research@2` (or `2` while it is
unique), arm defaulting to `with_skill`; `--dry-run` prints what
would be staged and drops nothing, and the TUI's `c` opens the same thing as a form with the copy
previewed before you press enter. The case keeps `promoted_from` (`run`, `case`, `arm`,
`fixture_sha256`) in its JSON, and `meta.json` records the fixture hash the run actually staged —
so a fixture edited after its promotion is visible instead of silently changing what a later
iteration measured.

Per arm it writes `stream.jsonl`, `stderr.txt`, `timing.json`, `changes.json` (new/modified/deleted
files vs the staged fixture), `grading.json` (mechanical checks + the skills the run actually
loaded + final response); per iteration `benchmark.json`, `report.md`, `feedback.json` (scaffold)
and `meta.json` with the contract, `SOUL.md` and skill-tree hashes.

Four things worth knowing:

- **Isolation is the default.** Each arm runs in a fresh temp run root (`<tmp>/work`), then the
  result is copied into `<skill>-workspace/iteration-N/<slug>/<arm>/`. Inside the repo the
  baseline arm can see the project and the other arm's output — it then reasons about being in an
  ablation, which wrecks the comparison. `--no-isolate` exists for debugging only.
- **The arms run sequentially**, never two processes on one profile home at a time.
- **Provisioning is part of the run.** Before each case the runner re-renders the contract, syncs
  that case's skill into `eval-vcc-with` and wipes both arms' memories, so iteration N+1 cannot
  inherit iteration N's lessons. Two runs cannot overlap: the run takes `.evalctl/run.lock`. The skill reaches the arm the way it reaches a real user:
  installed in the profile, loaded by the agent on its own (which is also what makes the trigger
  evidence meaningful).
- **Two containment checks per arm**: everything written sits under `MC-*/` or `_archive/`, and the
  production masterclasses root (the real profile's pinned working directory) is fingerprinted
  before and after the run, so a run that writes outside its run root fails instead of scoring
  clean.

Mechanical checks per arm also include the skill's own `go run -C <skill>/scripts . verify` when it
ships a `scripts/` CLI (`--no-verify` to skip). These are evidence, not assertions — declaring a
case's assertions does not make the runner grade them, so grading stays manual.

## Grading and iterating

Per arm: `grading.json` (mechanical checks, the loaded skills, the final response, and — after
grading — one verdict per assertion with its evidence plus a `summary`), then `benchmark.json` with
per-configuration pass rate / time / tokens and the delta, then `feedback.json` with your own review
notes per case. The loop: run → grade → read the failures, the delta and the transcripts → change
the skill → rerun into `iteration-<N+1>/` → grade → compare; stop when it is satisfactory or flat.

**Grading.** `evalctl grade --skill S [--iteration N]` runs one judge call per arm of the iteration
(`--iteration` defaults to the newest; `--force` re-grades arms that already have verdicts). The
judge is a Hermes call in `eval-vcc-without` — no skill installed, no pinned cwd, memories wiped —
launched with `--in <arm dir>`, so it can read `outputs/`. Its prompt carries the case's prompt, the
expected output, the declared assertions and the arm's final reply, and it never says which arm it
is reading: the comparison is blind. It must answer with one JSON object
(`assertion_results: [{text, passed, evidence}]`); a missing verdict is a FAIL with the reason, never
a default pass, and the summary is recounted from the verdicts rather than trusted from the judge.
One failed judge call leaves that arm ungraded and says so — a guessed verdict is worse than a
missing one.

**Patterns.** `report.md` and the lab read the whole iteration's verdicts per assertion text:
passes with and without the skill (no signal — remove or replace it), fails in both arms (broken or
too hard), passes with and fails without (the skill's contribution), passes without and fails with
(a regression). The guide's stddev is over cases and only printed above one case.

Mechanical checks (files exist, Subject line, citation format, containment, manifest/audio
durations) should come from the skills' own `scripts/ verify` and small shell checks rather than an
LLM reading files; reserve LLM grading for structure, style and pedagogical quality.

the `*-workspace/` dirs are gitignored.