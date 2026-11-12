// Package store reads and writes the two eval lanes on disk. It never runs anything: it is the
// only place that touches a stage skill's evals/evals.json and evals/trigger-evals.json.
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
	"evalctl/internal/stage"
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
	Dir         string // where the skill lives: <skills>/<name>, a group folder, or a plugin bundle
	Rel         string // Dir relative to SkillsDir ("" for a top-level skill)
	FilePath    string // <skills>/<name>/evals/evals.json
	TriggerPath string // <skills>/<name>/evals/trigger-evals.json
	Output      model.OutputFile
	Trigger     model.TriggerFile
	OnDisk      bool // evals.json exists (a trigger-only skill may not)
	TriggerOK   bool // trigger-evals.json exists and parsed
}

// Store is the whole eval tree in memory. Edits mutate it; writes are explicit and atomic.
type Store struct {
	Root      string      // the tree: .evalctl, its backups and the run lock
	SkillsDir string      // where the stage skills sit — the tree's own root, always
	Rules     stage.Rules // the tree's own conventions (empty = none declared)
	Skills    []*Skill
}

// ErrNoTree is "nothing here is a tree, and nothing above it is either". It is a sentinel because a
// caller can act on it — the interactive door offers to write a starter tree — while the profile
// error below is not: standing in a profile is answered by attaching one, not by scaffolding.
var ErrNoTree = errors.New("no eval tree")

// TreeMarker is the file a profile uses to name the tree it is measured from: one absolute path.
// It is what makes starting the tool inside a profile work — the profile's own skills are installs,
// and the sets they are measured on live in the tree.
const TreeMarker = ".evalctl-tree"

// Open loads the tree rooted at dir (walking up if dir is inside one).
//
// A tree is a project folder and only a project folder. A Hermes profile is not one: its skills/ is
// where Hermes installs skills, and a set kept in there is a second home for the same files with a
// different contract around them. The profile supplies the rules and the credentials; the tree holds
// what is measured and what the measurements produced.
func Open(dir string) (*Store, error) {
	root, err := FindRoot(dir)
	if err != nil {
		return nil, err
	}
	s := &Store{Root: root, SkillsDir: root, Rules: stage.Load(root)}

	candidates, err := scanSkills(root, false, 4)
	if err != nil {
		return nil, err
	}
	for _, cand := range candidates {
		name := filepath.Base(cand.dir)
		path := filepath.Join(cand.dir, "evals", "evals.json")
		triggerPath := filepath.Join(cand.dir, "evals", TriggerFileName)
		sk := &Skill{Name: name, Dir: cand.dir, Rel: cand.rel, FilePath: path, TriggerPath: triggerPath}
		sk.Output.SkillName = name

		if err := readJSON(path, &sk.Output); err == nil {
			if sk.Output.SkillName == "" {
				sk.Output.SkillName = name
			}
			sk.OnDisk = true
			for i := range sk.Output.Evals {
				if sk.Output.Evals[i].Files == nil {
					sk.Output.Evals[i].Files = []string{}
				}
			}
		} else if !isFile(path) {
			// A dir with a SKILL.md and no eval file yet is a skill somebody has just made: it is
			// listed so `add`/`init` have somewhere to write. In a profile that fallback is wrong —
			// skills/ holds every installed skill — so scanSkills only offers skills with a set there.
			sk.OnDisk = false
		} else {
			return nil, fmt.Errorf("%s: %w", rel(root, path), err)
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

// SkillDir is one stage skill's directory, wherever the layout puts it — a checkout's top level, a
// group folder, or a plugin bundle. An unknown name falls back to <skills>/<name>, which is where a
// new skill is written.
func (s *Store) SkillDir(name string) string {
	for _, sk := range s.Skills {
		if sk.Name == name {
			return sk.Dir
		}
	}
	return filepath.Join(s.SkillsDir, name)
}

// FindRoot walks up from start to the project root: the dir holding stage skills, each with its
// cases (<skill>/evals/evals.json) or its trigger set (<skill>/evals/trigger-evals.json) — either
// directly (a checkout) or under skills/ (a Hermes profile).
//
// A profile whose own skills carry no sets names its tree in `.evalctl-tree` (one absolute path,
// written by `evalctl profiles attach`), so starting the tool inside the profile it measures works
// instead of walking up to an empty folder.
func FindRoot(start string) (string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	dir := abs
	sawProfile := ""
	// The nearest profile an ancestor of the start turned out to be, if any: it is what the error
	// below names, and the first one seen is the one the caller is standing in.

	// A directory that merely holds a skill is a candidate; a directory that declares itself a tree
	// (its own evalctl.json) wins over it. Grouping skills in folders puts the skill one level below
	// the tree, so the nearest candidate would be the group folder — and then the tree's own
	// conventions would be read from the wrong place, or not at all.
	candidate := ""
	for {
		if target, ok := treeMarker(dir); ok && target != dir && isTreeRoot(target) {
			return target, nil
		}
		if isFile(filepath.Join(dir, stage.FileName)) && isTreeRoot(dir) {
			return dir, nil
		}
		if candidate == "" && isTreeRoot(dir) {
			candidate = dir
		}
		if sawProfile == "" && looksLikeProfile(dir) {
			sawProfile = dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			if candidate != "" {
				return candidate, nil
			}
			// A profile holds installed skills and no sets, so "no tree here" is the expected first
			// answer, not a mistake: name the one line that fixes it.
			if sawProfile != "" {
				// The one wording worth special-casing: somebody kept the sets inside the profile.
				if hasSkillDirs(filepath.Join(sawProfile, "skills")) {
					return "", fmt.Errorf("%s is a Hermes profile, and a profile is not an eval tree — its "+
						"skills/ holds installed skills, and a set kept there is measured under no tree of its "+
						"own. Move it to a project folder and run `evalctl profiles attach %s` from it "+
						"(or pass --root <folder>)", sawProfile, filepath.Base(sawProfile))
				}
				return "", fmt.Errorf("%s is a Hermes profile, and a profile's skills carry no eval sets — "+
					"name the tree it is measured from: evalctl profiles attach <tree> (or --root <tree>)", sawProfile)
			}
			return "", fmt.Errorf("%w found at or above %s (expected <skill>/evals/evals.json, "+
				"<skill>/evals/%s, or a %s naming one) — `evalctl init --skill <name>` creates one here",
				ErrNoTree, abs, TriggerFileName, TreeMarker)
		}
		dir = parent
	}
}

// looksLikeProfile: a Hermes profile keeps its settings and its skills one level down, and no eval
// set anywhere — that shape is what the error above explains instead of pointing at evals/.
func looksLikeProfile(dir string) bool {
	if info, err := os.Stat(filepath.Join(dir, "skills")); err == nil && info.IsDir() {
		return true
	}
	_, cfgErr := os.Stat(filepath.Join(dir, "config.yaml"))
	_, soulErr := os.Stat(filepath.Join(dir, "SOUL.md"))
	return cfgErr == nil && soulErr == nil
}

// isTreeRoot decides whether a directory is an eval tree: a project folder with a skill at its top
// level, or — when it says it is a project (its own evalctl.json, a .git, a plugins/ folder) — one
// whose skills are grouped or bundled deeper down. A Hermes profile's skills/ is deliberately not a
// tree: it holds installed skills, and the sets belong in the project folder that measures them.
func isTreeRoot(dir string) bool {
	// Nothing inside the Hermes home is a tree: skills/ there are Hermes's installs, and the
	// hermes-agent checkout inside it carries a skills/ folder of its own — reading that as a
	// project is how standing in a profile ended up reporting somebody else's duplicate skill names.
	if insideHermes(dir) {
		return false
	}
	if hasSkillDirs(dir) {
		return true
	}
	project := isFile(filepath.Join(dir, stage.FileName)) || isDirAt(filepath.Join(dir, ".git")) ||
		isDirAt(filepath.Join(dir, "plugins"))
	if !project {
		return false
	}
	// A scan that errors found skills and disliked them (two folders with one name). That is this
	// directory's problem to report, not a reason to walk further up and call the tree empty.
	found, err := scanSkills(dir, true, maxSkillDepth)
	return err != nil || len(found) > 0
}

// hermesHome is the Hermes install root: $HERMES_ROOT, or ~/.hermes.
func hermesHome() string {
	if v := strings.TrimSpace(os.Getenv("HERMES_ROOT")); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".hermes")
	}
	return ""
}

// insideHermes reports whether dir is the Hermes home or anything under it.
func insideHermes(dir string) bool {
	home := hermesHome()
	if home == "" {
		return false
	}
	rel, err := filepath.Rel(resolvedPath(home), resolvedPath(dir))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// resolvedPath follows symlinks where it can; /var and /private/var are the same directory on macOS
// and come back spelled both ways.
func resolvedPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// maxSkillDepth is how far down from a tree root a skill may sit: groups/, plugins/<p>/skills/, and
// one folder of slack for a bundle that nests its own skills.
const maxSkillDepth = 4

// hasSkillDirs is the shallow, cheap check root detection runs on every ancestor: a skill directly
// inside root and nowhere deeper, because a grandchild match would make any shared folder (/tmp, a
// home directory) into a tree for whoever happens to keep an eval set below it.
func hasSkillDirs(root string) bool {
	found, err := scanSkills(root, true, 1)
	return err != nil || len(found) > 0
}

func isDirAt(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// treeMarker reads a profile's tree pointer. A relative path is taken against the profile dir.
func treeMarker(dir string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(dir, TreeMarker))
	if err != nil {
		return "", false
	}
	target := strings.TrimSpace(string(b))
	if target == "" {
		return "", false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	return filepath.Clean(target), true
}

// skillDir is one skill directory and its path relative to the skills root.
type skillDir struct {
	dir string
	rel string
}

// scanSkills finds the stage skills under root, however they are arranged: at the top level, in
// group folders, or inside a plugin bundle (plugins/<name>/skills/<skill>). It walks at most depth
// levels and never descends into a skill it has already found — a skill's own
// scripts/ and references/ are its contents, not more skills.
//
// The name of a skill is its directory name; that is what a workspace, a profile install and a
// trigger row's `skill` field all key on, so two skills with one name is an error and not a
// silent shadowing.
func scanSkills(root string, profile bool, maxDepth int) ([]skillDir, error) {
	// Contents of a skill, and folders that are never a skill: descending into them costs time and
	// can only produce a false positive.
	skip := map[string]bool{
		"evals": true, "scripts": true, "references": true, "assets": true, "templates": true,
		"files": true, "node_modules": true, "vendor": true, "__pycache__": true,
	}
	var out []skillDir
	seen := map[string]string{}
	var walk func(dir, rel string, depth int) error
	walk = func(dir, rel string, depth int) error {
		hasSet := isFile(filepath.Join(dir, "evals", "evals.json")) || isFile(filepath.Join(dir, "evals", TriggerFileName))
		isSkill := hasSet || (!profile && isFile(filepath.Join(dir, "SKILL.md")))
		if isSkill && depth > 0 {
			name := filepath.Base(dir)
			if prev, dup := seen[name]; dup {
				return fmt.Errorf("two skills are named %q: %s and %s — a skill's folder name is its identity "+
					"(workspaces and profile installs key on it), so one of them needs a different name",
					name, prev, rel)
			}
			seen[name] = rel
			out = append(out, skillDir{dir: dir, rel: rel})
			return nil
		}
		if depth >= maxDepth {
			return nil
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			// An unreadable folder is not a reason to fail the whole tree.
			return nil
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasPrefix(name, ".") || skip[name] || strings.HasSuffix(name, "-workspace") {
				continue
			}
			childRel := filepath.ToSlash(filepath.Join(rel, name))
			if err := walk(filepath.Join(dir, name), childRel, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, "", 0); err != nil {
		return nil, err
	}
	return out, nil
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
		Dir:         s.SkillDir(name),
		FilePath:    filepath.Join(s.SkillDir(name), "evals", "evals.json"),
		TriggerPath: filepath.Join(s.SkillDir(name), "evals", TriggerFileName),
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
		return filepath.Join(s.SkillDir(skill), "evals", TriggerFileName)
	}
	return sk.TriggerPath
}

func (s *Store) OutputPathFor(skill string) string {
	sk := s.Skill(skill)
	if sk == nil {
		return filepath.Join(s.SkillDir(skill), "evals", "evals.json")
	}
	return sk.FilePath
}

// FixtureDir is <skill>/evals/files/<slug>/ — the run root mirror for one case.
func (s *Store) FixtureDir(skill, slug string) string {
	return filepath.Join(s.SkillDir(skill), "evals", "files", slug)
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
	if !strings.HasPrefix(filepath.Clean(dir), filepath.Clean(filepath.Join(s.SkillDir(skill), "evals", "files"))+string(os.PathSeparator)) {
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
