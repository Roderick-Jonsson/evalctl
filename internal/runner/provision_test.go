package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/profile"
	"evalctl/internal/store"
)

// fakeHermes is a stand-in for the hermes binary that behaves like the real one on the paths a run
// uses: `profile create` makes a live profile directory (an identity marker, so profile.Live sees
// it), `profile delete` removes one and leaves the tombstone Hermes leaves, and `chat` prints the
// one result line the harness reads. Every call is appended to $HERMES_CALLS when it is set, which
// is how a test asserts the lifecycle without watching the filesystem mid-run.
//
// Tests must point HERMES_ROOT at a temp dir with it: the lifecycle calls write nothing outside
// that root, and the operator's own ~/.hermes is never touched by the suite.
func fakeHermes(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fake-hermes")
	script := `#!/bin/sh
if [ -n "$HERMES_CALLS" ]; then printf '%s\n' "$*" >> "$HERMES_CALLS"; fi
eval "last=\${$#}"
# evalctl launches hermes with -p NAME chat ... so the verb is not $1: both the probe
# and the launch are recognised from the whole line.
case "$*" in
  *"chat --help"*)
    printf '%s\n' "--oneshot --query-file --format --in --yolo --max-turns"
    exit 0
    ;;
esac
case "$1" in
  profile)
    case "$2" in
      create)
        mkdir -p "$HERMES_ROOT/profiles/$3"
        printf 'description: fake sandbox\n' > "$HERMES_ROOT/profiles/$3/profile.yaml"
        rm -f "$HERMES_ROOT/profiles/.deleted/$3"
        ;;
      delete)
        rm -rf "$HERMES_ROOT/profiles/$last"
        mkdir -p "$HERMES_ROOT/profiles/.deleted"
        : > "$HERMES_ROOT/profiles/.deleted/$last"
        ;;
    esac
    ;;
esac
case "$*" in
  *" chat "*)
    printf '{"type":"result","text":"ok","duration_ms":5,"tokens":{"total":10}}\n'
    ;;
esac
exit 0
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// testProfile is the profile the runner tests measure in. It is set through $EVALCTL_PROFILE, which
// is the same lever an operator has: nothing about it is compiled in.
const testProfile = "test-profile"

// writeContract is the real profile's SOUL.md — the contract every arm reads a copy of. It is the
// one hand-written input of a measurement, so a tree without it cannot provision an arm.
func writeContract(t *testing.T) {
	t.Helper()
	write(t, filepath.Join(os.Getenv("HERMES_ROOT"), "profiles", testProfile, "SOUL.md"), "# contract\n")
	// An arm signs in with what this profile carries; a profile with nothing cannot provision one.
	write(t, filepath.Join(os.Getenv("HERMES_ROOT"), "profiles", testProfile, ".env"), "EVALCTL_TEST_KEY=test\n")
}

// sandbox is the profile an arm of this run runs in, derived the way the tool derives it.
func sandbox(t *testing.T, r *Runner, arm string) string {
	t.Helper()
	return r.Profiles.Name(arm)
}

// hermeticProfiles points the runner at a fake hermes and a throwaway profile root, so a test that
// runs the real code path still writes nothing into ~/.hermes.
func hermeticProfiles(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "hermes")
	calls := filepath.Join(t.TempDir(), "hermes-calls.txt")
	t.Setenv("HERMES_ROOT", home)
	t.Setenv("EVALCTL_PROFILE", testProfile)
	t.Setenv("HERMES_BIN", fakeHermes(t))
	t.Setenv("HERMES_CALLS", calls)
	return calls
}

// calls returns the hermes invocations the fake recorded, one per line.
func hermesCalls(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	return splitLines(string(b))
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// countCall counts the recorded invocations that equal want.
func countCall(calls []string, want string) int {
	n := 0
	for _, c := range calls {
		if c == want {
			n++
		}
	}
	return n
}

// sawCall reports whether one recorded invocation starts with want.
func sawCall(calls []string, want string) bool {
	for _, c := range calls {
		if len(c) >= len(want) && c[:len(want)] == want {
			return true
		}
	}
	return false
}

// profileDir is where a sandbox lives under the test's HERMES_ROOT.
func profileDir(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(os.Getenv("HERMES_ROOT"), "profiles", name)
}

func mustNotExist(t *testing.T, path, what string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("%s is still on disk at %s", what, path)
	}
}

func mustExist(t *testing.T, path, what string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s is missing from %s: %v", what, path, err)
	}
}

// TestARunCreatesItsSandboxesAndRemovesThem is the lifetime rule: the arms are provisioned for the
// spend and gone when it ends. Two runs in a row therefore start from a clean room — the second one
// cannot inherit the first one's caches, sessions or skill copies — and nothing the run created is
// left in the profile list for the pickers and the kanban roster to offer as a worker.
func TestARunCreatesItsSandboxesAndRemovesThem(t *testing.T) {
	s := qualityStore(t, `{"skill_name":"dummy","evals":[
	  {"id":1,"slug":"d1","prompt":"Roll one.","expected_output":"a face","files":[],"assertions":["a"]}]}`)
	writeContract(t)
	calls := os.Getenv("HERMES_CALLS")

	// A run that grades is one operation and two passes: the judge must not remove the arms its own
	// run still owns, so each arm is deleted exactly once — at the end.
	r := New(s)
	err := r.RunQuality(context.Background(), Options{Lane: store.LaneOutput, Skills: []string{"dummy"},
		CaseIDs: []int{1}, Arms: AllArms, Isolate: true, NoVerify: true, Fill: true, Grade: true,
		Budget: DefaultBudget, Log: ActivityFull})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{sandbox(t, r, profile.ArmWith), sandbox(t, r, profile.ArmWithout)} {
		if !sawCall(hermesCalls(t, calls), "profile create "+name) {
			t.Fatalf("%s was never created — the run borrowed a sandbox it did not own", name)
		}
		if got := countCall(hermesCalls(t, calls), "profile delete -y "+name); got != 1 {
			t.Fatalf("%s was deleted %d times, want once at the end of the run: %v", name, got, hermesCalls(t, calls))
		}
		mustNotExist(t, profileDir(t, name), name)
	}
}

// TestKeepLeavesTheSandboxesBehind: `--keep` is the manual-inspection path, and it is the only way a
// sandbox outlives its run.
func TestKeepLeavesTheSandboxesBehind(t *testing.T) {
	s := qualityStore(t, `{"skill_name":"dummy","evals":[
	  {"id":1,"slug":"d1","prompt":"Roll one.","expected_output":"a face","files":[],"assertions":["a"]}]}`)
	writeContract(t)

	r := New(s)
	err := r.RunQuality(context.Background(), Options{Lane: store.LaneOutput, Skills: []string{"dummy"},
		CaseIDs: []int{1}, Arms: AllArms, Isolate: true, NoVerify: true, Fill: true,
		Budget: DefaultBudget, Log: ActivityFull, Keep: true})
	if err != nil {
		t.Fatal(err)
	}
	mustExist(t, profileDir(t, sandbox(t, r, profile.ArmWith)), "the kept with-skill sandbox")
	if _, err := r.Profiles.Remove(sandbox(t, r, profile.ArmWith), false); err != nil {
		t.Fatalf("a kept sandbox is not removable: %v", err)
	}
	mustNotExist(t, profileDir(t, sandbox(t, r, profile.ArmWith)), "the with sandbox after remove")
}

// TestAStandaloneGradeProvisionsItsOwnRoom: grading is its own spend, days after the run it reads,
// so it creates the judge's sandbox itself and removes it when the pass ends — otherwise the default
// cleanup would turn every `evalctl grade` into a failed call in a profile that is not there.
func TestAStandaloneGradeProvisionsItsOwnRoom(t *testing.T) {
	s := qualityStore(t, `{"skill_name":"dummy","evals":[
	  {"id":1,"slug":"d1","prompt":"Roll one.","expected_output":"a face","files":[],"assertions":["a"]}]}`)
	writeContract(t)

	r := New(s)
	if err := r.RunQuality(context.Background(), Options{Lane: store.LaneOutput, Skills: []string{"dummy"},
		CaseIDs: []int{1}, Arms: AllArms, Isolate: true, NoVerify: true, Fill: true,
		Budget: DefaultBudget, Log: ActivityFull}); err != nil {
		t.Fatal(err)
	}
	// The fake hermes cannot judge — its chat line is not a verdict — so the pass fails on the
	// verdict and not on the sandbox. That is exactly the case this test needs: a grade that fails
	// still owns and releases its room.
	_, _ = r.GradeIteration(context.Background(), "dummy", 1, GradeOptions{Budget: DefaultGradeBudget})
	last := hermesCalls(t, os.Getenv("HERMES_CALLS"))
	var created, removed bool
	for _, c := range last {
		if strings.HasPrefix(c, "profile create "+sandbox(t, r, profile.ArmWithout)+" --no") {
			created = true
		}
		if c == "profile delete -y "+sandbox(t, r, profile.ArmWithout) {
			removed = true
		}
	}
	if !created || !removed {
		t.Fatalf("the judge did not provision and release its own room: %v", last)
	}
	mustNotExist(t, profileDir(t, sandbox(t, r, profile.ArmWithout)), "the judge's sandbox after the pass")
}
