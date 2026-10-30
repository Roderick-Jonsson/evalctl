package stage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A stage's own output is identified by its pattern, not a fixed name: the documents carry the
// workspace's topic slug (contract, Artifact descriptors).
func TestOwnMatchesTopicSuffixedNames(t *testing.T) {
	cases := []struct {
		skill string
		name  string
		want  bool
	}{
		{"source-research", "PS-writing-agent-skills.md", true},
		{"source-research", "PS.md", false},
		{"source-research", "sources", true},
		{"source-research", "LP-writing-agent-skills.md", false},
		{"learning-path", "LP-kubernetes-operators.md", true},
		{"learning-path", "PO-RevA-kubernetes-operators.md", true},
		{"learning-path", "PO-RevB-kubernetes-operators.md", true},
		{"learning-path", "PO-RevB.md", false},
		{"slide-builder", "SL-writing-agent-skills.md", true},
		{"slide-builder", "pages", true},
		{"slide-builder", "AV", false},
		{"av-production", "AV", true},
		{"no-such-skill", "PS-writing-agent-skills.md", false},
	}
	for _, c := range cases {
		if got := Own(c.skill, c.name); got != c.want {
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
	if got := strings.Join(Present(ws, "source-research"), ","); got != "PS-written.md,sources" {
		t.Errorf("source-research present = %q", got)
	}
	if got := strings.Join(Present(ws, "learning-path"), ","); got != "LP-written.md" {
		t.Errorf("learning-path present = %q", got)
	}
	if got := Present(filepath.Join(ws, "absent"), "learning-path"); len(got) != 0 {
		t.Errorf("a missing workspace yielded %v", got)
	}
}
