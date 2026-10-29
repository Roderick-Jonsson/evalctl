// Package version answers "which evalctl is this?" from three places, most specific first:
//
//  1. values stamped at build time (-ldflags, which is what a release binary carries),
//  2. the Go build info the compiler embeds — the git revision and whether the tree was dirty,
//  3. the version declared in this file, which is always the source of truth for the number.
//
// A plain `go build` therefore still names the commit it came from; a stamped build adds the
// release version and the build time. The number is deliberately hand-edited, not derived: it is
// a promise about the harness's behaviour, and the harness changing is what bumps it.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// Version is the declared version of the tool: bump it when the runner, the store rules, the
// profiles or the case format change, because an iteration measured by a different harness is not
// comparable with the last one.
var Version = "0.5.0"

// Stamped at build time; empty means "ask the build info".
var (
	Commit   = ""
	BuiltAt  = ""
	Modified = ""
)

// Info is everything worth printing about the binary in hand.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	BuiltAt   string `json:"built_at,omitempty"`
	Dirty     bool   `json:"dirty"`
	Source    string `json:"source"` // ldflags, buildinfo or declared
	GoVersion string `json:"go_version"`
}

// Resolve fills in what was not stamped, in order of how much it can be trusted.
func Resolve() Info {
	info := Info{Version: Version, Commit: Commit, BuiltAt: BuiltAt, Source: "declared",
		GoVersion: runtime.Version()}
	if info.Commit == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					if info.Commit == "" {
						info.Commit = s.Value
					}
				case "vcs.time":
					if info.BuiltAt == "" {
						info.BuiltAt = s.Value
					}
				case "vcs.modified":
					info.Dirty = s.Value == "true"
				}
			}
		}
		if info.Commit != "" {
			info.Source = "buildinfo"
		}
	} else {
		info.Source = "ldflags"
	}
	if Modified != "" {
		info.Dirty = Modified == "true"
	}
	if len(info.Commit) > 7 {
		info.Commit = info.Commit[:7]
	}
	return info
}

// Short is what a header has room for: "v0.5.0", with a "+" when the tree the binary came from
// was dirty — an unstamped or unclean binary must never look like a release.
func (i Info) Short() string {
	if i.Dirty {
		return "v" + i.Version + "+"
	}
	return "v" + i.Version
}

// String is the one-line identity used in reports: "0.5.0 (2622cb0, dirty)".
func (i Info) String() string {
	var parts []string
	if i.Commit != "" {
		parts = append(parts, i.Commit)
	}
	if i.Dirty {
		parts = append(parts, "dirty")
	}
	if len(parts) == 0 {
		return i.Version
	}
	return fmt.Sprintf("%s (%s)", i.Version, strings.Join(parts, ", "))
}
