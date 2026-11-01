package runner

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"evalctl/internal/store"
)

// Case is one graded output case, resolved against the tree: the case's declared identity (its
// slug), its prompt, and the fixture entries it stages.
type Case struct {
	Skill          string              `json:"skill"`
	ID             int                 `json:"id"`
	Slug           string              `json:"slug"`
	Prompt         string              `json:"prompt"`
	ExpectedOutput string              `json:"expected_output"`
	Files          []string            `json:"files"`
	Assertions     []string            `json:"assertions"`
	Promoted       *store.PromotedFrom `json:"promoted_from,omitempty"`
}

// SlugRE is a single directory name: the case's identity and its fixture dir.
var SlugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// LoadCases resolves the selected output cases. `only` nil means every case.
//
// The slug is declared on every case, never derived from a path — so a case cannot stage another
// case's fixture, `files` entries stay relative to it, and slug/`files` can never disagree.
func LoadCases(s *store.Store, skills []string, only map[int]bool) ([]Case, error) {
	var cases []Case
	for _, skill := range skills {
		sk := s.Skill(skill)
		if sk == nil || !sk.OnDisk {
			return nil, fmt.Errorf("missing %s/evals/evals.json", skill)
		}
		if sk.Output.SkillName != skill {
			return nil, fmt.Errorf("%s/evals/evals.json: skill_name is %q, expected %q",
				skill, sk.Output.SkillName, skill)
		}
		for _, c := range sk.Output.Evals {
			if only != nil && !only[c.ID] {
				continue
			}
			if strings.TrimSpace(c.Prompt) == "" {
				return nil, fmt.Errorf("%s case %d: missing 'prompt'", skill, c.ID)
			}
			slug := strings.TrimSpace(c.Slug)
			if !SlugRE.MatchString(slug) {
				return nil, fmt.Errorf("%s case %d: 'slug' must be a single dir name "+
					"(a-z 0-9 hyphen), got %q", skill, c.ID, c.Slug)
			}
			files := c.Files
			if files == nil {
				files = []string{}
			}
			promoted, err := store.DecodePromotedFrom(c)
			if err != nil {
				return nil, fmt.Errorf("%s case %d: %w", skill, c.ID, err)
			}
			cases = append(cases, Case{Skill: skill, ID: c.ID, Slug: slug, Prompt: c.Prompt,
				ExpectedOutput: c.ExpectedOutput, Files: files, Assertions: c.Assertions, Promoted: promoted})
		}
	}
	// The slug is the fixture dir AND the results dir name, so it must be unique per iteration.
	seen := map[string]string{}
	for _, c := range cases {
		where := fmt.Sprintf("%s case %d", c.Skill, c.ID)
		if prev, ok := seen[c.Slug]; ok {
			return nil, fmt.Errorf("duplicate slug %q: %s and %s", c.Slug, prev, where)
		}
		seen[c.Slug] = where
	}
	return cases, nil
}

// TriggerCase is one row of the trigger lane, resolved.
type TriggerCase struct {
	ID            int    `json:"id"`
	Skill         string `json:"skill"`
	Query         string `json:"query"`
	ShouldTrigger bool   `json:"should_trigger"`
	Split         string `json:"split"`
	Why           string `json:"why,omitempty"`
}

// TriggerConfig is the set-level knobs from trigger-evals.json, with the defaults filled in.
type TriggerConfig struct {
	Runs      int     `json:"runs"`
	Threshold float64 `json:"threshold"`
}

// TriggerConfigOf reads the file's runs/threshold, falling back to the documented defaults.
func (r *Runner) TriggerConfigOf() TriggerConfig {
	cfg := TriggerConfig{Runs: DefaultTriggerRuns, Threshold: DefaultTriggerThreshold}
	if r.Store.Trigger.Runs != nil && *r.Store.Trigger.Runs > 0 {
		cfg.Runs = *r.Store.Trigger.Runs
	}
	if r.Store.Trigger.Threshold != nil {
		cfg.Threshold = *r.Store.Trigger.Threshold
	}
	return cfg
}

// The trigger set's three splits. Train guides a description change, validation judges whether it
// generalizes, and holdout is the fresh set the guide asks for at the end: it is never part of a
// loop iteration, so `all` (the loop's own run) deliberately leaves it alone. The names live in the
// store because they are what the file says; the runner reads them from there.
const (
	SplitTrain      = store.SplitTrain
	SplitValidation = store.SplitValidation
	SplitHoldout    = store.SplitHoldout
)

// LoopSplits are the splits a loop iteration measures and scores.
var LoopSplits = []string{SplitTrain, SplitValidation}

// KnownSplit reports whether a query's declared split is one the runner accepts.
func KnownSplit(split string) bool { return store.KnownSplit(split) }

// LoadTriggerQueries validates the whole file — the guide's split rule is about the file, not
// the selection — then returns the rows the invocation asked for.
func (r *Runner) LoadTriggerQueries(skills []string, only map[int]bool, split string) ([]TriggerCase, error) {
	if !r.Store.TriggerOK {
		return nil, fmt.Errorf("missing evals/%s", store.TriggerFileName)
	}
	raw := r.Store.Trigger.Queries
	if len(raw) == 0 {
		return nil, fmt.Errorf("%s: 'queries' must be a non-empty list", store.TriggerFileName)
	}
	cases := make([]TriggerCase, 0, len(raw))
	seen := map[int]bool{}
	for _, q := range raw {
		where := fmt.Sprintf("%s query %d", store.TriggerFileName, q.ID)
		if q.ID <= 0 {
			return nil, fmt.Errorf("%s: every query needs a positive integer 'id'", where)
		}
		if seen[q.ID] {
			return nil, fmt.Errorf("%s: duplicate id", where)
		}
		seen[q.ID] = true
		if !r.Profiles.IsSkill(q.Skill) {
			return nil, fmt.Errorf("%s: 'skill' must be one of %s, got %q", where,
				strings.Join(r.Profiles.Skills, ", "), q.Skill)
		}
		if !KnownSplit(q.Split) {
			return nil, fmt.Errorf("%s: 'split' must be train, validation or holdout, got %q", where, q.Split)
		}
		if strings.TrimSpace(q.Query) == "" {
			return nil, fmt.Errorf("%s: 'query' is empty", where)
		}
		cases = append(cases, TriggerCase{ID: q.ID, Skill: q.Skill, Query: strings.TrimSpace(q.Query),
			ShouldTrigger: q.ShouldTrigger, Split: q.Split, Why: q.Why})
	}
	// Each half has to carry both labels or the pass rates measure different things.
	for _, name := range LoopSplits {
		labels := map[bool]string{}
		for _, c := range cases {
			if c.Split == name {
				labels[c.ShouldTrigger] = c.Skill
			}
		}
		if _, yes := labels[true]; !yes {
			return nil, fmt.Errorf("%s: split %q needs should-trigger AND should-not-trigger queries, "+
				"found none that should trigger", store.TriggerFileName, name)
		}
		if _, no := labels[false]; !no {
			return nil, fmt.Errorf("%s: split %q needs should-trigger AND should-not-trigger queries, "+
				"found none that should not trigger", store.TriggerFileName, name)
		}
	}
	if only != nil {
		var missing []string
		for id := range only {
			if !seen[id] {
				missing = append(missing, fmt.Sprintf("%d", id))
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return nil, fmt.Errorf("unknown trigger query id(s): %s", strings.Join(missing, ", "))
		}
	}
	picked := make([]TriggerCase, 0, len(cases))
	for _, c := range cases {
		if !contains(skills, c.Skill) {
			continue
		}
		if only != nil && !only[c.ID] {
			continue
		}
		// "all" is the loop's own set: train and validation only. A holdout row is fresh on
		// purpose, so measuring it here is how it stops being fresh.
		if split == "all" {
			if c.Split == SplitHoldout {
				continue
			}
		} else if c.Split != split {
			continue
		}
		picked = append(picked, c)
	}
	return picked, nil
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// FrontmatterDescription is the `description:` value from SKILL.md frontmatter, block scalars
// folded flat — the text the router actually reads. The parser lives in the store next to the
// writer, so the text the lab measures and the text it writes cannot be read two different ways.
func (r *Runner) FrontmatterDescription(skill string) string {
	return r.Store.SkillDescription(skill)
}

// LintRow is the guide's description principles as an advisory — never a gate.
type LintRow struct {
	Skill          string `json:"skill"`
	Chars          int    `json:"chars"`
	LimitOK        bool   `json:"limit_ok"`
	Imperative     bool   `json:"imperative"`
	CoversUnstated bool   `json:"covers_unstated"`
	Description    string `json:"description"`
}

// LintDescriptions checks the four description principles. Triggering is the verdict; this table
// just explains it.
func (r *Runner) LintDescriptions(skills []string) []LintRow {
	rows := make([]LintRow, 0, len(skills))
	for _, skill := range skills {
		text := r.FrontmatterDescription(skill)
		low := strings.ToLower(text)
		rows = append(rows, LintRow{
			Skill:          skill,
			Chars:          len([]rune(text)),
			LimitOK:        len([]rune(text)) <= DescriptionLimit,
			Imperative:     strings.HasPrefix(low, "use this skill") || strings.HasPrefix(low, "use when") || useRE.MatchString(low),
			CoversUnstated: strings.Contains(low, "even if"),
			Description:    text,
		})
	}
	return rows
}

var useRE = regexp.MustCompile(`^use\b`)
