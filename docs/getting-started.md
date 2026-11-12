# Getting started with evalctl, on your own project

evalctl measures one thing: whether a skill earns its place. It runs a Hermes agent twice — once
with your skill installed, once without it — and asks whether the skill changed the output. For the
description, it runs the agent on a set of requests and asks whether the right skill got loaded.

Nothing in the tool knows your project. It reads three files per skill and one optional settings file
for the tree. This page creates them, checks them, and runs the first measurement.

If you have not installed it: `go build -o bin/evalctl . && make install` in the evalctl checkout, or
grab a release binary (see the install section at the end). Everything below is the same either way.

---

## 1. Start a tree

```bash
mkdir ~/work/notes-evals && cd ~/work/notes-evals
evalctl init --skill meeting-notes --profile notes
```

`--profile` is the Hermes profile whose `SOUL.md` is the contract, i.e. the rules both arms are
graded against. If you leave it out, evalctl measures whichever profile you are standing in, or
Hermes's default — and it says so, loudly, at the start of every run.

You get:

```
evalctl.json                              the tree's own conventions (optional, editable)
meeting-notes/SKILL.md                    name + description in the frontmatter, then instructions
meeting-notes/evals/trigger-evals.json    the requests that should and should not load it
meeting-notes/evals/evals.json            the case: prompt, expected output, assertions
meeting-notes/evals/files/README.md       where a case's input files go
```

Four placeholder trigger rows (two train, two validation, one positive and one negative in each) and
one placeholder case. Everything is valid as written — the first `validate` is clean — so every
further error is one you introduced.

## 2. Write the three files

**`SKILL.md`.** The frontmatter `description` is the entire input an agent sees when deciding whether
to load the skill. Say *when* it applies, not what it contains. The body is the instructions; the
trigger lane does not read it, the output lane does.

**`trigger-evals.json`.** Real requests, in the words a user would type.

| field | meaning |
|---|---|
| `id` | unique within the file |
| `should_trigger` | `true` = this skill is the right one for the request |
| `split` | `train` (guides the wording), `validation` (judges it), `holdout` (never in the rates) |
| `why` | one line for the human reading the failure later; optional |
| `query` | the request itself |

Each half wants both labels. A half with one label is a warning, not a refusal — the run measures the
side it has and says so — but a rate over one label cannot tell you whether the description is ever
chosen *and* never chosen wrongly.

**`evals.json`.** The output case.

| field | meaning |
|---|---|
| `id`, `slug` | `slug` names the case's fixture dir and its result dir |
| `prompt` | the request the agent gets |
| `expected_output` | what a good answer contains, for the human reading the report |
| `files` | paths inside `evals/files/<slug>/` that get staged before the run |
| `assertions` | claims a judge can check by quoting the output; leave empty and the case is not graded |

## 3. Check before you spend

```bash
evalctl validate                  # the tree against the runner's rules
evalctl profiles status           # which profile is the contract, and what it can sign in with
evalctl trigger --skill meeting-notes --dry-run
```

The dry run prints the exact `hermes` command, the profile each arm runs in, and either the hermes
version it will drive or the reason it would refuse. Nothing is created and nothing is spent.

## 4. The trigger lane (is the description good?)

```bash
evalctl trigger --skill meeting-notes                 # 3 runs per query, ~4 minutes
evalctl report meeting-notes@trigger-1                # the rates, per split
evalctl lab --skill meeting-notes                     # what the loop asks for next
evalctl describe --skill meeting-notes                # a model proposes a new description
evalctl describe --skill meeting-notes --write        # stage it as the next candidate
evalctl trigger --skill meeting-notes                 # measure again: the staged wording is used
```

`describe` talks to an OpenAI-compatible endpoint. Point it anywhere:

```bash
export EVALCTL_DESCRIBE_BASE_URL=https://api.openai.com/v1
export EVALCTL_DESCRIBE_MODEL=gpt-4o-mini
export EVALCTL_DESCRIBE_KEY_ENV=OPENAI_API_KEY    # read the key from a variable you already have
```

`EVALCTL_DESCRIBE_KEY` holds a key directly; `EVALCTL_DESCRIBE_KEY_ENV` names the variable to read.
With neither, evalctl looks for the provider's conventional name and then Ollama's, and the error
lists everything it looked for. `evalctl describe --dry-run` prints the prompt and the provider
without calling anything.

## 5. The output lane (does the skill change the answer?)

```bash
evalctl run --skill meeting-notes --dry-run       # the plan: cases × arms
evalctl run --skill meeting-notes                 # both arms, then the judge grades the assertions
evalctl runs --skill meeting-notes                # pass rate per arm, with and without
evalctl report meeting-notes@1                    # the full report: delta, tokens, evidence
```

The with-skill arm runs in a sandbox with only your skill installed; the baseline runs with none. The
delta is what the skill is worth. A case with no assertions is measured for time and tokens and not
graded — write assertions after the first run shows what good looks like.

## 6. What each run leaves behind

| path | what |
|---|---|
| `<skill>-workspace/iteration-N/` | one output-lane iteration: arms, outputs, grading, report |
| `<skill>-workspace/trigger-N/` | one trigger-lane batch: records, score, report |
| `.evalctl/backups/` | a copy of every set file evalctl rewrote |

The four sandbox profiles are created for the run and deleted when it ends. `--keep` leaves them for
inspection; `evalctl profiles reap` removes what a killed run left behind.

---

## Your own profile is the lab

A profile with no skills is useless to open by hand. Instead of copying the tree's skills into it,
point it at the tree:

```bash
evalctl profiles link            # writes skills.external_dirs into the tree's profile
evalctl profiles status          # "simpleton scans /path/to/tree (external skills — no copy)"
evalctl profiles unlink          # drops it again
```

Hermes scans that folder beside its own `skills/`, so the profile's index holds the skills you are
working on: open it in the desktop and try things by hand. Nothing is copied, so an edit in the tree
is immediately the profile's edit — the skills you experiment with are the skills evalctl measures.

Two things that come with it, both fine for a lab and never true of an eval sandbox:

- the profile's agent can read the tree's `evals/` (the questions and the labels);
- the profile's agent can edit the tree (`skill_manage` writes where the skill lives).

Eval runs are untouched by any of this: they copy skills into throwaway sandboxes, strip `evals/`,
and never link the tree. If the folder ever moves, `evalctl profiles check` says the external dir is
missing — Hermes would otherwise skip it silently.

## Credentials and sign-in

An eval sandbox is a brand-new profile, so it can sign in only with what evalctl copies in:

- **API keys** — every key in the real profile's `.env` (or just the ones your tree lists in
  `env_keys`) is written into the sandbox's `.env`.
- **Browser logins** — Hermes writes a browser login to a file, and those are copied byte for byte:
  the profile's own (`google_token.json`, `*_credentials.json`, `*_token.json`,
  `*_client_secret.json`, `auth.json`, `oauth.json`) and the Hermes root's shared `auth.json`, which
  holds the provider credential pool for every profile.

Before the first spend, the run checks that the real profile has one or the other and refuses with
the reason if it does not:

```
evalctl: profile notes has no credentials to hand an eval sandbox: … holds no API key and
neither the profile nor ~/.hermes carries a login file (auth.json, credentials.json, oauth.json,
token.json). Sign in once in that profile, or put a key in its .env
```

A launch that fails to authenticate is reported as that, with the harness's own stderr line — not as
a skill that did not work.

## What evalctl expects from Hermes

It drives `hermes -p <profile> chat --oneshot --query-file … --format stream-json --in … --yolo`
(plus `--max-turns` in the trigger lane) and reads the `stream-json` frames. Before a run it asks
`hermes chat --help` whether those flags still exist, and refuses by name if one is missing, instead
of producing an iteration of zeros. `HERMES_BIN` points it at a different binary; `HERMES_ROOT` at a
different Hermes home.

It is plain Go and builds on macOS, Linux and Windows. The only platform-specific parts are killing a
run's child processes and the run lock.

## Where the skills may live

Inside the project folder: at its top level, in group folders (`groups/kitchen/<skill>`), or in a
plugin bundle (`plugins/<plugin>/skills/<skill>`) — evalctl walks four levels down and names each
skill by its folder name. Two folders with one name is an error, because a skill's name is what a
workspace, a profile install and a trigger row key on.

Not inside a Hermes profile. A profile's `skills/` is where Hermes installs skills; a set kept there
has no project folder of its own and no `evalctl.json` to share, so `evalctl init` refuses to create
one and `evalctl` refuses to read one. If you want to launch from inside the profile, attach it to
your folder:

```bash
evalctl profiles attach <profile>      # run in the project folder, once
cd ~/.hermes/profiles/<profile> && evalctl
```

## Installing it

```bash
# from source
git clone <the repo> && cd evalctl && make install     # builds, stamps, puts it on PATH

# or a release binary
# download evalctl_<version>_<os>_<arch> from the release page, chmod +x, move onto PATH
```

`evalctl version` says which build you have (version, commit, whether the tree was dirty, and when it
was built), and the TUI's top-right corner repeats it — a stale binary looks exactly like a fresh one
until that corner says when it was made.

## Reading the rest of these docs

The README's screens and examples use one sample project — a course pipeline with a `slide-builder`
skill. Nothing in the tool knows that name; wherever you see it, your skill's name goes there.
