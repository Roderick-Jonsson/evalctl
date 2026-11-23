# Runs: what one prints, writes, and must satisfy

A run is the expensive part, so it says what it is about to do before it spends, leaves the
evidence on disk, and refuses a tree that could not be staged.

## What a run says before it spends

Every non-dry run prints four things first, because each one used to be a silent assumption:

```
hermes Hermes Agent v0.23.1 … · evalctl v0.12.0 · /path/to/hermes
contract: SOUL.md of profile notes (evalctl.json) · arms sign in with env OLLAMA_API_KEY
```

- **the hermes it drives** — `hermes chat --help` is asked for the flags evalctl launches with
  (`--oneshot`, `--query-file`, `--format`, `--in`, `--yolo`, `--max-turns`); a missing one is a
  refusal that names it, not an iteration of zeros;
- **which profile's SOUL.md is the contract, and why** — `$EVALCTL_PROFILE`, the profile you are
  standing in, the tree's `evalctl.json`, the tree's own directory, or Hermes's default. The default
  is the one that used to be a guess, so it is called out as one;
- **what the eval sandboxes sign in with** — every `.env` key the profile carries, and every login
  file a browser sign-in left behind, which are copied into the sandbox byte for byte: the profile's
  own (`google_token.json`, `*_credentials.json`, `*_token.json`, `*_client_secret.json`) and the
  Hermes root's shared `auth.json`, which holds the provider credential pool. No credentials at all
  is a refusal, not a run;
- **nothing readable back** — an arm that emits no tool call and no `result` frame is an error with
  the harness's stderr under it; a credential failure is named as one. Neither is scored as a zero.

## What a run writes

```
<skill>-workspace/iteration-N/meta.json                 SOUL (the contract) + per-arm skill tree + fixture hashes
<skill>-workspace/iteration-N/<slug>/prompt.txt
<skill>-workspace/iteration-N/<slug>/judge-prompt.txt    what the judge was asked, verbatim
<skill>-workspace/iteration-N/<slug>/<arm>/             the arm dir
  outputs/                                          the run root: fixture staged, then what it wrote
  stream.jsonl activity.log timing.json changes.json grading.json stderr.txt
  judge-stream.jsonl judge-stderr.txt                    the grading call, after `grade`
<skill>-workspace/iteration-N/benchmark.json report.md feedback.json

<skill>-workspace/trigger-N/meta.json                   the skill's query-file hash + EVERY installed
                                                    skill's description verbatim
<skill>-workspace/trigger-N/q01-<skill>/query.txt
<skill>-workspace/trigger-N/q01-<skill>/run-1/stream.jsonl activity.log trigger.json stderr.txt
<skill>-workspace/trigger-N/trigger.json                per-run records + the score
<skill>-workspace/trigger-N/report.md

<skill>-workspace/pending-description.json              the one staged draft, when there is one
```

There is no description ledger. The wordings a skill has been through are its trigger iterations —
each one records the text it ran and the rate it produced — so the loop reads them instead of keeping
a second copy that could drift. Drift is derived the same way: if no iteration recorded the text
`SKILL.md` carries now, that wording has not been measured. What survives from the old ledger is the
one thing a run cannot supply: a draft waiting for its run, staged in the workspace. When a wording is
applied, the numbers that earned it are printed as a commit message, so git keeps what a pruned
workspace may lose.

`grading.json` carries the mechanical checks (containment, root guard, the skills the run actually
loaded, the skill's own `verify` output), the final response, and — once `evalctl grade` has run —
one verdict per declared assertion (`text`, `passed`, `evidence`) plus the arm's summary. The judge is
a Hermes call in the baseline profile (no skill installed, so the wording under test cannot bias it)
whose prompt never names the arm; it reads `outputs/` and quotes what it saw. `benchmark.json` carries
each arm's pass rate (mean and stddev over cases) beside its time and tokens, plus the quality delta;
`report.md` adds the per-assertion verdicts and the guide's pattern analysis. `feedback.json` stays
the human's own note per case, and the lab counts a non-empty one as review done.

## The rules it enforces

Errors — the tree would be rejected or could not be staged:

- trigger `id` is a positive integer, unique; `split` ∈ {train, validation, holdout}; `query` non-empty
- trigger `skill` must be a skill in the tree with an `evals/evals.json`
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

## Where next

- [The case format and the harness rules](../explanation/harness.md)
- [Recovering a bad write](../how-to/recover-and-backup.md)
