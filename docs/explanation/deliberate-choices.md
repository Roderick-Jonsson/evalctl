# Deliberate choices

The decisions that look like bugs until the reason is on the page: ids that do not renumber
themselves, assertions as a first-class field, unknown keys that survive, isolation as the
default.

## Deliberate choices

- **Ids never shift on delete.** Results are keyed `q<id>-<skill>` and iteration reports quote
  ids, so a gap is honest. `renumber` exists, asks first, and says what it breaks. `edit --id`
  is the same bargain at one row: it lists the result dirs and reports that still cite the old
  number and asks before the number moves (`--yes` when you mean it, `--dry-run` to only look).
  `add` and `promote` warn when the id they are handed is one history still cites — a gap in the
  file is not a gap on disk — but they never refuse: a create destroys nothing.
- **Assertions are a first-class field, not a blob.** `assertions` is a list of strings on the
  case (the agentskills.io grading unit), written only when the case has any: the guide adds them
  after a first graded iteration, and `"assertions": []` in every case would claim the opposite.
  `ASR` counts them, `v` lists them, `e` edits them one per line, and a search reads them — so
  "which case checks the manifest?" is a search, not a grep. Grading them is still manual; the
  runner records no verdict.
- **Unknown JSON keys survive.** The format grows; keys this tool does not know are carried
  through a round trip untouched. Saving an unchanged file is byte-identical — there is a test for
  that.
- **Fixture dirs are separate from cases.** Deleting a case does not delete the files it staged
  unless asked, because the fixture is the expensive part.
- **Duplicate detection is word-overlap (Jaccard), not embeddings.** It catches "test the same
  intent twice with different words" on sets of this size, offline, with no model.
- **Isolation is the default.** Each arm runs in a fresh temp root (`<tmp>/work`), then the result
  is copied into its arm dir's `outputs/`. Inside the repo the baseline arm can see the project and the
  other arm's output, and then reasons about being in an ablation. `--isolate=false` exists for
  debugging only.
- **The arms run sequentially, and a run holds `.evalctl/run.lock`.** An arm writes `skills/` and
  `memories/` inside its sandbox, so two concurrent runs would grade a half-synced profile. A
  second `evalctl run` refuses instead of racing.
- **The parent grades; the child is never asked.** Containment, the production-root fingerprint,
  the load evidence and the skill's own `verify` are all read from disk and the process exit —
  a child's self-report is not evidence.
- **Two containment checks per arm**: everything written is inside the arm's run root, and the
  production root (the real profile's `terminal.cwd`) is fingerprinted before and after every arm, so
  a run that writes outside its run root fails instead of scoring clean. A tree that declares
  top-level prefixes in `evalctl.json` (this one does: `MC-`, `_archive/`) gets a third reading.
- **A launch that died is not a miss.** A trigger run that emitted no tool call is reported as
  unmeasured with its stderr and excluded from the rate.
- **Trigger runs are stopped the moment the skill loads.** A positive stops on its own skill, a
  negative on *any* skill in the tree (the first load is the routing decision);
  negatives also run with a 3-turn cap. Without this, one negative query cost ~160s instead of ~5s.
- **No grading, no assertions.** `grading.json` is evidence, not a verdict.
- **A reference is shipped, not measured.** Promoting into `<skill>/references/` changes the tree
  every later run installs (and the tree hash `meta.json` records), so it takes the runner's lock
  and refuses while an arm is going — the same reason a fixture promotion does.

## Where next

- [The rules a tree must satisfy](../reference/runs.md#the-rules-it-enforces)
- [The harness](harness.md)
