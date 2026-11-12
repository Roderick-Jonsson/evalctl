package cli

import (
	"sort"
	"strings"
	"testing"
)

// TestCompletionScripts: each shell gets a script that calls the callback, on stdout, exit 0.
func TestCompletionScripts(t *testing.T) {
	for _, shell := range completionShells {
		code, out, errOut := capture(t, func() int { return Run([]string{"completion", shell}) })
		if code != 0 {
			t.Errorf("completion %s exited %d", shell, code)
		}
		if errOut != "" {
			t.Errorf("completion %s wrote to stderr: %q", shell, errOut)
		}
		if !strings.Contains(out, "__complete") {
			t.Errorf("completion %s does not call __complete", shell)
		}
		if !strings.Contains(out, "evalctl") {
			t.Errorf("completion %s never names the command", shell)
		}
	}
	// A shell it does not know, and no shell at all, are refusals that name the choices.
	if code, _, errOut := capture(t, func() int { return Run([]string{"completion", "tcsh"}) }); code != 2 ||
		!strings.Contains(errOut, "bash, zsh, fish") {
		t.Errorf("completion tcsh: exit %d, %q", code, errOut)
	}
	if code, _, errOut := capture(t, func() int { return Run([]string{"completion"}) }); code != 2 ||
		!strings.Contains(errOut, "bash|zsh|fish") {
		t.Errorf("completion with no shell: exit %d, %q", code, errOut)
	}
}

// TestCompletionCandidates: the callback answers each shape of the line, and never fails — a
// non-zero exit would paint an error across the user's command line.
func TestCompletionCandidates(t *testing.T) {
	dir := scratchTree(t)
	t.Setenv("EVALCTL_ROOT", dir)

	values := func(args ...string) []string {
		code, out, errOut := capture(t, func() int { return Run(append([]string{"__complete"}, args...)) })
		if code != 0 {
			t.Fatalf("__complete %v exited %d: %s", args, code, errOut)
		}
		if errOut != "" {
			t.Fatalf("__complete %v wrote to stderr: %q", args, errOut)
		}
		var vs []string
		for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
			if line == "" {
				continue
			}
			vs = append(vs, strings.SplitN(line, "\t", 2)[0])
		}
		return vs
	}
	has := func(vs []string, want ...string) bool {
		set := map[string]bool{}
		for _, v := range vs {
			set[v] = true
		}
		for _, w := range want {
			if !set[w] {
				return false
			}
		}
		return true
	}

	verbs := values("")
	if !has(verbs, "init", "adopt", "ls", "run", "profiles", "completion", "help") {
		t.Errorf("the command list is missing verbs: %v", verbs)
	}
	seen := map[string]bool{}
	for _, v := range verbs {
		if seen[v] {
			t.Errorf("%q is offered twice", v)
		}
		seen[v] = true
	}

	if got := values("l"); !has(got, "ls", "lab") {
		t.Errorf("prefix l gave %v", got)
	}
	for _, v := range values("l") {
		if !strings.HasPrefix(v, "l") {
			t.Errorf("prefix l offered %q", v)
		}
	}

	flags := values("ls", "-")
	for _, f := range flags {
		if !strings.HasPrefix(f, "--") {
			t.Errorf("flag completion offered %q", f)
		}
	}
	if !has(flags, "--lane", "--json", "--root", "--theme", "--help") {
		t.Errorf("ls flags are missing: %v", flags)
	}

	if got := values("ls", "--lane", ""); !has(got, "trigger", "output", "both") {
		t.Errorf("--lane values: %v", got)
	}
	if got := values("run", "--arms", ""); !has(got, "with_skill", "without_skill") {
		t.Errorf("--arms values: %v", got)
	}
	if got := values("run", "--skill", ""); !has(got, "demo") {
		t.Errorf("--skill did not offer the tree's skills: %v", got)
	}
	if got := values("profiles", ""); !has(got, "status", "check", "ensure", "attach") {
		t.Errorf("profiles sub-verbs: %v", got)
	}
	if got := values("theme", ""); !has(got, "auto", "midnight", "daylight") {
		t.Errorf("theme names: %v", got)
	}
	if got := values("completion", ""); !has(got, "bash", "zsh", "fish") {
		t.Errorf("completion shells: %v", got)
	}
	// Unknown words are silence, not an error.
	if code, out, _ := capture(t, func() int { return Run([]string{"__complete", "nonsense", "--bogus"}) }); code != 0 || out != "" {
		t.Errorf("unexpected words: exit %d, output %q", code, out)
	}
}

// TestCompletionKnowsEveryFlag: verbFlags stands in for the FlagSets a callback cannot build, so it
// has to agree with the page — which is generated from the FlagSet itself. A flag added to a verb
// without a line here fails this test rather than silently vanishing from the shell.
func TestCompletionKnowsEveryFlag(t *testing.T) {
	globals := map[string]bool{"root": true, "theme": true, "no-input": true, "help": true}
	for verb := range helpAliases {
		if canonicalVerb(verb) != verb {
			continue // aliases resolve to the verb they name
		}
		printed := map[string]bool{}
		_, out, _ := capture(t, func() int { return Run([]string{verb, "-h"}) })
		for _, line := range strings.Split(out, "\n") {
			// A flag row starts at the second column; a wrapped description's continuation line
			// starts further in and may well begin with a flag name of its own.
			if !strings.HasPrefix(line, "  --") {
				continue
			}
			name := strings.TrimPrefix(strings.Fields(strings.TrimSpace(line))[0], "--")
			if globals[name] {
				continue
			}
			printed[name] = true
		}
		declared := map[string]bool{}
		for _, f := range flagsFor(verb) {
			declared[f] = true
		}
		var missing, extra []string
		for f := range printed {
			if !declared[f] {
				missing = append(missing, f)
			}
		}
		for f := range declared {
			if !printed[f] {
				extra = append(extra, f)
			}
		}
		sort.Strings(missing)
		sort.Strings(extra)
		if len(missing) > 0 {
			t.Errorf("%s: completion is missing %v", verb, missing)
		}
		if len(extra) > 0 {
			t.Errorf("%s: completion lists flags the verb does not have: %v", verb, extra)
		}
	}
}
