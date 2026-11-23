# Undo a bad trim: backups and renumbering

Every write to the sets keeps the previous bytes. When a trim takes the last negative out of a
split, or a delete takes the wrong case, this is how you get it back.

## Backups

Every write to the sets copies the previous file to `.evalctl/backups/<timestamp>--<flattened path>`
(200 per file, oldest pruned); a reference replaced with `--force` is backed up the same way. To
undo a trim, copy the backup back over the file. Runs are never backed up — they are their own
evidence, and the `*-workspace/` dirs are gitignored. `.evalctl/references.json` is the one other file the
tool keeps there: which run each promoted reference came from, and the hash of the bytes it
landed as.

## Where next

- [`renumber` and `edit --id`, and what they break](../reference/cli.md)
- [Why ids never shift on delete](../explanation/deliberate-choices.md)
