package cli

// The CLI reference page as markdown.
//
// It is not written by hand and it is not a second copy of the pages: it is those pages, rendered
// for a web reader instead of a terminal. Each verb's text is produced by running that verb's own
// `-h` through the same code the shell uses, then re-laid out — verbs become headings, flag rows
// become a table, examples become a block — so a flag cannot exist in the binary and be missing
// from the reference, and the reference cannot list a flag the binary has dropped.
//
// `make docs` writes it to docs/reference/cli.md; a test compares the file on disk with this
// function and fails when they differ, which is what keeps the page honest between releases.

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

var mdSlugStrip = regexp.MustCompile(`[^\w\s-]`)

// titleize turns a page's SHOUTED section title into a sentence-case heading: "WHICH ROWS" and
// "ORDER AND SHAPE" read as shouts in a document, and the terminal's capitals are there to be
// found at a glance in a wall of lower-case prose, which is not a problem a heading has.
func titleize(s string) string {
	lower := strings.ToLower(s)
	if lower == "" {
		return lower
	}
	return strings.ToUpper(lower[:1]) + lower[1:]
}

// mdSlug is GitHub's heading anchor: lower case, punctuation dropped, runs of spaces to a dash.
func mdSlug(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "`", "")
	s = mdSlugStrip.ReplaceAllString(s, "")
	s = strings.Join(strings.Fields(s), "-")
	return s
}

// verbOf takes "evalctl promote" and "evalctl add --lane L" to "promote" and "add".
func verbOf(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
}

// helpText runs one verb's `-h` and returns the page it printed. The verb writes to os.Stdout by
// design — help is primary output and can be piped — so the pipe is how the page is read back.
func helpText(verb string) (string, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	saved := os.Stdout
	os.Stdout = w
	// `--help` is the same page plus DETAILS: a reference wants the whole thing, and a terminal
	// reader is only one of the two audiences this generator serves.
	code := Run([]string{verb, "--help"})
	os.Stdout = saved
	w.Close()
	raw, _ := io.ReadAll(r)
	r.Close()
	if code != 0 {
		return "", fmt.Errorf("%s -h exited %d", verb, code)
	}
	return ansiEscape.ReplaceAllString(string(raw), ""), nil
}

// helpSection is one UPPERCASE block of a verb's page: USAGE, EXAMPLES, a flag group, DETAILS.
type helpSection struct {
	title string
	lines []string
}

var sectionTitle = regexp.MustCompile(`^([A-Z][A-Z0-9 ,&/().:_-]*)$`)

// splitSections cuts a help page at its section headers. A section header is an unindented,
// upper-case line; everything indented under it belongs to it.
func splitSections(text string) []helpSection {
	var out []helpSection
	cur := helpSection{title: ""}
	for _, line := range strings.Split(text, "\n") {
		if m := sectionTitle.FindStringSubmatch(strings.TrimRight(line, " ")); m != nil && m[1] != "" {
			if len(cur.lines) > 0 || cur.title != "" {
				out = append(out, cur)
			}
			cur = helpSection{title: m[1]}
			continue
		}
		cur.lines = append(cur.lines, line)
	}
	out = append(out, cur)
	return out
}

// flagRow is one flag parsed off a page: its spelling and what the page says about it.
type flagRow struct{ cell, what string }

var flagStart = regexp.MustCompile(`^\s{2}(-{1,2}[A-Za-z][A-Za-z0-9-]*)(\s+<[^>]+>)?(\s{2,})(.*)$`)

// parseFlags reads a flag group's rows. A row begins at two spaces of indent and a flag name; the
// lines under it that are indented to the description column are its description's continuation,
// which the terminal page wrapped and a web page must not.
func parseFlagRows(lines []string) []flagRow {
	var rows []flagRow
	for _, line := range lines {
		if m := flagStart.FindStringSubmatch(strings.TrimRight(line, " ")); m != nil {
			cell := m[1] + m[2]
			rows = append(rows, flagRow{cell: cell, what: strings.TrimSpace(m[4])})
			continue
		}
		if len(rows) == 0 {
			continue
		}
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		rows[len(rows)-1].what = strings.TrimSpace(rows[len(rows)-1].what + " " + t)
	}
	return rows
}

// para joins a wrapped block back into one line: the page was wrapped for a terminal, and a
// markdown paragraph that keeps those breaks reads as ragged short lines.
func para(lines []string) []string {
	var out []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.Join(strings.Fields(strings.Join(cur, " ")), " "))
			cur = nil
		}
	}
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			flush()
			continue
		}
		cur = append(cur, strings.TrimSpace(l))
	}
	flush()
	return out
}

func cellText(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "\n", " ")
}

// markdownVerb renders one verb: its summary as a heading, then usage, examples, flags, details.
func markdownVerb(b *strings.Builder, verb, cmd string) error {
	text, err := helpText(verb)
	if err != nil {
		return err
	}
	doc := helpDocs[verb]
	summary := doc.summary
	if summary == "" {
		// The first line of the page is `evalctl <verb> — <summary>`; take what is after the dash.
		if first, _, ok := strings.Cut(text, "\n"); ok {
			if _, after, found := strings.Cut(first, "— "); found {
				summary = strings.TrimSpace(after)
			}
		}
	}

	fmt.Fprintf(b, "### %s\n\n", cmd)
	if summary != "" {
		fmt.Fprintf(b, "%s\n\n", summary)
	}

	for _, sec := range splitSections(text) {
		switch sec.title {
		case "USAGE":
			var lines []string
			for _, l := range sec.lines {
				if t := strings.TrimSpace(l); strings.HasPrefix(t, "evalctl") {
					lines = append(lines, t)
				}
			}
			if len(lines) > 0 {
				b.WriteString("**Usage**\n\n```bash\n")
				b.WriteString(strings.Join(lines, "\n"))
				b.WriteString("\n```\n\n")
			}
		case "EXAMPLES":
			var lines []string
			for _, l := range sec.lines {
				t := strings.TrimSpace(l)
				if strings.HasPrefix(t, "$ ") {
					lines = append(lines, t[2:])
				}
			}
			if len(lines) > 0 {
				b.WriteString("**Examples**\n\n```bash\n")
				b.WriteString(strings.Join(lines, "\n"))
				b.WriteString("\n```\n\n")
			}
		case "DETAILS":
			ps := para(sec.lines)
			if len(ps) > 0 {
				b.WriteString("**Details**\n\n")
				for _, p := range ps {
					// A paragraph that opens with `evalctl ` is a grammar line the terminal wrapped,
					// not a sentence: a reference keeps those verbatim, in a block.
					if strings.HasPrefix(p, "evalctl ") && !strings.Contains(p, " — ") {
						b.WriteString("```bash\n" + p + "\n```\n\n")
						continue
					}
					b.WriteString(p + "\n\n")
				}
			}
		case "", "GLOBAL FLAGS", "EXIT CODES", "LEARN MORE":
			// Global flags, exit codes and the footer are printed once for the whole page.
		default:
			rows := parseFlagRows(sec.lines)
			if len(rows) == 0 {
				continue
			}
			fmt.Fprintf(b, "#### %s\n\n", titleize(sec.title))
			b.WriteString("| flag | what it does |\n|---|---|\n")
			for _, r := range rows {
				fmt.Fprintf(b, "| `%s` | %s |\n", cellText(strings.TrimSpace(r.cell)), cellText(r.what))
			}
			b.WriteString("\n")
		}
	}
	return nil
}

// ReferenceMarkdown is the whole CLI reference page.
func ReferenceMarkdown() (string, error) {
	var b strings.Builder
	b.WriteString(`# CLI reference

Every verb, its grammar and its flags. This page is generated from the binary — ` + "`make docs`" + ` writes
it from the same pages ` + "`evalctl <verb> -h`" + ` prints — so a flag cannot exist in the command and be
missing here, and nothing here can outlive the flag it describes.

Each verb answers ` + "`-h`" + ` with its own page and ` + "`--help`" + ` with that page plus its details; both are
below, in full. The four global flags are at the foot of this page, once, rather than in every list.

Three things are true of almost every verb, so they are stated once rather than repeated per table:
a read verb takes ` + "`--json`" + `; every write to the sets is atomic and leaves the previous bytes in
` + "`.evalctl/backups/`" + `; and ` + "`--no-input`" + ` turns prompting off everywhere, with a command that would have
asked naming the flag to pass instead. Patterns rather than grammar — sorting a set, a bulk trim, a
run from a script — live in [Work the sets from the shell](../how-to/manage-the-sets.md).

## Contents

`)

	for _, g := range indexGroups {
		fmt.Fprintf(&b, "**%s** — ", titleize(g.title))
		var links []string
		for _, e := range g.entries {
			if v := verbOf(e.cmd); v != "" {
				links = append(links, fmt.Sprintf("[`%s`](#%s)", e.cmd, mdSlug(e.cmd)))
			} else {
				links = append(links, fmt.Sprintf("`%s`", e.cmd))
			}
		}
		b.WriteString(strings.Join(links, " · "))
		b.WriteString("\n\n")
	}

	for _, g := range indexGroups {
		fmt.Fprintf(&b, "## %s\n\n", titleize(g.title))
		for _, e := range g.entries {
			verb := verbOf(e.cmd)
			if verb == "" {
				continue // the bare `evalctl` entry is the TUI; it has its own page below
			}
			if err := markdownVerb(&b, verb, e.cmd); err != nil {
				return "", err
			}
		}
	}

	b.WriteString("## Global flags\n\n| flag | what it does |\n|---|---|\n")
	for _, f := range globalFlags {
		fmt.Fprintf(&b, "| `%s` | %s |\n", f.cell(), cellText(f.usage+" "+f.annotations()))
	}
	b.WriteString("\n")

	b.WriteString("## Notes\n\n")
	for _, n := range notes {
		b.WriteString(strings.Join(strings.Fields(n), " ") + "\n\n")
	}

	b.WriteString("## Exit codes\n\n" + exitCodes + "\n\n")

	b.WriteString(`## Where next

- [Work the sets from the shell](../how-to/manage-the-sets.md) — the patterns, with the examples
  worth copying.
- [Your first eval](../tutorials/first-eval.md) — the whole loop once, from an empty folder.
- [The map of these docs](../README.md).
- ` + "`evalctl help`" + ` — the command index, in seven groups, in the terminal.
`)
	return b.String(), nil
}

// printMarkdownReference is `evalctl help --markdown`: the reference page on stdout, for a pipe.
func printMarkdownReference() int {
	md, err := ReferenceMarkdown()
	if err != nil {
		fmt.Fprintf(os.Stderr, "evalctl: %v\n", err)
		return 2
	}
	fmt.Print(md)
	return 0
}
