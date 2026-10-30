package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// referenceTree is a tree with the producing skill and the consuming one, plus a finished
// source-research run whose arm dir holds both what the stage wrote and the runner's own
// artifacts: a reference takes what the stage wrote and nothing else.
func referenceTree(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "source-research", "evals", "evals.json"), `{
  "skill_name": "source-research",
  "evals": [
    {"id": 4, "slug": "src-4-agentskills", "prompt": "research it", "expected_output": "PS-written.md", "files": []}
  ]
}`)
	mustWrite(t, filepath.Join(root, "source-research", "references", "PS-written.md"), "the worked example\n")
	mustWrite(t, filepath.Join(root, "learning-path", "evals", "evals.json"), `{
  "skill_name": "learning-path",
  "evals": [
    {"id": 1, "slug": "lp-1-outline", "prompt": "build it", "expected_output": "LP-written.md", "files": []}
  ]
}`)
	arm := filepath.Join(root, "source-research-workspace", "iteration-3", "src-4-agentskills", "with_skill", "outputs")
	mustWrite(t, filepath.Join(arm, "MC-written", "PS-written.md"), "**Subject Matter:**\n\nWriting Agent Skills\n\n**Type:**\n\nPrimary Sources\n")
	mustWrite(t, filepath.Join(arm, "MC-written", "sources", "home.md"), "raw snapshot")
	mustWrite(t, filepath.Join(arm, "MC-written", "LP-written.md"), "the next stage's answer")
	mustWrite(t, filepath.Join(arm, "stream.jsonl"), `{"type":"result"}`)

	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return s, arm
}

func refReq(arm string) ReferenceRequest {
	return ReferenceRequest{Skill: "source-research", Source: arm, Run: "iteration-3",
		Case: "src-4-agentskills", Arm: "with_skill", As: "run-3/"}
}

// The point of the verb: the stage's own output is exactly what a fixture promotion drops.
func TestPromoteReferencesTakesTheStagesOwnOutputs(t *testing.T) {
	s, arm := referenceTree(t)
	res, err := s.PromoteReferences(refReq(arm))
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if got := strings.Join(res.Files, ","); got != "PS-written.md,sources/home.md" {
		t.Errorf("took %q, want PS-written.md and its sources", got)
	}
	if got := strings.Join(res.Targets, ","); got != "run-3/PS-written.md,run-3/sources/home.md" {
		t.Errorf("targets %q", got)
	}
	if got := strings.Join(res.Skipped, ","); got != "LP-written.md" {
		t.Errorf("left behind %q, want only the next stage's artifact", got)
	}
	for _, name := range []string{"run-3/PS-written.md", "run-3/sources/home.md"} {
		if _, err := os.Stat(filepath.Join(s.ReferencesDir("source-research"), filepath.FromSlash(name))); err != nil {
			t.Errorf("reference %s not written: %v", name, err)
		}
	}
	// The hand-written example and the runner's own artifacts are untouched.
	if b, err := os.ReadFile(filepath.Join(s.ReferencesDir("source-research"), "PS-written.md")); err != nil || string(b) != "the worked example\n" {
		t.Errorf("the existing PS-written.md changed: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(s.ReferencesDir("source-research"), "stream.jsonl")); err == nil {
		t.Error("the runner's artifact was copied into the skill")
	}
	if res.LogPath != s.ReferenceLogPath() {
		t.Errorf("log path = %q", res.LogPath)
	}
}

func TestPromoteReferencesLogsWhereItCameFrom(t *testing.T) {
	s, arm := referenceTree(t)
	if _, err := s.PromoteReferences(refReq(arm)); err != nil {
		t.Fatal(err)
	}
	log := readReferenceLog(t, s)
	if len(log) != 2 {
		t.Fatalf("log = %+v, want one record per file", log)
	}
	first := log[0]
	if first.Path != "source-research/references/run-3/PS-written.md" || first.Source != "PS-written.md" ||
		first.Run != "iteration-3" || first.Case != "src-4-agentskills" || first.Arm != "with_skill" {
		t.Errorf("record = %+v", first)
	}
	if len(first.SHA256) != 64 {
		t.Errorf("sha256 = %q", first.SHA256)
	}

	// Re-promoting the same paths replaces their records instead of doubling them.
	req := refReq(arm)
	req.Force = true
	if _, err := s.PromoteReferences(req); err != nil {
		t.Fatal(err)
	}
	if log := readReferenceLog(t, s); len(log) != 2 {
		t.Errorf("log = %d records after a re-promote, want 2", len(log))
	}
}

func readReferenceLog(t *testing.T, s *Store) []ReferenceRecord {
	t.Helper()
	b, err := os.ReadFile(s.ReferenceLogPath())
	if err != nil {
		t.Fatalf("no provenance log: %v", err)
	}
	var log []ReferenceRecord
	if err := json.Unmarshal(b, &log); err != nil {
		t.Fatalf("log is not JSON: %v", err)
	}
	return log
}

func TestPromoteReferencesNeverReplacesWithoutForce(t *testing.T) {
	s, arm := referenceTree(t)
	req := refReq(arm)
	req.File, req.As = []string{"PS-written.md"}, "PS-written.md"
	if _, err := s.PromoteReferences(req); err == nil || !strings.Contains(err.Error(), "already in references/") {
		t.Fatalf("err = %v, want a refusal over the hand-written example", err)
	}
	if b, _ := os.ReadFile(filepath.Join(s.ReferencesDir("source-research"), "PS-written.md")); string(b) != "the worked example\n" {
		t.Error("a refused promotion still overwrote the reference")
	}

	req.Force = true
	if _, err := s.PromoteReferences(req); err != nil {
		t.Fatalf("--force: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(s.ReferencesDir("source-research"), "PS-written.md")); strings.Contains(string(b), "worked example") {
		t.Error("--force did not replace the bytes")
	}
	backups, _ := filepath.Glob(filepath.Join(s.Root, ".evalctl", "backups", "*references_PS-written.md"))
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want the replaced bytes kept", backups)
	}
	if b, _ := os.ReadFile(backups[0]); string(b) != "the worked example\n" {
		t.Errorf("backup holds %q", b)
	}
}

func TestPromoteReferencesDryRunWritesNothing(t *testing.T) {
	s, arm := referenceTree(t)
	req := refReq(arm)
	req.DryRun = true
	res, err := s.PromoteReferences(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 2 {
		t.Errorf("dry run reported %v", res.Files)
	}
	if _, err := os.Stat(filepath.Join(s.ReferencesDir("source-research"), "run-3")); err == nil {
		t.Error("dry run wrote the reference")
	}
	if _, err := os.Stat(s.ReferenceLogPath()); err == nil {
		t.Error("dry run wrote the log")
	}
}

func TestPromoteReferencesAllTakesTheWorkspace(t *testing.T) {
	s, arm := referenceTree(t)
	res, err := s.PromoteReferences(ReferenceRequest{Skill: "source-research", Source: arm, All: true, As: "run-3/"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(res.Files, ","); got != "LP-written.md,PS-written.md,sources/home.md" {
		t.Errorf("took %q, want the workspace minus its runner artifacts", got)
	}
	if len(res.Skipped) != 0 {
		t.Errorf("left behind %v", res.Skipped)
	}
}

func TestPromoteReferencesFileNarrowsToAPath(t *testing.T) {
	s, arm := referenceTree(t)
	res, err := s.PromoteReferences(ReferenceRequest{Skill: "source-research", Source: arm,
		File: []string{"sources/"}, As: "raw/"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(res.Targets, ","); got != "raw/sources/home.md" {
		t.Errorf("targets %q, want the workspace-relative path kept under --as", got)
	}
	if got := strings.Join(res.Skipped, ","); got != "LP-written.md,PS-written.md" {
		t.Errorf("left behind %q", got)
	}
}

// A taken name is a refusal, not a silent overwrite: the hand-written example is the thing a
// promotion is meant to sit beside.
func TestPromoteReferencesRefusesToOverwriteAnExample(t *testing.T) {
	s, arm := referenceTree(t)
	_, err := s.PromoteReferences(ReferenceRequest{Skill: "source-research", Source: arm, All: true})
	if err == nil || !strings.Contains(err.Error(), "already in references/: PS-written.md") {
		t.Fatalf("err = %v, want the collision named", err)
	}
}

func TestPromoteReferencesRefusesASetUnderOneName(t *testing.T) {
	s, arm := referenceTree(t)
	if _, err := s.PromoteReferences(ReferenceRequest{Skill: "source-research", Source: arm,
		File: []string{"PS-written.md", "sources/"}, As: "PS-run.md"}); err == nil {
		t.Fatal("a file name for two files was accepted")
	}
}

func TestPromoteReferencesRefusesToEscapeReferences(t *testing.T) {
	s, arm := referenceTree(t)
	for _, as := range []string{"../PS.md", "/etc/PS.md", "a/../../PS.md"} {
		if _, err := s.PromoteReferences(ReferenceRequest{Skill: "source-research", Source: arm,
			File: []string{"PS-written.md"}, As: as}); err == nil {
			t.Errorf("--as %q was accepted", as)
		}
	}
}

func TestPromoteReferencesNamesWhatItCouldNotFind(t *testing.T) {
	s, arm := referenceTree(t)
	_, err := s.PromoteReferences(ReferenceRequest{Skill: "source-research", Source: arm, File: []string{"nope.md"}})
	if err == nil || !strings.Contains(err.Error(), "it has:") {
		t.Fatalf("err = %v, want the workspace listing", err)
	}
	if !strings.Contains(err.Error(), "PS-written.md") {
		t.Errorf("err %q does not name what is there", err)
	}
}

func TestPromoteReferencesNeedsOneWorkspace(t *testing.T) {
	s, arm := referenceTree(t)
	mustWrite(t, filepath.Join(arm, "MC-second", "PS-second.md"), "another masterclass")
	_, err := s.PromoteReferences(refReq(arm))
	if err == nil || !strings.Contains(err.Error(), "--workspace") {
		t.Fatalf("err = %v, want an ambiguity refusal", err)
	}
	req := refReq(arm)
	req.Workspace = "MC-second"
	res, err := s.PromoteReferences(req)
	if err != nil {
		t.Fatalf("--workspace: %v", err)
	}
	if res.Workspace != "MC-second" {
		t.Errorf("workspace = %q", res.Workspace)
	}
}

// The preview is what the form re-reads on every key, so it must be free of side effects and must
// report a taken name before enter is pressed, not after.
func TestReferencePreviewReportsCollisions(t *testing.T) {
	s, arm := referenceTree(t)
	res, err := s.ReferencePreview(ReferenceRequest{Skill: "source-research", Source: arm,
		File: []string{"PS-written.md"}, As: "PS-written.md"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Overwrote, ",") != "PS-written.md" {
		t.Errorf("collisions = %v", res.Overwrote)
	}
	if b, _ := os.ReadFile(filepath.Join(s.ReferencesDir("source-research"), "PS-written.md")); string(b) != "the worked example\n" {
		t.Error("the preview wrote to the reference")
	}
	if _, err := os.Stat(s.ReferenceLogPath()); err == nil {
		t.Error("the preview wrote the log")
	}
}
