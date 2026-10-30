package tui

import (
	"strconv"
	"strings"
	"testing"

	"evalctl/internal/store"
)

// TestColumnFocusSortCycles: a number key focuses a column, o walks it ascending → descending →
// file order, and the rows really move while the cursor keeps pointing at the same row.
func TestColumnFocusSortCycles(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneTrigger), 120, 30)
	if m.focusedColumnName() != "" {
		t.Fatal("a column was focused before any key")
	}
	m.Update(key("4")) // SPLIT
	if m.focusedColumnName() != "split" {
		t.Fatalf("focused column = %q", m.focusedColumnName())
	}
	m.cursor = 1
	before := m.rows[1].ID

	m.Update(key("o")) // ascending: train, train, validation, validation = 1,2,3,4
	if idsOf(m.rows) != "1,2,3,4" {
		t.Fatalf("ascending order = %s", idsOf(m.rows))
	}
	if m.rows[m.cursor].ID != before {
		t.Fatalf("cursor lost its row: was %d, now %d", before, m.rows[m.cursor].ID)
	}
	m.Update(key("o")) // descending
	if idsOf(m.rows) != "3,4,1,2" {
		t.Fatalf("descending order = %s", idsOf(m.rows))
	}
	m.Update(key("o")) // back to file order
	if idsOf(m.rows) != "1,2,3,4" || len(m.sortKeys) != 0 {
		t.Fatalf("third press = %s, keys %+v", idsOf(m.rows), m.sortKeys)
	}
	if !strings.Contains(m.View(), "sort=") && len(m.sortKeys) > 0 {
		t.Fatalf("filter line does not name the sort:\n%s", m.View())
	}
}

// TestColumnGroupRendersSections: g groups by the focused column, headers carry a count, and the
// cursor stays in row space so j/k never lands on a header.
func TestColumnGroupRendersSections(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneTrigger), 120, 30)
	m.Update(key("2")) // SKILL
	m.Update(key("g"))
	if m.groupField != "skill" {
		t.Fatalf("groupField = %q", m.groupField)
	}
	items := m.items()
	if len(items) != 6 { // 4 rows + 2 headers
		t.Fatalf("drawn list has %d items, want 6: %+v", len(items), items)
	}
	view := m.View()
	for _, want := range []string{"skill alpha · 2", "skill beta · 2", "group=skill"} {
		if !strings.Contains(view, want) {
			t.Fatalf("grouped view missing %q:\n%s", want, view)
		}
	}
	// Every row is still reachable by j; nothing lands on a header.
	seen := map[int]bool{}
	for i := 0; i < len(m.rows); i++ {
		if e, ok := m.focused(); ok {
			seen[e.ID] = true
		}
		m.Update(key("j"))
	}
	if len(seen) != 4 {
		t.Fatalf("walked %d distinct rows, want 4", len(seen))
	}
	// The whole list fits, so the offset never drifts on a group header.
	if m.offset != 0 {
		t.Fatalf("offset = %d with everything visible", m.offset)
	}
}

// TestColumnSearchIsScoped: "/" with a column focused searches only that column.
func TestColumnSearchIsScoped(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneTrigger), 120, 30)
	m.Update(key("2")) // SKILL
	m.Update(key("/"))
	if m.searchIn != "skill" {
		t.Fatalf("search scope = %q", m.searchIn)
	}
	for _, r := range "beta" {
		m.Update(key(string(r)))
	}
	if len(m.rows) != 2 {
		t.Fatalf("skill-scoped search kept %d rows, want 2", len(m.rows))
	}
	for _, e := range m.rows {
		if e.Skill != "beta" {
			t.Fatalf("row from another skill survived: %+v", e)
		}
	}
	// The same words unscoped: no row is lost that mentions beta, and the scope is released.
	m.Update(key("esc"))
	if m.search.Value() != "" || m.searchIn != "" {
		t.Fatalf("esc left search=%q scope=%q", m.search.Value(), m.searchIn)
	}
	if len(m.rows) != 4 {
		t.Fatalf("clearing the search kept %d rows", len(m.rows))
	}
}

// TestColumnFocusClearsAndDropsOnLaneSwitch: 0 / esc drop the focus, and a column the other lane
// does not have (split) cannot survive the switch.
func TestColumnFocusClearsAndDropsOnLaneSwitch(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneTrigger), 120, 30)
	m.Update(key("4")) // SPLIT
	m.Update(key("g"))
	if m.groupField != "split" {
		t.Fatalf("groupField = %q", m.groupField)
	}
	m.Update(key("0"))
	if m.focusedColumnName() != "" {
		t.Fatal("0 did not clear the column focus")
	}
	if m.groupField != "split" {
		t.Fatal("clearing the focus also dropped the grouping")
	}
	m.Update(key("shift+tab")) // the output lane has no split column
	if m.groupField != "" || len(m.sortKeys) != 0 {
		t.Fatalf("lane switch kept group=%q sort=%+v", m.groupField, m.sortKeys)
	}
	if len(m.items()) != len(m.rows) {
		t.Fatal("an empty group column still drew headers")
	}
	// A column the number does not reach is refused, not silently clamped.
	m.Update(key("9"))
	if m.focusedColumnName() != "" {
		t.Fatalf("9 focused %q on a 5-column lane", m.focusedColumnName())
	}
}

func idsOf(rows []store.Entry) string {
	parts := make([]string, len(rows))
	for i, e := range rows {
		parts[i] = strconv.Itoa(e.ID)
	}
	return strings.Join(parts, ",")
}
