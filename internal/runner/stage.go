package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileInfo is one file's fingerprint inside a run root.
type FileInfo struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// Modified is a file that survived a run with different bytes.
type Modified struct {
	BeforeBytes int64 `json:"before_bytes"`
	AfterBytes  int64 `json:"after_bytes"`
}

// Changes is what a run did to its run root, written to changes.json.
type Changes struct {
	New      map[string]int64    `json:"new"`
	Modified map[string]Modified `json:"modified"`
	Deleted  []string            `json:"deleted"`
	Count    int                 `json:"count"`
}

// Inventory fingerprints every file under root.
func Inventory(root string) (map[string]FileInfo, error) {
	out := map[string]FileInfo{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		out[filepath.ToSlash(rel)] = FileInfo{SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(b))}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Diff is new/modified/deleted between two inventories, sorted for a readable JSON file.
func Diff(before, after map[string]FileInfo) Changes {
	out := Changes{New: map[string]int64{}, Modified: map[string]Modified{}, Deleted: []string{}}
	for path, a := range after {
		if b, ok := before[path]; ok {
			if b.SHA256 != a.SHA256 {
				out.Modified[path] = Modified{BeforeBytes: b.Bytes, AfterBytes: a.Bytes}
			}
			continue
		}
		out.New[path] = a.Bytes
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			out.Deleted = append(out.Deleted, path)
		}
	}
	sort.Strings(out.Deleted)
	out.Count = len(out.New) + len(out.Modified) + len(out.Deleted)
	return out
}

// Containment is the contract check: a stage writes inside MC-[topic]/ (archives may sit in
// _archive/).
type Containment struct {
	OK      bool     `json:"ok"`
	Outside []string `json:"outside"`
	Touched int      `json:"touched"`
}

// CheckContainment flags any touched file outside MC-*/ or _archive/.
func CheckContainment(before, after map[string]FileInfo) Containment {
	outside := []string{}
	touched := 0
	for path, a := range after {
		b, ok := before[path]
		if ok && b.SHA256 == a.SHA256 {
			continue
		}
		touched++
		if !strings.HasPrefix(path, "MC-") && !strings.HasPrefix(path, "_archive/") {
			outside = append(outside, path)
		}
	}
	sort.Strings(outside)
	return Containment{OK: len(outside) == 0, Outside: outside, Touched: touched}
}

// Stage seeds an empty run root from the case's fixture dir. `files` are relative to the fixture
// dir, so the slug is the only locator and an entry that escapes it is refused.
func Stage(fixtureDir string, files []string, runRoot string) error {
	if err := os.MkdirAll(runRoot, 0o755); err != nil {
		return err
	}
	info, err := os.Stat(fixtureDir)
	if err != nil || !info.IsDir() {
		if len(files) == 0 {
			return nil // a prompt-only case: the run root starts empty
		}
		return fmt.Errorf("missing fixture dir %s", fixtureDir)
	}
	if len(files) == 0 {
		// Optional extra staging, but files: [] claims nothing.
		return CopyTree(fixtureDir, runRoot)
	}
	root, err := filepath.Abs(fixtureDir)
	if err != nil {
		return err
	}
	for _, rel := range files {
		target := filepath.Join(root, filepath.FromSlash(rel))
		if !within(root, target) {
			return fmt.Errorf("file %q escapes %s", rel, fixtureDir)
		}
		if info, err := os.Stat(target); err != nil || info.IsDir() {
			return fmt.Errorf("file %q is not in %s (fixture dir is named by the slug)", rel, fixtureDir)
		}
	}
	return CopyTree(fixtureDir, runRoot)
}

// CopyTree copies src into dst, merging with whatever is there (dirs_exist_ok).
func CopyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		source, err := os.Open(path)
		if err != nil {
			return err
		}
		defer source.Close()
		info, err := source.Stat()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, source); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

func within(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if target == root {
		return true
	}
	return strings.HasPrefix(target, root+string(os.PathSeparator))
}

// Snapshot is a cheap fingerprint of the production masterclass root — a run must leave it
// untouched.
type Snapshot struct {
	Exists  bool              `json:"exists"`
	Entries map[string]string `json:"entries"`
}

// RootSnapshot records size:mtime for everything under the production root.
func RootSnapshot(root string) Snapshot {
	snap := Snapshot{Entries: map[string]string{}}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return snap
	}
	snap.Exists = true
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		snap.Entries[filepath.ToSlash(rel)] = fmt.Sprintf("%d:%d", st.Size(), st.ModTime().Unix())
		return nil
	})
	return snap
}

// Guard is the before/after verdict on the production root.
type Guard struct {
	OK      bool     `json:"ok"`
	Root    string   `json:"root"`
	Existed bool     `json:"existed"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
	Changed []string `json:"changed"`
}

// RootGuard compares two snapshots and reports the first 20 deviations per kind.
func RootGuard(before, after Snapshot, root string) Guard {
	guard := Guard{Root: root, Existed: before.Exists, Added: []string{}, Removed: []string{}, Changed: []string{}}
	for path := range after.Entries {
		if _, ok := before.Entries[path]; !ok {
			guard.Added = append(guard.Added, path)
		}
	}
	for path, old := range before.Entries {
		current, ok := after.Entries[path]
		if !ok {
			guard.Removed = append(guard.Removed, path)
			continue
		}
		if current != old {
			guard.Changed = append(guard.Changed, path)
		}
	}
	sort.Strings(guard.Added)
	sort.Strings(guard.Removed)
	sort.Strings(guard.Changed)
	guard.OK = len(guard.Added) == 0 && len(guard.Removed) == 0 && len(guard.Changed) == 0 &&
		before.Exists == after.Exists
	guard.Added = head20(guard.Added)
	guard.Removed = head20(guard.Removed)
	guard.Changed = head20(guard.Changed)
	return guard
}

func head20(in []string) []string {
	if len(in) > 20 {
		return in[:20]
	}
	return in
}
