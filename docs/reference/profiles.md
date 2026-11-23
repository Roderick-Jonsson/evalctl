# Profiles and the four sandboxes

A Hermes profile supplies the contract (`SOUL.md`) and the credentials; the tree supplies the
skills and holds the results. The bridge is one file, and the sandboxes are named from the
profile.

## The tree, the profile and the lab

**A tree is a project folder, and only a project folder.** A Hermes profile is not one: its `skills/`
is where Hermes installs skills, so a set kept in there would be the same files in a second home,
with no `evalctl.json` anybody could share and the profile's own `SOUL.md` standing in as the
contract. `evalctl init` refuses to scaffold inside a profile, and `evalctl` inside one refuses to
read it, naming the two ways forward.

The bridge between the two is `.evalctl-tree`: one line, written by `evalctl profiles attach <name>`
from the project folder, which lets `cd ~/.hermes/profiles/<name> && evalctl` measure that folder's
sets under that profile's rules. The profile supplies the contract (`SOUL.md`) and the credentials;
the folder supplies the skills and holds the results. No profile name is compiled in: the profile you
are standing in wins, `$EVALCTL_PROFILE` overrides it, and a run says which of those it used.

**Provisioning verbs are opt-in on the real profile.** `profiles ensure`, `sync`, `render` and
`scrub` write; without `--real` (or naming it) they touch only the eval sandboxes and print that the
real profile was left alone. The run lock is keyed on the measured profile rather than the tree, so
two checkouts measuring one profile wait for each other instead of sharing a sandbox.

**The measured profile is also your lab.** A profile with no skills is useless to open by hand, which
is what made it look like clutter in the picker. `evalctl profiles link` fixes that by reference, not
by copy:

```bash
evalctl profiles link                       # the tree's profile scans this folder (skills.external_dirs)
evalctl profiles status                     # says whether it scans the folder, and where
evalctl profiles unlink                     # back to its own skills/ only
```

Hermes scans that folder beside `~/.hermes/skills/`, so the profile's index holds the same skills
evalctl measures — edit the tree and the profile sees the edit, with nothing to re-sync. Two costs,
both deliberate: the desktop agent can read the tree's `evals/` (fine for a lab, which is why eval
sandboxes never get a link) and it can edit the tree (edits land in git). A folder that later moves is
reported by `profiles check` — Hermes itself skips a missing external dir without a word.

## Where next

- [Provisioning verbs in the CLI](cli.md)
- [Credentials a sandbox needs](../tutorials/first-eval.md#credentials-and-sign-in)
