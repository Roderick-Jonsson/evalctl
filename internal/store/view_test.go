package store

import (
	"strings"
	"testing"
)

// openTest opens the shared fixture tree (four triggers, two output cases).
func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(writeTestTree(t))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func ids(rows []Entry) []int {
	out := make([]int, len(rows))
	for i, e := range rows {
		out[i] = e.ID
	}
	return out
}

func eqInts(t *testing.T, got, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestSortByColumnKeepsTiesInFileOrder(t *testing.T) {
	s := openTest(t)
	// File order is skill by skill: alpha 1-4, then beta 5-8; by id descending it must reverse exactly.
	rows := s.RowsOrdered(LaneTrigger, Filter{}, Order{Keys: []Sort{{Field: "id", Desc: true}}})
	eqInts(t, ids(rows), []int{8, 7, 6, 5, 4, 3, 2, 1})

	// Sorting by label groups the four "no" rows first and leaves their file order alone.
	rows = s.RowsOrdered(LaneTrigger, Filter{}, Order{Keys: []Sort{{Field: "label"}}})
	eqInts(t, ids(rows), []int{2, 4, 6, 8, 1, 3, 5, 7})

	// Two keys: split, then id descending.
	rows = s.RowsOrdered(LaneTrigger, Filter{}, Order{Keys: []Sort{{Field: "split"}, {Field: "id", Desc: true}}})
	eqInts(t, ids(rows), []int{6, 5, 2, 1, 8, 7, 4, 3})

	// No keys is file order, untouched.
	rows = s.RowsOrdered(LaneTrigger, Filter{}, Order{})
	eqInts(t, ids(rows), []int{1, 2, 3, 4, 5, 6, 7, 8})
}

func TestSortIsCaseInsensitiveAndNumericForIDs(t *testing.T) {
	s := openTest(t)
	rows := s.RowsOrdered(LaneOutput, Filter{}, Order{Keys: []Sort{{Field: "slug"}}})
	if len(rows) != 2 || rows[0].Slug != "a-1" || rows[1].Slug != "b-1" {
		t.Fatalf("slug sort gave %+v", rows)
	}
	if ColumnValue(rows[0], "dupe") != "-" {
		t.Fatalf("empty dupe cell = %q", ColumnValue(rows[0], "dupe"))
	}
}

func TestGroupEntriesHeadersAndCounts(t *testing.T) {
	s := openTest(t)
	rows := s.RowsOrdered(LaneTrigger, Filter{}, Order{Keys: []Sort{{Field: "id"}}})
	groups := GroupEntries(rows, "skill")
	if len(groups) != 2 {
		t.Fatalf("want 2 groups, got %d: %+v", len(groups), groups)
	}
	if groups[0].Key != "alpha" || len(groups[0].Entries) != 4 {
		t.Fatalf("first group = %q with %d rows", groups[0].Key, len(groups[0].Entries))
	}
	if groups[1].Key != "beta" || len(groups[1].Entries) != 4 {
		t.Fatalf("second group = %q with %d rows", groups[1].Key, len(groups[1].Entries))
	}
	if got := GroupLabel("skill", groups[0].Key); got != "skill alpha" {
		t.Fatalf("label = %q", got)
	}
	// Inside a group the rows keep the order they were given.
	eqInts(t, ids(groups[1].Entries), []int{5, 6, 7, 8})

	// No group column: one unnamed group, so callers have a single rendering path.
	flat := GroupEntries(rows, "")
	if len(flat) != 1 || flat[0].Key != "" || len(flat[0].Entries) != 8 {
		t.Fatalf("flat grouping = %+v", flat)
	}
}

func TestGroupOrderIsNumericAndPutsEmptyLast(t *testing.T) {
	rows := []Entry{
		{Lane: LaneOutput, Skill: "alpha", Slug: "a-1", Dupe: 0},
		{Lane: LaneOutput, Skill: "", Slug: "b-1", Dupe: 2},
	}
	groups := GroupEntries(rows, "dupe")
	// "-" is a real value (unique), not an empty cell: it sorts as 0, ahead of #2.
	if len(groups) != 2 || groups[0].Key != "-" || groups[1].Key != "#2" {
		t.Fatalf("dupe group order = %+v", groups)
	}

	// A genuinely empty cell (no `why` written) goes last, however it compares as text.
	rows = []Entry{
		{Lane: LaneTrigger, ID: 1, Skill: "alpha", Why: ""},
		{Lane: LaneTrigger, ID: 2, Skill: "alpha", Why: "clear case"},
	}
	groups = GroupEntries(rows, "why")
	if len(groups) != 2 || groups[0].Key != "clear case" || groups[1].Key != "" {
		t.Fatalf("why group order = %+v", groups)
	}
	if GroupLabel("why", "") != "why (none)" {
		t.Fatalf("empty group label = %q", GroupLabel("why", ""))
	}
}

func TestSearchScopedToColumn(t *testing.T) {
	s := openTest(t)
	// "operator" lives only in a query body; scoping to skill must miss it.
	if got := len(s.RowsOrdered(LaneTrigger, Filter{Search: "operator", In: "skill"}, Order{})); got != 0 {
		t.Fatalf("skill-scoped search matched %d rows", got)
	}
	if got := len(s.RowsOrdered(LaneTrigger, Filter{Search: "operator"}, Order{})); got != 1 {
		t.Fatalf("unscoped search matched %d rows, want 1", got)
	}
	// "beta" is a skill, so scoped to that column it is exactly its own rows.
	if got := len(s.RowsOrdered(LaneTrigger, Filter{Search: "beta", In: "skill"}, Order{})); got != 4 {
		t.Fatalf("skill-scoped search matched %d rows, want 4", got)
	}
	// A scope the lane does not have falls back to the full text rather than matching nothing:
	// `ls --lane both --in slug` still lists the trigger rows that mention the word.
	if got := len(s.RowsOrdered(LaneTrigger, Filter{Search: "operator", In: "slug"}, Order{})); got != 1 {
		t.Fatalf("out-of-lane scope matched %d rows, want 1", got)
	}
}

func TestOrderValidateNamesTheColumns(t *testing.T) {
	s := openTest(t)
	_ = s
	bad := Order{Keys: []Sort{{Field: "nope"}}}
	err := bad.Validate(LaneTrigger)
	if err == nil || !strings.Contains(err.Error(), "this lane has: id, skill") {
		t.Fatalf("error = %v", err)
	}
	if err := (Order{Group: "nope"}).Validate(LaneOutput); err == nil {
		t.Fatal("unknown group column was accepted")
	}
	if err := (Order{Keys: []Sort{{Field: "text"}}, Group: "skill"}).Validate(LaneOutput); err != nil {
		t.Fatalf("valid order rejected: %v", err)
	}
	// why is not in any table but may still be searched or grouped.
	if err := (Order{Group: "why"}).Validate(LaneTrigger); err != nil {
		t.Fatalf("grouping by why rejected: %v", err)
	}
}

func TestParseSort(t *testing.T) {
	keys, err := ParseSort("skill,id:desc")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0].Field != "skill" || keys[0].Desc || !keys[1].Desc {
		t.Fatalf("parsed %+v", keys)
	}
	if _, err := ParseSort("id:sideways"); err == nil {
		t.Fatal("bad direction accepted")
	}
}
