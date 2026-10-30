package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/store"
)

// promptPromoteTree is promoteTree plus the trigger lane it lacks, so `t` has somewhere to write.
func promptPromoteTree(t *testing.T) *store.Store {
	t.Helper()
	s := promoteTree(t)
	body := `{
  "note": "two rows, both labels, both splits",
  "runs": 3,
  "threshold": 0.5,
  "queries": [
    {"id": 1, "skill": "source-research", "should_trigger": true, "split": "train", "why": "clear", "query": "research the operator pattern"},
    {"id": 2, "skill": "source-research", "should_trigger": false, "split": "train", "why": "chat only", "query": "just paste it here"},
    {"id": 3, "skill": "learning-path", "should_trigger": true, "split": "validation", "why": "clear", "query": "turn the research into a teaching order"},
    {"id": 4, "skill": "learning-path", "should_trigger": false, "split": "validation", "why": "near miss", "query": "render slide two as an image"}
  ]
}
`
	path := filepath.Join(s.Root, "trigger", "trigger-evals.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	return reopened
}

func TestPromptPromoteKeyAddsATriggerRow(t *testing.T) {
	s := promptPromoteTree(t)
	m := sized(newModel(s, store.LaneOutput), 140, 40)
	focusRow(t, m, "src-4-agentskills")
	m.Update(key("t"))

	if !strings.Contains(m.status, "trigger #5 added") || !strings.Contains(m.status, "source-research case #4") {
		t.Fatalf("status = %q", m.status)
	}
	if m.lane != store.LaneOutput || m.mode != screenList {
		t.Error("t should leave the list where it was — the row lands in the other lane")
	}

	reopened, err := store.Open(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	qs := reopened.Trigger.Queries
	if len(qs) != 5 {
		t.Fatalf("queries = %d, want 5", len(qs))
	}
	q := qs[4]
	if q.Query != "research it" || q.Skill != "source-research" || q.Split != "train" || !q.ShouldTrigger {
		t.Errorf("row = %+v", q)
	}
	if prov, err := store.DecodePromotedQuery(q); err != nil || prov == nil || prov.Slug != "src-4-agentskills" {
		t.Errorf("provenance = %+v, %v", prov, err)
	}
}

func TestPromptPromoteKeyRefusesASecondTime(t *testing.T) {
	s := promptPromoteTree(t)
	m := sized(newModel(s, store.LaneOutput), 140, 40)
	focusRow(t, m, "src-4-agentskills")
	m.Update(key("t"))
	m.Update(key("t"))

	if !m.statusErr || !strings.Contains(m.status, "already carries this prompt") {
		t.Fatalf("status = %q (err=%v)", m.status, m.statusErr)
	}
	if len(s.Trigger.Queries) != 5 {
		t.Errorf("queries = %d, want the refusal to append nothing", len(s.Trigger.Queries))
	}
}

func TestPromptPromoteKeyNeedsTheOutputLane(t *testing.T) {
	s := promptPromoteTree(t)
	m := sized(newModel(s, store.LaneTrigger), 140, 40)
	m.Update(key("t"))
	if !m.statusErr || !strings.Contains(m.status, "tab to the output lane") {
		t.Fatalf("status = %q", m.status)
	}
	if len(s.Trigger.Queries) != 4 {
		t.Error("a trigger-lane press wrote a row")
	}
}

func TestPromptPromoteKeyRefusesOverUnsavedEdits(t *testing.T) {
	s := promptPromoteTree(t)
	m := sized(newModel(s, store.LaneOutput), 140, 40)
	focusRow(t, m, "src-4-agentskills")
	m.dirty = true
	m.Update(key("t"))
	if !strings.Contains(m.status, "ctrl+s first") {
		t.Fatalf("status = %q", m.status)
	}
	if len(s.Trigger.Queries) != 4 {
		t.Error("an unsaved-edits refusal still wrote the row")
	}
}
