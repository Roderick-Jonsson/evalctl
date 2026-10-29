package cli

import (
	"flag"
	"fmt"
	"os"

	"evalctl/internal/version"
)

// runVersion prints which build is in hand. It needs no eval tree, so it parses its own flags
// instead of going through common.parse: `evalctl version` has to work from anywhere.
func runVersion(args []string) int {
	fs := flag.NewFlagSet("evalctl version", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	short := fs.Bool("short", false, "the version alone, for scripts")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	info := version.Resolve()
	if *asJSON {
		if err := writeJSON(os.Stdout, info); err != nil {
			return fail(err)
		}
		return 0
	}
	if *short {
		fmt.Println("evalctl " + info.Short())
		return 0
	}
	fmt.Println(styleTitle.Render("evalctl " + info.Short()))
	fmt.Printf("  version     %s\n", info.Version)
	commit := info.Commit
	if commit == "" {
		commit = "(unknown — built without VCS stamping)"
	} else if info.Dirty {
		commit += " (dirty tree)"
	}
	fmt.Printf("  commit      %s\n", commit)
	built := info.BuiltAt
	if built == "" {
		built = "(unknown)"
	}
	fmt.Printf("  built       %s\n", built)
	fmt.Printf("  from        %s\n", versionSource(info.Source))
	fmt.Printf("  go          %s\n", info.GoVersion)
	return 0
}

// versionSource explains where the commit came from, because "built from a dirty tree with a
// bare go build" and "a stamped release binary" must never look the same.
func versionSource(source string) string {
	switch source {
	case "ldflags":
		return "ldflags (stamped at build time — see the Makefile)"
	case "buildinfo":
		return "buildinfo (plain go build: the commit is whatever the working tree was)"
	}
	return "declared (no build info: this binary does not know its commit)"
}
