package store

import (
	"sort"

	"evalctl/internal/format"
)

// SkillSummary is one stage skill as a unit of work: the cases it owns, how many of them carry
// assertions, the fixtures on disk, the trigger rows that test its description, and the
// validation findings that belong to it (its own cases and its own queries).
//
// This is the shape both faces start from — the TUI's skill list and `evalctl skills` — because
// the set is worked on one skill at a time, and a flat list of every row in every lane hides
// which skill a row belongs to and which skill is actually in trouble.
type SkillSummary struct {
	Skill      string `json:"skill"`
	Cases      int    `json:"cases"`
	Assertions int    `json:"assertions"` // assertions across every case
	Graded     int    `json:"cases_with_assertions"`
	Fixtures   int    `json:"fixtures"` // cases with a fixture dir on disk
	Files      int    `json:"fixture_files"`
	Queries    int    `json:"queries"` // trigger rows naming this skill
	Should     int    `json:"should"`
	ShouldNot  int    `json:"should_not"`
	Errors     int    `json:"errors"`
	Warnings   int    `json:"warnings"`
	OnDisk     bool   `json:"evals_on_disk"`
}

// Health is the badge a reader needs: clean, warnings, or errors.
func (s SkillSummary) Health() string {
	switch {
	case s.Errors > 0:
		return format.Plural(s.Errors, "error")
	case s.Warnings > 0:
		return format.Plural(s.Warnings, "warning")
	}
	return "clean"
}

// Summaries is every stage skill, alphabetically, with its counts. A skill the trigger file
// names but the tree does not carry gets a row too: an orphaned row set is exactly the kind of
// scattered state this view exists to surface.
func (s *Store) Summaries() []SkillSummary {
	bySkill := map[string]*SkillSummary{}
	order := []string{}
	get := func(name string) *SkillSummary {
		if sum, ok := bySkill[name]; ok {
			return sum
		}
		sum := &SkillSummary{Skill: name}
		bySkill[name] = sum
		order = append(order, name)
		return sum
	}

	for _, sk := range s.Skills {
		sum := get(sk.Name)
		sum.OnDisk = sk.OnDisk
		for _, c := range sk.Output.Evals {
			sum.Cases++
			if n := len(c.Assertions); n > 0 {
				sum.Graded++
				sum.Assertions += n
			}
			if s.FixtureExists(sk.Name, c.Slug) {
				sum.Fixtures++
				sum.Files += len(s.FixtureFiles(sk.Name, c.Slug))
			}
		}
	}
	for _, q := range s.Trigger.Queries {
		sum := get(q.Skill)
		if q.Skill == "" {
			continue
		}
		sum.Queries++
		if q.ShouldTrigger {
			sum.Should++
		} else {
			sum.ShouldNot++
		}
	}
	for _, i := range s.Validate() {
		if i.Skill == "" {
			continue
		}
		sum := get(i.Skill)
		if i.Severity == SeverityError {
			sum.Errors++
		} else {
			sum.Warnings++
		}
	}

	out := make([]SkillSummary, 0, len(order))
	for _, name := range order {
		out = append(out, *bySkill[name])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Skill < out[j].Skill })
	return out
}

// SummaryOf is one skill's counts, and whether the tree knows the skill at all.
func (s *Store) SummaryOf(name string) (SkillSummary, bool) {
	for _, sum := range s.Summaries() {
		if sum.Skill == name {
			return sum, true
		}
	}
	return SkillSummary{Skill: name}, false
}
