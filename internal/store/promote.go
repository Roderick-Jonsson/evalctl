package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"evalctl/internal/model"
	"evalctl/internal/stage"
)

// PromotedFrom records where a case's fixture came from: another stage's eval output. It rides
// in the case JSON under a key the agentskills.io format does not define, so it survives every
// round trip like any other unknown key — and a reader of evals.json sees the ancestry without
// opening the producing skill's workspace, which is gitignored.
type PromotedFrom struct {
	Skill         string `json:"skill,omitempty"` // source-research — the workspace the run lives in
	Run           string `json:"run"`             // iteration-2
	Case          string `json:"case"`            // src-4-agentskills
	Arm           string `json:"arm"`             // with_skill
	FixtureSHA256 string `json:"fixture_sha256"`  // hash of the fixture as promoted
}

// RefOf names a run unambiguously: source-research@iteration-2. A run with no owning workspace
// (the trigger lane) is just its own name.
func RefOf(skill, run string) string {
	if skill == "" {
		return run
	}
	return skill + "@" + run
}

// Ref names the source run unambiguously: source-research@iteration-2.
func (p *PromotedFrom) Ref() string {
	if p == nil {
		return ""
	}
	return RefOf(p.Skill, p.Run)
}

// ProvenanceKey is the case JSON key a PromotedFrom is stored under.
const ProvenanceKey = "promoted_from"

// DecodePromotedFrom reads a case's promotion ancestry; nil when the case was not promoted.
func DecodePromotedFrom(c model.OutputCase) (*PromotedFrom, error) {
	raw, ok := c.Extra[ProvenanceKey]
	if !ok {
		return nil, nil
	}
	var p PromotedFrom
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", ProvenanceKey, err)
	}
	return &p, nil
}

// PromoteRequest is one fixture promotion: a finished run's produced workspace, copied into a
// new case's fixture with the target stage's own outputs left behind.
type PromoteRequest struct {
	Skill       string
	Slug        string
	ID          int
	Prompt      string
	Expected    string
	Source      string // the arm dir under <skill>-workspace/iteration-N/<case>/
	SourceSkill string // provenance: the skill whose workspace the run lives in
	Run         string // provenance: iteration-2
	Case        string // provenance: the run's case slug
	Arm         string // provenance: with_skill
	DryRun      bool
}

// Promotion is what a promote does, or would do on a dry run.
type Promotion struct {
	Skill       string   `json:"skill"`
	SourceSkill string   `json:"source_skill,omitempty"`
	Slug        string   `json:"slug"`
	ID          int      `json:"id"`
	Run         string   `json:"run"`
	Case        string   `json:"case"`
	Arm         string   `json:"arm"`
	Source      string   `json:"source"`
	Workspaces  []string `json:"workspaces"`
	Files       []string `json:"files"`
	Skipped     []string `json:"skipped"`
	FixtureDir  string   `json:"fixture_dir"`
	SHA256      string   `json:"fixture_sha256"`
}

// PromotedQueryFrom records where a trigger row's text came from: the output case whose prompt
// it is. It rides in the query's own JSON, under the same unknown key an output promotion uses,
// so `evalctl show` and a reader of trigger-evals.json see the ancestry.
type PromotedQueryFrom struct {
	Skill string `json:"skill"` // source-research
	Case  int    `json:"case"`  // 4
	Slug  string `json:"slug"`  // src-4-agentskills
}

// DecodePromotedQuery reads a trigger row's promotion ancestry; nil when it was written by hand.
func DecodePromotedQuery(q model.TriggerQuery) (*PromotedQueryFrom, error) {
	raw, ok := q.Extra[ProvenanceKey]
	if !ok {
		return nil, nil
	}
	var p PromotedQueryFrom
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", ProvenanceKey, err)
	}
	return &p, nil
}

// PromotePromptRequest is one prompt promotion: an output case's prompt becomes a row of the
// trigger lane. Index is the case's row in Skill; Under is the skill the query tests (empty =
// the case's own skill) — naming another stage with ShouldTrigger=false is how a case prompt
// becomes a real negative for a sibling skill.
type PromotePromptRequest struct {
	Skill         string
	Index         int
	Under         string
	Split         string // train or validation (empty = train)
	ShouldTrigger bool
	Why           string // empty = a sentence naming the source case
	ID            int    // empty = next free trigger id
	DryRun        bool
}

// QueryPromotion is what a prompt promotion does, or would do on a dry run.
type QueryPromotion struct {
	ID            int    `json:"id"`
	Skill         string `json:"skill"`
	Split         string `json:"split"`
	ShouldTrigger bool   `json:"should_trigger"`
	Why           string `json:"why"`
	Query         string `json:"query"`
	SourceSkill   string `json:"source_skill"`
	SourceID      int    `json:"source_case"`
	SourceSlug    string `json:"source_slug"`
	Index         int    `json:"index"`
}

// PromotePrompt turns an output case's prompt into a trigger query: same text, the case's skill
// under test, and the provenance recorded on the row. The row is only appended in memory; the
// caller saves.
//
// An identical query is refused rather than duplicated: the trigger set answers "does the right
// skill load for this request?", and two rows of the same text measure one thing twice.
func (s *Store) PromotePrompt(req PromotePromptRequest) (QueryPromotion, error) {
	src := s.Skill(req.Skill)
	if src == nil {
		return QueryPromotion{}, fmt.Errorf("unknown stage skill %q", req.Skill)
	}
	if req.Index < 0 || req.Index >= len(src.Output.Evals) {
		return QueryPromotion{}, fmt.Errorf("%s case row %d is out of range", req.Skill, req.Index)
	}
	cse := src.Output.Evals[req.Index]
	query := strings.TrimSpace(cse.Prompt)
	if query == "" {
		return QueryPromotion{}, fmt.Errorf("%s case #%d has no prompt to promote", src.Name, cse.ID)
	}
	if req.Under == "" {
		req.Under = src.Name
	}
	under := s.Skill(req.Under)
	if under == nil {
		return QueryPromotion{}, fmt.Errorf("unknown stage skill %q", req.Under)
	}
	if !under.TriggerOK {
		return QueryPromotion{}, fmt.Errorf("%s/evals/%s is missing — the row would have no file to live in",
			under.Name, TriggerFileName)
	}
	norm := normalizeQuery(query)
	for _, q := range s.Queries() {
		if normalizeQuery(q.Query) == norm {
			return QueryPromotion{}, fmt.Errorf("trigger #%d already carries this prompt (%s, %s) — nothing to promote",
				q.ID, q.Skill, splitOrLabel(q))
		}
	}
	split := strings.TrimSpace(req.Split)
	if split == "" {
		split = "train"
	}
	if split != "train" && split != "validation" {
		return QueryPromotion{}, fmt.Errorf("split must be train or validation, got %q", split)
	}
	id := req.ID
	if id == 0 {
		id = s.NextTriggerID(under.Name)
	}
	out := QueryPromotion{ID: id, Skill: under.Name, Split: split, ShouldTrigger: req.ShouldTrigger,
		Why: promptWhy(req, src.Name, cse), Query: query, SourceSkill: src.Name, SourceID: cse.ID,
		SourceSlug: cse.Slug, Index: len(s.QueriesOf(under.Name))}
	if req.DryRun {
		return out, nil
	}

	prov, err := json.Marshal(PromotedQueryFrom{Skill: src.Name, Case: cse.ID, Slug: cse.Slug})
	if err != nil {
		return QueryPromotion{}, err
	}
	q := model.TriggerQuery{ID: id, Skill: under.Name, ShouldTrigger: req.ShouldTrigger, Split: split,
		Why: out.Why, Query: query, Extra: map[string]json.RawMessage{ProvenanceKey: prov}}
	if err := s.AddTrigger(under.Name, q); err != nil {
		return QueryPromotion{}, err
	}
	return out, nil
}

// promptWhy is the reason a human reads when the row fails: the flag the prompt originally came
// from, and what the row claims about the skill it tests.
func promptWhy(req PromotePromptRequest, source string, cse model.OutputCase) string {
	if why := strings.TrimSpace(req.Why); why != "" {
		return why
	}
	why := fmt.Sprintf("promoted from %s case #%d (%s): the prompt a graded case was given",
		source, cse.ID, cse.Slug)
	switch {
	case req.Under == source && req.ShouldTrigger:
		return why + fmt.Sprintf(" — it must select %s on its own", source)
	case req.Under == source:
		return why + fmt.Sprintf(" — it must not select %s", source)
	case req.ShouldTrigger:
		return why + fmt.Sprintf(" — it must select %s too", req.Under)
	default:
		return why + fmt.Sprintf(" — it must not select %s", req.Under)
	}
}

// PromoteOutput turns a run's output into the next stage's fixture: it copies the MC-[topic]/
// workspaces the arm produced, leaves the target stage's own outputs (stage.Outputs) behind,
// appends a case carrying the provenance, and never overwrites an existing fixture dir.
//
// The case is only appended in memory; the caller saves. A dry run reads the source and reports
// what it would stage without touching the tree.
func (s *Store) PromoteOutput(req PromoteRequest) (Promotion, error) {
	sk := s.Skill(req.Skill)
	if sk == nil || !sk.OnDisk {
		return Promotion{}, fmt.Errorf("unknown stage skill %q (promote needs a skill with an evals/evals.json)", req.Skill)
	}
	if req.ID == 0 {
		req.ID = s.NextCaseID(req.Skill)
	}

	src, err := filepath.Abs(req.Source)
	if err != nil {
		return Promotion{}, err
	}
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
		return Promotion{}, fmt.Errorf("source run dir not found: %s", req.Source)
	}
	src = armOutputs(src)

	content, files, skipped, workspaces, err := readPromotion(src, req.Skill)
	if err != nil {
		return Promotion{}, err
	}
	if len(files) == 0 {
		return Promotion{}, fmt.Errorf("nothing to promote: %s holds no MC-*/ workspace (or the target stage's outputs are all of it)", src)
	}
	dir := s.FixtureDir(req.Skill, req.Slug)
	if s.FixtureExists(req.Skill, req.Slug) {
		return Promotion{}, fmt.Errorf("fixture %s already exists — a promotion never overwrites one; pick a new slug",
			rel(s.Root, dir))
	}

	cse := model.OutputCase{ID: req.ID, Slug: req.Slug, Prompt: req.Prompt,
		ExpectedOutput: req.Expected, Files: files}
	// checkOutput owns slug/id/prompt rules: the same ones `add` and the runner apply.
	if err := s.checkOutput(req.Skill, cse, -1); err != nil {
		return Promotion{}, err
	}

	out := Promotion{Skill: req.Skill, SourceSkill: req.SourceSkill, Slug: req.Slug, ID: req.ID,
		Run: req.Run, Case: req.Case, Arm: req.Arm, Source: src, Workspaces: workspaces,
		Files: files, Skipped: skipped, FixtureDir: dir}
	if req.DryRun {
		return out, nil
	}

	for _, name := range files {
		target := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return Promotion{}, err
		}
		if err := os.WriteFile(target, content[name], 0o644); err != nil {
			return Promotion{}, err
		}
	}
	sha, err := FixtureSHA256(dir)
	if err != nil {
		return Promotion{}, err
	}
	out.SHA256 = sha

	prov, err := json.Marshal(PromotedFrom{Skill: req.SourceSkill, Run: req.Run, Case: req.Case,
		Arm: req.Arm, FixtureSHA256: sha})
	if err != nil {
		return Promotion{}, err
	}
	if cse.Extra == nil {
		cse.Extra = map[string]json.RawMessage{}
	}
	cse.Extra[ProvenanceKey] = prov
	if err := s.AddOutput(req.Skill, cse); err != nil {
		return Promotion{}, err
	}
	return out, nil
}

// PromotionPreview is the read-only half of a promotion: what a copy of src into skill's fixture
// would stage and what it would leave behind. It touches nothing, so an interactive form can
// call it on every change of the source.
func PromotionPreview(src, skill string) (Promotion, error) {
	abs, err := filepath.Abs(src)
	if err != nil {
		return Promotion{}, err
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return Promotion{}, fmt.Errorf("source run dir not found: %s", src)
	}
	_, files, skipped, workspaces, err := readPromotion(abs, skill)
	if err != nil {
		return Promotion{}, err
	}
	return Promotion{Skill: skill, Source: abs, Workspaces: workspaces, Files: files, Skipped: skipped}, nil
}

// armOutputs is where a run's produced tree lives: <arm>/outputs. A run from the older layout kept
// its MC-*/ workspaces directly in the arm dir, and those still read.
func armOutputs(dir string) string {
	outputs := filepath.Join(dir, "outputs")
	if info, err := os.Stat(outputs); err == nil && info.IsDir() {
		return outputs
	}
	return dir
}

// readPromotion reads a run's produced tree into memory: only MC-*/ workspaces are carried (the arm
// dir also holds the runner's own artifacts), and `_archive/` stays behind because a fixture is
// a run's input, not its history. The target stage's own outputs (stage.Own) are skipped: they
// are the stage's answers, and a fixture must hold only its inputs.
func readPromotion(src, skill string) (content map[string][]byte, files, skipped, workspaces []string, err error) {
	content = map[string][]byte{}
	src = armOutputs(src)
	entries, err := os.ReadDir(src)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "MC-") {
			continue
		}
		root := filepath.Join(src, entry.Name())
		workspaces = append(workspaces, entry.Name())
		walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if path == root {
				return nil
			}
			name := rel(root, path)
			key := filepath.ToSlash(filepath.Join(entry.Name(), name))
			if stage.Own(skill, d.Name()) {
				if d.IsDir() {
					skipped = append(skipped, key+"/")
					return fs.SkipDir
				}
				skipped = append(skipped, key)
				return nil
			}
			if d.IsDir() {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			content[key] = b
			files = append(files, key)
			return nil
		})
		if walkErr != nil {
			return nil, nil, nil, nil, walkErr
		}
	}
	sort.Strings(files)
	sort.Strings(skipped)
	sort.Strings(workspaces)
	return content, files, skipped, workspaces, nil
}

// FixtureSHA256 fingerprints a fixture dir the way meta.json records it: every file's relative
// path and bytes, sorted. A fixture edited after its run shows up as a different hash than the
// one the case declared at promotion time. An absent fixture hashes to "".
func FixtureSHA256(dir string) (string, error) {
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.IsDir()) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	content := map[string][]byte{}
	var paths []string
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel(dir, path))
		content[name] = b
		paths = append(paths, name)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		h.Write([]byte(p))
		h.Write([]byte{0})
		h.Write(content[p])
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
