// Package model holds the two eval lanes' on-disk shapes exactly as the runner reads them.
//
// Two rules drive everything here: the JSON must stay readable in git diffs (key order as
// humans wrote it) and unknown keys must survive a round trip untouched — the agentskills.io
// format grows (assertions is the documented next field) and a manager that eats a field it
// does not know about silently loses work.
package model

import (
	"bytes"
	"encoding/json"
	"sort"
)

// TriggerQuery is one row of evals/trigger-evals.json: the query, the skill under test, the
// label, the split, and the human-readable reason the row exists.
type TriggerQuery struct {
	ID            int    `json:"id"`
	Skill         string `json:"skill"`
	ShouldTrigger bool   `json:"should_trigger"`
	Split         string `json:"split"`
	Why           string `json:"why,omitempty"`
	Query         string `json:"query"`

	Extra map[string]json.RawMessage `json:"-"`
}

type triggerAlias TriggerQuery

func (q *TriggerQuery) UnmarshalJSON(b []byte) error {
	var a triggerAlias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*q = TriggerQuery(a)
	var err error
	q.Extra, err = extras(b, "id", "skill", "should_trigger", "split", "why", "query")
	return err
}

func (q TriggerQuery) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	for _, f := range []struct {
		name string
		val  any
	}{
		{"id", q.ID},
		{"skill", q.Skill},
		{"should_trigger", q.ShouldTrigger},
		{"split", q.Split},
		{"why", q.Why},
		{"query", q.Query},
	} {
		if f.name == "why" && f.val.(string) == "" {
			continue
		}
		if err := field(&b, &first, f.name, f.val); err != nil {
			return nil, err
		}
	}
	if err := extraFields(&b, &first, q.Extra); err != nil {
		return nil, err
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// TriggerFile is the trigger lane's file: the set-level knobs first, the queries last.
type TriggerFile struct {
	Note      string         `json:"note,omitempty"`
	Runs      *int           `json:"runs,omitempty"`
	Threshold *float64       `json:"threshold,omitempty"`
	Queries   []TriggerQuery `json:"queries"`

	Extra map[string]json.RawMessage `json:"-"`
}

type triggerFileAlias TriggerFile

func (f *TriggerFile) UnmarshalJSON(b []byte) error {
	var a triggerFileAlias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*f = TriggerFile(a)
	var err error
	f.Extra, err = extras(b, "note", "runs", "threshold", "queries")
	return err
}

func (f TriggerFile) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	if f.Note != "" {
		if err := field(&b, &first, "note", f.Note); err != nil {
			return nil, err
		}
	}
	if f.Runs != nil {
		if err := field(&b, &first, "runs", *f.Runs); err != nil {
			return nil, err
		}
	}
	if f.Threshold != nil {
		if err := field(&b, &first, "threshold", *f.Threshold); err != nil {
			return nil, err
		}
	}
	if err := field(&b, &first, "queries", nonNilQueries(f.Queries)); err != nil {
		return nil, err
	}
	if err := extraFields(&b, &first, f.Extra); err != nil {
		return nil, err
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// OutputCase is one graded case of <skill>/evals/evals.json. `files` is always present (the
// standard's pre-flight check wants the key) and always relative to the slug's fixture dir.
//
// `assertions` is the standard's grading unit: one verifiable statement per entry, graded PASS
// or FAIL with evidence. The key is written only when the case has any, because a case that has
// none yet reads better as the shape it actually is — the guide adds assertions after the first
// graded iteration, and an empty array in every case would say the opposite.
type OutputCase struct {
	ID             int      `json:"id"`
	Slug           string   `json:"slug"`
	Prompt         string   `json:"prompt"`
	ExpectedOutput string   `json:"expected_output"`
	Files          []string `json:"files"`
	Assertions     []string `json:"assertions,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

type outputAlias OutputCase

func (c *OutputCase) UnmarshalJSON(b []byte) error {
	var a outputAlias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*c = OutputCase(a)
	var err error
	c.Extra, err = extras(b, "id", "slug", "prompt", "expected_output", "files", "assertions")
	return err
}

func (c OutputCase) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	files := c.Files
	if files == nil {
		files = []string{}
	}
	for _, f := range []struct {
		name string
		val  any
	}{
		{"id", c.ID},
		{"slug", c.Slug},
		{"prompt", c.Prompt},
		{"expected_output", c.ExpectedOutput},
		{"files", files},
	} {
		if err := field(&b, &first, f.name, f.val); err != nil {
			return nil, err
		}
	}
	if len(c.Assertions) > 0 {
		if err := field(&b, &first, "assertions", c.Assertions); err != nil {
			return nil, err
		}
	}
	if err := extraFields(&b, &first, c.Extra); err != nil {
		return nil, err
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// OutputFile is one stage skill's case list.
type OutputFile struct {
	SkillName string       `json:"skill_name"`
	Evals     []OutputCase `json:"evals"`

	Extra map[string]json.RawMessage `json:"-"`
}

type outputFileAlias OutputFile

func (f *OutputFile) UnmarshalJSON(b []byte) error {
	var a outputFileAlias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*f = OutputFile(a)
	var err error
	f.Extra, err = extras(b, "skill_name", "evals")
	return err
}

func (f OutputFile) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	if err := field(&b, &first, "skill_name", f.SkillName); err != nil {
		return nil, err
	}
	evals := f.Evals
	if evals == nil {
		evals = []OutputCase{}
	}
	if err := field(&b, &first, "evals", evals); err != nil {
		return nil, err
	}
	if err := extraFields(&b, &first, f.Extra); err != nil {
		return nil, err
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// ---------------------------------------------------------------- helpers

func nonNilQueries(in []TriggerQuery) []TriggerQuery {
	if in == nil {
		return []TriggerQuery{}
	}
	return in
}

func field(b *bytes.Buffer, first *bool, name string, val any) error {
	raw, err := json.Marshal(val)
	if err != nil {
		return err
	}
	if !*first {
		b.WriteByte(',')
	}
	*first = false
	key, err := json.Marshal(name)
	if err != nil {
		return err
	}
	b.Write(key)
	b.WriteByte(':')
	b.Write(raw)
	return nil
}

func extraFields(b *bytes.Buffer, first *bool, extra map[string]json.RawMessage) error {
	if len(extra) == 0 {
		return nil
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys) // stable output: an untouched file keeps its byte shape on re-save
	for _, k := range keys {
		if err := field(b, first, k, extra[k]); err != nil {
			return err
		}
	}
	return nil
}

func extras(b []byte, known ...string) (map[string]json.RawMessage, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, err
	}
	for _, k := range known {
		delete(all, k)
	}
	if len(all) == 0 {
		return nil, nil
	}
	return all, nil
}
