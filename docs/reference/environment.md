# Environment, platforms and the build stamp

What the binary reads from the shell, which platforms it builds for, and why the version in the
corner is a fact you can trust.

## What it reads from the shell

Every variable below is optional; the default is what evalctl does when it is unset.

| variable | what it decides |
|---|---|
| `EVALCTL_ROOT` | the tree, when you are not standing inside one (`--root` does the same for one call) |
| `EVALCTL_PROFILE` | the Hermes profile whose `SOUL.md` is the contract, overriding the one you are standing in and the tree's own `evalctl.json` |
| `EVALCTL_GLYPHS` | the default for `--glyphs`: `auto` (follow the locale), `unicode`, `ascii` |
| `EVALCTL_THEME` | the default palette: `midnight`, `high-contrast`, `daylight` |
| `HERMES_BIN` | a different `hermes` binary to drive |
| `HERMES_ROOT` | a different Hermes home (the shared `auth.json` lives under it) |
| `EVALCTL_ENV_FILE` | the one env file consulted for the `describe` call, instead of the nearest `.env` walking up from the working directory, then `~/.config`, then the harness's own `.env` |
| `EVALCTL_DESCRIBE_KEY` | the key the `describe` call uses — the value itself, so a provider other than Ollama does not have to borrow another product's variable name |
| `EVALCTL_DESCRIBE_KEY_ENV` | a variable to read instead of `EVALCTL_DESCRIBE_KEY`, for a machine that already exports one |
| `EVALCTL_DESCRIBE_BASE_URL`, `EVALCTL_DESCRIBE_MODEL` | the endpoint and the model that proposal is written with (defaults: `https://ollama.com/v1`, `gpt-oss:120b`) |
| `EVALCTL_PARITY_ROOT` | the tree the `Rescore` parity test re-scores a pre-port iteration from; unset, that test skips |
| `COLUMNS`, `COLORFGBG`, `LANG`/`LC_ALL`/`LC_CTYPE` | read, never set: the width a piped help page wraps to, the background probe's Windows fallback, and the locale that decides `auto` glyphs |

The `describe` key is never printed and never stored: the screens say which *variable* answered and
*which file* it came from, because that is what you need to fix a wrong key, and never the value.

## Version

The number lives in `internal/version/version.go` and is hand-edited: bump it when the runner, the
store rules, the profiles or the case format change, because an iteration measured by a different
harness is not comparable with the last one. It is also the first thing in the artifact:

- the TUI header's right corner carries `v0.9.0+ · 11:06` — the version, a `+` when the tool's own
  sources were dirty at build time, and then the build's own clock, local time, read from the ldflags
  stamp. It is there because the binary somebody is looking at and the binary somebody built are not
  always the same one: a stale `bin/evalctl` looks exactly like a fresh one until the corner says
  when it was made. An unstamped `go build` shows no clock rather than the commit's time, which is
  the one number in this tool that would make a stale binary look fresh;
- `<skill>-workspace/iteration-N/meta.json` and `<skill>-workspace/trigger-N/meta.json` record `provenance.tool`
  (version, commit, dirty, `built_at`);
- both `report.md` files name the build that generated them, and a run's start line prints it.

`make build` stamps the commit, the build time and the dirty flag; `make install` stamps it the same
way and drops the binary in `$GOBIN`. A plain `go build` still names
its commit through the Go build info — `evalctl version` says which of the three sources it used
(ldflags, build info, or nothing but the declared number), so a release binary and a scratch build
never look the same. `make check` runs gofmt and vet; `make test` the suite.

## Platforms

macOS, Linux and Windows build from the same source: the two platform-specific pieces (ending a run's
child processes, and the exclusive run lock) sit behind build tags, and `GOOS=windows go build ./...`
is part of the check. The terminal-background probe is a no-op on Windows, which falls back to
`COLORFGBG` and the dark palette.

## Where next

- [Install it](../how-to/install.md)
- [Themes and glyphs](../explanation/colour.md)
