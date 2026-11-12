package stage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pipeline is this repo's own vocabulary, held here as test data so the package itself names no
// project: the harness must read these from a tree, never carry them.
var pipeline = Rules{
	Order: []string{"source-research", "learning-path", "slide-builder", "av-production"},
	Outputs: map[string][]string{
		"source-research": {"PS-*.md", "sources"},
		"learning-path":   {"LP-*.md", "PO-RevA-*.md", "PO-RevB-*.md"},
		"slide-builder":   {"SL-*.md", "pages"},
		"av-production":   {"AV"},
	},
}

// A skill's own output is identified by its pattern, not a fixed name: the documents carry the
// workspace's topic slug.
func TestOwnMatchesTopicSuffixedNames(t *testing.T) {
	cases := []struct {
		skill string
		name  string
		want  bool
	}{
		{"source-research", "PS-writing-agent-skills.md", true},
		{"source-research", "PS.md", false},
		{"source-research", "sources", true},
		{"learning-path", "LP-kubernetes-operators.md", true},
		{"learning-path", "PO-RevB.md", false},
		{"slide-builder", "pages", true},
		{"av-production", "AV", true},
		{"no-such-skill", "PS-writing-agent-skills.md", false},
	}
	for _, c := range cases {
		if got := pipeline.Own(c.skill, c.name); got != c.want {
			t.Errorf("Own(%q, %q) = %v, want %v", c.skill, c.name, got, c.want)
		}
	}
}

// Present is the patterns resolved against one workspace, so a caller that needs real paths gets
// the names this workspace holds — and nothing from a workspace that is not there.
func TestPresentResolvesThePatterns(t *testing.T) {
	ws := t.TempDir()
	for _, name := range []string{"PS-written.md", "LP-written.md", "sources", "notes.txt"} {
		path := filepath.Join(ws, name)
		if strings.Contains(name, ".md") || strings.Contains(name, ".txt") {
			if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		} else if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(pipeline.Present(ws, "source-research"), ","); got != "PS-written.md,sources" {
		t.Errorf("source-research present = %q", got)
	}
	if got := strings.Join(pipeline.Present(ws, "learning-path"), ","); got != "LP-written.md" {
		t.Errorf("learning-path present = %q", got)
	}
	if got := pipeline.Present(filepath.Join(ws, "absent"), "learning-path"); len(got) != 0 {
		t.Errorf("a missing workspace yielded %v", got)
	}
}

// A tree that declares nothing gets neutral defaults: no order (alphabetical), nothing recognised
// as an answer, every folder allowed, the whole produced tree carried. This is what makes the tool
// usable on a project it has never heard of.
func TestEmptyRulesAreNeutral(t *testing.T) {
	var r Rules
	if got := strings.Join(r.Rank([]string{"writer", "reviewer"}), ","); got != "reviewer,writer" {
		t.Fatalf("order = %q", got)
	}
	if r.Own("writer", "brief.md") {
		t.Fatal("an empty rule table recognised an answer")
	}
	if !r.Allows("brief.md") || !r.Allows("MC-x/PS.md") {
		t.Fatal("an empty rule table forbade a path")
	}
	if !r.Carries("anything") {
		t.Fatal("an empty rule table dropped the produced tree")
	}
	if r.Declared() {
		t.Fatal("an empty rule table read as declared")
	}
}

// A declared order is a rank, with unknown skills last and alphabetical — the same rule the eval
// tree has always used for "the skill under test".
func TestDeclaredOrderRanksAndFallsBack(t *testing.T) {
	r := Rules{Order: []string{"b", "a"}}
	if got := strings.Join(r.Rank([]string{"z", "a", "y", "b"}), ","); got != "b,a,y,z" {
		t.Fatalf("rank = %q", got)
	}
}

// Load reads the file at the tree root and refuses to guess when it is broken.
func TestLoad(t *testing.T) {
	root := t.TempDir()
	if got := Load(root); got.Declared() {
		t.Fatal("a tree with no evalctl.json declared rules")
	}
	body := `{"skill_order":["a","b"],"containment_prefixes":["MC-","_archive/"],"env_keys":["K"]}`
	if err := os.WriteFile(filepath.Join(root, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Load(root)
	if len(r.Order) != 2 || !r.Allows("MC-x") || r.Allows("brief.md") || len(r.Env) != 1 {
		t.Fatalf("rules = %+v", r)
	}
	if err := os.WriteFile(filepath.Join(root, FileName), []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := Load(root); !strings.Contains(r.Note, "not valid JSON") {
		t.Fatalf("a broken file was swallowed: %+v", r)
	}
}
