package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"golang.org/x/term"
)

// Every verb has three things worth reading: what it is for, a grammar, and the examples an
// operator copies. The flag list is deliberately not written here — it is read off the very
// FlagSet the verb parses, so a flag cannot be documented in one place and required in another,
// and `-h` can never fall behind the code. What is written here is the *order*: a flag's group and
// position, because alphabetical is the one order that never says what belongs with what.
type helpDoc struct {
	summary  string
	usage    []string
	examples []string
}

// helpDocs is the prose half of the CLI's documentation, one entry per canonical verb. Usage lines
// state the grammar, not the flag inventory: the inventory below them is read off the FlagSet, so
// repeating it here only creates a second place to fall out of date.
var helpDocs = map[string]helpDoc{
	"init": {
		summary: "start a project: evalctl.json and one skill holding both eval sets",
		usage:   []string{"evalctl init [--skill NAME] [--dir DIR] [--profile P] [--force]"},
		examples: []string{
			"evalctl init --skill slide-builder        # in this folder",
			"evalctl init --dir ~/code/evals --skill x --profile default",
		},
	},
	"adopt": {
		summary: "declare a folder that already holds skills: add the sets each skill is missing",
		usage:   []string{"evalctl adopt [--dir DIR] [--skills a,b] [--profile P] [--link]"},
		examples: []string{
			"evalctl adopt --dir .                     # never replaces a file that is already there",
			"evalctl adopt --profile default --link    # and make that profile see this folder",
		},
	},
	"ls": {
		summary: "list rows from either lane, as one table",
		usage:   []string{"evalctl ls [--lane trigger|output|both] [--skill S] [flags]"},
		examples: []string{
			"evalctl ls --lane trigger --label no --split train",
			"evalctl ls --skill slide-builder --json",
			"evalctl ls --dupes --sort skill,id:desc",
		},
	},
	"show": {
		summary: "one row in full: the prompt, the expected output, the assertions, the fixture",
		usage:   []string{"evalctl show <ref> [--lane trigger|output] [--json]"},
		examples: []string{
			"evalctl show t12                          # a trigger query",
			"evalctl show o:slide-builder:3             # an output case, by skill",
		},
	},
	"add": {
		summary: "create a row in either lane",
		usage: []string{
			"evalctl add --lane trigger --skill S --query <text|-> --label yes|no --split S [flags]",
			"evalctl add --lane output  --skill S --slug NAME [flags]",
		},
		examples: []string{
			"evalctl add --lane trigger --skill source-research --label no --split train --query -",
			"evalctl add --lane output --skill slide-builder --slug sb-4-x --prompt \"...\" --expected \"...\"",
		},
	},
	"edit": {
		summary: "change only the flags you pass",
		usage:   []string{"evalctl edit <ref> [--field value ...] [--id N [--yes]] [--dry-run]"},
		examples: []string{
			"evalctl edit t12 --split validation",
			"evalctl edit o:slide-builder:3 --slug sb-3-dense-item-renamed   # moves the fixture dir",
			"evalctl edit o:slide-builder:3 --id 9                            # says what cites #3 first",
		},
	},
	"rm": {
		summary: "delete rows by ref, by filter, or by lane",
		usage: []string{
			"evalctl rm <ref...> [--with-fixture] [--dry-run] [--yes]",
			"evalctl rm [--lane L] [--skill S] [--label yes|no] [--split …] [--search Q] [--dupes]",
			"evalctl rm --all --lane trigger|output",
		},
		examples: []string{
			"evalctl rm t6 t7 t9 --yes",
			"evalctl rm --lane trigger --dupes --dry-run    # the list, nothing written",
			"evalctl rm o:slide-builder:2 --with-fixture --yes",
		},
	},
	"mv": {
		summary: "move an output case, and its fixture, to another stage skill",
		usage:   []string{"evalctl mv <ref> --to-skill S [--lane output] [--dry-run]"},
		examples: []string{
			"evalctl mv o:slide-builder:3 --to-skill av-production",
		},
	},
	"promote": {
		summary: "a run's output becomes a case, a reference, or a trigger row",
		usage: []string{
			"evalctl promote --from <run>/<case>[/<arm>] --skill S --slug NAME [flags]",
			"evalctl promote --to-references --from <run>/<case>[/<arm>] [--skill S] [flags]",
			"evalctl promote --to-trigger --from <case-ref> [--skill S] [flags]",
		},
		examples: []string{
			"evalctl promote --from 2/src-4-agentskills/with_skill --skill learning-path --slug lp-4-x",
			"evalctl promote --to-references --from 3/lp-4-agentskills --dry-run",
			"evalctl promote --to-trigger --from o:slide-builder:3 --label no --skill learning-path",
		},
	},
	"dupes": {
		summary: "near-duplicate rows, grouped — the shortlist for a trim",
		usage:   []string{"evalctl dupes [--lane trigger|output|both] [--threshold 0.60] [--json]"},
		examples: []string{
			"evalctl dupes",
			"evalctl dupes --lane trigger --threshold 0.75",
		},
	},
	"stats": {
		summary: "counts per skill, label and split, with the duplicate totals",
		usage:   []string{"evalctl stats [--json]"},
		examples: []string{
			"evalctl stats",
			"evalctl stats --json | jq .trigger.by_skill",
		},
	},
	"validate": {
		summary: "check the tree against the runner's rules",
		usage:   []string{"evalctl validate [--strict] [--json]"},
		examples: []string{
			"evalctl validate                          # findings, exit 3 when there are errors",
			"evalctl validate --strict                 # warnings count as findings too",
		},
	},
	"renumber": {
		summary: "close id gaps; it asks first, because every report cites those numbers",
		usage:   []string{"evalctl renumber [--lane trigger|output|both] [--yes] [--dry-run]"},
		examples: []string{
			"evalctl renumber --lane trigger",
		},
	},
	"lab": {
		summary: "where a skill's optimization loop stands, and the guide's next move",
		usage:   []string{"evalctl lab --skill S [--lane trigger|output] [--json]"},
		examples: []string{
			"evalctl lab --skill slide-builder",
			"evalctl lab --skill dummy --lane output --json",
		},
	},
	"describe": {
		summary: "ask a model for a new description from the train failures, and print the diff",
		usage:   []string{"evalctl describe --skill S [--candidate N] [--dry-run|--write]"},
		examples: []string{
			"evalctl describe --skill source-research --dry-run   # the prompt and the provider",
			"evalctl describe --skill source-research --write     # the diff, written and backed up",
		},
	},
	"grade": {
		summary: "output lane: the judge grades every arm's assertions for an iteration",
		usage:   []string{"evalctl grade --skill S [--iteration N] [--case IDS] [--force]"},
		examples: []string{
			"evalctl grade --skill dummy --iteration 2 --force",
		},
	},
	"revise": {
		summary: "propose a revision to one case from its verdicts (the output lane's own step)",
		usage:   []string{"evalctl revise --skill S [--case N] [--iteration N] [--apply]"},
		examples: []string{
			"evalctl revise --skill slide-builder --case 3 --dry-run",
		},
	},
	"run": {
		summary: "output lane: run every selected case with and without the skill, then grade it",
		usage:   []string{"evalctl run [--skill S|all] [--case IDS] [flags]"},
		examples: []string{
			"evalctl run --list                         # the cases and their prompts, nothing runs",
			"evalctl run --dry-run                      # the plan and the exact hermes commands",
			"evalctl run --skill slide-builder --iteration 2 --resume",
		},
	},
	"trigger": {
		summary: "trigger lane: does the right skill load for each query, N runs per query",
		usage:   []string{"evalctl trigger [--skill S|all] [--query IDS] [--split all|train|validation|holdout]"},
		examples: []string{
			"evalctl trigger --list",
			"evalctl trigger --split train --runs 1     # a cheap probe of two queries",
			"evalctl trigger                            # the loop set: train + validation",
		},
	},
	"runs": {
		summary: "iterations per workspace, with pass rates and report state",
		usage:   []string{"evalctl runs [--lane output|trigger] [--skill S] [--json]"},
		examples: []string{
			"evalctl runs",
			"evalctl runs --lane trigger --json",
		},
	},
	"report": {
		summary: "an iteration's report.md, rendered — the source when piped",
		usage:   []string{"evalctl report <run> [--lane output|trigger] [--raw] [--width N]"},
		examples: []string{
			"evalctl report 3",
			"evalctl report trigger-2 --raw",
			"evalctl report 3 | head -20                # a pipe gets the markdown, not the ANSI",
		},
	},
	"profiles": {
		summary: "the Hermes profiles the lanes measure in: status and provisioning",
		usage: []string{
			"evalctl profiles [status] [--json]",
			"evalctl profiles check|ensure [--dry-run] [--real] [--json]",
			"evalctl profiles sync [PROFILE] [--skills a,b] [--real]",
			"evalctl profiles render|scrub|reap [--real] [--dry-run]",
			"evalctl profiles attach|link|unlink [PROFILE] [--dry-run]",
		},
		examples: []string{
			"evalctl profiles                       # the status table",
			"evalctl profiles check                 # exits 1 if anything drifted",
			"evalctl profiles ensure --dry-run      # what create/env/soul/sync would do",
		},
	},
	"theme": {
		summary: "the colour schemes, each with its roles' contrast on its own background",
		usage:   []string{"evalctl theme [NAME]"},
		examples: []string{
			"evalctl theme                          # every palette, with contrast ratios",
			"evalctl theme daylight                 # a sample screen in that palette",
			"export EVALCTL_THEME=high-contrast     # keep it",
		},
	},
	"skills": {
		summary: "the set by skill: cases, fixtures, trigger rows, health",
		usage:   []string{"evalctl skills [--json]"},
		examples: []string{
			"evalctl skills",
			"evalctl skills --json | jq '.[] | select(.errors > 0)'",
		},
	},
	"tui": {
		summary: "the workbench: trigger optimization, step by step",
		usage:   []string{"evalctl [tui] [--skill S] [--lane trigger|output] [--glyphs auto|unicode|ascii]"},
		examples: []string{
			"evalctl                                # the door",
			"evalctl tui --skill slide-builder --lane output",
		},
	},
	"version": {
		summary: "which build this is: version, commit, dirty tree, go",
		usage:   []string{"evalctl version [--short] [--json]"},
		examples: []string{
			"evalctl version --short                # the version alone, for scripts",
		},
	},
}

// flagGroup is a named run of flags. Every flag a verb registers must be named by one of its
// groups; anything left over lands in "OTHER FLAGS", and a test fails if that section appears —
// so a flag added without filing it is caught rather than silently buried at the bottom.
type flagGroup struct {
	title string
	names []string
}

// flagGroups is declared where grouping earns its keep: a page of eight or more flags. A four-flag
// verb reads better as one flat list than as three headings with one row each.
var flagGroups = map[string][]flagGroup{
	"ls": {
		{"WHICH ROWS", []string{"lane", "skill", "label", "split", "search", "in", "dupes"}},
		{"ORDER AND SHAPE", []string{"sort", "group", "width", "json"}},
	},
	"add": {
		{"THE ROW", []string{"lane", "skill", "id"}},
		{"TRIGGER LANE", []string{"query", "why", "label", "split"}},
		{"OUTPUT LANE", []string{"slug", "prompt", "expected", "files", "assertions"}},
	},
	"edit": {
		{"THE ROW", []string{"lane", "id", "skill"}},
		{"TRIGGER FIELDS", []string{"query", "why", "label", "split"}},
		{"OUTPUT FIELDS", []string{"prompt", "expected", "files", "assertions", "slug"}},
		{"WRITING", []string{"dry-run", "yes"}},
	},
	"rm": {
		{"THE SELECTION", []string{"lane", "all", "skill", "label", "split", "search", "dupes"}},
		{"WRITING", []string{"with-fixture", "dry-run", "yes"}},
	},
	"promote": {
		{"THE SOURCE", []string{"from", "workspace"}},
		{"THE TARGET", []string{"skill", "slug", "id", "prompt", "expected"}},
		{"REFERENCES MODE", []string{"to-references", "file", "as", "all", "force"}},
		{"TRIGGER MODE", []string{"to-trigger", "label", "split", "why"}},
		{"WRITING", []string{"dry-run"}},
	},
	"run": {
		{"WHICH ROWS", []string{"skill", "case", "query", "split", "iteration", "lane"}},
		{"EXECUTION", []string{"arms", "model", "budget", "max-turns", "runs", "isolate", "no-verify", "keep"}},
		{"RESUMING", []string{"resume", "force", "fill"}},
		{"GRADING", []string{"grade", "no-grade", "threshold"}},
		{"WHAT IT PRINTS", []string{"list", "dry-run", "log"}},
	},
	"grade": {
		{"WHICH ITERATION", []string{"skill", "iteration", "case"}},
		{"THE CALL", []string{"model", "budget", "keep", "force"}},
		{"OUTPUT", []string{"dry-run", "json"}},
	},
	"describe": {
		{"THE CALL", []string{"skill", "candidate", "model", "budget"}},
		{"OUTPUT", []string{"dry-run", "write", "json"}},
	},
}

// globalFlags are taken out of the line before dispatch, so they sit on no verb's FlagSet; they are
// printed once at the foot of every page instead of repeated through the middle of each list.
var globalFlags = []flagDoc{
	{name: "root", arg: "<DIR>", usage: "the eval tree, or a profile naming one (default: nearest dir holding it; $EVALCTL_ROOT)"},
	{name: "theme", arg: "<NAME>", usage: "auto, midnight, high-contrast, daylight ($EVALCTL_THEME)"},
	{name: "no-input", usage: "never prompt; a command that would have asked names the flag to pass"},
	{name: "help", usage: "this page"},
}

// helpDocsLine is the last line of every page: where the long-form documentation lives.
const helpDocsLine = "README.md § CLI · docs/getting-started.md · evalctl help <verb>"

// currentVerb is the canonical verb of the command line being run. It exists so an error can
// point at the page the operator needs (`try: evalctl ls -h`) instead of a placeholder.
var currentVerb string

// helpAliases maps every accepted spelling to the canonical verb whose help it shows, so
// `evalctl help list` and `evalctl ls -h` are the same page.
var helpAliases = map[string]string{
	"help": "help", "version": "version", "tui": "tui",
	"init": "init", "new": "init", "scaffold": "init",
	"adopt":  "adopt",
	"skills": "skills", "skill": "skills",
	"ls": "ls", "list": "ls",
	"show": "show", "add": "add", "edit": "edit",
	"rm": "rm", "delete": "rm", "mv": "mv", "move": "mv",
	"promote": "promote", "dupes": "dupes", "duplicates": "dupes",
	"stats": "stats", "validate": "validate", "check": "validate",
	"renumber": "renumber", "run": "run", "trigger": "trigger",
	"lab": "lab", "grade": "grade", "revise": "revise", "describe": "describe",
	"runs": "runs", "results": "runs", "report": "report",
	"profiles": "profiles", "profile": "profiles",
	"theme": "theme", "themes": "theme", "colour": "theme", "color": "theme",
}

func canonicalVerb(v string) string {
	if c, ok := helpAliases[v]; ok {
		return c
	}
	return ""
}

// knownVerb is the dispatch table's own list, so `--root X nonsense` stays a TUI invocation with a
// bad flag rather than being silently reordered.
func knownVerb(v string) bool { return canonicalVerb(v) != "" }

// wantsHelp reports -h/--help anywhere before the `--` terminator: help is answered from any
// position, which is how it can be added to the end of an invocation that already works.
func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		switch a {
		case "-h", "--help", "-help", "--h":
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- flag rows

// flagDoc is one row of a flags block: a name, its value placeholder, its description and — when
// it is not the zero value and the prose does not already say so — the default.
type flagDoc struct {
	name  string
	arg   string
	usage string
	def   string
}

func (d flagDoc) cell() string {
	if d.arg == "" {
		return "--" + d.name
	}
	return "--" + d.name + " " + d.arg
}

// flagDocs reads a verb's flags off its own set. The placeholder comes from a backquoted word in
// the usage string when there is one, and otherwise from the flag's name, because `<SKILL>` says
// more at a glance than `<string>`.
func flagDocs(fs *flag.FlagSet) []flagDoc {
	var out []flagDoc
	fs.VisitAll(func(f *flag.Flag) {
		if f.Name == "root" {
			return // global; printed once at the foot of the page
		}
		boolFlag := false
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			boolFlag = true
		}
		arg := ""
		if !boolFlag {
			ph, _ := flag.UnquoteUsage(f)
			switch ph {
			case "string", "int", "int64", "uint", "float", "duration", "value":
				ph = ""
			}
			if ph == "" {
				ph = "<" + strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_")) + ">"
			}
			arg = ph
		}
		def := ""
		// The prose often states the default itself ("all (default) or comma-separated …", or a
		// list the default repeats verbatim); printing it again is noise, not information.
		said := strings.Contains(strings.ToLower(f.Usage), "default") ||
			(f.DefValue != "" && f.DefValue != "false" && strings.TrimSpace(f.Usage) == f.DefValue)
		if !said {
			switch {
			case boolFlag && f.DefValue == "true":
				def = "(default: true)"
			case !boolFlag && f.DefValue != "" && f.DefValue != "0" && f.DefValue != "false":
				def = "(default: " + f.DefValue + ")"
			}
		}
		out = append(out, flagDoc{name: f.Name, arg: arg, usage: f.Usage, def: def})
	})
	return out
}

// orderedFlags walks a verb's declared groups, then reports anything they did not name, so the
// renderer can put the leftovers last and a test can prove that section never appears.
func orderedFlags(docs []flagDoc, groups []flagGroup) ([]flagGroup, []flagDoc) {
	index := make(map[string]bool, len(docs))
	for _, d := range docs {
		index[d.name] = true
	}
	used := make(map[string]bool, len(docs))
	var out []flagGroup
	for _, g := range groups {
		var names []string
		for _, n := range g.names {
			if !index[n] {
				continue // a group may name a flag this verb does not register (run vs trigger)
			}
			names = append(names, n)
			used[n] = true
		}
		if len(names) > 0 {
			out = append(out, flagGroup{title: g.title, names: names})
		}
	}
	var rest []flagDoc
	for _, d := range docs {
		if !used[d.name] {
			rest = append(rest, d)
		}
	}
	return out, rest
}

func filterDocs(docs []flagDoc, names []string) []flagDoc {
	index := make(map[string]flagDoc, len(docs))
	for _, d := range docs {
		index[d.name] = d
	}
	out := make([]flagDoc, 0, len(names))
	for _, n := range names {
		if d, ok := index[n]; ok {
			out = append(out, d)
		}
	}
	return out
}

func namesOf(docs []flagDoc) []string {
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.name)
	}
	return out
}

// ---------------------------------------------------------------- layout

// helpWidth is the column the page lays out to: the terminal's, clamped so a wide window does not
// stretch descriptions into an unreadable ribbon and a narrow one still gets a description column.
func helpWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		if w > 100 {
			return 100
		}
		if w < 60 {
			return 60
		}
		return w
	}
	return 100
}

// descriptionColumn is where every description starts: two cells of indent, the widest name cell on
// the page, two cells of gap. Flags and globals share one column, so the page reads down one edge.
func descriptionColumn(sets ...[]flagDoc) int {
	widest := 0
	for _, set := range sets {
		for _, d := range set {
			if n := len(d.cell()); n > widest {
				widest = n
			}
		}
	}
	if widest > 30 {
		widest = 30
	}
	return 2 + widest + 2
}

// wrap breaks text on spaces to width, preserving newlines the author wrote.
func wrap(text string, width int) []string {
	if width < 10 {
		width = 10
	}
	var out []string
	for _, para := range strings.Split(text, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := words[0]
		for _, word := range words[1:] {
			if len(line)+1+len(word) > width {
				out = append(out, line)
				line = word
				continue
			}
			line += " " + word
		}
		out = append(out, line)
	}
	return out
}

// section is the page's heading register: one weight, one colour, upper case, so a reader scanning
// for "where do the flags start" finds the same shape every time. The colour comes from the active
// palette and disappears when the page is piped — Lip Gloss emits no escapes without a TTY.
func section(title string) string { return styleSection.Render(title) }

// printGroup draws a heading and its rows at the page's description column.
func printGroup(w io.Writer, title string, docs []flagDoc, col, width int) {
	if len(docs) == 0 {
		return
	}
	fmt.Fprintln(w, section(title))
	for _, d := range docs {
		text := d.usage
		if d.def != "" {
			text += "  " + d.def
		}
		cell := d.cell()
		lines := wrap(text, width-col)
		if len(cell) > col-4 {
			// A name too long for the column keeps its description on the next line rather than
			// pushing every other description right.
			fmt.Fprintln(w, "  "+cell)
			for _, l := range lines {
				fmt.Fprintln(w, strings.Repeat(" ", col)+l)
			}
			continue
		}
		fmt.Fprintf(w, "  %s%s%s\n", cell, strings.Repeat(" ", col-2-len(cell)), lines[0])
		for _, l := range lines[1:] {
			fmt.Fprintln(w, strings.Repeat(" ", col)+l)
		}
	}
	fmt.Fprintln(w)
}

// printExamples draws the examples block. Every line gets the shell's own prompt, so a copy of it
// runs as it reads.
func printExamples(w io.Writer, examples []string) {
	if len(examples) == 0 {
		return
	}
	fmt.Fprintln(w, section("EXAMPLES"))
	for _, e := range examples {
		e = strings.TrimPrefix(e, "$ ")
		fmt.Fprintln(w, "  $ "+e)
	}
	fmt.Fprintln(w)
}

// printFooter closes the page: the codes a script branches on, then the documentation.
func printFooter(w io.Writer) {
	fmt.Fprintln(w, section("EXIT CODES"))
	fmt.Fprintln(w, "  0 done · 1 drift (profiles check) · 2 refused (bad usage, unknown ref, nothing")
	fmt.Fprintln(w, "  selected, a prompt with no terminal) · 3 validation findings · 130 Ctrl-C")
	fmt.Fprintln(w)
	fmt.Fprintln(w, section("LEARN MORE"))
	fmt.Fprintln(w, "  "+helpDocsLine)
	fmt.Fprintln(w)
}

// ---------------------------------------------------------------- the pages

// helpPage is the page a verb's own FlagSet produces.
func helpPage(fs *flag.FlagSet) string {
	name := currentVerb
	if name == "" || name == "help" {
		name = strings.TrimPrefix(fs.Name(), "evalctl ")
	}
	doc, ok := helpDocs[name]
	if !ok {
		doc = helpDoc{summary: "evalctl " + name}
	}
	width := helpWidth()
	docs := flagDocs(fs)
	groups, rest := orderedFlags(docs, flagGroups[name])
	if len(groups) == 0 {
		groups = []flagGroup{{title: "FLAGS"}}
		if len(rest) > 0 {
			groups[0].names = namesOf(rest)
			rest = nil
		}
	}
	if len(rest) > 0 {
		groups = append(groups, flagGroup{title: "OTHER FLAGS", names: namesOf(rest)})
	}
	col := descriptionColumn(docs, globalFlags)

	var b strings.Builder
	// The title block wraps like everything else: a summary long enough to run past the page's
	// width hangs under its own first word instead of running to the edge.
	prefix := "evalctl " + name + " — "
	title := wrap(doc.summary, width-len(prefix))
	fmt.Fprint(&b, styleTitle.Render(prefix)+title[0]+"\n")
	for _, l := range title[1:] {
		b.WriteString(strings.Repeat(" ", len(prefix)) + l + "\n")
	}
	b.WriteString("\n")
	if len(doc.usage) > 0 {
		b.WriteString(section("USAGE") + "\n")
		for _, u := range doc.usage {
			b.WriteString("  " + u + "\n")
		}
		b.WriteString("\n")
	}
	printExamples(&b, doc.examples)
	for _, g := range groups {
		printGroup(&b, g.title, filterDocs(docs, g.names), col, width)
	}
	printGroup(&b, "GLOBAL FLAGS", globalFlags, col, width)
	printFooter(&b)
	return b.String()
}

// printHelp writes the page to stdout: help is the primary output of a help request, so it can be
// piped and paged like any other output.
func printHelp(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprint(w, helpPage(fs))
}

// printVerbHelp answers `evalctl help <verb>` and `evalctl <verb> -h` from the verb's own flag set
// by re-dispatching with -h: one code path, so the two can never disagree.
func printVerbHelp(verb string) int {
	return Run(append([]string{verb}, "-h"))
}

// ---------------------------------------------------------------- the index

// indexEntry is one line of the top-level page: a command form and what it is for.
type indexEntry struct{ cmd, what string }

// indexGroups is the top level as an index, not a manual: groups of one-line entries in the order
// an operator meets them, each pointing at the verb's own page for the detail. Alphabetical order
// would put `add` before `init` and tell a newcomer nothing about where to start.
var indexGroups = []struct {
	title   string
	entries []indexEntry
}{
	{"START HERE", []indexEntry{
		{"evalctl init", "start a project: evalctl.json + one skill holding both eval sets"},
		{"evalctl adopt", "add the sets an existing folder's skills are missing"},
		{"evalctl", "the TUI workbench, step by step (no arguments)"},
		{"evalctl help [verb]", "this page, or one verb's page with its flags and examples"},
	}},
	{"MANAGE THE SETS", []indexEntry{
		{"evalctl skills", "the set by skill: cases, fixtures, trigger rows, health"},
		{"evalctl ls", "list rows from either lane, as one table"},
		{"evalctl show <ref>", "one row in full: prompt, expected output, assertions, fixture"},
		{"evalctl add --lane L", "create a trigger query or an output case"},
		{"evalctl edit <ref>", "change only the flags you pass"},
		{"evalctl rm <ref...>", "delete rows by ref, by filter, or by lane"},
		{"evalctl mv <ref>", "move an output case, and its fixture, to another skill"},
		{"evalctl promote", "a run's output becomes a case, a reference, or a trigger row"},
		{"evalctl dupes", "near-duplicate rows, grouped — the shortlist for a trim"},
		{"evalctl stats", "counts per skill, label and split"},
		{"evalctl validate", "check the tree against the runner's rules"},
		{"evalctl renumber", "close id gaps (breaks comparability with old results)"},
	}},
	{"MEASURE", []indexEntry{
		{"evalctl lab --skill S", "where that loop stands, and the guide's next move"},
		{"evalctl trigger", "trigger lane: does the right skill load for each query?"},
		{"evalctl run", "output lane: each case with and without the skill, then graded"},
		{"evalctl grade --skill S", "output lane: the judge grades every arm's assertions"},
		{"evalctl revise --skill S", "propose a change to one case from its verdicts"},
		{"evalctl describe --skill S", "ask a model for a new description from the train failures"},
	}},
	{"BROWSE", []indexEntry{
		{"evalctl runs", "iterations per workspace, with pass rates and report state"},
		{"evalctl report <run>", "an iteration's report.md, rendered (--raw for the source)"},
	}},
	{"PROFILES AND COLOUR", []indexEntry{
		{"evalctl profiles [cmd]", "status/check/ensure/reap/render/sync/scrub/link/attach"},
		{"evalctl theme [name]", "the colour schemes, with each role's contrast on its background"},
	}},
}

// notes are the paragraphs that used to sit in the middle of the command list. They hold for the
// whole tool — the sandboxes' lifetime, the profiles link, the lanes' logs, refs and backups — so
// they close the page rather than interrupting the index.
var notes = []string{
	"Sandboxes are created by the run that needs them and removed when it ends: one that outlives " +
		"its run carries that run's caches and sessions into the next measurement. --keep holds them " +
		"for inspection; \"profiles reap\" clears what a killed run left behind.",
	"Refs: 12 --lane trigger | t12 | trigger:12 | o:slide-builder:3 | slide-builder:3. Every write " +
		"to the sets is atomic and leaves a copy in .evalctl/backups/.",
	"The rest — the profiles link, --log levels, what a sandbox is made of — is in README § CLI.",
}

// printUsage draws the top-level page: the index of commands, the notes that hold for all of them,
// then the same footer every verb page has.
func printUsage(w io.Writer) {
	width := helpWidth()
	col := 26
	for _, g := range indexGroups {
		for _, e := range g.entries {
			if n := len(e.cmd) + 4; n > col { // four, not two: the widest entry still gets a gap
				col = n
			}
		}
	}
	if col > width/2 {
		col = width / 2
	}

	fmt.Fprintf(w, "%s — %s\n\n", styleTitle.Render("evalctl"),
		"manage and run a skill's eval sets: trigger queries and output cases.")
	for _, g := range indexGroups {
		fmt.Fprintln(w, section(g.title))
		for _, e := range g.entries {
			if len(e.cmd)+2 > col {
				fmt.Fprintln(w, "  "+e.cmd)
				for _, l := range wrap(e.what, width-4) {
					fmt.Fprintln(w, "    "+l)
				}
				continue
			}
			lines := wrap(e.what, width-col)
			fmt.Fprintf(w, "  %s%s%s\n", e.cmd, strings.Repeat(" ", col-2-len(e.cmd)), lines[0])
			for _, l := range lines[1:] {
				fmt.Fprintln(w, strings.Repeat(" ", col)+l)
			}
		}
		fmt.Fprintln(w)
	}
	printGroup(w, "GLOBAL FLAGS", globalFlags, descriptionColumn(globalFlags), width)

	fmt.Fprintln(w, section("NOTES"))
	for _, n := range notes {
		for _, l := range wrap(n, width-4) {
			fmt.Fprintln(w, "  "+l)
		}
		fmt.Fprintln(w)
	}
	printFooter(w)
}

// ---------------------------------------------------------------- suggestions

// flagLine matches the flag package's own listing ("  -dupes"), which spells long flags with one
// dash. The CLI accepts both; the page teaches the one it documents.
var flagLine = regexp.MustCompile(`(?m)^  -([A-Za-z])`)

func doubleDash(s string) string { return flagLine.ReplaceAllString(s, "  --$1") }

// suggestVerb names the closest verb to a typo, so a misspelling is one line from the fix.
func suggestVerb(word string) string {
	best, bestDist := "", 3
	for v := range helpAliases {
		if d := editDistance(word, v); d < bestDist {
			best, bestDist = v, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	// The two short strings here are a verb and a typo; the quadratic table is the whole of it.
	a, b = strings.ToLower(a), strings.ToLower(b)
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, min(cur[j-1]+1, prev[j-1]+cost))
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
