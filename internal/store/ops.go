package store

import (
	"fmt"
	"sort"
	"strings"

	"evalctl/internal/model"
)

// RowRef points at one mutable row.
type RowRef struct {
	Lane  string `json:"lane"`
	Skill string `json:"skill"`
	Index int    `json:"index"`
}

// DeleteRows removes the given rows. Fixture dirs go too only when withFixture is set, and
// only for output rows. Rows are deleted highest-index first so earlier indices stay valid.
// It returns a human description of everything that changed; the caller writes the files.
func (s *Store) DeleteRows(refs []RowRef, withFixture bool) ([]string, error) {
	var changes []string
	bySkill := map[string][]int{}
	triggerBySkill := map[string][]int{}
	for _, r := range refs {
		switch r.Lane {
		case LaneTrigger:
			triggerBySkill[r.Skill] = append(triggerBySkill[r.Skill], r.Index)
		case LaneOutput:
			bySkill[r.Skill] = append(bySkill[r.Skill], r.Index)
		default:
			return changes, fmt.Errorf("unknown lane %q", r.Lane)
		}
	}

	for skill, indices := range triggerBySkill {
		sk := s.Skill(skill)
		if sk == nil {
			return changes, fmt.Errorf("unknown skill %q", skill)
		}
		sort.Sort(sort.Reverse(sort.IntSlice(indices)))
		for _, i := range indices {
			if i < 0 || i >= len(sk.Trigger.Queries) {
				return changes, fmt.Errorf("%s trigger row %d is out of range", skill, i)
			}
			q := sk.Trigger.Queries[i]
			sk.Trigger.Queries = append(sk.Trigger.Queries[:i], sk.Trigger.Queries[i+1:]...)
			changes = append(changes, fmt.Sprintf("removed trigger #%d (%s, %s, %s)",
				q.ID, q.Skill, splitOrLabel(q), clip(q.Query, 60)))
		}
	}

	for skill, indices := range bySkill {
		sk := s.Skill(skill)
		if sk == nil {
			return changes, fmt.Errorf("unknown skill %q", skill)
		}
		sort.Sort(sort.Reverse(sort.IntSlice(indices)))
		for _, i := range indices {
			if i < 0 || i >= len(sk.Output.Evals) {
				return changes, fmt.Errorf("%s case row %d is out of range", skill, i)
			}
			c := sk.Output.Evals[i]
			sk.Output.Evals = append(sk.Output.Evals[:i], sk.Output.Evals[i+1:]...)
			changes = append(changes, fmt.Sprintf("removed %s case #%d (%s)", skill, c.ID, c.Slug))
			if withFixture && s.FixtureExists(skill, c.Slug) {
				if err := s.DeleteFixture(skill, c.Slug); err != nil {
					return changes, err
				}
				changes = append(changes, "  deleted fixture "+rel(s.Root, s.FixtureDir(skill, c.Slug)))
			}
		}
	}
	return changes, nil
}

// AddTrigger appends a query to one skill's file, refusing duplicate ids and a row that names
// another skill: the file a row sits in IS the skill under test.
func (s *Store) AddTrigger(skill string, q model.TriggerQuery) error {
	sk := s.Skill(skill)
	if sk == nil {
		return fmt.Errorf("unknown skill %q", skill)
	}
	q.Skill = skill
	if err := s.checkTrigger(skill, q, -1); err != nil {
		return err
	}
	sk.Trigger.Queries = append(sk.Trigger.Queries, q)
	sk.TriggerOK = true
	return nil
}

// UpdateTrigger replaces the query at index in one skill's file.
func (s *Store) UpdateTrigger(skill string, index int, q model.TriggerQuery) error {
	sk := s.Skill(skill)
	if sk == nil {
		return fmt.Errorf("unknown skill %q", skill)
	}
	if index < 0 || index >= len(sk.Trigger.Queries) {
		return fmt.Errorf("%s trigger row %d is out of range", skill, index)
	}
	q.Skill = skill
	if err := s.checkTrigger(skill, q, index); err != nil {
		return err
	}
	sk.Trigger.Queries[index] = q
	return nil
}

// SetTriggerRuns records how many times each of one skill's queries is measured. It is the set's own
// knob — the run that reads it reads it from the file — so it is written where the queries are.
func (s *Store) SetTriggerRuns(skill string, runs int) error {
	if runs < 1 {
		return fmt.Errorf("runs must be at least 1")
	}
	sk := s.Skill(skill)
	if sk == nil || !sk.TriggerOK {
		return fmt.Errorf("missing %s", s.TriggerPathFor(skill))
	}
	sk.Trigger.Runs = &runs
	return s.SaveTrigger(skill)
}

func (s *Store) checkTrigger(skill string, q model.TriggerQuery, self int) error {
	if q.ID <= 0 {
		return fmt.Errorf("id must be a positive integer")
	}
	if q.Skill != "" && q.Skill != skill {
		return fmt.Errorf("skill is %q but this row lives in %s/evals/%s — a row's skill is its file", q.Skill, skill, TriggerFileName)
	}
	for i, other := range s.QueriesOf(skill) {
		if i != self && other.ID == q.ID {
			return fmt.Errorf("id %d is already used by %s", q.ID, clip(other.Query, 40))
		}
	}
	if strings.TrimSpace(q.Query) == "" {
		return fmt.Errorf("query is empty")
	}
	if !KnownSplit(q.Split) {
		return fmt.Errorf("split must be train, validation or holdout")
	}
	return nil
}

// AddOutput appends a case to a skill, refusing a slug another case already owns.
func (s *Store) AddOutput(skill string, c model.OutputCase) error {
	sk := s.EnsureSkill(skill)
	if sk == nil {
		return fmt.Errorf("invalid skill name %q", skill)
	}
	if err := s.checkOutput(skill, c, -1); err != nil {
		return err
	}
	if c.Files == nil {
		c.Files = []string{}
	}
	sk.Output.Evals = append(sk.Output.Evals, c)
	return nil
}

// UpdateOutput replaces a case, moving its fixture when the slug changes and moving the case
// to another skill's file when the skill changes.
func (s *Store) UpdateOutput(skill string, index int, c model.OutputCase) ([]string, error) {
	sk := s.Skill(skill)
	if sk == nil {
		return nil, fmt.Errorf("unknown skill %q", skill)
	}
	if index < 0 || index >= len(sk.Output.Evals) {
		return nil, fmt.Errorf("%s case row %d is out of range", skill, index)
	}
	if err := s.checkOutput(skill, c, index); err != nil {
		return nil, err
	}
	if c.Files == nil {
		c.Files = []string{}
	}
	old := sk.Output.Evals[index]
	var notes []string

	if old.Slug != c.Slug {
		if err := s.RenameFixture(skill, old.Slug, c.Slug); err != nil {
			return nil, err
		}
		notes = append(notes, fmt.Sprintf("fixture %s -> %s", old.Slug, c.Slug))
	}
	sk.Output.Evals[index] = c
	return notes, nil
}

// MoveOutput moves a case to another stage skill: the case leaves this file, keeps its slug
// and fixture (moved with it), and takes the target's next free id.
func (s *Store) MoveOutput(skill string, index int, toSkill string) ([]string, error) {
	src := s.Skill(skill)
	if src == nil {
		return nil, fmt.Errorf("unknown skill %q", skill)
	}
	if index < 0 || index >= len(src.Output.Evals) {
		return nil, fmt.Errorf("%s case row %d is out of range", skill, index)
	}
	if toSkill == skill {
		return nil, nil
	}
	dst := s.EnsureSkill(toSkill)
	if dst == nil {
		return nil, fmt.Errorf("invalid skill name %q", toSkill)
	}
	c := src.Output.Evals[index]
	for i, other := range dst.Output.Evals {
		if other.Slug == c.Slug {
			return nil, fmt.Errorf("%s already has a case with slug %q (#%d)", toSkill, c.Slug, other.ID)
		}
		_ = i
	}
	notes := []string{fmt.Sprintf("%s case #%d %s -> %s", skill, c.ID, c.Slug, toSkill)}
	if err := s.MoveFixture(skill, toSkill, c.Slug); err != nil {
		return nil, err
	}
	c.ID = s.NextCaseID(toSkill)
	src.Output.Evals = append(src.Output.Evals[:index], src.Output.Evals[index+1:]...)
	dst.Output.Evals = append(dst.Output.Evals, c)
	return notes, nil
}

func (s *Store) checkOutput(skill string, c model.OutputCase, self int) error {
	if !SlugPattern.MatchString(c.Slug) {
		return fmt.Errorf("slug must be lowercase a-z, 0-9 and hyphens, got %q", c.Slug)
	}
	if c.ID <= 0 {
		return fmt.Errorf("id must be a positive integer")
	}
	sk := s.Skill(skill)
	if sk != nil {
		for i, other := range sk.Output.Evals {
			if i == self {
				continue
			}
			if other.ID == c.ID {
				return fmt.Errorf("%s already has a case with id %d", skill, c.ID)
			}
		}
	}
	for _, other := range s.Skills {
		for i, oc := range other.Output.Evals {
			if other.Name == skill && i == self {
				continue
			}
			if oc.Slug == c.Slug {
				return fmt.Errorf("slug %q is already used by %s case #%d", c.Slug, other.Name, oc.ID)
			}
		}
	}
	if strings.TrimSpace(c.Prompt) == "" {
		return fmt.Errorf("prompt is empty")
	}
	return nil
}

// RenumberTrigger closes id gaps inside one skill's file and keeps its order. This breaks
// comparability with earlier result dirs (q<id>-<skill>), which is why it is never automatic.
func (s *Store) RenumberTrigger(skill string) []string {
	sk := s.Skill(skill)
	if sk == nil {
		return nil
	}
	var changes []string
	for i := range sk.Trigger.Queries {
		want := i + 1
		if sk.Trigger.Queries[i].ID != want {
			changes = append(changes, fmt.Sprintf("%s trigger #%d -> #%d", skill, sk.Trigger.Queries[i].ID, want))
			sk.Trigger.Queries[i].ID = want
		}
	}
	return changes
}

// RenumberOutput closes id gaps inside one skill, in file order.
func (s *Store) RenumberOutput(skill string) []string {
	sk := s.Skill(skill)
	if sk == nil {
		return nil
	}
	var changes []string
	for i := range sk.Output.Evals {
		want := i + 1
		if sk.Output.Evals[i].ID != want {
			changes = append(changes, fmt.Sprintf("%s #%d -> #%d", skill, sk.Output.Evals[i].ID, want))
			sk.Output.Evals[i].ID = want
		}
	}
	return changes
}

func splitOrLabel(q model.TriggerQuery) string {
	if q.ShouldTrigger {
		return q.Split + ", should-trigger"
	}
	return q.Split + ", should-not-trigger"
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
