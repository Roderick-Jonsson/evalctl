package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The human's half of the output loop.
//
// Every iteration writes feedback.json as a scaffold — one empty string per case that ran — and
// until now nothing ever put a word in it: the pane read the notes and no key wrote one, so "read what
// the human said" was a sentence about a file only a text editor could fill. This is the write, and
// it lands where the reading already looks: the iteration's own feedback.json, keyed by slug.
//
// The note is the operator's, not the lab's: it is not a verdict, it never enters a rate, and the
// judge never sees it. It is what a revision is written from beside the failed assertions, which is
// why it belongs to an iteration — the same note on the next iteration would describe a different
// run.

// WriteFeedback stores one case's note for one iteration. The file is backed up first, like every
// other write of this tool, and the keys of the cases that ran are kept: a note is added beside the
// scaffold, never in place of it.
func (r *Runner) WriteFeedback(skill string, iteration int, slug, text string) error {
	if !SlugRE.MatchString(slug) {
		return fmt.Errorf("%q is not a case slug", slug)
	}
	iterDir := r.IterDir(r.WorkspaceDir(skill), iteration)
	if info, err := os.Stat(iterDir); err != nil || !info.IsDir() {
		return fmt.Errorf("iteration %d has no directory in %s-workspace — R runs it first",
			iteration, skill)
	}
	path := filepath.Join(iterDir, "feedback.json")
	notes := map[string]string{}
	_ = readJSONFile(path, &notes)
	if len(notes) == 0 {
		// No scaffold: seed the keys from the case directories that are on disk, so the file is the
		// review list the iteration's own report points at rather than one key floating alone.
		entries, _ := os.ReadDir(iterDir)
		for _, e := range entries {
			if e.IsDir() && SlugRE.MatchString(e.Name()) {
				notes[e.Name()] = ""
			}
		}
	}
	if _, ok := notes[slug]; !ok && len(notes) > 0 {
		// The case is not part of this iteration: a note beside no run is a claim about work that
		// was not done, and the next iteration's report would count it.
		return fmt.Errorf("%s did not run in iteration %d — the note belongs to a case that did", slug, iteration)
	}
	if err := r.Store.BackupFile(path); err != nil {
		return err
	}
	notes[slug] = strings.TrimSpace(text)
	return writeJSONFile(path, notes)
}

// QualityFeedback is the notes one iteration carries, keyed by slug, in the order the file has them.
func (r *Runner) QualityFeedback(skill string, iteration int) (map[string]string, error) {
	path := filepath.Join(r.IterDir(r.WorkspaceDir(skill), iteration), "feedback.json")
	notes := map[string]string{}
	if err := readJSONFile(path, &notes); err != nil {
		return nil, err
	}
	return notes, nil
}
