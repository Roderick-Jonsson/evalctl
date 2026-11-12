package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
)

// capture runs fn with this process's stdout and stderr pointed at pipes, and returns the exit
// code with both streams. Help and errors are written to the real process streams, so this is the
// only way to assert where a line went.
func capture(t *testing.T, fn func() int) (int, string, string) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW

	var wg sync.WaitGroup
	var outBuf, errBuf strings.Builder
	read := func(r *os.File, into *strings.Builder) {
		defer wg.Done()
		b, _ := io.ReadAll(r)
		into.Write(b)
	}
	wg.Add(2)
	go read(outR, &outBuf)
	go read(errR, &errBuf)

	code := fn()

	os.Stdout, os.Stderr = oldOut, oldErr
	outW.Close()
	errW.Close()
	wg.Wait()
	outR.Close()
	errR.Close()
	return code, outBuf.String(), errBuf.String()
}

// TestEveryVerbHasItsOwnPage: -h is answered by every verb, goes to stdout, exits 0, and carries a
// description, the flag list and the exit codes. A verb that forgets one is a verb whose page is
// the flag package's raw dump again.
func TestEveryVerbHasItsOwnPage(t *testing.T) {
	seen := map[string]bool{}
	var verbs []string
	for _, canonical := range helpAliases {
		if !seen[canonical] {
			seen[canonical] = true
			verbs = append(verbs, canonical)
		}
	}
	sort.Strings(verbs)
	verbs = append(verbs, "help") // the top-level page is the full usage block

	for _, verb := range verbs {
		if verb == "help" {
			code, out, errOut := capture(t, func() int { return Run([]string{"help"}) })
			if code != 0 || !strings.Contains(out, "evalctl — manage") || errOut != "" {
				t.Errorf("help: exit %d, stderr %q", code, errOut)
			}
			continue
		}
		code, out, errOut := capture(t, func() int { return Run([]string{verb, "-h"}) })
		if code != 0 {
			t.Errorf("%s -h exited %d, want 0", verb, code)
		}
		if errOut != "" {
			t.Errorf("%s -h wrote %d bytes to stderr; help belongs on stdout", verb, len(errOut))
		}
		for _, want := range []string{"evalctl " + verb + " — ", "USAGE", "EXAMPLES", "GLOBAL FLAGS", "--root", "EXIT CODES", "LEARN MORE"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s -h output is missing %q", verb, want)
			}
		}
		if strings.Contains(out, "\x1b[") {
			t.Errorf("%s -h emitted ANSI escapes into a pipe", verb)
		}
		// Every flag a verb registers is filed under a group it declares: an unfiled one shows up
		// as "OTHER FLAGS", which is how a flag added without a home gets noticed.
		if strings.Contains(out, "OTHER FLAGS") {
			t.Errorf("%s -h has an unfiled flag (OTHER FLAGS section)", verb)
		}
	}
}

// TestHelpPagesFitTheLayout: no line runs past the layout width and none contains a tab. Both are
// what made the stdlib dump hard to scan — a tab-indented second line per flag, and descriptions
// that ran to the terminal's edge.
func TestHelpPagesFitTheLayout(t *testing.T) {
	for _, verb := range []string{"ls", "run", "promote", "profiles", "help"} {
		args := []string{verb, "-h"}
		if verb == "help" {
			args = []string{"help"}
		}
		code, out, _ := capture(t, func() int { return Run(args) })
		if code != 0 {
			t.Fatalf("%s: exit %d", verb, code)
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "\t") {
				t.Errorf("%s -h: tab in %q", verb, line)
			}
			if n := len([]rune(line)); n > 110 {
				t.Errorf("%s -h: %d columns — %q", verb, n, line)
			}
		}
	}
}

// TestColumnsIsHonoured: a pipe has no width to ask for, but the caller still has one in mind —
// `COLUMNS=72 evalctl ls -h` must fit 72 columns, the way uv wraps to it.
func TestColumnsIsHonoured(t *testing.T) {
	for _, width := range []string{"56", "72", "96"} {
		t.Setenv("COLUMNS", width)
		limit := 0
		fmt.Sscanf(width, "%d", &limit)
		for _, args := range [][]string{{"ls", "-h"}, {"run", "-h"}, {"rm", "-h"}, {"profiles", "-h"}, {"promote", "--help"}, {"help"}} {
			_, out, _ := capture(t, func() int { return Run(args) })
			for _, line := range strings.Split(out, "\n") {
				if n := len([]rune(line)); n > limit {
					t.Errorf("%s: %d columns under COLUMNS=%s: %q", strings.Join(args, " "), n, width, line)
				}
			}
		}
	}
}

// TestChoicesAreOnThePageAndInTheError: one table, two surfaces. A verb that declares allowed
// values must print them, and must refuse a value outside them naming the same list.
func TestChoicesAreOnThePageAndInTheError(t *testing.T) {
	for verb, flags := range flagChoices {
		_, out, _ := capture(t, func() int { return Run([]string{verb, "-h"}) })
		for name, values := range flags {
			want := "[possible values: " + strings.Join(values, ", ") + "]"
			if !strings.Contains(out, want) {
				t.Errorf("%s -h does not print %q", verb, want)
			}
			if err := requireOneOf(verb, name, "definitely-not-a-value"); err == nil {
				t.Errorf("requireOneOf(%s, %s) accepted a bogus value", verb, name)
				continue
			}
			msg := requireOneOf(verb, name, "definitely-not-a-value").Error()
			for _, v := range values {
				if !strings.Contains(msg, v) {
					t.Errorf("%s: the refusal does not name %q: %s", verb, v, msg)
				}
			}
			if err := requireOneOf(verb, name, values[0]); err != nil {
				t.Errorf("%s: %q was refused: %v", verb, values[0], err)
			}
		}
	}
}

// TestLongHelpCarriesTheDetails: `-h` is the page, `--help` is the page plus the prose that used to
// make it a wall — and each points at the other.
func TestLongHelpCarriesTheDetails(t *testing.T) {
	shortCode, short, _ := capture(t, func() int { return Run([]string{"run", "-h"}) })
	longCode, long, _ := capture(t, func() int { return Run([]string{"run", "--help"}) })
	if shortCode != 0 || longCode != 0 {
		t.Fatalf("exits %d and %d", shortCode, longCode)
	}
	if strings.Contains(short, "DETAILS") {
		t.Error("-h printed the DETAILS section")
	}
	if !strings.Contains(short, "the longer notes: evalctl run --help") {
		t.Error("-h does not point at --help")
	}
	if !strings.Contains(long, "DETAILS") || !strings.Contains(long, "Sandboxes are created by the run") {
		t.Error("--help did not print the details")
	}
	if !strings.Contains(long, "shorter page: evalctl run -h") {
		t.Error("--help does not point back at -h")
	}
	// A verb with no details still has one page, and it does not advertise a longer one.
	_, plain, _ := capture(t, func() int { return Run([]string{"stats", "-h"}) })
	if strings.Contains(plain, "the longer notes:") {
		t.Error("stats -h promises a longer page it does not have")
	}
}

// TestRunFlagsAreGroupedByWhatTheyDo: the two lanes' flags are interleaved alphabetically on the
// FlagSet, and that is exactly the order a reader cannot use. Each flag must sit under the heading
// that says what it is for.
func TestRunFlagsAreGroupedByWhatTheyDo(t *testing.T) {
	_, out, _ := capture(t, func() int { return Run([]string{"run", "-h"}) })
	group := func(title string) string {
		start := strings.Index(out, title)
		if start < 0 {
			t.Fatalf("no %s section", title)
		}
		rest := out[start+len(title):]
		if end := strings.Index(rest, "\n\n"); end >= 0 {
			return rest[:end]
		}
		return rest
	}
	for title, flags := range map[string][]string{
		"WHICH ROWS":     {"--skill", "--case", "--query", "--split", "--iteration"},
		"EXECUTION":      {"--arms", "--budget", "--max-turns", "--runs", "--isolate"},
		"RESUMING":       {"--resume", "--force", "--fill"},
		"GRADING":        {"--grade", "--no-grade", "--threshold"},
		"WHAT IT PRINTS": {"--list", "--dry-run", "--log"},
	} {
		body := group(title)
		for _, f := range flags {
			if !strings.Contains(body, f+" ") {
				t.Errorf("%s is not under %s", f, title)
			}
		}
	}
}

// TestHelpVerbIsTheFlag: `help <verb>` and `<verb> -h` are one code path, so they cannot drift.
func TestHelpVerbIsTheFlag(t *testing.T) {
	codeA, wantOut, _ := capture(t, func() int { return Run([]string{"ls", "-h"}) })
	codeB, gotOut, gotErr := capture(t, func() int { return Run([]string{"help", "ls"}) })
	if codeA != 0 || codeB != 0 {
		t.Fatalf("exits %d and %d, want 0", codeA, codeB)
	}
	if gotOut != wantOut {
		t.Errorf("help ls differs from ls -h:\n--- ls -h ---\n%s\n--- help ls ---\n%s", wantOut, gotOut)
	}
	if gotErr != "" {
		t.Errorf("help ls wrote to stderr: %q", gotErr)
	}
	// An alias resolves to the same page.
	_, aliasOut, _ := capture(t, func() int { return Run([]string{"help", "list"}) })
	if aliasOut != wantOut {
		t.Error("help list is not the ls page")
	}
}

// TestUnknownVerbSuggestsTheNearest: a misspelled verb names its neighbour instead of dumping usage.
func TestUnknownVerbSuggestsTheNearest(t *testing.T) {
	code, out, errOut := capture(t, func() int { return Run([]string{"stat"}) })
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if out != "" {
		t.Errorf("stderr-only error wrote %d bytes to stdout", len(out))
	}
	for _, want := range []string{`unknown command "stat"`, "did you mean: evalctl stats", "evalctl help"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("error is missing %q: %q", want, errOut)
		}
	}
}

// TestUnknownFlagPointsAtTheVerbsPage: the pointer names the verb that was run, not "<verb>".
func TestUnknownFlagPointsAtTheVerbsPage(t *testing.T) {
	code, _, errOut := capture(t, func() int { return Run([]string{"ls", "--nonsense"}) })
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "try: evalctl ls -h") {
		t.Errorf("error does not name the verb: %q", errOut)
	}
}

// scratchTree makes a starter tree and returns its root.
func scratchTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if code, _, errOut := capture(t, func() int {
		return runInit([]string{"--dir", dir, "--skill", "demo"})
	}); code != 0 {
		t.Fatalf("init exited %d: %s", code, errOut)
	}
	return dir
}

// TestConfirmRefusesWhenNobodyCanAnswer: with stdin not a terminal there is no one to ask, so the
// command refuses and says which flag to pass. It used to read EOF, print "aborted" and exit 0 —
// which a script reads as success.
func TestConfirmRefusesWhenNobodyCanAnswer(t *testing.T) {
	dir := scratchTree(t)
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	old := os.Stdin
	os.Stdin = devnull
	defer func() { os.Stdin = old }()

	code, out, errOut := capture(t, func() int {
		return runRemove([]string{"--root", dir, "--all", "--lane", "trigger"})
	})
	if code != 2 {
		t.Errorf("exit %d, want 2 (refused)", code)
	}
	if !strings.Contains(errOut, "--yes") {
		t.Errorf("the refusal must name --yes: %q", errOut)
	}
	if strings.Contains(out, "[y/N]") {
		t.Error("the prompt was written to stdout")
	}
	// Nothing was deleted: the tree still has its four trigger rows.
	if code, lsOut, _ := capture(t, func() int { return runList([]string{"--root", dir, "--lane", "trigger"}) }); code != 0 || strings.Contains(lsOut, "no rows match") {
		t.Error("the refused delete removed rows")
	}
}

// TestNoInputNeverPrompts: --no-input is global and answers the same way as a pipe.
func TestNoInputNeverPrompts(t *testing.T) {
	dir := scratchTree(t)
	code, _, errOut := capture(t, func() int {
		return Run([]string{"rm", "--root", dir, "--all", "--lane", "trigger", "--no-input"})
	})
	if code != 2 || !strings.Contains(errOut, "--yes") {
		t.Fatalf("exit %d, stderr %q — want a refusal naming --yes", code, errOut)
	}
	// And --yes still does the work.
	if code, _, errOut := capture(t, func() int {
		return runRemove([]string{"--root", dir, "--all", "--lane", "trigger", "--yes"})
	}); code != 0 {
		t.Fatalf("rm --yes exited %d: %s", code, errOut)
	}
}

// TestValidateFindingsHaveTheirOwnExitCode: 3 means "the tree breaks the runner's rules", which is
// a different answer from 2 ("the command could not be parsed").
func TestValidateFindingsHaveTheirOwnExitCode(t *testing.T) {
	dir := scratchTree(t)
	if code, _, errOut := capture(t, func() int {
		return runRemove([]string{"--root", dir, "--all", "--lane", "trigger", "--yes"})
	}); code != 0 {
		t.Fatalf("rm exited %d: %s", code, errOut)
	}
	if code, _, _ := capture(t, func() int { return runValidate([]string{"--root", dir}) }); code != 3 {
		t.Errorf("validate with findings exited %d, want 3", code)
	}
	if code, _, _ := capture(t, func() int { return runValidate([]string{"--root", dir, "--bogus"}) }); code != 2 {
		t.Errorf("validate with a bad flag exited %d, want 2", code)
	}
}

// TestSkillMustExist: a --skill that names nothing is a refusal, not an empty table with exit 0.
func TestSkillMustExist(t *testing.T) {
	dir := scratchTree(t)
	code, _, errOut := capture(t, func() int { return runList([]string{"--root", dir, "--skill", "nope"}) })
	if code != 2 {
		t.Errorf("ls --skill nope exited %d, want 2 (%s)", code, errOut)
	}
	if !strings.Contains(errOut, "known: demo") {
		t.Errorf("the refusal does not list the known skills: %q", errOut)
	}
}

// TestGlobalFlagBeforeAPositionalVerb: `--root` in front of `theme` is a flag, not a palette name.
func TestGlobalFlagBeforeAPositionalVerb(t *testing.T) {
	dir := scratchTree(t)
	code, out, errOut := capture(t, func() int {
		return Run([]string{"--root", dir, "theme"})
	})
	if code != 0 {
		t.Fatalf("theme after --root exited %d: %s", code, errOut)
	}
	if !strings.Contains(out, "themes") {
		t.Errorf("theme did not list the palettes: %q", out)
	}
	if code, _, errOut := capture(t, func() int {
		return Run([]string{"--root", dir, "theme", "daylight", "extra"})
	}); code != 2 || !strings.Contains(errOut, "unexpected argument") {
		t.Errorf("theme with two names exited %d (%s), want a usage error", code, errOut)
	}
}

// TestStrayPositionalIsRefused: `evalctl ls extra` is a typo or a flag missing its dash; silently
// listing everything is the wrong answer to both.
func TestStrayPositionalIsRefused(t *testing.T) {
	dir := scratchTree(t)
	if code, _, errOut := capture(t, func() int { return runList([]string{"--root", dir, "extra"}) }); code != 2 {
		t.Errorf("ls extra exited %d (%s), want 2", code, errOut)
	}
	if code, _, errOut := capture(t, func() int { return runStats([]string{"--root", dir, "foo"}) }); code != 2 {
		t.Errorf("stats foo exited %d (%s), want 2", code, errOut)
	}
}
