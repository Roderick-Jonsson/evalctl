package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"evalctl/internal/stage"
)

// ReferenceRequest is one reference promotion: an artifact a finished run produced becomes a
// worked example in the producing skill's own references/ dir — the file an agent reads to
// learn the shape of the output, not an input to the next run.
//
// It is the mirror image of PromoteRequest: that one carries a run's workspace into the *next*
// stage's fixture and drops the stage's own outputs, this one carries exactly those outputs
// (stage.Outputs) back into the skill that wrote them.
type ReferenceRequest struct {
	Skill     string   // the skill whose references/ receives it (default: the case's own skill)
	Source    string   // the arm dir under evals/results
	Run       string   // provenance: iteration-2
	Case      string   // provenance: the run's case slug
	Arm       string   // provenance: with_skill
	Workspace string   // MC-dir inside the arm (empty: the only one, or an error)
	File      []string // workspace-relative paths; empty = the stage's own outputs
	All       bool     // take the whole workspace instead
	As        string   // destination inside references/ — a rename, or a dir when several files
	Force     bool     // replace an existing reference (the old bytes go to .evalctl/backups/)
	DryRun    bool
}

// Reference is what a reference promotion does, or would do on a dry run.
type Reference struct {
	Skill     string   `json:"skill"`
	Run       string   `json:"run"`
	Case      string   `json:"case"`
	Arm       string   `json:"arm"`
	Source    string   `json:"source"`
	Workspace string   `json:"workspace"`
	Dir       string   `json:"references_dir"`
	Files     []string `json:"files"`     // workspace-relative, taken
	Targets   []string `json:"targets"`   // references-relative, where each file lands
	Skipped   []string `json:"skipped"`   // workspace files deliberately not taken
	Overwrote []string `json:"overwrote"` // targets that already existed
	LogPath   string   `json:"log"`
}

// ReferenceRecord is one line of .evalctl/references.json: which run a reference came from, and
// the hash of the bytes it landed as. The log is gitignored on purpose — the reference itself is
// tracked, so git carries the human history and this carries the machine's.
type ReferenceRecord struct {
	Path       string    `json:"path"`   // learning-path/references/LP-<topic>.md, root-relative
	Source     string    `json:"source"` // MC-x/LP-<topic>.md, workspace-relative
	Run        string    `json:"run"`
	Case       string    `json:"case"`
	Arm        string    `json:"arm"`
	SHA256     string    `json:"sha256"`
	PromotedAt time.Time `json:"promoted_at"`
}

// ReferenceLogPath is where the promotions record where each reference came from.
func (s *Store) ReferenceLogPath() string {
	return filepath.Join(s.Root, ".evalctl", "references.json")
}

// ReferencesDir is <skill>/references — installed with the skill (profile.SkillParts), so
// whatever lands here is what the agent sees on its next run.
func (s *Store) ReferencesDir(skill string) string {
	return filepath.Join(s.Root, skill, "references")
}

// PromoteReferences copies a run's own stage outputs into the skill's references/ dir.
//
// A reference is a shipped file, so nothing is guessed twice: an existing path is refused unless
// Force is set, a target may not escape references/, and every write is recorded in
// .evalctl/references.json. A dry run reads the source and reports the plan, writing nothing.
func (s *Store) PromoteReferences(req ReferenceRequest) (Reference, error) {
	out, ws, err := s.referencePlan(req)
	if err != nil {
		return Reference{}, err
	}
	if len(out.Overwrote) > 0 && !req.Force && !req.DryRun {
		return Reference{}, fmt.Errorf("already in references/: %s — a promotion never replaces a reference; "+
			"pass --as <other-name> to keep both, or --force to replace it (the old bytes go to .evalctl/backups/)",
			strings.Join(out.Overwrote, ", "))
	}
	if req.DryRun {
		return out, nil
	}

	dir := out.Dir
	records := make([]ReferenceRecord, 0, len(out.Files))
	for i, name := range out.Files {
		content, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(name)))
		if err != nil {
			return Reference{}, err
		}
		target := filepath.Join(dir, filepath.FromSlash(out.Targets[i]))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return Reference{}, err
		}
		if isFile(target) {
			if err := s.backup(target); err != nil {
				return Reference{}, err
			}
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return Reference{}, err
		}
		sum := sha256.Sum256(content)
		records = append(records, ReferenceRecord{
			Path:   filepath.ToSlash(filepath.Join(req.Skill, "references", out.Targets[i])),
			Source: name, Run: req.Run, Case: req.Case, Arm: req.Arm,
			SHA256: hex.EncodeToString(sum[:]), PromotedAt: time.Now().UTC(),
		})
	}
	if err := s.appendReferenceLog(records); err != nil {
		return out, fmt.Errorf("the reference was copied but the provenance log did not save: %w", err)
	}
	return out, nil
}

// ReferencePreview is the read-only half of a reference promotion, so an interactive form can
// re-read the source on every arrow key. It reports the collisions too: a form has to be able to
// say "that name is taken" before enter is pressed, not after.
func (s *Store) ReferencePreview(req ReferenceRequest) (Reference, error) {
	out, _, err := s.referencePlan(req)
	return out, err
}

// referencePlan resolves a request against the tree without writing: which workspace, which
// files, where each one would land, and which targets already exist.
func (s *Store) referencePlan(req ReferenceRequest) (Reference, string, error) {
	sk := s.Skill(req.Skill)
	if sk == nil || !sk.OnDisk {
		return Reference{}, "", fmt.Errorf("unknown stage skill %q (a reference lands in that skill's own references/ dir)", req.Skill)
	}
	src, err := filepath.Abs(req.Source)
	if err != nil {
		return Reference{}, "", err
	}
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
		return Reference{}, "", fmt.Errorf("source run dir not found: %s", req.Source)
	}
	workspaces := WorkspaceDirs(src)
	if len(workspaces) == 0 {
		return Reference{}, "", fmt.Errorf("nothing to promote: %s holds no MC-*/ workspace", src)
	}
	ws, err := pickWorkspace(src, workspaces, req.Workspace)
	if err != nil {
		return Reference{}, "", err
	}
	files, skipped, err := selectReferences(ws, req)
	if err != nil {
		return Reference{}, "", err
	}
	targets, err := referenceTargets(files, req.As)
	if err != nil {
		return Reference{}, "", err
	}

	dir := s.ReferencesDir(req.Skill)
	out := Reference{Skill: req.Skill, Run: req.Run, Case: req.Case, Arm: req.Arm, Source: src,
		Workspace: filepath.Base(ws), Dir: dir, Files: files, Targets: targets, Skipped: skipped,
		LogPath: s.ReferenceLogPath()}
	for _, t := range targets {
		if isFile(filepath.Join(dir, filepath.FromSlash(t))) {
			out.Overwrote = append(out.Overwrote, t)
		}
	}
	return out, ws, nil
}

// WorkspaceDirs lists the MC-* dirs a run's arm produced, sorted.
func WorkspaceDirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "MC-") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// pickWorkspace resolves which workspace a promotion is about. One is the common case; several
// means the run wrote more than one masterclass, and guessing one would ship the wrong example.
func pickWorkspace(src string, workspaces []string, want string) (string, error) {
	want = strings.TrimSpace(want)
	if want != "" {
		for _, name := range workspaces {
			if name == want {
				return filepath.Join(src, name), nil
			}
		}
		return "", fmt.Errorf("no workspace %q in %s (on disk: %s)", want, src, strings.Join(workspaces, ", "))
	}
	if len(workspaces) > 1 {
		return "", fmt.Errorf("%s holds %d workspaces (%s) — pass --workspace to say which one",
			src, len(workspaces), strings.Join(workspaces, ", "))
	}
	return filepath.Join(src, workspaces[0]), nil
}

// selectReferences returns what to take from the workspace and what it deliberately leaves: the
// stage's own outputs (stage.Outputs) by default, one path with File, everything with All.
func selectReferences(ws string, req ReferenceRequest) (taken, skipped []string, err error) {
	defaults := stage.Outputs[req.Skill]
	switch {
	case len(req.File) > 0:
		for _, name := range req.File {
			rel, err := cleanInside(name)
			if err != nil {
				return nil, nil, err
			}
			found, err := collectFiles(ws, rel)
			if err != nil {
				return nil, nil, fmt.Errorf("%s holds no %q — it has: %s", filepath.Base(ws), name,
					strings.Join(topLevel(ws), ", "))
			}
			taken = append(taken, found...)
		}
	case req.All:
		all, err := collectFiles(ws, "")
		if err != nil {
			return nil, nil, err
		}
		taken = all
	case len(defaults) == 0:
		return nil, nil, fmt.Errorf("no stage output table for %q (internal/stage) — pass --file <path> or --all", req.Skill)
	default:
		// The patterns are matched against what this workspace holds: the documents are
		// <PREFIX>-<topic>.md, so the entry names, not the patterns, are what collectFiles takes.
		for _, name := range stage.Present(ws, req.Skill) {
			found, err := collectFiles(ws, name)
			if err != nil {
				continue // this stage did not write that artifact
			}
			taken = append(taken, found...)
		}
		if len(taken) == 0 {
			return nil, nil, fmt.Errorf("the run wrote none of %s's outputs (%s) — it has: %s; pass --file <path> or --all",
				req.Skill, strings.Join(defaults, ", "), strings.Join(topLevel(ws), ", "))
		}
	}
	taken = dedupeSorted(taken)
	if len(taken) == 0 {
		return nil, nil, fmt.Errorf("nothing to promote in %s", filepath.Base(ws))
	}
	all, err := collectFiles(ws, "")
	if err != nil {
		return nil, nil, err
	}
	keep := map[string]bool{}
	for _, name := range taken {
		keep[name] = true
	}
	for _, name := range all {
		if !keep[name] {
			skipped = append(skipped, name)
		}
	}
	return taken, skipped, nil
}

// collectFiles lists the files under one workspace-relative path, recursively for a dir. An
// empty name means the whole workspace.
func collectFiles(ws, name string) ([]string, error) {
	root := filepath.Join(ws, filepath.FromSlash(name))
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{path.Clean(filepath.ToSlash(name))}, nil
	}
	var out []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Name() == ".DS_Store" {
			return nil
		}
		out = append(out, filepath.ToSlash(rel(ws, p)))
		return nil
	})
	return out, err
}

// topLevel is the workspace's own listing, quoted back when a selection finds nothing: it is how
// the caller discovers what --file could name.
func topLevel(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		out = append(out, name)
	}
	return out
}

// cleanInside rejects a workspace-relative path that would read outside the workspace.
func cleanInside(name string) (string, error) {
	clean := path.Clean(filepath.ToSlash(strings.TrimSpace(name)))
	if clean == "." || clean == "" || strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("bad path %q: a reference is selected by a path inside the workspace", name)
	}
	return clean, nil
}

// referenceTargets maps workspace-relative files onto references/-relative paths: the same path
// by default. --as renames when it names one file, and must end in "/" when several are going in
// — one file name cannot hold a set, and guessing a directory from a name ships a surprise.
func referenceTargets(files []string, as string) ([]string, error) {
	raw := strings.TrimSpace(filepath.ToSlash(as))
	if strings.HasPrefix(raw, "/") {
		return nil, fmt.Errorf("bad --as %q: it is relative to references/, not an absolute path", as)
	}
	raw = strings.TrimPrefix(raw, "references/")
	isDir := len(files) > 1 || strings.HasSuffix(raw, "/")
	if len(files) > 1 && raw != "" && !strings.HasSuffix(raw, "/") {
		return nil, fmt.Errorf("--as %q names several files one destination — end it with / to name a dir, "+
			"or use --file to promote a single path", as)
	}
	base := path.Clean(strings.Trim(raw, "/"))
	if base == "." {
		base = ""
	}
	if base != "" {
		if err := checkTarget(base); err != nil {
			return nil, fmt.Errorf("bad --as %q: %w", as, err)
		}
	}
	out := make([]string, 0, len(files))
	for _, name := range files {
		target := name
		if base != "" {
			if isDir {
				target = path.Join(base, name)
			} else {
				target = base
			}
		}
		if err := checkTarget(target); err != nil {
			return nil, err
		}
		out = append(out, target)
	}
	return out, nil
}

// checkTarget keeps a destination inside <skill>/references/.
func checkTarget(target string) error {
	if target == "" || strings.HasPrefix(target, "/") || strings.Contains(target, "\\") {
		return fmt.Errorf("bad reference path %q", target)
	}
	for _, part := range strings.Split(target, "/") {
		if part == ".." || part == "." || part == "" {
			return fmt.Errorf("bad reference path %q: it must stay inside references/", target)
		}
	}
	return nil
}

// appendReferenceLog records one promotion per path in .evalctl/references.json: a re-promoted
// path replaces its older entry, because only the newest bytes are on disk.
func (s *Store) appendReferenceLog(records []ReferenceRecord) error {
	path := s.ReferenceLogPath()
	var log []ReferenceRecord
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &log); err != nil {
			return fmt.Errorf("%s: %w", rel(s.Root, path), err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, rec := range records {
		log = replaceRecord(log, rec)
	}
	sort.SliceStable(log, func(i, j int) bool { return log[i].Path < log[j].Path })
	b, err := json.MarshalIndent(log, "", "  ")
	if err != nil {
		return err
	}
	return s.writeFile(path, append(b, '\n'))
}

func replaceRecord(log []ReferenceRecord, rec ReferenceRecord) []ReferenceRecord {
	for i := range log {
		if log[i].Path == rec.Path {
			log[i] = rec
			return log
		}
	}
	return append(log, rec)
}

func dedupeSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
