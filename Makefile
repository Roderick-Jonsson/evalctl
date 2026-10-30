# evalctl — one binary, one number.
#
# `make build` stamps the version, the commit and whether the working tree was dirty, so
# `evalctl version`, the TUI header, every meta.json and every report say which build measured
# what. A plain `go build` still works: it falls back to the commit the compiler embedded, and the
# version declared in internal/version/version.go.

# `make install` puts the same stamped binary on PATH ($GOBIN, i.e. `go env GOPATH`/bin), so
# `evalctl` runs from anywhere in the eval tree without a path into this directory. Bare `make`
# installs, and `make hooks` installs a post-commit hook that reinstalls whenever a commit touches
# the tool — the copy on PATH is the one actually run, so it should never be the stale one.

MODULE  := evalctl/internal/version
GOBIN   := $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)
GITDIR  := $(shell git rev-parse --absolute-git-dir 2>/dev/null)
HOOKS   := $(GITDIR)/hooks
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null)
DIRTY   := $(shell git diff --quiet 2>/dev/null && echo false || echo true)
BUILT   := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X $(MODULE).Commit=$(COMMIT) -X $(MODULE).BuiltAt=$(BUILT) -X $(MODULE).Modified=$(DIRTY)

.DEFAULT_GOAL := install

.PHONY: all build install uninstall hooks version test check clean

all: install

build:
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/evalctl .

install:
	go install -ldflags "$(LDFLAGS)" .
	@echo "installed $(GOBIN)/evalctl"
	@case ":$$PATH:" in *":$(GOBIN):"*) ;; *) echo "warning: $(GOBIN) is not on PATH";; esac

uninstall:
	rm -f $(GOBIN)/evalctl
	@echo "removed $(GOBIN)/evalctl (left the git hook in place — `make -C evalctl hooks` re-adds, delete $(HOOKS)/post-commit to remove)"

hooks:
	@src="$(CURDIR)/githooks/post-commit"; dst="$(HOOKS)/post-commit"; \
	if [ -e "$$dst" ] && ! grep -q 'evalctl: reinstall on commit' "$$dst" 2>/dev/null; then \
		echo "refusing: $$dst exists and is not ours — merge githooks/post-commit by hand"; exit 1; \
	fi; \
	cp "$$src" "$$dst" && chmod +x "$$dst" && echo "installed $$dst"

version: build
	./bin/evalctl version

test:
	go test ./...

check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt:"; echo "$$out"; exit 1; fi
	go vet ./...

clean:
	rm -rf bin
