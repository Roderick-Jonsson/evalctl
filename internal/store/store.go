// Package store reads and writes the two eval lanes on disk. It never runs anything: it is the
// only place that touches <root>/<skill>/evals/evals.json and <root>/<skill>/evals/trigger-evals.json.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"evalctl/internal/model"
)

const (
	LaneTrigger = "trigger"
	LaneOutput  = "output"

	TriggerFileName = "trigger-evals.json"

	// The trigger set's three splits. Train guides a description change, validation judges whether
	// it generalizes, and holdout is the fresh set written at the end, which no loop iteration may
	// measure — freshness is the whole of its value.
	SplitTrain      = "train"
	SplitValidation = "validation"
	SplitHoldout    = "holdout"
)

// KnownSplit reports whether a query's declared split is one the runner accepts.
func KnownSplit(split string) bool {
	return split == SplitTrain || split == SplitValidation || split == SplitHoldout
}

// Skill is one stage skill: its name, its case file, the trigger set that tests its description,
// and the rows in each.
type Skill struct {
	Name        string
	Dir         string // <root>/<name>
	FilePath    string // <root>/<name>/evals/evals.json
	TriggerPath string // <root>/<name>/evals/trigger-evals.json
	Output      model.OutputFile
	Trigger     model.TriggerFile
	OnDisk      bool // evals.json exists (a trigger-only skill may not)
	TriggerOK   bool // trigger-evals.json exists and parsed
}

// Store is the whole eval tree in memory. Edits mutate it; writes are explicit and atomic.
type Store struct {
	Root   string
	Skills []*Skill
}

// Open loads the tree rooted at dir (walking up if dir is inside one).
func Open(dir string) (*Store, error) {
	root, err := FindRoot(dir)
	if err != nil {
		return nil, err
	}
	s := &Store{Root: root}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		// evals/ is a skill's own case dir and *-workspace/ a skill's run output: neither is a
		// stage skill.
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "evals" ||
			strings.HasSuffix(e.Name(), "-workspace") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		path := filepath.Join(dir, "evals", "evals.json")
		triggerPath := filepath.Join(dir, "evals", TriggerFileName)
		sk := &Skill{Name: e.Name(), Dir: dir, FilePath: path, TriggerPath: triggerPath}
		sk.Output.SkillName = e.Name()

		if _, err := os.Stat(path); err != nil {
			// A dir with a SKILL.md and no eval file yet is a skill somebody has just made. Hiding it
			// until a case exists hides the two things the tool is for — a description to tune and a
			// set to declare — and a skill you cannot see is a skill you cannot start.
			if _, skillErr := os.Stat(filepath.Join(dir, "SKILL.md")); skillErr != nil {
				continue
			}
		} else {
			if err := readJSON(path, &sk.Output); err != nil {
				return nil, fmt.Errorf("%s: %w", rel(root, path), err)
			}
			if sk.Output.SkillName == "" {
				sk.Output.SkillName = e.Name()
			}
			sk.OnDisk = true
			for i := range sk.Output.Evals {
				if sk.Output.Evals[i].Files == nil {
					sk.Output.Evals[i].Files = []string{}
				}
			}
		}

		// The trigger set lives beside the cases: one file per skill, and a row's `skill` is the
		// file it sits in.
		if _, err := os.Stat(triggerPath); err == nil {
			if err := readJSON(triggerPath, &sk.Trigger); err != nil {
				return nil, fmt.Errorf("%s: %w", rel(root, triggerPath), err)
			}
			sk.TriggerOK = true
		}
		s.Skills = append(s.Skills, sk)
	}
	sort.Slice(s.Skills, func(i, j int) bool { return s.Skills[i].Name < s.Skills[j].Name })
	return s, nil
}

// FindRoot walks up from start to the project root: the dir holding stage skills, each with its
// cases (<skill>/evals/evals.json) or its trigger set (<skill>/evals/trigger-evals.json).
func FindRoot(start string) (string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	dir := abs
	for {
		if hasSkillDirs(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no eval tree found at or above %s (expected <skill>/evals/evals.json or <skill>/evals/%s)", abs, TriggerFileName)
		}
		dir = parent
	}
}

func hasSkillDirs(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name(), "evals")
		if isFile(filepath.Join(dir, "evals.json")) || isFile(filepath.Join(dir, TriggerFileName)) {
			return true
		}
	}
	return false
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// ---------------------------------------------------------------- lookups

// SkillNames lists stage skills in alphabetical order.
func (s *Store) SkillNames() []string {
	out := make([]string, 0, len(s.Skills))
	for _, sk := range s.Skills {
		out = append(out, sk.Name)
	}
	return out
}

// Skill looks a stage skill up, creating an empty in-memory one if it is new but legal.
func (s *Store) Skill(name string) *Skill {
	name = strings.TrimSpace(name)
	for _, sk := range s.Skills {
		if sk.Name == name {
			return sk
		}
	}
	return nil
}

// EnsureSkill returns the skill, adding an in-memory entry (written on save) when missing.
func (s *Store) EnsureSkill(name string) *Skill {
	if sk := s.Skill(name); sk != nil {
		return sk
	}
	if name == "" || strings.ContainsAny(name, `/\`) {
		return nil
	}
	sk := &Skill{
		Name:        name,
		Dir:         filepath.Join(s.Root, name),
		FilePath:    filepath.Join(s.Root, name, "evals", "evals.json"),
		TriggerPath: filepath.Join(s.Root, name, "evals", TriggerFileName),
		Output:      model.OutputFile{SkillName: name},
	}
	s.Skills = append(s.Skills, sk)
	sort.Slice(s.Skills, func(i, j int) bool { return s.Skills[i].Name < s.Skills[j].Name })
	return sk
}

// Triggers lists the skills that carry a trigger set, in pipeline order: one file per skill, so
// this is the lane's whole population.
func (s *Store) Triggers() []*Skill {
	var out []*Skill
	for _, sk := range s.Skills {
		if sk.TriggerOK {
			out = append(out, sk)
		}
	}
	return out
}

// QueriesOf is one skill's trigger rows, in file order.
func (s *Store) QueriesOf(skill string) []model.TriggerQuery {
	sk := s.Skill(skill)
	if sk == nil {
		return nil
	}
	return sk.Trigger.Queries
}

// TriggerOf is the skill's trigger file knobs: how many runs and what threshold. ok is false when
// the skill has no trigger set at all.
func (s *Store) TriggerOf(skill string) (model.TriggerFile, bool) {
	sk := s.Skill(skill)
	if sk == nil || !sk.TriggerOK {
		return model.TriggerFile{}, false
	}
	return sk.Trigger, true
}

// KnownSkills is every stage skill the tree carries. A trigger row's `skill` is the file it lives
// in, so there are no orphans to keep editable.
func (s *Store) KnownSkills() []string {
	return s.SkillNames()
}

// SkillOfSlug finds the stage skill a run's case slug belongs to, so a promotion can default to
// the skill that produced the artifact (references) or the one that consumes it (fixtures)
// instead of guessing from a directory name.
func (s *Store) SkillOfSlug(slug string) string {
	for _, sk := range s.Skills {
		for _, c := range sk.Output.Evals {
			if c.Slug == slug {
				return sk.Name
			}
		}
	}
	return ""
}

// TriggerPathFor is one skill's trigger file.
func (s *Store) TriggerPathFor(skill string) string {
	sk := s.Skill(skill)
	if sk == nil {
		return filepath.Join(s.Root, skill, "evals", TriggerFileName)
	}
	return sk.TriggerPath
}

func (s *Store) OutputPathFor(skill string) string {
	sk := s.Skill(skill)
	if sk == nil {
		return filepath.Join(s.Root, skill, "evals", "evals.json")
	}
	return sk.FilePath
}

// FixtureDir is <skill>/evals/files/<slug>/ — the run root mirror for one case.
func (s *Store) FixtureDir(skill, slug string) string {
	return filepath.Join(s.Root, skill, "evals", "files", slug)
}

// FixtureFiles lists the fixture dir relative to itself, sorted; nil when it does not exist.
func (s *Store) FixtureFiles(skill, slug string) []string {
	dir := s.FixtureDir(skill, slug)
	var out []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		out = append(out, rel(dir, path))
		return nil
	})
	sort.Strings(out)
	return out
}

func (s *Store) FixtureExists(skill, slug string) bool {
	info, err := os.Stat(s.FixtureDir(skill, slug))
	return err == nil && info.IsDir()
}

// ---------------------------------------------------------------- writes

// SaveTrigger writes one skill's trigger lane, backing up whatever was there.
func (s *Store) SaveTrigger(skill string) error {
	sk := s.Skill(skill)
	if sk == nil {
		return fmt.Errorf("unknown skill %q", skill)
	}
	b, err := json.MarshalIndent(sk.Trigger, "", "  ")
	if err != nil {
		return err
	}
	if err := s.writeFile(sk.TriggerPath, append(b, '\n')); err != nil {
		return err
	}
	sk.TriggerOK = true
	return nil
}

// SaveSkill writes one stage skill's case file.
func (s *Store) SaveSkill(name string) error {
	sk := s.Skill(name)
	if sk == nil {
		return fmt.Errorf("unknown skill %q", name)
	}
	sk.Output.SkillName = sk.Name
	b, err := json.MarshalIndent(sk.Output, "", "  ")
	if err != nil {
		return err
	}
	if err := s.writeFile(sk.FilePath, append(b, '\n')); err != nil {
		return err
	}
	sk.OnDisk = true
	return nil
}

// SaveAll writes both lanes. A lane whose file does not exist is not created.
func (s *Store) SaveAll() error {
	var errs []string
	for _, sk := range s.Skills {
		if sk.TriggerOK {
			if err := s.SaveTrigger(sk.Name); err != nil {
				errs = append(errs, err.Error())
			}
		}
		if sk.OnDisk {
			if err := s.SaveSkill(sk.Name); err != nil {
				errs = append(errs, err.Error())
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (s *Store) writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
		if err := s.backup(path); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".evalctl-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// BackupFile copies a file's bytes into .evalctl/backups under their timestamped name, the way every
// write of this tool's own does. It exists for the files a lane writes INTO a workspace — a case's
// feedback.json is not the store's, but a note typed into it is as easy to lose as an assertion, and
// the recovery has to be the same one. A file that is not there yet has nothing to back up.
func (s *Store) BackupFile(path string) error {
	if !isFile(path) {
		return nil
	}
	return s.backup(path)
}

// backup copies the previous bytes of path into .evalctl/backups so a bad trim is undoable.
func (s *Store) backup(path string) error {
	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.Root, ".evalctl", "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	stamp := time.Now().Format("20060102-150405.000000")
	flat := strings.ReplaceAll(rel(s.Root, path), string(os.PathSeparator), "_")
	name := fmt.Sprintf("%s--%s", stamp, flat)
	if err := os.WriteFile(filepath.Join(dir, name), old, 0o644); err != nil {
		return err
	}
	return pruneBackups(dir, 200)
}

func pruneBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var files []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e)
		}
	}
	if len(files) <= keep {
		return nil
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })
	for _, e := range files[:len(files)-keep] {
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
	return nil
}

// DeleteFixture removes a case's fixture dir. Refuses to leave evals/files/ entirely.
func (s *Store) DeleteFixture(skill, slug string) error {
	dir := s.FixtureDir(skill, slug)
	if !strings.HasPrefix(filepath.Clean(dir), filepath.Clean(filepath.Join(s.Root, skill, "evals", "files"))+string(os.PathSeparator)) {
		return fmt.Errorf("refusing to delete outside the fixture dir: %s", dir)
	}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return os.RemoveAll(dir)
}

// RenameFixture moves a fixture dir when a case's slug changes.
func (s *Store) RenameFixture(skill, oldSlug, newSlug string) error {
	if oldSlug == newSlug || oldSlug == "" || newSlug == "" {
		return nil
	}
	from := s.FixtureDir(skill, oldSlug)
	to := s.FixtureDir(skill, newSlug)
	if _, err := os.Stat(from); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if _, err := os.Stat(to); err == nil {
		return fmt.Errorf("fixture dir for %q already exists: %s", newSlug, rel(s.Root, to))
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return os.Rename(from, to)
}

// MoveFixture carries a case's fixture dir to another skill when the case moves skills.
func (s *Store) MoveFixture(fromSkill, toSkill, slug string) error {
	if fromSkill == toSkill {
		return nil
	}
	from := s.FixtureDir(fromSkill, slug)
	to := s.FixtureDir(toSkill, slug)
	if _, err := os.Stat(from); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if _, err := os.Stat(to); err == nil {
		return fmt.Errorf("%s already has a fixture dir for %q", toSkill, slug)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return os.Rename(from, to)
}

// ---------------------------------------------------------------- ids

// NextTriggerID is max(id)+1 inside one skill's file: ids stay stable forever, deletions leave gaps
// on purpose so old result dirs (q<id>-<skill>) keep pointing at the same query.
func (s *Store) NextTriggerID(skill string) int {
	max := 0
	for _, q := range s.QueriesOf(skill) {
		if q.ID > max {
			max = q.ID
		}
	}
	return max + 1
}

// NextCaseID is max(id)+1 for one skill's cases.
func (s *Store) NextCaseID(skill string) int {
	sk := s.Skill(skill)
	if sk == nil {
		return 1
	}
	max := 0
	for _, c := range sk.Output.Evals {
		if c.ID > max {
			max = c.ID
		}
	}
	return max + 1
}

// ---------------------------------------------------------------- helpers

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func rel(base, path string) string {
	r, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return r
}

// AtoiField parses a CLI numeric argument with a clearer error than strconv.
func AtoiField(name, val string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil {
		return 0, fmt.Errorf("%s must be a number, got %q", name, val)
	}
	return n, nil
}
