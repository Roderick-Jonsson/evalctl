package store

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Severity ranks an issue by what it costs: Error means the runner refuses the tree or the
// manager cannot stage it; Warning means the tree runs but the measurement is unsound.
type Severity int

const (
	SeverityError Severity = iota
	SeverityWarning
)

func (s Severity) String() string {
	if s == SeverityError {
		return "error"
	}
	return "warn"
}

// Issue is one validation finding, addressable back to the row it came from.
type Issue struct {
	Severity Severity
	Lane     string
	Skill    string
	ID       int
	Slug     string
	Msg      string
}

func (i Issue) String() string {
	where := i.Lane
	if i.Skill != "" {
		where += "/" + i.Skill
	}
	if i.ID != 0 {
		where += fmt.Sprintf(" #%d", i.ID)
	}
	if i.Slug != "" {
		where += " " + i.Slug
	}
	return fmt.Sprintf("%-5s %-28s %s", i.Severity.String(), where, i.Msg)
}

// SlugPattern is the runner's rule for a case slug: one directory name, lowercase.
var SlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Validate checks both lanes against the rules the runner enforces, plus the fixture-dir
// consistency it assumes. Order is stable: trigger first, then skills alphabetically.
func (s *Store) Validate() []Issue {
	var issues []Issue
	issues = append(issues, s.validateTrigger()...)

	slugOwner := map[string]string{}
	for _, sk := range s.Skills {
		issues = append(issues, s.validateSkill(sk)...)
		for _, c := range sk.Output.Evals {
			if c.Slug == "" {
				continue
			}
			if prev, dup := slugOwner[c.Slug]; dup {
				issues = append(issues, Issue{SeverityError, LaneOutput, sk.Name, c.ID, c.Slug,
					fmt.Sprintf("duplicate slug across skills: also used by %s", prev)})
			} else {
				slugOwner[c.Slug] = sk.Name
			}
		}
	}
	return issues
}

func (s *Store) validateTrigger() []Issue {
	if !s.TriggerOK {
		return []Issue{{SeverityError, LaneTrigger, "", 0, "",
			"trigger/trigger-evals.json is missing — the trigger lane cannot be managed"}}
	}
	var issues []Issue
	qs := s.Trigger.Queries
	if len(qs) == 0 {
		issues = append(issues, Issue{SeverityError, LaneTrigger, "", 0, "",
			"no queries: the runner treats an empty query list as fatal"})
	}

	known := map[string]bool{}
	for _, n := range s.SkillNames() {
		known[n] = true
	}
	seenIDs := map[int]bool{}
	splitLabels := map[string]map[bool]bool{SplitTrain: {}, SplitValidation: {}, SplitHoldout: {}}
	splitCounts := map[string]int{}
	textSeen := map[string]int{}

	for _, q := range qs {
		base := Issue{Lane: LaneTrigger, Skill: q.Skill, ID: q.ID}
		if q.ID <= 0 {
			one := base
			one.Severity = SeverityError
			one.Msg = "id must be a positive integer"
			issues = append(issues, one)
		}
		if seenIDs[q.ID] {
			one := base
			one.Severity = SeverityError
			one.Msg = "duplicate id"
			issues = append(issues, one)
		}
		seenIDs[q.ID] = true
		if !known[q.Skill] {
			one := base
			one.Severity = SeverityError
			one.Msg = fmt.Sprintf("skill %q has no evals/evals.json in the tree (the runner rejects it)", q.Skill)
			issues = append(issues, one)
		}
		if !KnownSplit(q.Split) {
			one := base
			one.Severity = SeverityError
			one.Msg = fmt.Sprintf("split must be train, validation or holdout, got %q", q.Split)
			issues = append(issues, one)
			continue
		}
		if strings.TrimSpace(q.Query) == "" {
			one := base
			one.Severity = SeverityError
			one.Msg = "query is empty"
			issues = append(issues, one)
		}
		splitLabels[q.Split][q.ShouldTrigger] = true
		splitCounts[q.Split]++

		norm := normalizeQuery(q.Query)
		if prev, dup := textSeen[norm]; dup && norm != "" {
			one := base
			one.Severity = SeverityWarning
			one.Msg = fmt.Sprintf("query text is identical to #%d", prev)
			issues = append(issues, one)
		} else if norm != "" {
			textSeen[norm] = q.ID
		}
	}

	// the runner hard-fails when a split holds only one label: both halves must carry both.
	for _, name := range []string{SplitTrain, SplitValidation} {
		labels := splitLabels[name]
		if len(labels) != 2 {
			issues = append(issues, Issue{SeverityError, LaneTrigger, "", 0, "",
				fmt.Sprintf("%s split has no %s queries — the runner refuses a split that carries one label",
					name, missingLabel(labels))})
		}
		if splitCounts[name] == 0 {
			issues = append(issues, Issue{SeverityError, LaneTrigger, "", 0, "",
				fmt.Sprintf("%s split is empty", name)})
		}
	}
	if total := splitCounts["train"] + splitCounts["validation"]; total > 0 {
		share := float64(splitCounts["validation"]) / float64(total)
		if share < 0.2 || share > 0.6 {
			issues = append(issues, Issue{SeverityWarning, LaneTrigger, "", 0, "",
				fmt.Sprintf("split is %d train / %d validation (%.0f%% validation); the guide asks for roughly 60/40",
					splitCounts["train"], splitCounts["validation"], share*100)})
		}
	}
	return issues
}

// normalizeQuery is the comparison both a duplicate check and a promotion use: whitespace runs
// collapse, case is ignored, so "Run  This " and "run this" are the same request.
func normalizeQuery(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func missingLabel(labels map[bool]bool) string {
	switch {
	case labels[true]:
		return "should-not-trigger"
	case labels[false]:
		return "should-trigger"
	default:
		return "any"
	}
}

func (s *Store) validateSkill(sk *Skill) []Issue {
	var issues []Issue
	if sk.Output.SkillName != sk.Name {
		issues = append(issues, Issue{SeverityError, LaneOutput, sk.Name, 0, "",
			fmt.Sprintf("skill_name is %q, the runner requires it to match the dir %q", sk.Output.SkillName, sk.Name)})
	}
	if len(sk.Output.Evals) == 0 {
		issues = append(issues, Issue{SeverityWarning, LaneOutput, sk.Name, 0, "",
			"no cases: the runner will have nothing to measure for this stage"})
	}
	seenIDs := map[int]bool{}
	for _, c := range sk.Output.Evals {
		base := Issue{Lane: LaneOutput, Skill: sk.Name, ID: c.ID, Slug: c.Slug}
		add := func(sev Severity, msg string) {
			one := base
			one.Severity = sev
			one.Msg = msg
			issues = append(issues, one)
		}
		if c.ID <= 0 {
			add(SeverityError, "id must be a positive integer")
		}
		if seenIDs[c.ID] {
			add(SeverityError, "duplicate id in this skill")
		}
		seenIDs[c.ID] = true
		if !SlugPattern.MatchString(c.Slug) {
			add(SeverityError, fmt.Sprintf("slug must be a-z 0-9 hyphen, got %q", c.Slug))
		}
		if strings.TrimSpace(c.Prompt) == "" {
			add(SeverityError, "prompt is empty")
		}
		if strings.TrimSpace(c.ExpectedOutput) == "" {
			add(SeverityWarning, "expected_output is empty: grading has nothing to check against")
		}
		for i, a := range c.Assertions {
			if strings.TrimSpace(a) == "" {
				add(SeverityError, fmt.Sprintf("assertion %d is empty — an empty assertion can never be graded", i+1))
			}
		}
		fixture := s.FixtureDir(sk.Name, c.Slug)
		exists := s.FixtureExists(sk.Name, c.Slug)
		if len(c.Files) > 0 && !exists {
			add(SeverityError, fmt.Sprintf("files are declared but the fixture dir is missing: %s", rel(s.Root, fixture)))
			continue
		}
		for _, f := range c.Files {
			if strings.HasPrefix(f, "/") || strings.Contains(f, "..") {
				add(SeverityError, fmt.Sprintf("file %q must be a path inside the fixture dir", f))
				continue
			}
			if !isFile(fixture + "/" + strings.TrimPrefix(f, "./")) {
				add(SeverityError, fmt.Sprintf("file %q is not on disk in %s", f, rel(s.Root, fixture)))
			}
		}
		if exists && len(c.Files) == 0 {
			add(SeverityWarning, fmt.Sprintf("fixture dir %s exists but files is empty — the runner stages the whole dir anyway",
				rel(s.Root, fixture)))
		}
	}
	return issues
}

// Stats is a lane's shape at a glance: what a trim is choosing between.
type Stats struct {
	Lane        string         `json:"lane"`
	Total       int            `json:"total"`
	BySkill     map[string]int `json:"by_skill"`
	ByLabel     map[string]int `json:"by_label"`
	BySplit     map[string]int `json:"by_split"`
	DupeRows    int            `json:"dupe_rows"`
	Clusters    int            `json:"dupe_clusters"`
	LongestText int            `json:"longest_text_chars"`
}

// Stats summarises a lane.
func (s *Store) Stats(lane string) Stats {
	st := Stats{Lane: lane, BySkill: map[string]int{}, ByLabel: map[string]int{}, BySplit: map[string]int{}}
	rows := s.Rows(lane, Filter{})
	clusters := map[int]bool{}
	for _, e := range rows {
		st.Total++
		st.BySkill[e.Skill]++
		if e.Label != "" {
			st.ByLabel[e.Label]++
		}
		if e.Split != "" {
			st.BySplit[e.Split]++
		}
		if e.Dupe != 0 {
			st.DupeRows++
			clusters[e.Dupe] = true
		}
		if n := len([]rune(e.Text)); n > st.LongestText {
			st.LongestText = n
		}
	}
	st.Clusters = len(clusters)
	return st
}

// SortedCounts flattens a count map for stable output.
func SortedCounts(m map[string]int) [][2]interface{} {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([][2]interface{}, 0, len(keys))
	for _, k := range keys {
		out = append(out, [2]interface{}{k, m[k]})
	}
	return out
}
