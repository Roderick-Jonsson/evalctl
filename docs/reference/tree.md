# The tree: skills, folders and conventions

What counts as a skill, where the walk finds one, and the optional `evalctl.json` that declares
whatever a project means by "the order of its skills" or "a run's output".

## Skills may be grouped

A skill is any directory with `SKILL.md` and an `evals/` set, up to four levels down: at the tree's
top level, in group folders (`groups/kitchen/recipe-notes`), or inside a plugin bundle
(`plugins/<plugin>/skills/<skill>`). The folder name is the skill's name — a workspace, a profile
install and a trigger row's `skill` field all key on it — so two folders with one name is an error
that names both paths.

A tree whose skills are grouped declares itself: its own `evalctl.json` (which `init` writes) is what
makes the walk from inside `groups/kitchen/recipe-notes` find the tree and not the group folder. Deep
layouts are only scanned for a directory that says it is a project — our own marker, a `.git`, or a
`plugins/` folder — so a shared parent (`/tmp`, a home directory) never reads as somebody's tree.

## A tree's own conventions

The tool is a general eval runner: it names no skill, document or folder. Whatever a project means by
"a run's output", "the order of its skills" or "a workspace" is declared in an optional
`evalctl.json` at the tree's root:

```json
{
  "profile": "video-course-creator",
  "skill_order": ["source-research", "learning-path", "slide-builder", "av-production"],
  "skill_outputs": { "slide-builder": ["SL-*.md", "pages"] },
  "containment_prefixes": ["MC-", "_archive/"],
  "workspace_prefixes": ["MC-"],
  "env_keys": ["OLLAMA_API_KEY"]
}
```

Every field is optional and empty means "no convention" — alphabetical skills, nothing recognised as
an answer, anything inside the run root, the whole produced tree carried, the real profile's `.env`.
`profile` is what a tree started from outside its profile says, so the contract and the sandbox names
are that profile's. The sandbox names themselves are always derived: `eval-<profile>-with|without|
trigger|revise`.

`--glyphs auto|unicode|ascii` (`$EVALCTL_GLYPHS` as the default) picks the marks the frame draws
itself: the cursor, the block in front of the region that has the keys, the rail's marks for a step that
is done, current or locked, and a run in flight. `auto` follows the locale. The ASCII profile draws the same frame in `> | - + x` for a
terminal without UTF-8 — same widths, same columns, and a test that no frame mark escapes it.

## Where next

- [The profile on the other side of the tree](profiles.md)
- [Validation: what a tree must satisfy](runs.md#the-rules-it-enforces)
