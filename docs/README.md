# The evalctl docs

Eighteen pages, four kinds. Pick by what you are trying to do, not by where the file sits:

| kind | answers | where |
|---|---|---|
| **Tutorial** | *I am new — walk me through it.* A lesson that ends with one skill measured. | [`tutorials/`](tutorials/) |
| **How-to** | *I am trying to do X.* Steps, in the order you need them, with nothing to learn along the way. | [`how-to/`](how-to/) |
| **Reference** | *What exactly does this do?* Complete, dry, made for looking things up. | [`reference/`](reference/) |
| **Explanation** | *Why is it like this?* The reasoning behind a choice you might otherwise read as a bug. | [`explanation/`](explanation/) |

## Tutorial

- [Your first eval](tutorials/first-eval.md) — scaffold a tree, write the three files, run the trigger
  lane, run the output lane, read what each run left behind. No prior reading needed.

## How-to

Setting up:

- [Install it so `evalctl` runs from anywhere](how-to/install.md)
- [Start or adopt a tree](how-to/start-or-adopt-a-tree.md) — `init` from nothing, or `adopt` a folder
  whose skills were written for Hermes and never measured.

Working the loop:

- [Run the trigger lane: tune a description](how-to/run-the-trigger-lane.md) — the door, the skill
  chooser, the two sections, the four-quadrant iteration, and what a run shows while it spends.
- [Generalize on the validation set, then apply the winner](how-to/generalize-and-apply.md) — rank
  every measured wording, run the ones that owe a rate, pick one into `SKILL.md`.
- [Run the output lane: judge what the skill produces](how-to/run-the-output-lane.md) — cases,
  assertions, one iteration, and the trend across iterations.

Moving results:

- [Promote a run's output](how-to/promote-a-run.md) — into the next stage's fixture, into the skill's
  own `references/`, or into the trigger lane as a query.
- [Undo a bad trim: backups and renumbering](how-to/recover-and-backup.md)

## Reference

- [CLI](reference/cli.md) — every verb, grammar and flag; `--json`, help pages, completion, exit codes.
- [TUI and keys](reference/tui.md) — the frame, where the keys are, and what the TUI deliberately
  does not do.
- [The tree and its conventions](reference/tree.md) — what counts as a skill, how the walk finds one,
  and every field of `evalctl.json`.
- [Profiles](reference/profiles.md) — the real profile and the four sandboxes, `.evalctl-tree`, and the
  provisioning verbs.
- [Runs, files and rules](reference/runs.md) — what a run prints before it spends, what it writes, and
  the validation rules a tree must satisfy.
- [Environment and platforms](reference/environment.md) — environment variables, platform support, and
  the build stamp in the corner.

## Explanation

- [Why it exists](explanation/why-it-exists.md) — the two Python files this replaced, and the parity
  test that keeps the port honest.
- [The harness](explanation/harness.md) — the case format, the two lanes, the profiles, and the rules
  the harness keeps out of the skill it grades.
- [Deliberate choices](explanation/deliberate-choices.md) — ids that do not renumber, assertions as a
  field, unknown keys that survive, isolation as the default.
- [Colour](explanation/colour.md) — roles rather than colours, three palettes held to measured
  contrast, and why a terminal's own ANSI blue is not readable.

## Not in these docs

`docs/screens-tree.drawio` is the diagram of the screens, kept beside the pages that quote them. The
release pipeline lives in `.goreleaser.yaml` and `.github/workflows/release.yml`; how to work on the
tool is in [`contributing.md`](../contributing.md).
