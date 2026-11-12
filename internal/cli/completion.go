package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"evalctl/internal/store"
	"evalctl/internal/theme"
)

// Shell completion, the way gh and uv ship it: a script the shell sources once, and a hidden verb it
// calls back into. The callback is the half that knows the tree — `--skill <TAB>` offers this
// project's skills — while the script stays a fixed five lines that never needs updating.
//
// The scripts are written to stdout, so they install with a redirect and no side effects:
//
//	eval "$(evalctl completion bash)"                  # in ~/.bashrc
//	eval "$(evalctl completion zsh)"                   # in ~/.zshrc
//	evalctl completion fish > ~/.config/fish/completions/evalctl.fish

const bashCompletion = `# evalctl completion for bash. Add to ~/.bashrc:
#   eval "$(evalctl completion bash)"
_evalctl() {
    local cur="${COMP_WORDS[COMP_CWORD]}"
    local -a words=("${COMP_WORDS[@]:1}")
    local line
    line=$(evalctl __complete "${words[@]}" 2>/dev/null | cut -f1)
    # shellcheck disable=SC2207
    COMPREPLY=($(compgen -W "${line}" -- "${cur}"))
}
complete -F _evalctl evalctl
`

const zshCompletion = `#compdef evalctl
# evalctl completion for zsh. Add to ~/.zshrc:
#   eval "$(evalctl completion zsh)"
_evalctl() {
    local -a cands vals
    local c
    cands=("${(@f)$(evalctl __complete ${words[2,-1]} 2>/dev/null)}")
    for c in "${cands[@]}"; do
        [[ -n "$c" ]] && vals+=("${c%%$'\t'*}")
    done
    compadd -a vals
}
compdef _evalctl evalctl
`

const fishCompletion = `# evalctl completion for fish. Install:
#   evalctl completion fish > ~/.config/fish/completions/evalctl.fish
function __evalctl_complete
    set -l tokens (commandline -opc)
    set -l cur (commandline -ct)
    evalctl __complete $tokens[2..-1] $cur 2>/dev/null
end
complete -c evalctl -f -a '(__evalctl_complete)'
`

// completionShells is the allowed set for the one positional this verb takes.
var completionShells = []string{"bash", "zsh", "fish"}

// runCompletion prints the script for a shell.
func runCompletion(args []string) int {
	fs, _ := newFS("completion")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return fail(usageErr{fmt.Errorf("usage: evalctl completion %s", strings.Join(completionShells, "|"))})
	}
	switch rest[0] {
	case "bash":
		fmt.Print(bashCompletion)
	case "zsh":
		fmt.Print(zshCompletion)
	case "fish":
		fmt.Print(fishCompletion)
	default:
		return fail(usageErr{fmt.Errorf("--shell %q is not one of: %s",
			rest[0], strings.Join(completionShells, ", "))})
	}
	return 0
}

// profileSubverbs are the second word of `evalctl profiles …`.
var profileSubverbs = []struct{ name, what string }{
	{"status", "the status table"},
	{"check", "exit 1 if anything drifted"},
	{"ensure", "create and provision the sandboxes"},
	{"reap", "remove what a killed run left behind"},
	{"render", "copy the real SOUL.md into the arms"},
	{"sync", "bring a profile's skills to the tree's state"},
	{"scrub", "wipe sandbox memories"},
	{"attach", "name the tree a profile is measured from"},
	{"link", "make a profile scan this folder"},
	{"unlink", "undo that"},
}

// runComplete answers the shell's callback. It never fails: a completion that prints an error is a
// completion the shell has to paint over the user's command line, so every path here ends in a
// candidate list and exit 0.
func runComplete(args []string) int {
	if len(args) == 0 {
		args = []string{""}
	}
	partial := args[len(args)-1]
	before := args[:len(args)-1]
	verb := firstWord(before)

	out := candidates(verb, before, partial)
	for _, c := range out {
		if c.desc != "" {
			fmt.Fprintf(os.Stdout, "%s\t%s\n", c.value, c.desc)
			continue
		}
		fmt.Fprintln(os.Stdout, c.value)
	}
	return 0
}

// candidate is one completion, with the description fish shows beside it.
type candidate struct{ value, desc string }

// firstWord is the verb being completed: the first argument that is not a flag or a flag's value.
func firstWord(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			if !strings.Contains(a, "=") && flagTakesValue(a) && i+1 < len(args) {
				i++
			}
			continue
		}
		return a
	}
	return ""
}

// flagTakesValue reports whether a global flag consumes the next word. Only the globals matter
// here: a verb's flags come after its name, so the verb is already known by then.
func flagTakesValue(flag string) bool {
	switch strings.TrimLeft(flag, "-") {
	case "root", "theme":
		return true
	}
	return false
}

// candidates is the whole policy of the callback, in the order the decision is made: a flag's
// value, then the command list, then a verb's flags, then a namespace's second word.
func candidates(verb string, before []string, partial string) []candidate {
	if name := valueFlag(before, verb); name != "" {
		return prefixFilter(valuesFor(verb, name), partial)
	}
	if verb == "" {
		return prefixFilter(verbCandidates(), partial)
	}
	if strings.HasPrefix(partial, "-") {
		return prefixFilter(flagCandidates(verb), partial)
	}
	// The second word of a verb that is a namespace. An empty partial after `profiles` means the
	// user has typed `evalctl profiles ` and wants the sub-verbs.
	switch canonicalOrSelf(verb) {
	case "profiles":
		if len(before) == 1 {
			return prefixFilter(profileCandidates(), partial)
		}
	case "theme":
		if len(before) == 1 {
			return prefixFilter(themeCandidates(), partial)
		}
	case "completion":
		if len(before) == 1 {
			var out []candidate
			for _, sh := range completionShells {
				out = append(out, candidate{sh, "a completion script"})
			}
			return prefixFilter(out, partial)
		}
	case "help":
		if len(before) == 1 {
			return prefixFilter(verbCandidates(), partial)
		}
	}
	return nil
}

// valueFlag names the flag whose value the cursor is on, or "" when the cursor is on a word of its
// own. Only a flag with something to offer is returned, so a boolean never swallows the next word.
func valueFlag(before []string, verb string) string {
	if len(before) == 0 {
		return ""
	}
	last := before[len(before)-1]
	if !strings.HasPrefix(last, "-") || strings.Contains(last, "=") {
		return ""
	}
	name := strings.TrimLeft(last, "-")
	if len(valuesFor(verb, name)) == 0 {
		return ""
	}
	return name
}

func lastWord(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[len(args)-1]
}

// valuesFor is what a flag can be set to: the tree's skills, the palettes, or its declared choices.
func valuesFor(verb, name string) []candidate {
	key := canonicalOrSelf(verb)
	switch name {
	case "theme", "colour", "color":
		return themeCandidates()
	case "skill", "skills":
		var out []candidate
		for _, sk := range completionSkills() {
			out = append(out, candidate{sk, ""})
		}
		return out
	}
	if !hasFlag(key, name) {
		return nil
	}
	choices := flagChoices[key][name]
	if len(choices) == 0 && key == "trigger" {
		choices = flagChoices["run"][name]
	}
	var out []candidate
	for _, c := range choices {
		out = append(out, candidate{c, ""})
	}
	return out
}

// flagsFor is a verb's flag names. `trigger` has no entry of its own: it is `run` with the lane
// chosen, and it shares that verb's flag set.
func flagsFor(verb string) []string {
	if verb == "trigger" {
		return verbFlags["run"]
	}
	return verbFlags[verb]
}

// hasFlag reports whether a verb registers a flag: the completion table is the only list a callback
// can consult without running the verb.
func hasFlag(verb, name string) bool {
	for _, f := range flagsFor(verb) {
		if f == name {
			return true
		}
	}
	return false
}

func canonicalOrSelf(v string) string {
	if c := canonicalVerb(v); c != "" {
		return c
	}
	return v
}

// completionSkills is the tree's stage skills. A completion runs in a shell with no tree open as
// often as with one, so a store that will not open simply offers nothing.
func completionSkills() []string {
	root := os.Getenv("EVALCTL_ROOT")
	if root == "" {
		root = "."
	}
	s, err := store.Open(root)
	if err != nil || s == nil {
		return nil
	}
	return s.SkillNames()
}

// verbCandidates is the command list in the order the top-level page prints it.
func verbCandidates() []candidate {
	seen := map[string]bool{}
	var out []candidate
	add := func(value, desc string) {
		if seen[value] {
			return
		}
		seen[value] = true
		out = append(out, candidate{value, desc})
	}
	for _, g := range indexGroups {
		for _, e := range g.entries {
			fields := strings.Fields(e.cmd)
			if len(fields) < 2 {
				continue
			}
			add(fields[1], e.what)
		}
	}
	add("tui", "the workbench, step by step")
	add("version", "which build this is")
	add("help", "this page, or one verb's page")
	return out
}

func profileCandidates() []candidate {
	var out []candidate
	for _, sv := range profileSubverbs {
		out = append(out, candidate{sv.name, sv.what})
	}
	return out
}

func themeCandidates() []candidate {
	out := []candidate{{"auto", "pick by background"}}
	for _, p := range theme.Palettes() {
		kind := "dark"
		if p.Light {
			kind = "light"
		}
		out = append(out, candidate{p.Name, "on a " + kind + " background"})
	}
	return out
}

// flagCandidates is a verb's flags plus the four globals, in page order.
func flagCandidates(verb string) []candidate {
	key := canonicalVerb(verb)
	if key == "" {
		key = verb
	}
	var out []candidate
	for _, name := range flagsFor(key) {
		out = append(out, candidate{"--" + name, ""})
	}
	for _, g := range globalFlags {
		out = append(out, candidate{"--" + g.name, g.usage})
	}
	return out
}

// prefixFilter keeps the candidates that start with what has been typed, and sorts them so a menu
// does not depend on map order.
func prefixFilter(in []candidate, prefix string) []candidate {
	out := make([]candidate, 0, len(in))
	for _, c := range in {
		if prefix == "" || strings.HasPrefix(c.value, prefix) {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].value < out[j].value })
	return out
}

// verbFlags is the flag names each verb registers. It exists because a completion callback cannot
// build a verb's FlagSet without running the verb; TestCompletionKnowsEveryFlag compares this table
// against the flags each page actually prints, so a flag added without a line here fails the build
// rather than silently vanishing from completion.
var verbFlags = map[string][]string{
	"init":       {"skill", "dir", "profile", "force"},
	"adopt":      {"dir", "profile", "link", "skills"},
	"ls":         {"lane", "skill", "label", "split", "search", "in", "sort", "group", "dupes", "json", "width"},
	"show":       {"lane", "json"},
	"add":        {"lane", "id", "skill", "query", "why", "split", "label", "slug", "prompt", "expected", "files", "assertions"},
	"edit":       {"lane", "id", "skill", "query", "why", "split", "label", "slug", "prompt", "expected", "files", "assertions", "dry-run", "yes"},
	"rm":         {"lane", "skill", "label", "split", "search", "dupes", "all", "with-fixture", "dry-run", "yes"},
	"mv":         {"lane", "to-skill"},
	"promote":    {"from", "workspace", "skill", "slug", "id", "prompt", "expected", "to-references", "file", "as", "all", "force", "to-trigger", "label", "split", "why", "dry-run"},
	"dupes":      {"lane", "threshold", "json"},
	"stats":      {"json"},
	"validate":   {"strict", "json"},
	"renumber":   {"lane", "yes"},
	"lab":        {"skill", "lane", "json"},
	"describe":   {"skill", "candidate", "model", "budget", "dry-run", "write", "json"},
	"grade":      {"skill", "iteration", "case", "model", "budget", "keep", "force", "dry-run", "json"},
	"revise":     {"skill", "iteration", "model", "budget", "keep", "apply", "dry-run"},
	"run":        {"skill", "model", "budget", "resume", "force", "fill", "dry-run", "list", "keep", "iteration", "lane", "log", "case", "arms", "no-verify", "grade", "no-grade", "isolate", "query", "split", "runs", "threshold", "max-turns"},
	"runs":       {"json", "lane", "skill"},
	"report":     {"lane", "raw", "width"},
	"profiles":   {"skills", "dry-run", "json", "real"},
	"theme":      {},
	"skills":     {"json"},
	"tui":        {"lane", "skill", "glyphs"},
	"version":    {"json", "short"},
	"completion": {},
}
