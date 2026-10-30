package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// IDRef is one artifact on disk that still cites a row by its old id.
type IDRef struct {
	Path string // root-relative, the way a reader addresses it
	What string // "result dir" or "report"
}

// IDReferences lists what still cites lane's row #id under root. An id is not just a field: the
// trigger lane names every result dir q<id>-<skill>, and both reports print the number, so moving
// an id leaves that history pointing at a row that is no longer #id. Read-only — `edit --id` prints
// this before it asks.
func IDReferences(root, lane, skill string, id int) []IDRef {
	var refs []IDRef
	switch lane {
	case LaneTrigger:
		refs = triggerIDRefs(root, id)
	case LaneOutput:
		refs = outputIDRefs(root, skill, id)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Path < refs[j].Path })
	return refs
}

// triggerIDRefs finds q<id>-<skill> dirs in every trigger iteration, plus the reports that quote
// the id (its failure bullets and its every-query table both address rows by number).
func triggerIDRefs(root string, id int) []IDRef {
	var refs []IDRef
	dirs, _ := filepath.Glob(filepath.Join(root, "trigger-workspace", "trigger-*"))
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if n, ok := caseDirID(e.Name()); ok && n == id {
				refs = append(refs, IDRef{Path: rootRelative(root, filepath.Join(dir, e.Name())), What: "result dir"})
			}
		}
		report := filepath.Join(dir, "report.md")
		if mentions(report, fmt.Sprintf("q%02d", id), fmt.Sprintf("| %d |", id)) {
			refs = append(refs, IDRef{Path: rootRelative(root, report), What: "report"})
		}
	}
	return refs
}

// outputIDRefs finds the iteration reports that head this case. The output lane keys everything else
// — result dirs, feedback.json, meta.json — by slug, so a report heading is the only place its id
// survives on disk.
func outputIDRefs(root, skill string, id int) []IDRef {
	reports, _ := filepath.Glob(filepath.Join(root, skill+"-workspace", "iteration-*", "report.md"))
	heading := fmt.Sprintf("· %s case %d", skill, id)
	var refs []IDRef
	for _, path := range reports {
		if mentions(path, heading) {
			refs = append(refs, IDRef{Path: rootRelative(root, path), What: "report"})
		}
	}
	return refs
}

// caseDirID reads the id out of a trigger case dir name, q<digits>-<skill>.
func caseDirID(name string) (int, bool) {
	if !strings.HasPrefix(name, "q") {
		return 0, false
	}
	rest := name[1:]
	dash := strings.Index(rest, "-")
	if dash <= 0 {
		return 0, false
	}
	n, err := strconv.Atoi(rest[:dash])
	if err != nil {
		return 0, false
	}
	return n, true
}

// mentions reports whether the file at path holds any of tokens as a whole word. Missing files are
// simply not mentions: a tree that never ran has no results to warn about. The boundary is spelled
// out rather than \b because a token can end in punctuation ("| 3 |").
func mentions(path string, tokens ...string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	text := string(b)
	for _, token := range tokens {
		re := regexp.MustCompile(`(?:^|[^0-9A-Za-z])` + regexp.QuoteMeta(token) + `(?:[^0-9A-Za-z]|$)`)
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

func rootRelative(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return filepath.ToSlash(rel)
}
