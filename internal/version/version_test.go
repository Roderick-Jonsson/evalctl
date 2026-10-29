package version

import (
	"strings"
	"testing"
)

// withGlobals runs a check with the stamp variables set, then puts them back: they are package
// state, and a leaked stamp would make every later test look like a release build.
func withGlobals(t *testing.T, commit, builtAt, modified string, fn func()) {
	t.Helper()
	oldCommit, oldBuilt, oldModified := Commit, BuiltAt, Modified
	Commit, BuiltAt, Modified = commit, builtAt, modified
	defer func() { Commit, BuiltAt, Modified = oldCommit, oldBuilt, oldModified }()
	fn()
}

func TestStampWinsOverBuildInfo(t *testing.T) {
	withGlobals(t, "abcdef1234567", "2026-10-29T12:00:00Z", "true", func() {
		info := Resolve()
		if info.Source != "ldflags" {
			t.Errorf("source = %q, want ldflags", info.Source)
		}
		if info.Commit != "abcdef1" {
			t.Errorf("commit = %q, want the short form", info.Commit)
		}
		if !info.Dirty || info.BuiltAt != "2026-10-29T12:00:00Z" {
			t.Errorf("info = %+v", info)
		}
		if info.Short() != "v"+Version+"+" {
			t.Errorf("short = %q — a dirty binary must not read as a release", info.Short())
		}
		if !strings.Contains(info.String(), "abcdef1, dirty") {
			t.Errorf("string = %q", info.String())
		}
	})
}

func TestCleanStampReadsAsARelease(t *testing.T) {
	withGlobals(t, "abcdef1234", "", "false", func() {
		info := Resolve()
		if info.Dirty || info.Short() != "v"+Version {
			t.Errorf("info = %+v short = %q", info, info.Short())
		}
		if strings.Contains(info.String(), "dirty") {
			t.Errorf("string = %q", info.String())
		}
	})
}

func TestUnstampedFallsBackToBuildInfo(t *testing.T) {
	withGlobals(t, "", "", "", func() {
		info := Resolve()
		// A test binary built inside a git checkout carries vcs settings; one built without VCS
		// (a tarball, `go run`) reports nothing and must say so instead of inventing a commit.
		if info.Source != "buildinfo" && info.Source != "declared" {
			t.Fatalf("source = %q", info.Source)
		}
		if info.Source == "declared" && info.Commit != "" {
			t.Errorf("a declared-only binary claimed commit %q", info.Commit)
		}
		if info.Source == "buildinfo" && len(info.Commit) != 7 {
			t.Errorf("commit = %q, want 7 characters", info.Commit)
		}
		if info.Version != Version {
			t.Errorf("version = %q, want the declared %q", info.Version, Version)
		}
		if info.GoVersion == "" {
			t.Error("go version is empty")
		}
	})
}

func TestFormatsWithoutACommit(t *testing.T) {
	info := Info{Version: "1.2.3"}
	if got := info.String(); got != "1.2.3" {
		t.Errorf("string = %q", got)
	}
	if got := info.Short(); got != "v1.2.3" {
		t.Errorf("short = %q", got)
	}
}
