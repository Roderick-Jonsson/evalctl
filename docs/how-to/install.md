# Install it so `evalctl` runs from anywhere

Two ways in: a stamped build from source, or a release binary. Either ends with `evalctl` on
`PATH` answering `evalctl version` with the number you installed.

## Build and install

```bash
cd evalctl
make                          # same as `make install`: the binary on PATH is the default goal
make install                  # stamped build, put on PATH — then `evalctl` from anywhere in the tree
make hooks                    # post-commit hook: reinstalls whenever a commit touches the tool
make build                    # stamped: commit, build time, dirty flag → ./bin/evalctl
go build -o bin/evalctl .     # plain build: commit comes from the Go build info
./bin/evalctl                 # TUI: the door, then the loop, step by step
```

## From a release binary

```bash
# download evalctl_<version>_<os>_<arch>.tar.gz from the release page, then
chmod +x evalctl && mv evalctl "$(go env GOPATH)/bin/"
```

A release binary answers `evalctl version` with the tag it was built from, the commit, and the time
it was built, so a binary copied between machines says which one it is.

## The installed binary

Once installed (`$(go env GOPATH)/bin/evalctl`, which `make install` warns you about if it is not
on `PATH`), no path into this directory is needed:

```bash
evalctl                       # TUI: what to optimize, then which skill
evalctl tui --skill slide-builder   # straight into one skill's loop
evalctl tui --glyphs ascii          # unicode marks and rules, or the ASCII fallback
evalctl version               # which build this is
make uninstall                # take it back off PATH
```

## Which tree it reads

`--root` picks the tree (default: walk up for the eval tree, or `$EVALCTL_ROOT`), and global flags
may come before the verb: `evalctl --root DIR validate`. Running it from inside the tree just works,
installed or not.

## Where next

- [The post-commit hook that keeps the binary fresh](../../contributing.md#the-post-commit-hook)
- [Start or adopt a tree](start-or-adopt-a-tree.md)
