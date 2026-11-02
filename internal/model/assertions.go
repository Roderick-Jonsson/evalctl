package model

import "strings"

// SplitAssertions turns a field's text into a case's grading list: one assertion per line, and a
// ';' does the same job so a single-line editor is not a trap. Empty entries are dropped rather
// than kept as blanks — an assertion that says nothing can never be graded, and the validator
// rejects one as an error, so a blank line in a field is a formatting accident, not a verdict.
//
// It lives here, not in the CLI, because the CLI and the workbench write the same list: two
// splitters would eventually disagree about a line ending and the file would depend on which
// screen typed it.
func SplitAssertions(raw string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == ';' }) {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
