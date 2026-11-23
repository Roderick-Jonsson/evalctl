# evalctl

**An eval runner for Hermes skills.** One Go binary that measures whether a skill earns its place —
whether the right skill loads, and whether the work comes out better with it than without.

It knows no project: the skills, the tree's conventions and the Hermes profile are read from where you
point it.

```bash
make install        # or `go install .` — a stamped binary on PATH
evalctl             # the TUI, on whatever tree you are standing in
evalctl init --skill meeting-notes --profile notes    # or scaffold one from nothing
```

## Highlights

- **Two lanes, one question each.** *Trigger* — saying this, does the right skill load? The
  description in `SKILL.md` is the whole of what decides it. *Output* — with the skill loaded, is the
  answer better? Every case runs twice, with and without, and a judge grades both arms.
- **A loop, not a dashboard.** The TUI asks one question at a time — which optimization, which skill,
  which of its two activities — then draws that activity's own screens. No tabs, no sortable table to
  manage before you can start.
- **The split is enforced, not suggested.** Iteration may learn only from the train set;
  generalization ranks wordings on the validation set, which is never used to write one. Ranking stays
  locked until five iterations have been measured, because below that a rate is a coin.
- **It refuses instead of guessing.** A missing `hermes` flag, a profile with no credentials, an arm
  that emitted no tool call — each is a named refusal or an *unmeasured* row, never an iteration of
  zeros.
- **Nothing about your project is compiled in.** Whatever a tree means by "the order of its skills" or
  "a run's output" is declared in an optional `evalctl.json`; the binary names no skill, document or
  folder.
- **Every run is evidence on disk.** Per-arm `stream.jsonl`, `grading.json`, `benchmark.json`, a
  `report.md` that names the build which produced it, and a backup of every set it writes.

## The front screen

```
 what to optimize                                                                   v0.12.0 · 11:06
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
                                    │
▌ OPTIMIZATIONS                     │   TRIGGER OPTIMIZATION
  ──────────────────────────────    │   ──────────────────────────────────────────────────────────
▶ trigger optimization  built       │   built · the description loop
                                    │   A skill only helps if it gets loaded, and the description is
  output optimization  built        │   the whole of the input — an agent decides from that one
                                    │   paragraph. This lane measures the wording, revises it on what
                                    │   the train set failed, and generalizes on a split no revision
                                    │   could read.
                                    │
                                    │   → enter  pick the skill whose wording gets tuned

enter open · q quit                                                        ? keys
```

## What you need

- **Hermes Agent** installed, with the profile you want to measure. evalctl drives
  `hermes chat --oneshot … --format stream-json` and reads the frames; it asks `hermes chat --help`
  first and refuses by name if a flag it needs is gone.
- **Go 1.26 or newer** to build it. There is no runtime to install and no daemon to run: one binary,
  macOS/Linux/Windows.
- A **profile with credentials** — an API key in its `.env`, or a login file a browser sign-in left
  behind. The sandboxes are copies; your real profile is never the one that runs.

## Docs

The documentation is split the way the work is: four kinds of page, one per intent. The full map is
[`docs/README.md`](docs/README.md).

**Learn it end to end** — [Your first eval](docs/tutorials/first-eval.md): scaffold a tree, write the
three files, run both lanes, read what each run left behind.

**Get one thing done:**

- [Install it](docs/how-to/install.md) · [Start or adopt a tree](docs/how-to/start-or-adopt-a-tree.md)
- [Run the trigger lane](docs/how-to/run-the-trigger-lane.md) ·
  [Generalize and apply the winner](docs/how-to/generalize-and-apply.md)
- [Run the output lane](docs/how-to/run-the-output-lane.md) ·
  [Promote a run's output](docs/how-to/promote-a-run.md)
- [Undo a bad trim](docs/how-to/recover-and-backup.md)

**Look something up:** [CLI](docs/reference/cli.md) · [TUI and keys](docs/reference/tui.md) ·
[The tree and its conventions](docs/reference/tree.md) · [Profiles](docs/reference/profiles.md) ·
[Runs, files and rules](docs/reference/runs.md) · [Environment and platforms](docs/reference/environment.md)

**Understand why it is like this:** [Why it exists](docs/explanation/why-it-exists.md) ·
[The harness](docs/explanation/harness.md) · [Deliberate choices](docs/explanation/deliberate-choices.md) ·
[Colour](docs/explanation/colour.md)

## Where it is

`v0.12.0`, used daily against a real tree — a course pipeline whose five stage skills feed each
other's fixtures. The screens in these docs are that project, and `slide-builder` is one of its
skills; nothing in the tool knows either name.

A tag is a release: `.github/workflows/release.yml` runs `make check` and `make test`, then goreleaser
builds every platform, so a tag on a broken tree fails before it ships.

## Licence

Not decided yet — ask, or open an issue if you want to use it.
