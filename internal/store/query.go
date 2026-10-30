package store

import (
	"sort"
	"strconv"
	"strings"
	"unicode"

	"evalctl/internal/model"
)

// Filter narrows a lane's rows. Empty fields mean "any"; Dupes keeps only rows that have a
// near-twin elsewhere in the same lane — the trimming case.
type Filter struct {
	Skill  string
	Label  string // "yes" / "no" (trigger only)
	Split  string // "train" / "validation" (trigger only)
	Search string
	In     string // search only this column ("" = every text column)
	Dupes  bool
}

// Entry is the read-only view of one row, shared by the CLI and the TUI.
type Entry struct {
	Lane   string `json:"lane"`
	Index  int    `json:"index"` // index into the lane's slice
	ID     int    `json:"id"`
	Skill  string `json:"skill"`
	Slug   string `json:"slug,omitempty"`
	Label  string `json:"label,omitempty"` // "yes" / "no" (trigger), "" (output)
	Split  string `json:"split,omitempty"`
	Text   string `json:"text"` // query / prompt
	Why    string `json:"why,omitempty"`
	Dupe   int    `json:"dupe,omitempty"`   // 1-based near-duplicate cluster, 0 = unique
	Assert int    `json:"assert,omitempty"` // output: how many assertions the case declares
	// Assertions is the case's grading list, carried so a search can find a case by what it
	// checks ("which case covers the manifest?") instead of by what it was asked.
	Assertions []string `json:"assertions,omitempty"`
}

// Rows lists a lane's entries in file order, filtered.
func (s *Store) Rows(lane string, f Filter) []Entry {
	clusters := s.DupeClusters(lane, DefaultDupeThreshold)
	out := []Entry{}
	switch lane {
	case LaneTrigger:
		for i, q := range s.Trigger.Queries {
			e := Entry{Lane: lane, Index: i, ID: q.ID, Skill: q.Skill, Split: q.Split,
				Text: q.Query, Why: q.Why, Dupe: clusters[rowKey(lane, q.Skill, i)]}
			if q.ShouldTrigger {
				e.Label = "yes"
			} else {
				e.Label = "no"
			}
			if f.matches(e) {
				out = append(out, e)
			}
		}
	case LaneOutput:
		for _, sk := range s.Skills {
			for i, c := range sk.Output.Evals {
				e := Entry{Lane: lane, Index: i, ID: c.ID, Skill: sk.Name, Slug: c.Slug,
					Text: c.Prompt, Dupe: clusters[rowKey(lane, sk.Name, i)], Assert: len(c.Assertions),
					Assertions: c.Assertions}
				if f.matches(e) {
					out = append(out, e)
				}
			}
		}
	}
	return out
}

// EntryAt rebuilds one row's view.
func (s *Store) EntryAt(lane string, skill string, index int) (Entry, bool) {
	switch lane {
	case LaneTrigger:
		if index < 0 || index >= len(s.Trigger.Queries) {
			return Entry{}, false
		}
		q := s.Trigger.Queries[index]
		e := Entry{Lane: lane, Index: index, ID: q.ID, Skill: q.Skill, Split: q.Split,
			Text: q.Query, Why: q.Why, Dupe: s.DupeClusters(lane, DefaultDupeThreshold)[rowKey(lane, q.Skill, index)]}
		if q.ShouldTrigger {
			e.Label = "yes"
		} else {
			e.Label = "no"
		}
		return e, true
	case LaneOutput:
		sk := s.Skill(skill)
		if sk == nil || index < 0 || index >= len(sk.Output.Evals) {
			return Entry{}, false
		}
		c := sk.Output.Evals[index]
		return Entry{Lane: lane, Index: index, ID: c.ID, Skill: sk.Name, Slug: c.Slug,
			Text: c.Prompt, Dupe: s.DupeClusters(lane, DefaultDupeThreshold)[rowKey(lane, sk.Name, index)],
			Assert: len(c.Assertions), Assertions: c.Assertions}, true
	}
	return Entry{}, false
}

func (f Filter) matches(e Entry) bool {
	if f.Skill != "" && e.Skill != f.Skill {
		return false
	}
	if f.Label != "" && e.Label != f.Label {
		return false
	}
	if f.Split != "" && e.Split != f.Split {
		return false
	}
	if f.Dupes && e.Dupe == 0 {
		return false
	}
	if q := strings.TrimSpace(f.Search); q != "" {
		needle := strings.ToLower(q)
		var hay string
		if f.In != "" && ColumnExists(e.Lane, f.In) {
			hay = strings.ToLower(ColumnValue(e, f.In))
		} else {
			hay = strings.ToLower(strings.Join(append([]string{e.Text, e.Slug, e.Why, e.Skill, e.Split},
				e.Assertions...), " "))
		}
		if !strings.Contains(hay, needle) {
			return false
		}
	}
	return true
}

// Queries mirrors the file's trigger queries in order, for the CLI.
func (s *Store) Queries() []model.TriggerQuery { return s.Trigger.Queries }

// DefaultDupeThreshold is the word-overlap at which two prompts are "the same test twice".
const DefaultDupeThreshold = 0.6

// DupeClusters groups a lane's rows by text similarity. Key: rowKey(lane, skill, index).
// Value: 1-based cluster id, present only for rows that have at least one near-twin.
func (s *Store) DupeClusters(lane string, threshold float64) map[string]int {
	type item struct {
		key   string
		words map[string]bool
	}
	var items []item
	switch lane {
	case LaneTrigger:
		for i, q := range s.Trigger.Queries {
			items = append(items, item{rowKey(lane, q.Skill, i), tokenSet(q.Query)})
		}
	case LaneOutput:
		for _, sk := range s.Skills {
			for i, c := range sk.Output.Evals {
				items = append(items, item{rowKey(lane, sk.Name, i), tokenSet(c.Prompt)})
			}
		}
	}

	parent := make([]int, len(items))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if jaccard(items[i].words, items[j].words) >= threshold {
				ri, rj := find(i), find(j)
				if ri != rj {
					parent[rj] = ri
				}
			}
		}
	}
	groups := map[int][]string{}
	for i := range items {
		groups[find(i)] = append(groups[find(i)], items[i].key)
	}
	clusters := map[string]int{}
	id := 0
	// Deterministic ids: order groups by their smallest key.
	var roots []int
	for root, members := range groups {
		if len(members) > 1 {
			roots = append(roots, root)
		}
	}
	sort.Slice(roots, func(a, b int) bool { return minKey(groups[roots[a]]) < minKey(groups[roots[b]]) })
	for _, root := range roots {
		id++
		for _, key := range groups[root] {
			clusters[key] = id
		}
	}
	return clusters
}

// DupeGroups returns the near-duplicate groups as rows, for reporting.
func (s *Store) DupeGroups(lane string, threshold float64) [][]Entry {
	clusters := s.DupeClusters(lane, threshold)
	byCluster := map[int][]Entry{}
	for _, e := range s.Rows(lane, Filter{}) {
		group := clusters[rowKey(lane, e.Skill, e.Index)]
		if group == 0 {
			continue
		}
		byCluster[group] = append(byCluster[group], e)
	}
	var ids []int
	for id := range byCluster {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([][]Entry, 0, len(ids))
	for _, id := range ids {
		out = append(out, byCluster[id])
	}
	return out
}

// rowKey identifies a row across skills without a global index: trigger rows are per-file,
// output rows are per-skill.
func rowKey(lane, skill string, index int) string {
	return lane + "|" + skill + "|" + strconv.Itoa(index)
}

func minKey(in []string) string {
	m := in[0]
	for _, v := range in[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true, "was": true, "were": true,
	"it": true, "this": true, "that": true, "these": true, "those": true, "to": true, "of": true,
	"for": true, "and": true, "or": true, "in": true, "on": true, "at": true, "with": true,
	"i": true, "me": true, "my": true, "we": true, "you": true, "your": true, "do": true,
	"does": true, "just": true, "so": true, "but": true, "be": true, "as": true, "by": true,
	"from": true, "not": true, "no": true, "if": true, "then": true, "there": true, "here": true,
	"have": true, "has": true, "need": true, "want": true, "can": true, "what": true,
	"when": true, "which": true, "one": true, "all": true, "now": true, "please": true,
}

func tokenSet(text string) map[string]bool {
	set := map[string]bool{}
	var b strings.Builder
	flush := func() {
		w := b.String()
		b.Reset()
		if w != "" && len(w) > 1 && !stopWords[w] {
			set[w] = true
		}
	}
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return set
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for w := range a {
		if b[w] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}
