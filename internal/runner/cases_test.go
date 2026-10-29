package runner

import (
	"path/filepath"
	"testing"
)

// promotedJSON is a case carrying the key a promotion writes. The standard has no field for it,
// so it lives in the unknown-key space — and must reach the runner from there.
const promotedJSON = `{
  "skill_name": "learning-path",
  "evals": [
    {
      "id": 1,
      "slug": "lp-1-outline",
      "prompt": "hand made",
      "expected_output": "LP.md",
      "files": ["MC-x/PS.md"]
    },
    {
      "id": 2,
      "slug": "lp-2-agentskills",
      "prompt": "the MC-writing-agent-skills workspace here",
      "expected_output": "LP.md, PO-RevA.md, PO-RevB.md",
      "files": ["MC-writing-agent-skills/PS.md", "MC-writing-agent-skills/sources/home.md"],
      "promoted_from": {
        "run": "iteration-2",
        "case": "src-4-agentskills",
        "arm": "with_skill",
        "fixture_sha256": "abc123"
      }
    }
  ]
}`

func promotedTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "learning-path", "evals", "evals.json"), promotedJSON)
	return root
}

func TestLoadCasesCarriesPromotionProvenance(t *testing.T) {
	cases, err := LoadCases(mustStore(t, promotedTree(t)), []string{"learning-path"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 {
		t.Fatalf("cases = %d", len(cases))
	}
	if cases[0].Promoted != nil {
		t.Errorf("a hand-made case grew provenance: %+v", cases[0].Promoted)
	}
	p := cases[1].Promoted
	if p == nil {
		t.Fatal("promoted_from did not reach the runner")
	}
	if p.Run != "iteration-2" || p.Case != "src-4-agentskills" || p.Arm != "with_skill" || p.FixtureSHA256 != "abc123" {
		t.Errorf("provenance = %+v", p)
	}
}

func TestLoadCasesRejectsMalformedProvenance(t *testing.T) {
	root := promotedTree(t)
	writeFile(t, filepath.Join(root, "learning-path", "evals", "evals.json"),
		`{"skill_name":"learning-path","evals":[{"id":1,"slug":"lp-1-outline","prompt":"x",
		  "expected_output":"y","files":[],"promoted_from":"not an object"}]}`)
	if _, err := LoadCases(mustStore(t, root), []string{"learning-path"}, nil); err == nil {
		t.Fatal("a malformed promoted_from must fail the load")
	}
}
