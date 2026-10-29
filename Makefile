# evalctl — one binary, one number.
#
# `make build` stamps the version, the commit and whether the working tree was dirty, so
# `evalctl version`, the TUI header, every meta.json and every report say which build measured
# what. A plain `go build` still works: it falls back to the commit the compiler embedded, and the
# version declared in internal/version/version.go.

MODULE  := evalctl/internal/version
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null)
DIRTY   := $(shell git diff --quiet 2>/dev/null && echo false || echo true)
BUILT   := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X $(MODULE).Commit=$(COMMIT) -X $(MODULE).BuiltAt=$(BUILT) -X $(MODULE).Modified=$(DIRTY)

.PHONY: build version test check clean

build:
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/evalctl .

version: build
	./bin/evalctl version

test:
	go test ./...

check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt:"; echo "$$out"; exit 1; fi
	go vet ./...

clean:
	rm -rf bin
