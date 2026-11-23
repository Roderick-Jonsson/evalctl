package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReferencePageIsCurrent: docs/reference/cli.md is generated (`make docs`), and this is what
// keeps it generated. A flag added, renamed or re-grouped without regenerating fails here, which
// is the only way a reference page stays true: the alternative is a reader who trusts it and a
// page that quietly describes last month's grammar.
func TestReferencePageIsCurrent(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "reference", "cli.md")
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no reference page in this checkout: %v", err)
	}
	generated, err := ReferenceMarkdown()
	if err != nil {
		t.Fatalf("ReferenceMarkdown: %v", err)
	}
	if string(onDisk) == generated {
		return
	}
	// A line-by-line report beats one 900-line diff in a test failure.
	want := strings.Split(string(onDisk), "\n")
	got := strings.Split(generated, "\n")
	for i := 0; i < len(want) || i < len(got); i++ {
		var w, g string
		if i < len(want) {
			w = want[i]
		}
		if i < len(got) {
			g = got[i]
		}
		if w != g {
			t.Fatalf("docs/reference/cli.md is stale at line %d — run `make docs`\n on disk: %q\n generated: %q", i+1, w, g)
		}
	}
}

// TestReferenceCoversEveryVerb: the page is the index, so a verb missing from it is a verb nobody
// can find from the docs.
func TestReferenceCoversEveryVerb(t *testing.T) {
	page, err := ReferenceMarkdown()
	if err != nil {
		t.Fatalf("ReferenceMarkdown: %v", err)
	}
	for _, g := range indexGroups {
		for _, e := range g.entries {
			verb := verbOf(e.cmd)
			if verb == "" || verb == "help" {
				continue // the bare `evalctl` is the TUI; `help` is the index itself
			}
			if !strings.Contains(page, "### "+e.cmd+"\n") {
				t.Errorf("%q has no section on the reference page", e.cmd)
			}
		}
	}
}
