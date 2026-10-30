package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"evalctl/internal/store"
)

// press sends one key and runs whatever command it returned, so a scan the form scheduled has
// landed by the time an assertion reads the preview. It is how these tests drive the async path
// the binary actually takes.
func press(m *Model, k string) {
	_, cmd := m.Update(key(k))
	drainCmds(m, cmd, 0)
}

func drainCmds(m *Model, cmd tea.Cmd, depth int) {
	if cmd == nil || depth > 6 {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			drainCmds(m, c, depth+1)
		}
		return
	}
	_, next := m.Update(msg)
	drainCmds(m, next, depth+1)
}

// promoteTree is a tree with two stage skills and one finished source-research run: the arm dir
// holds the workspace it produced plus the target stage's own answers, which a promotion must
// leave behind.
func promoteTree(t *testing.T) *store.Store {
	t.Helper()
	root := t.TempDir()
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "learning-path", "evals", "evals.json"), `{
  "skill_name": "learning-path",
  "evals": [
    {"id": 1, "slug": "lp-1-outline", "prompt": "build the path", "expected_output": "LP-written.md", "files": []}
  ]
}`)
	write(filepath.Join(root, "source-research", "evals", "evals.json"), `{
  "skill_name": "source-research",
  "evals": [
    {"id": 4, "slug": "src-4-agentskills", "prompt": "research it", "expected_output": "PS-written.md", "files": []}
  ]
}`)
	arm := filepath.Join(root, "source-research-workspace", "iteration-2", "src-4-agentskills", "with_skill", "outputs")
	write(filepath.Join(arm, "MC-written", "PS-written.md"), "**Subject Matter:**\n\nWriting Agent Skills\n\n**Type:**\n\nPrimary Sources\n")
	write(filepath.Join(arm, "MC-written", "sources", "home.md"), "raw")
	write(filepath.Join(arm, "MC-written", "LP-written.md"), "the answer")
	write(filepath.Join(arm, "stream.jsonl"), `{"type":"result"}`)
	write(filepath.Join(root, "source-research-workspace", "iteration-2", "src-4-agentskills", "without_skill", "outputs", "MC-written", "PS-written.md"), "bare")

	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// focusLabel drives the form's cursor to a row the way a person would.
func focusLabel(t *testing.T, m *Model, label string) {
	t.Helper()
	for i, row := range m.promote.rows() {
		if row.label == label {
			m.promote.cursor = i
			return
		}
	}
	t.Fatalf("row %q is not in the form: %+v", label, m.promote.rows())
}

// focusRow moves the list cursor onto a row by slug.
func focusRow(t *testing.T, m *Model, slug string) {
	t.Helper()
	for i, e := range m.rows {
		if e.Slug == slug {
			m.cursor = i
			return
		}
	}
	t.Fatalf("no row with slug %q", slug)
}

func TestPromoteFormOpensFromTheOutputLane(t *testing.T) {
	m := sized(newModel(promoteTree(t), store.LaneOutput), 140, 40)
	focusRow(t, m, "src-4-agentskills")
	press(m, "c")
	if m.mode != screenPromote || m.promote == nil {
		t.Fatal("c did not open the promote form in the output lane")
	}
	p := m.promote
	if p.runs[p.runIx].name != "iteration-2" || p.caseSlug() != "src-4-agentskills" {
		t.Errorf("source = %s/%s, want the newest run and the focused case", p.runs[p.runIx].name, p.caseSlug())
	}
	if p.arm() != "with_skill" {
		t.Errorf("arm = %q, want with_skill", p.arm())
	}
	if p.skill() != "learning-path" {
		t.Errorf("target skill = %q, want the stage after source-research", p.skill())
	}
	if p.id != 2 || p.slug != "lp-2-agentskills" {
		t.Errorf("new case = #%d %q, want #2 lp-2-agentskills", p.id, p.slug)
	}
	if strings.Join(p.preview.Files, ",") != "MC-written/PS-written.md,MC-written/sources/home.md" {
		t.Errorf("preview files = %v", p.preview.Files)
	}
	if strings.Join(p.preview.Skipped, ",") != "MC-written/LP-written.md" {
		t.Errorf("preview kept %v", p.preview.Skipped)
	}
	view := m.View()
	for _, want := range []string{"promote ·", "from run", "iteration-2", "will stage", "left behind"} {
		if !strings.Contains(view, want) {
			t.Fatalf("form view missing %q:\n%s", want, view)
		}
	}
}

func TestPromoteFromTheFormWritesTheFixtureAndTheCase(t *testing.T) {
	s := promoteTree(t)
	m := sized(newModel(s, store.LaneOutput), 140, 40)
	focusRow(t, m, "src-4-agentskills")
	press(m, "c")
	focusLabel(t, m, "enter to promote")
	press(m, "enter")

	if m.mode != screenList || m.promote != nil {
		t.Fatal("a finished promotion should return to the list")
	}
	if !strings.Contains(m.status, "promoted source-research@iteration-2/src-4-agentskills/with_skill") {
		t.Fatalf("status = %q", m.status)
	}
	fixture := s.FixtureDir("learning-path", "lp-2-agentskills")
	if _, err := os.Stat(filepath.Join(fixture, "MC-written", "PS-written.md")); err != nil {
		t.Fatalf("fixture not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture, "MC-written", "sources", "home.md")); err != nil {
		t.Error("sources did not ride along")
	}
	if _, err := os.Stat(filepath.Join(fixture, "MC-written", "LP-written.md")); err == nil {
		t.Error("the target stage's own output was carried forward")
	}
	if _, err := os.Stat(filepath.Join(fixture, "stream.jsonl")); err == nil {
		t.Error("the runner's artifact was copied")
	}
	// The case file is written by the promotion itself, and the new row is under the cursor.
	reopened, err := store.Open(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	cases := reopened.Skill("learning-path").Output.Evals
	if len(cases) != 2 || cases[1].Slug != "lp-2-agentskills" {
		t.Fatalf("saved cases = %+v", cases)
	}
	if prov, err := store.DecodePromotedFrom(cases[1]); err != nil || prov == nil || prov.Run != "iteration-2" {
		t.Errorf("provenance = %+v, %v", prov, err)
	}
	if m.rows[m.cursor].Slug != "lp-2-agentskills" || m.rows[m.cursor].Skill != "learning-path" {
		t.Errorf("cursor is on %+v", m.rows[m.cursor])
	}
}

func TestPromoteFormDryRunWritesNothing(t *testing.T) {
	s := promoteTree(t)
	m := sized(newModel(s, store.LaneOutput), 140, 40)
	focusRow(t, m, "src-4-agentskills")
	press(m, "c")
	focusLabel(t, m, "dry run")
	press(m, "right")
	focusLabel(t, m, "enter to promote")
	press(m, "enter")

	if !strings.Contains(m.status, "dry run") {
		t.Fatalf("status = %q", m.status)
	}
	if s.FixtureExists("learning-path", "lp-2-agentskills") {
		t.Error("dry run created the fixture")
	}
	if len(s.Skill("learning-path").Output.Evals) != 1 {
		t.Error("dry run appended a case")
	}
	if m.promote == nil {
		t.Error("a dry run should leave the form open")
	}
}

func TestPromoteFormRefusesOverUnsavedEdits(t *testing.T) {
	s := promoteTree(t)
	m := sized(newModel(s, store.LaneOutput), 140, 40)
	focusRow(t, m, "src-4-agentskills")
	press(m, "c")
	m.dirty = true
	focusLabel(t, m, "enter to promote")
	press(m, "enter")

	if !strings.Contains(m.status, "ctrl+s first") {
		t.Fatalf("status = %q, want the save-first refusal", m.status)
	}
	if s.FixtureExists("learning-path", "lp-2-agentskills") {
		t.Error("a refused promotion still copied the fixture")
	}
}

func TestPromoteFormEditsSlugAndCyclesTheTarget(t *testing.T) {
	m := sized(newModel(promoteTree(t), store.LaneOutput), 140, 40)
	focusRow(t, m, "src-4-agentskills")
	press(m, "c")

	focusLabel(t, m, "slug")
	press(m, "enter")
	if !m.promote.editing {
		t.Fatal("enter on the slug row should open the input")
	}
	m.promote.field.SetValue("lp-2-hand-picked")
	press(m, "enter")
	if m.promote.slug != "lp-2-hand-picked" {
		t.Fatalf("slug = %q", m.promote.slug)
	}

	// Cycling the target re-reads the preview and keeps the hand-typed slug.
	focusLabel(t, m, "target skill")
	press(m, "right")
	if m.promote.skill() != "source-research" {
		t.Fatalf("target = %q", m.promote.skill())
	}
	if m.promote.slug != "lp-2-hand-picked" {
		t.Errorf("an edited slug was overwritten: %q", m.promote.slug)
	}
	if strings.Join(m.promote.preview.Skipped, ",") != "MC-written/PS-written.md,MC-written/sources/" {
		t.Errorf("source-research promotion kept %v", m.promote.preview.Skipped)
	}
}

func TestPromoteNeedsTheOutputLaneAndAFinishedRun(t *testing.T) {
	m := sized(newModel(promoteTree(t), store.LaneTrigger), 140, 30)
	press(m, "c")
	if m.mode == screenPromote {
		t.Fatal("the trigger lane has nothing to promote")
	}
	if !strings.Contains(m.status, "output lane") {
		t.Fatalf("status = %q", m.status)
	}

	empty := sized(newModel(testStore(t), store.LaneOutput), 140, 30)
	empty.Update(key("c"))
	if empty.mode == screenPromote || !strings.Contains(empty.status, "*-workspace") {
		t.Fatalf("mode = %v status = %q — an empty results dir must be reported", empty.mode, empty.status)
	}
}

func TestPromoteFormCancelWritesNothing(t *testing.T) {
	s := promoteTree(t)
	m := sized(newModel(s, store.LaneOutput), 140, 40)
	focusRow(t, m, "src-4-agentskills")
	press(m, "c")
	press(m, "esc")
	if m.mode != screenList || m.promote != nil {
		t.Fatal("esc should close the form")
	}
	if s.FixtureExists("learning-path", "lp-2-agentskills") {
		t.Error("cancelling still created the fixture")
	}
}

// The other destination: the producing skill's own references/, so the next run's agent reads
// the shape a good run produced.
func TestPromoteFormSwitchesToReferences(t *testing.T) {
	m := sized(newModel(promoteTree(t), store.LaneOutput), 140, 40)
	focusRow(t, m, "src-4-agentskills")
	press(m, "c")
	focusLabel(t, m, "destination")
	press(m, "right")

	p := m.promote
	if p.dest != destReferences {
		t.Fatalf("destination = %d, want the references one", p.dest)
	}
	if p.skill() != "source-research" {
		t.Errorf("target skill = %q, want the stage that produced the artifact", p.skill())
	}
	for _, label := range []string{"file", "as", "workspace"} {
		focusLabel(t, m, label)
	}
	for _, row := range p.rows() {
		if row.label == "slug" || row.label == "prompt" {
			t.Fatalf("a references promotion has no case, so the %q row must be gone", row.label)
		}
	}
}

func TestPromoteFormWritesTheReference(t *testing.T) {
	s := promoteTree(t)
	m := sized(newModel(s, store.LaneOutput), 140, 40)
	focusRow(t, m, "src-4-agentskills")
	press(m, "c")
	focusLabel(t, m, "destination")
	press(m, "right")
	if got := strings.Join(m.promote.reference.Files, ","); got != "PS-written.md,sources/home.md" {
		t.Fatalf("preview took %q", got)
	}
	focusLabel(t, m, "enter to promote")
	press(m, "enter")

	if m.mode != screenList || m.promote != nil {
		t.Fatal("a finished promotion should return to the list")
	}
	if !strings.Contains(m.status, "source-research/references/") || !strings.Contains(m.status, "SKILL.md") {
		t.Fatalf("status = %q", m.status)
	}
	dir := s.ReferencesDir("source-research")
	for _, name := range []string{"PS-written.md", "sources/home.md"} {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("reference %s not written: %v", name, err)
		}
		if !strings.Contains(string(b), "Subject Matter") && !strings.Contains(name, "sources") {
			t.Errorf("%s holds %q", name, b)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "LP-written.md")); err == nil {
		t.Error("the next stage's artifact was shipped as a reference")
	}
	if _, err := os.Stat(s.ReferenceLogPath()); err != nil {
		t.Errorf("no provenance log: %v", err)
	}
}

// A taken name must stop the form before it writes, and say what to do about it.
func TestPromoteFormRefusesAReferenceAlreadyThere(t *testing.T) {
	s := promoteTree(t)
	if err := os.MkdirAll(s.ReferencesDir("source-research"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.ReferencesDir("source-research"), "PS-written.md"), []byte("hand written\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := sized(newModel(s, store.LaneOutput), 140, 40)
	focusRow(t, m, "src-4-agentskills")
	press(m, "c")
	focusLabel(t, m, "destination")
	press(m, "right")

	if strings.Join(m.promote.reference.Overwrote, ",") != "PS-written.md" {
		t.Fatalf("collisions = %v", m.promote.reference.Overwrote)
	}
	if view := m.View(); !strings.Contains(view, "already in references/") {
		t.Fatalf("the form does not say the name is taken:\n%s", view)
	}
	focusLabel(t, m, "enter to promote")
	press(m, "enter")
	if !strings.Contains(m.status, "already in references/") {
		t.Errorf("status = %q, want the refusal", m.status)
	}
	if b, _ := os.ReadFile(filepath.Join(s.ReferencesDir("source-research"), "PS-written.md")); string(b) != "hand written\n" {
		t.Error("the hand-written reference was overwritten")
	}
}
