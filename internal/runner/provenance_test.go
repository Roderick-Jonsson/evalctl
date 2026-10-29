package runner

import (
	"path/filepath"
	"testing"

	"evalctl/internal/store"
)

// TestFixtureProvenanceRecordsHashesAndAncestry covers the meta.json half of a promotion: a
// promoted fixture is hashed as staged and carries its ancestry, a hand-made one is hashed only,
// and a prompt-only case stays out of the map.
func TestFixtureProvenanceRecordsHashesAndAncestry(t *testing.T) {
	s, root := newTree(t)
	writeFile(t, filepath.Join(root, "slide-builder", "evals", "files", "sb-1-slides", "PO-RevB.md"), "outline")
	writeFile(t, filepath.Join(root, "source-research", "evals", "files", "src-1-new-topic", "raw", "page.md"), "raw")
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	cases, err := LoadCases(s, []string{"slide-builder", "source-research"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	prov := &store.PromotedFrom{Run: "iteration-2", Case: "src-4-agentskills", Arm: "with_skill", FixtureSHA256: "abc"}
	for i := range cases {
		if cases[i].Slug == "src-1-new-topic" {
			cases[i].Promoted = prov
		}
	}

	fixtures, err := New(s).fixtureProvenance(cases)
	if err != nil {
		t.Fatal(err)
	}
	hand, ok := fixtures["sb-1-slides"]
	if !ok || hand.SHA256 == "" || hand.Promoted != nil {
		t.Errorf("hand-made fixture = %+v (present %v)", hand, ok)
	}
	promoted, ok := fixtures["src-1-new-topic"]
	if !ok || promoted.SHA256 == "" || promoted.Promoted == nil || promoted.Promoted.Run != "iteration-2" {
		t.Errorf("promoted fixture = %+v (present %v)", promoted, ok)
	}
	if _, ok := fixtures["sb-2-no-outline"]; ok {
		t.Error("a prompt-only case with no ancestry was recorded")
	}
}
