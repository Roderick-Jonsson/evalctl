package store

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// A column is one field of an Entry that a lane's table can show, sort, group and search by.
// The names here are the vocabulary of both faces — `--sort`/`--group`/`--in` on the CLI and the
// number keys in the TUI — so a column cannot exist in one place and go missing in the other.
//
// Ordering and grouping are read models: they never touch a file, and the row key
// (lane|skill|index) means a sort can reorder the list without changing what a selection means.
var (
	triggerColumnNames = []string{"id", "skill", "label", "split", "dupe", "text"}
	// "assert" is the number of assertions a case declares: the one column that answers "which
	// cases are still ungraded shape" without opening each case.
	outputColumnNames = []string{"id", "skill", "slug", "assert", "dupe", "text"}
)

// ColumnNames lists a lane's columns in display order.
func ColumnNames(lane string) []string {
	if lane == LaneOutput {
		return append([]string(nil), outputColumnNames...)
	}
	return append([]string(nil), triggerColumnNames...)
}

// ColumnExists reports whether the lane has that column. "why" is a field of every row even
// though no lane puts it in the table, so it may be searched and grouped but not sorted by.
func ColumnExists(lane, name string) bool {
	if name == "why" {
		return true
	}
	for _, c := range ColumnNames(lane) {
		if c == name {
			return true
		}
	}
	return false
}

// ColumnValue renders one cell: what the table prints, what a group header counts, and what a
// column-scoped search compares against. Empty means "this lane has no such column".
func ColumnValue(e Entry, field string) string {
	switch field {
	case "id":
		return strconv.Itoa(e.ID)
	case "skill":
		return e.Skill
	case "label":
		return e.Label
	case "split":
		return e.Split
	case "assert":
		if e.Assert == 0 {
			return "-"
		}
		return strconv.Itoa(e.Assert)
	case "dupe":
		if e.Dupe == 0 {
			return "-"
		}
		return "#" + strconv.Itoa(e.Dupe)
	case "slug":
		return e.Slug
	case "text":
		return oneLine(e.Text)
	case "why":
		return oneLine(e.Why)
	}
	return ""
}

func numericColumn(field string) bool { return field == "id" || field == "dupe" || field == "assert" }

// numericValue turns a cell into the number a numeric column sorts and groups by; "-" (the
// empty dupe cell) sorts before #1.
func numericValue(e Entry, field string) int {
	switch field {
	case "id":
		return e.ID
	case "dupe":
		return e.Dupe
	case "assert":
		return e.Assert
	}
	return 0
}

// Sort is one sort key. Desc reverses that key only, so a multi-key sort can mix directions.
type Sort struct {
	Field string
	Desc  bool
}

// Order is how a lane's rows are presented: the sort keys in priority order, then an optional
// group column. Both are display-only; the file order is whatever the set says it is.
type Order struct {
	Keys  []Sort
	Group string
}

// ParseSort reads "skill", "id:desc" or a comma-separated list "skill,id:desc".
func ParseSort(spec string) ([]Sort, error) {
	var out []Sort
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		s := Sort{Field: part}
		if i := strings.Index(part, ":"); i >= 0 {
			s.Field = part[:i]
			switch strings.ToLower(part[i+1:]) {
			case "desc":
				s.Desc = true
			case "asc", "":
			default:
				return nil, fmt.Errorf("--sort %q: direction must be asc or desc", part)
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// Validate names a column the lane does not have, and says which ones it does.
func (o Order) Validate(lane string) error {
	for _, k := range o.Keys {
		if !ColumnExists(lane, k.Field) {
			return fmt.Errorf("unknown column %q; this lane has: %s", k.Field, strings.Join(ColumnNames(lane), ", "))
		}
	}
	if o.Group != "" && !ColumnExists(lane, o.Group) {
		return fmt.Errorf("unknown group column %q; this lane has: %s", o.Group, strings.Join(ColumnNames(lane), ", "))
	}
	return nil
}

// Group is a set of rows that share one column value.
type Group struct {
	Key     string // the value as displayed; "" is shown as "(none)" by the caller
	Entries []Entry
}

// GroupIndex is one group by row position — the shape a cursor needs, since the TUI's selection
// and highlight are indices into the ordered row slice.
type GroupIndex struct {
	Key  string
	Rows []int
}

// GroupIndices groups rows by one column and keeps both the group order and each group's row
// order. With field == "" it returns a single unnamed group, so a caller never needs two
// rendering paths.
func GroupIndices(rows []Entry, field string) []GroupIndex {
	if field == "" {
		all := make([]int, len(rows))
		for i := range rows {
			all[i] = i
		}
		return []GroupIndex{{Rows: all}}
	}
	index := map[string]int{}
	var groups []GroupIndex
	for i, e := range rows {
		key := ColumnValue(e, field)
		at, ok := index[key]
		if !ok {
			at = len(groups)
			index[key] = at
			groups = append(groups, GroupIndex{Key: key})
		}
		groups[at].Rows = append(groups[at].Rows, i)
	}
	numeric := numericColumn(field)
	sort.SliceStable(groups, func(a, b int) bool {
		ka, kb := groups[a].Key, groups[b].Key
		if (ka == "") != (kb == "") {
			return kb == ""
		}
		if numeric {
			na, _ := strconv.Atoi(strings.TrimPrefix(ka, "#"))
			nb, _ := strconv.Atoi(strings.TrimPrefix(kb, "#"))
			return na < nb
		}
		return strings.ToLower(ka) < strings.ToLower(kb)
	})
	return groups
}

// GroupEntries is GroupIndices with the rows themselves attached, for the CLI's grouped table.
func GroupEntries(rows []Entry, field string) []Group {
	indices := GroupIndices(rows, field)
	out := make([]Group, 0, len(indices))
	for _, g := range indices {
		entries := make([]Entry, 0, len(g.Rows))
		for _, i := range g.Rows {
			entries = append(entries, rows[i])
		}
		out = append(out, Group{Key: g.Key, Entries: entries})
	}
	return out
}

// GroupLabel is what a header line prints for a group: the column and its value.
func GroupLabel(field, key string) string {
	if key == "" {
		return field + " (none)"
	}
	return field + " " + key
}

// RowsOrdered is Rows plus the presentation order: filtered, sorted, still one flat slice.
// Sorting falls back to id, then skill, so two rows that tie on every key never swap places
// between refreshes (a cursor that jumps on its own is worse than an arbitrary one).
func (s *Store) RowsOrdered(lane string, f Filter, o Order) []Entry {
	rows := s.Rows(lane, f)
	if len(o.Keys) == 0 {
		return rows
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		for _, k := range o.Keys {
			c := compareColumn(a, b, k.Field)
			if c == 0 {
				continue
			}
			if k.Desc {
				return c > 0
			}
			return c < 0
		}
		return false
	})
	return rows
}

func compareColumn(a, b Entry, field string) int {
	if numericColumn(field) {
		na, nb := numericValue(a, field), numericValue(b, field)
		switch {
		case na < nb:
			return -1
		case na > nb:
			return 1
		}
		return 0
	}
	sa := strings.ToLower(ColumnValue(a, field))
	sb := strings.ToLower(ColumnValue(b, field))
	return strings.Compare(sa, sb)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}
