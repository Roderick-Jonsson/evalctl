package model

import (
	"bytes"
	"encoding/json"
)

// DescriptionLedger is one skill's record of a description-optimization loop: the candidates the
// description went through, the one-line reason each exists, and which one SKILL.md was given.
//
// It is the only description state the tool owns. Measurements are not stored here — a trigger
// iteration already writes every skill's description text into its own meta.json, so the loop is
// joined against that by hash instead of copied into a second file that would drift from the first.
//
// The file is committed with the set: the reasoning behind a description is as much part of the
// eval set as the `why` on a query, and a description nobody can explain is one the next change
// will undo.
type DescriptionLedger struct {
	Skill      string                 `json:"skill"`
	Applied    *AppliedDescription    `json:"applied,omitempty"`
	Candidates []DescriptionCandidate `json:"candidates"`

	Extra map[string]json.RawMessage `json:"-"`
}

type descriptionLedgerAlias DescriptionLedger

func (l *DescriptionLedger) UnmarshalJSON(b []byte) error {
	var a descriptionLedgerAlias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*l = DescriptionLedger(a)
	var err error
	l.Extra, err = extras(b, "skill", "applied", "candidates")
	return err
}

func (l DescriptionLedger) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	for _, f := range []struct {
		name string
		val  any
	}{
		{"skill", l.Skill},
		{"applied", l.Applied},
		{"candidates", nonNilCandidates(l.Candidates)},
	} {
		if f.name == "applied" && l.Applied == nil {
			continue
		}
		if err := field(&b, &first, f.name, f.val); err != nil {
			return nil, err
		}
	}
	if err := extraFields(&b, &first, l.Extra); err != nil {
		return nil, err
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// AppliedDescription is the winner the loop settled on, and the iteration that measured it. The
// hash is what makes drift detectable: if SKILL.md's description no longer hashes to this, the file
// has moved since the last measurement and the number beside it describes text nobody has run.
type AppliedDescription struct {
	Candidate int    `json:"candidate"`
	Iteration int    `json:"iteration,omitempty"`
	SHA256    string `json:"sha256"`
	At        string `json:"at,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

type appliedAlias AppliedDescription

func (a *AppliedDescription) UnmarshalJSON(b []byte) error {
	var v appliedAlias
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*a = AppliedDescription(v)
	var err error
	a.Extra, err = extras(b, "candidate", "iteration", "sha256", "at")
	return err
}

func (a AppliedDescription) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	for _, f := range []struct {
		name string
		val  any
	}{
		{"candidate", a.Candidate},
		{"iteration", a.Iteration},
		{"sha256", a.SHA256},
		{"at", a.At},
	} {
		if f.name == "iteration" && a.Iteration == 0 {
			continue
		}
		if f.name == "at" && a.At == "" {
			continue
		}
		if err := field(&b, &first, f.name, f.val); err != nil {
			return nil, err
		}
	}
	if err := extraFields(&b, &first, a.Extra); err != nil {
		return nil, err
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// DescriptionCandidate is one wording of the description: the text, what changed and why, and the
// candidate it was revised from. Parent is what makes a revision a record and not a rewrite: the
// triage of the next iteration can say which failures this revision fixed and which it broke.
type DescriptionCandidate struct {
	ID      int    `json:"id"`
	Parent  int    `json:"parent,omitempty"`
	Origin  string `json:"origin,omitempty"` // "file" — copied from SKILL.md; "revision" — written in the lab
	Note    string `json:"note,omitempty"`
	Created string `json:"created,omitempty"`
	Text    string `json:"text"`

	Extra map[string]json.RawMessage `json:"-"`
}

// Candidate origins.
const (
	CandidateFromFile     = "file"
	CandidateFromRevision = "revision"
)

type candidateAlias DescriptionCandidate

func (c *DescriptionCandidate) UnmarshalJSON(b []byte) error {
	var v candidateAlias
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*c = DescriptionCandidate(v)
	var err error
	c.Extra, err = extras(b, "id", "parent", "origin", "note", "created", "text")
	return err
}

func (c DescriptionCandidate) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	for _, f := range []struct {
		name string
		val  any
	}{
		{"id", c.ID},
		{"parent", c.Parent},
		{"origin", c.Origin},
		{"note", c.Note},
		{"created", c.Created},
		{"text", c.Text},
	} {
		if f.name == "parent" && c.Parent == 0 {
			continue
		}
		if (f.name == "origin" || f.name == "note" || f.name == "created") && f.val.(string) == "" {
			continue
		}
		if err := field(&b, &first, f.name, f.val); err != nil {
			return nil, err
		}
	}
	if err := extraFields(&b, &first, c.Extra); err != nil {
		return nil, err
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func nonNilCandidates(in []DescriptionCandidate) []DescriptionCandidate {
	if in == nil {
		return []DescriptionCandidate{}
	}
	return in
}
