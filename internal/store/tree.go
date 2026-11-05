package store

import (
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The screens are drawings of the disk, so they have to notice when the disk is not what it was.
//
// A screen that keeps the tree it opened with is only right for as long as nobody adds a skill,
// writes a row or finishes a run in another window — and the thing nobody adds is exactly the thing
// the tool is asked to show. So the tree gets one number, cheap enough to ask for every second, and
// each screen re-reads when the number moves.
//
// It is a stat walk, not a watcher: fsnotify on macOS is one process's view of a handful of fds, it
// costs a dependency, and it misses what iCloud or a network mount rewrites behind it. The tree
// here is a few hundred entries, so asking is cheaper than subscribing — and asking is answered
// from the filesystem itself, not from a log of events that can have gaps.

// TreeStamp folds every path under root into one short string: the path, its size, its modification
// time and whether it is a directory. Two stamps differ exactly when something a screen could show
// has appeared, gone, or been rewritten.
//
// It walks what the store reads and prunes what no screen shows: dot-directories (`.git`, and
// `.evalctl`'s backups) are skipped whole, and a `*-workspace` is read down to its iteration level —
// the iteration dir and the report files in it — because a run's payload is tens of thousands of
// case files and one new skill must not cost a walk of those.
// workspaceStampFile is the files under a run's arm that a screen reads back: the verdicts, the reply,
// the cost, the note a human wrote, and the iteration's own summaries. Everything else in a workspace
// is the run's raw material.
func workspaceStampFile(name string) bool {
	switch name {
	case "grading.json", "timing.json", "meta.json", "feedback.json", "benchmark.json", "report.md":
		return true
	}
	return false
}

func TreeStamp(root string) (string, error) {
	h := fnv.New64a()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A missing root is not a tree with nothing in it: the caller must be told, so it
			// keeps the reading it has instead of re-opening a tree that is not there.
			if path == root {
				return err
			}
			// A path that vanished between the readdir and the stat is not a failure: it is a
			// change, and the next stamp will be taken of a tree that no longer has it.
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		parts := strings.Split(rel, "/")
		if strings.HasSuffix(parts[0], "-workspace") {
			switch {
			case d.IsDir() && len(parts) >= 5:
				// Under an arm: the run's raw material, and nothing a screen reads.
				return fs.SkipDir
			case d.IsDir() && len(parts) == 4:
				// The arm's own directory is walked but not hashed: its mtime moves whenever the run
				// adds a file to it, and a payload of 27k files must not cost a re-read per tick. A new
				// arm still shows, because the case directory above it moves.
				return nil
			case !d.IsDir() && !workspaceStampFile(d.Name()):
				// A log, a stream, an output: the run's own material, not a screen's reading.
				return nil
			}
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00%t\n", rel, info.Size(), info.ModTime().UnixNano(), d.IsDir())
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%016x", h.Sum64()), nil
}
