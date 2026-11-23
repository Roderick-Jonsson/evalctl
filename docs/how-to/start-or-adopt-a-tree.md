# Start or adopt a tree

A tree is a project folder that holds skills and their eval sets. `init` writes one from
nothing; `adopt` declares a folder that already has skills written for Hermes and never
measured them — which is the common case.

## Starting a tree

```bash
mkdir notes-evals && cd notes-evals
evalctl init --skill meeting-notes --profile notes
evalctl validate
evalctl trigger --skill meeting-notes --dry-run
```

`init` writes the four files a skill needs (`SKILL.md`, `evals/evals.json`,
`evals/trigger-evals.json`, an empty `evals/files/`) plus an `evalctl.json` for the tree. The starter
is valid as written: `validate` is clean on it, so the first run is one edit away and not a scavenger
hunt. The placeholders say what a real row looks like; `--force` replaces an existing skill.

### A folder that already holds skills

The common case is not an empty directory: it is a project whose skills were written for Hermes and
never measured. `adopt` declares it in place — it reads the folder, adds the eval sets each skill is
missing, writes the conventions file, and never touches a `SKILL.md` or replaces an existing set:

```bash
evalctl adopt                          # every skill the folder holds
evalctl adopt --skills notes,recipes   # only these
evalctl adopt --profile notes-lab --link   # name the profile, create it if needed, link it
```

`--link` is the whole flow in one command: the profile is created when the name is new (Hermes
writes its SOUL.md and model default), then pointed at this folder through `skills.external_dirs`, so
it can be worked in by hand afterwards.

The TUI's first step does all of this from one screen: run `evalctl` where nothing is a tree and it
reads the folder first — **declare this folder** with the skills it found (all selected) when there
are any, **start a project** with a suggested name when there are none — with the profile filled in
from the tree, an existing profile of that name, or the folder's own name. The line under the form
says what ctrl+s will do, including *creates profile X and links it to this folder*. One keystroke,
then the door opens on the tree. Same writer either way: `internal/scaffold`.

## Where next

- [The tutorial: your first eval](../tutorials/first-eval.md)
- [Where skills may live, and what a tree is not](../reference/tree.md)
