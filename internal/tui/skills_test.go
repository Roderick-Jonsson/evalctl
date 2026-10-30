package tui

import (
	"strings"
	"testing"

	"evalctl/internal/store"
)

// skillModel is the model as the binary opens it: on the skill list, not on a lane.
func skillModel(t *testing.T) *Model {
	t.Helper()
	m := sized(newModel(testStore(t), store.LaneTrigger), 140, 40)
	m.openSkills()
	return m
}

func TestSkillListCountsEverySkill(t *testing.T) {
	m := skillModel(t)
	view := m.View()
	for _, want := range []string{"SKILL", "CASES", "GRADED", "FIXT", "FILES", "TRIG+", "TRIG-", "HEALTH", "alpha", "beta", "all skills"} {
		if !strings.Contains(view, want) {
			t.Fatalf("skill list missing %q:\n%s", want, view)
		}
	}
	// The aggregate row is first: the way back to the unscoped view should not be at the bottom
	// of a list that grows with every stage.
	if len(m.skills.rows) != 3 || !m.skills.rows[0].all {
		t.Fatalf("rows = %d (first all=%v), want the aggregate plus two skills",
			len(m.skills.rows), len(m.skills.rows) > 0 && m.skills.rows[0].all)
	}
	total := m.skills.rows[0].sum
	if total.Cases != 2 || total.Queries != 4 || total.Fixtures != 1 || total.Files != 1 {
		t.Fatalf("aggregate = %+v, want 2 cases, 4 queries, 1 fixture dir with 1 file", total)
	}
	alpha := m.skills.rows[1].sum
	if alpha.Skill != "alpha" || alpha.Cases != 1 || alpha.Queries != 2 || alpha.Should != 1 || alpha.ShouldNot != 1 {
		t.Fatalf("alpha = %+v", alpha)
	}
}

func TestEnteringASkillScopesBothLanes(t *testing.T) {
	m := skillModel(t)
	m.Update(key("j"))     // off the aggregate row, onto alpha
	m.Update(key("enter")) // open it
	if m.mode != screenList {
		t.Fatalf("enter did not open the list (mode %v)", m.mode)
	}
	if m.skill != "alpha" || m.lane != store.LaneOutput {
		t.Fatalf("scope = %q lane = %q, want alpha on its output cases", m.skill, m.lane)
	}
	if len(m.rows) != 1 || m.rows[0].Skill != "alpha" || m.rows[0].Lane != store.LaneOutput {
		t.Fatalf("rows = %+v, want alpha's one case", m.rows)
	}
	// Scoped, the SKILL column would repeat the header on every row.
	if strings.Contains(m.View(), "SKILL") {
		t.Fatalf("scoped view still draws the skill column:\n%s", m.View())
	}
	if !strings.Contains(m.View(), " alpha ") {
		t.Fatalf("header does not name the scope:\n%s", m.View())
	}

	m.Update(key("tab"))
	if len(m.rows) != 2 || m.rows[0].Lane != store.LaneTrigger {
		t.Fatalf("tab rows = %+v, want alpha's two trigger rows", m.rows)
	}
	for _, e := range m.rows {
		if e.Skill != "alpha" {
			t.Fatalf("tab left the scope: %+v", e)
		}
	}

	// esc is the way back — after it has cleared a column focus and a selection, as before.
	m.Update(key("esc"))
	if m.mode != screenSkills {
		t.Fatalf("esc did not return to the skill list (mode %v)", m.mode)
	}
	// The list reopens on the skill you came from, marked, so the way back in is where you left it.
	row := m.skills.rows[m.skills.cursor]
	if row.all || row.sum.Skill != "alpha" {
		t.Fatalf("skill list reopened on %+v, want alpha", row.sum)
	}
}

func TestAggregateRowIsTheUnscopedView(t *testing.T) {
	m := skillModel(t)
	m.Update(key("enter")) // the aggregate row is the cursor's start
	if m.skill != "" || m.mode != screenList {
		t.Fatalf("scope = %q mode = %v, want the unscoped list", m.skill, m.mode)
	}
	if len(m.rows) != 4 || !strings.Contains(m.View(), "SKILL") {
		t.Fatalf("unscoped rows = %d, want every trigger row with the skill column", len(m.rows))
	}
}

func TestNewCaseInAScopedSkillPrefillsTheSkill(t *testing.T) {
	m := skillModel(t)
	m.Update(key("j"))
	m.Update(key("enter"))
	m.Update(key("n"))
	if m.mode != screenEditor || m.editor == nil {
		t.Fatal("n did not open the case editor")
	}
	if m.editor.d.skill != "alpha" {
		t.Fatalf("new case skill = %q, want alpha", m.editor.d.skill)
	}
}

func TestEditorWritesAssertions(t *testing.T) {
	s := testStore(t)
	m := sized(newModel(s, store.LaneOutput), 140, 40)
	m.lane = store.LaneOutput
	m.openEditor(store.Entry{Lane: store.LaneOutput, Skill: "alpha", Index: 0}, false)
	if m.editor == nil {
		t.Fatal("editor did not open")
	}
	m.editor.d.assert = "The output is a file\nIt names the operator, the pattern"
	m.editor.apply(m)

	c := s.Skill("alpha").Output.Evals[0]
	want := []string{"The output is a file", "It names the operator, the pattern"}
	if len(c.Assertions) != len(want) {
		t.Fatalf("assertions = %v, want %v", c.Assertions, want)
	}
	for i := range want {
		if c.Assertions[i] != want[i] {
			t.Fatalf("assertions = %v, want %v (a comma is content, not a separator)", c.Assertions, want)
		}
	}
	if !m.dirty {
		t.Fatal("an assertion edit must mark the store dirty")
	}
	// The list shows the count, so an assertion edit is visible without opening the case.
	m.mode = screenList
	m.refresh()
	if m.rows[0].Assert != 2 {
		t.Fatalf("row assert count = %d, want 2", m.rows[0].Assert)
	}
}
