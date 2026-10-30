package runner

import (
	"path/filepath"
	"testing"
)

// TestIDReferencesFindsWhatCitesAnID covers both lanes: the trigger lane names its result dirs
// q<id>-<skill> and quotes the id in the report, the output lane keeps the id only in the report
// heading — and a neighbouring id must not be read as a hit for the one being moved.
func TestIDReferencesFindsWhatCitesAnID(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "trigger-workspace", "trigger-1", "q03-alpha", "query.txt"), "x\n")
	writeFile(t, filepath.Join(root, "trigger-workspace", "trigger-1", "q30-alpha", "query.txt"), "x\n")
	writeFile(t, filepath.Join(root, "trigger-workspace", "trigger-1", "report.md"),
		"# trigger · iteration 1\n\n| # | Skill | Should |\n|---|---|---|\n| 3 | `alpha` | yes |\n")
	writeFile(t, filepath.Join(root, "trigger-workspace", "trigger-2", "q03-alpha", "query.txt"), "x\n")
	writeFile(t, filepath.Join(root, "trigger-workspace", "trigger-2", "report.md"),
		"# trigger · iteration 2\n\n| # | Skill | Should |\n|---|---|---|\n| 7 | `alpha` | yes |\n")

	got := IDReferences(root, LaneTrigger, "", 3)
	want := []string{
		"trigger-workspace/trigger-1/q03-alpha",
		"trigger-workspace/trigger-1/report.md",
		"trigger-workspace/trigger-2/q03-alpha",
	}
	if len(got) != len(want) {
		t.Fatalf("refs = %+v, want %v", got, want)
	}
	for i, ref := range got {
		if ref.Path != want[i] {
			t.Fatalf("ref %d = %q, want %q", i, ref.Path, want[i])
		}
	}
	if got[0].What != "result dir" || got[1].What != "report" {
		t.Fatalf("kinds = %q, %q", got[0].What, got[1].What)
	}

	// The output lane keys dirs and meta by slug, so only the report heading cites the id.
	writeFile(t, filepath.Join(root, "slide-builder-workspace", "iteration-2", "report.md"),
		"# slide-builder · eval iteration 2\n\n## sb-slides · slide-builder case 3\n")
	out := IDReferences(root, LaneOutput, "slide-builder", 3)
	if len(out) != 1 || out[0].Path != "slide-builder-workspace/iteration-2/report.md" {
		t.Fatalf("output refs = %+v", out)
	}
	if n := IDReferences(root, LaneOutput, "slide-builder", 4); len(n) != 0 {
		t.Fatalf("a different id must not match the heading, got %+v", n)
	}

	// A tree that never ran says nothing rather than failing.
	if got := IDReferences(root, LaneTrigger, "", 99); len(got) != 0 {
		t.Fatalf("unused id = %+v, want none", got)
	}
}
