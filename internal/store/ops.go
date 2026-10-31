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
	var triggerIdx []int
	for _, r := range refs {
		switch r.Lane {
		case LaneTrigger:
			triggerIdx = append(triggerIdx, r.Index)
		case LaneOutput:
			bySkill[r.Skill] = append(bySkill[r.Skill], r.Index)
		default:
			return changes, fmt.Errorf("unknown lane %q", r.Lane)
		}
	}

	if len(triggerIdx) > 0 {
		sort.Sort(sort.Reverse(sort.IntSlice(triggerIdx)))
		for _, i := range triggerIdx {
			if i < 0 || i >= len(s.Trigger.Queries) {
				return changes, fmt.Errorf("trigger row %d is out of range", i)
			}
			q := s.Trigger.Queries[i]
			s.Trigger.Queries = append(s.Trigger.Queries[:i], s.Trigger.Queries[i+1:]...)
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

// AddTrigger appends a query, refusing duplicate ids.
func (s *Store) AddTrigger(q model.TriggerQuery) error {
	if err := s.checkTrigger(q, -1); err != nil {
		return err
	}
	s.Trigger.Queries = append(s.Trigger.Queries, q)
	return nil
}

// UpdateTrigger replaces the query at index, returning a fixture note when the row changed
// skill (trigger rows never own fixtures, so the note is informational).
func (s *Store) UpdateTrigger(index int, q model.TriggerQuery) error {
	if index < 0 || index >= len(s.Trigger.Queries) {
		return fmt.Errorf("trigger row %d is out of range", index)
	}
	if err := s.checkTrigger(q, index); err != nil {
		return err
	}
	s.Trigger.Queries[index] = q
	return nil
}

func (s *Store) checkTrigger(q model.TriggerQuery, self int) error {
	if q.ID <= 0 {
		return fmt.Errorf("id must be a positive integer")
	}
	for i, other := range s.Trigger.Queries {
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

// RenumberTrigger closes id gaps and keeps the file's order. This breaks comparability with
// earlier result dirs (q<id>-<skill>), which is why it is never automatic.
func (s *Store) RenumberTrigger() []string {
	var changes []string
	for i := range s.Trigger.Queries {
		want := i + 1
		if s.Trigger.Queries[i].ID != want {
			changes = append(changes, fmt.Sprintf("trigger #%d -> #%d", s.Trigger.Queries[i].ID, want))
			s.Trigger.Queries[i].ID = want
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
