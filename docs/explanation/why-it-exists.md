# Why it exists

The loop that measured the sets used to live in two Python files that had to agree with the
manager about slugs, splits and fixture dirs. This is what replaced them, and what the port is
still checked against.

## Why it exists

The set is worked on one stage skill at a time and the work is one loop — measure a wording, revise
it, generalize it — so the TUI is that loop and nothing else: the door, the skill, its two sections.

The sets grew faster than they got pruned, and the loop that measured them lived in two Python
files that had to agree with the manager about slugs, splits and fixture dirs. So the rules live
in one place (`internal/store` for the sets, `internal/runner` for the loop) and the CLI and the
TUI both go through them.

The Python originals (`evals/run.py`, `evals/profiles.py`) are gone: this binary is the only
runner. `go test ./internal/runner -run Parity` still re-scores a trigger iteration recorded
before the port from its own `trigger.json` records and compares every verdict, so the port stays
checkable against what the old loop produced.

## Where next

- [The two lanes, and why they are separate](../explanation/harness.md)
- [Tests and the parity check](../../contributing.md)
