package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/stage"
)

// declarePipeline writes this repo's own conventions into a test tree: the harness reads them from
// the tree, so a test that wants pipeline behaviour has to say so the way a project does.
func declarePipeline(t *testing.T, root string) {
	t.Helper()
	mustWrite(t, filepath.Join(root, stage.FileName), `{
  "skill_order": ["source-research", "learning-path", "slide-builder", "av-production"],
  "skill_outputs": {
    "source-research": ["PS-*.md", "sources"],
    "learning-path": ["LP-*.md", "PO-RevA-*.md", "PO-RevB-*.md"],
    "slide-builder": ["SL-*.md", "pages"],
    "av-production": ["AV"]
  },
  "containment_prefixes": ["MC-", "_archive/"],
  "workspace_prefixes": ["MC-"]
}`)
}

// promotionTree is a tree with one hand-made learning-path case plus a finished source-research
// run whose arm dir holds both the workspace it produced and the runner's own artifacts.
func promotionTree(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	declarePipeline(t, root)
	mustWrite(t, filepath.Join(root, "learning-path", "evals", "evals.json"), `{
  "skill_name": "learning-path",
  "evals": [
    {
      "id": 1,
      "slug": "lp-1-outline",
      "prompt": "build the path from the PS-x.md here",
      "expected_output": "LP-x.md and the PO files",
      "files": ["MC-x/PS-x.md"]
    }
  ]
}`)
	mustWrite(t, filepath.Join(root, "learning-path", "evals", "files", "lp-1-outline", "MC-x", "PS-x.md"),
		"**Subject Matter:**\n\nhand made\n")

	arm := filepath.Join(root, "source-research-workspace", "iteration-2", "src-4-agentskills", "with_skill", "outputs")
	mustWrite(t, filepath.Join(arm, "MC-writing-agent-skills", "PS-writing-agent-skills.md"),
		"**Subject Matter:**\n\nWriting Agent Skills\n\n**Type:**\n\nPrimary Sources\n")
	mustWrite(t, filepath.Join(arm, "MC-writing-agent-skills", "sources", "home.md"), "raw snapshot")
	mustWrite(t, filepath.Join(arm, "MC-writing-agent-skills", "LP-writing-agent-skills.md"), "the answer to the next case")
	mustWrite(t, filepath.Join(arm, "MC-writing-agent-skills", "PO-RevA-writing-agent-skills.md"), "the answer")
	mustWrite(t, filepath.Join(arm, "stream.jsonl"), `{"type":"result"}`)
	mustWrite(t, filepath.Join(arm, "grading.json"), `{}`)

	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return s, arm
}

func TestPromoteOutputCuratesTheFixture(t *testing.T) {
	s, arm := promotionTree(t)
	res, err := s.PromoteOutput(PromoteRequest{
		Skill: "learning-path", Slug: "lp-2-promoted", Prompt: "the MC-writing-agent-skills workspace here",
		Expected: "LP-<topic>.md, PO-RevA-<topic>.md, PO-RevB-<topic>.md", Source: arm, Run: "iteration-2",
		Case: "src-4-agentskills", Arm: "with_skill",
	})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}

	want := []string{"MC-writing-agent-skills/PS-writing-agent-skills.md", "MC-writing-agent-skills/sources/home.md"}
	if strings.Join(res.Files, ",") != strings.Join(want, ",") {
		t.Errorf("staged %v, want %v", res.Files, want)
	}
	if got := strings.Join(res.Skipped, ","); got != "MC-writing-agent-skills/LP-writing-agent-skills.md,MC-writing-agent-skills/PO-RevA-writing-agent-skills.md" {
		t.Errorf("left behind %q", got)
	}
	for _, name := range []string{"stream.jsonl", "grading.json"} {
		if _, err := os.Stat(filepath.Join(res.FixtureDir, name)); err == nil {
			t.Errorf("runner artifact %s was copied into the fixture", name)
		}
	}
	if _, err := os.Stat(filepath.Join(res.FixtureDir, "MC-writing-agent-skills", "LP-writing-agent-skills.md")); err == nil {
		t.Error("the target stage's own output was carried forward")
	}
	if res.SHA256 == "" {
		t.Error("fixture hash is empty")
	}

	sk := s.Skill("learning-path")
	if len(sk.Output.Evals) != 2 {
		t.Fatalf("cases = %d, want 2", len(sk.Output.Evals))
	}
	cse := sk.Output.Evals[1]
	if cse.ID != 2 || cse.Slug != "lp-2-promoted" {
		t.Errorf("appended %s case #%d", cse.Slug, cse.ID)
	}
	prov, err := DecodePromotedFrom(cse)
	if err != nil || prov == nil {
		t.Fatalf("provenance: %v %v", prov, err)
	}
	if prov.Run != "iteration-2" || prov.Case != "src-4-agentskills" || prov.Arm != "with_skill" ||
		prov.FixtureSHA256 != res.SHA256 {
		t.Errorf("provenance = %+v", prov)
	}
}

func TestPromoteOutputProvenanceSurvivesSave(t *testing.T) {
	s, arm := promotionTree(t)
	if _, err := s.PromoteOutput(PromoteRequest{
		Skill: "learning-path", Slug: "lp-2-promoted", Prompt: "build it", Source: arm,
		Run: "iteration-2", Case: "src-4-agentskills", Arm: "with_skill",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSkill("learning-path"); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	cases := reopened.Skill("learning-path").Output.Evals
	if len(cases) != 2 {
		t.Fatalf("cases after save = %d", len(cases))
	}
	prov, err := DecodePromotedFrom(cases[1])
	if err != nil {
		t.Fatal(err)
	}
	if prov == nil || prov.Run != "iteration-2" {
		t.Fatalf("provenance did not round trip: %+v", prov)
	}
	sha, err := FixtureSHA256(reopened.FixtureDir("learning-path", "lp-2-promoted"))
	if err != nil || sha != prov.FixtureSHA256 {
		t.Errorf("fixture hash drifted: %q vs %q (%v)", sha, prov.FixtureSHA256, err)
	}
	// The hand-made case is untouched by a promotion into a new slug.
	if cases[0].Slug != "lp-1-outline" || len(cases[0].Extra) != 0 {
		t.Errorf("existing case changed: %+v", cases[0])
	}
}

func TestPromoteOutputNeverOverwritesAFixture(t *testing.T) {
	s, arm := promotionTree(t)
	req := PromoteRequest{Skill: "learning-path", Slug: "lp-2-promoted", Prompt: "build it", Source: arm,
		Run: "iteration-2", Case: "src-4-agentskills", Arm: "with_skill"}
	if _, err := s.PromoteOutput(req); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PromoteOutput(req); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second promote: err = %v, want a refusal", err)
	}
}

func TestPromoteOutputDryRunWritesNothing(t *testing.T) {
	s, arm := promotionTree(t)
	res, err := s.PromoteOutput(PromoteRequest{
		Skill: "learning-path", Slug: "lp-3-dry", Prompt: "build it", Source: arm,
		Run: "iteration-2", Case: "src-4-agentskills", Arm: "with_skill", DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) == 0 {
		t.Error("a dry run must still report what it would stage")
	}
	if s.FixtureExists("learning-path", "lp-3-dry") {
		t.Error("dry run created the fixture dir")
	}
	if len(s.Skill("learning-path").Output.Evals) != 1 {
		t.Error("dry run appended a case")
	}
}

func TestPromoteOutputRefusesEmptyWorkspace(t *testing.T) {
	s, arm := promotionTree(t)
	if err := os.RemoveAll(filepath.Join(arm, "MC-writing-agent-skills")); err != nil {
		t.Fatal(err)
	}
	_, err := s.PromoteOutput(PromoteRequest{Skill: "learning-path", Slug: "lp-4-none", Prompt: "build it",
		Source: arm, Run: "iteration-2", Case: "src-4-agentskills", Arm: "with_skill"})
	if err == nil || !strings.Contains(err.Error(), "nothing to promote") {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

func TestPromoteOutputNeedsAPromptAndAKnownSkill(t *testing.T) {
	s, arm := promotionTree(t)
	if _, err := s.PromoteOutput(PromoteRequest{Skill: "learning-path", Slug: "lp-5", Source: arm,
		Run: "iteration-2", Case: "src-4-agentskills", Arm: "with_skill"}); err == nil {
		t.Error("an empty prompt must be refused")
	}
	if _, err := s.PromoteOutput(PromoteRequest{Skill: "no-such-skill", Slug: "lp-6", Prompt: "x",
		Source: arm}); err == nil {
		t.Error("an unknown skill must be refused")
	}
}

func TestFixtureSHA256TracksContent(t *testing.T) {
	dir := t.TempDir()
	if sha, err := FixtureSHA256(filepath.Join(dir, "absent")); sha != "" || err != nil {
		t.Errorf("absent fixture = %q, %v", sha, err)
	}
	mustWrite(t, filepath.Join(dir, "MC-x", "PS-x.md"), "one")
	first, err := FixtureSHA256(dir)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "MC-x", "PS-x.md"), "two")
	second, err := FixtureSHA256(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Error("an edited fixture kept its hash")
	}
}
