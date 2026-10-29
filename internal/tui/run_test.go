package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"

	"evalctl/internal/runner"
	"evalctl/internal/store"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testModel(t *testing.T) *Model {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "evals", "trigger-evals.json"), `{
  "queries": [
    {"id": 1, "skill": "slide-builder", "should_trigger": true, "split": "train", "query": "build slides"},
    {"id": 2, "skill": "slide-builder", "should_trigger": false, "split": "train", "query": "research this"},
    {"id": 3, "skill": "slide-builder", "should_trigger": true, "split": "validation", "query": "make a deck"},
    {"id": 4, "skill": "slide-builder", "should_trigger": false, "split": "validation", "query": "plan a path"}
  ]
}`)
	writeFile(t, filepath.Join(root, "slide-builder", "evals", "evals.json"), `{
  "skill_name": "slide-builder",
  "evals": [
    {"id": 1, "slug": "sb-1-slides", "prompt": "build slides", "expected_output": "SL.md", "files": []},
    {"id": 2, "slug": "sb-2-no-outline", "prompt": "PS.md only", "expected_output": "point at learning-path", "files": []}
  ]
}`)
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return newModel(s, store.LaneOutput)
}

// TestLauncherPrefillsFromTheSelection is the gesture the whole feature exists for: pick rows,
// press R, and the form already targets exactly those rows.
func TestLauncherPrefillsFromTheSelection(t *testing.T) {
	m := testModel(t)
	m.cursor = 1
	m.chosen[m.key(m.rows[1])] = true
	m.openLauncher()

	l := m.launcher
	if l == nil {
		t.Fatal("launcher did not open")
	}
	if l.lane != store.LaneOutput || l.budget != 1200 {
		t.Fatalf("output lane should default to a 1200s budget: %+v", l)
	}
	if len(l.caseIDs) != 1 || l.caseIDs[0] != 2 {
		t.Fatalf("case ids = %v (want the selected case 2)", l.caseIDs)
	}
	if l.target != "the focused row" {
		t.Fatalf("target = %q", l.target)
	}
	if l.arms != "both" || !l.isolate || !l.verify {
		t.Fatalf("defaults = %+v", l)
	}
	opts := m.optionsOf()
	if len(opts.Arms) != 2 || opts.Budget != 0 && opts.Budget.Seconds() != 1200 {
		t.Fatalf("options = %+v", opts)
	}

	// With nothing selected the whole lane is the target and the skill filter is dropped.
	m.chosen = map[string]bool{}
	m.openLauncher()
	if m.launcher.target != "every row in this lane" || m.launcher.caseIDs != nil {
		t.Fatalf("whole-lane target = %+v", m.launcher)
	}
	if m.launcher.skills != nil {
		t.Fatalf("skills should stay nil (=all) for a whole-lane run, got %v", m.launcher.skills)
	}
}

func TestProgressTableMirrorsTheEvents(t *testing.T) {
	m := testModel(t)
	m.progress = &progressState{lane: store.LaneOutput, index: map[string]int{}}

	m.applyEvent(runner.Event{Kind: runner.EventStart, Message: "iteration 3 · 1 case × 2 arms"})
	m.applyEvent(runner.Event{Kind: runner.EventCase, Case: "sb-1-slides", Message: "sb-1-slides · slide-builder case 1"})
	m.applyEvent(runner.Event{Kind: runner.EventArmStart, Case: "sb-1-slides", Arm: runner.ArmWith})
	with := runner.ArmResult{
		Timing:  runner.Timing{DurationMS: 1000},
		Checks:  runner.Checks{Containment: runner.Containment{OK: true}},
		Changes: runner.Changes{New: map[string]int64{}, Modified: map[string]runner.Modified{}, Deleted: []string{}},
	}
	m.applyEvent(runner.Event{Kind: runner.EventArmDone, Case: "sb-1-slides", Arm: runner.ArmWith,
		Message: "with_skill: exit=0 1.0s 100 tokens", Result: &with})
	m.applyEvent(runner.Event{Kind: runner.EventArmStart, Case: "sb-1-slides", Arm: runner.ArmWithout})
	outside := runner.ArmResult{Checks: runner.Checks{Containment: runner.Containment{
		OK: false, Outside: []string{"notes/leak.md"}}}}
	m.applyEvent(runner.Event{Kind: runner.EventArmDone, Case: "sb-1-slides", Arm: runner.ArmWithout,
		Message: "without_skill: containment=OUTSIDE WORKSPACE", Result: &outside})
	m.applyEvent(runner.Event{Kind: runner.EventDone, Case: "iteration-3", Message: "wrote report.md"})

	p := m.progress
	if len(p.cases) != 1 || len(p.cases[0].arms) != 2 {
		t.Fatalf("table = %+v", p.cases)
	}
	if p.cases[0].arms[0].status != "done" {
		t.Fatalf("with_skill should be done: %+v", p.cases[0].arms[0])
	}
	if p.cases[0].arms[1].status != "failed" {
		t.Fatalf("an arm that wrote outside the workspace must read as failed: %+v", p.cases[0].arms[1])
	}
	if p.runRef != "iteration-3" || p.summary == "" {
		t.Fatalf("run ref/summary not captured: %+v", p)
	}
	if view := m.progressView(); view == "" || !contains(view, "2/2 arms done") {
		t.Fatalf("progress view is wrong:\n%s", view)
	}
}

func TestResultsBrowserReadsReportIntoTheViewer(t *testing.T) {
	m := testModel(t)
	root := m.store.Root
	writeFile(t, filepath.Join(root, "evals", "results", "trigger-2", "meta.json"),
		`{"iteration":2,"lane":"trigger","queries":["q01-slide-builder"]}`)
	writeFile(t, filepath.Join(root, "evals", "results", "trigger-2", "trigger.json"),
		`{"score":{"threshold":0.5,"runs":3,"overall":{"judged":1,"passed":1,"rate":1}}}`)
	writeFile(t, filepath.Join(root, "evals", "results", "trigger-2", "report.md"), "# Trigger eval 2\n\n**Pass rate 100%**\n")

	m.openRuns()
	if len(m.runsList.infos) != 1 || m.runsList.infos[0].Name != "trigger-2" {
		t.Fatalf("runs = %+v", m.runsList)
	}
	if rate := m.runsList.infos[0].PassRate; rate == nil || *rate != 1 {
		t.Fatalf("pass rate = %v", rate)
	}
	m.updateRuns(key("enter"))
	if m.mode != screenViewer || m.viewer == nil {
		t.Fatalf("enter should open the report, mode=%v", m.mode)
	}
	if !contains(joinLines(m.viewer.lines), "Pass rate 100%") {
		t.Fatalf("viewer lines = %v", m.viewer.lines)
	}
}

func TestProgressErrorIsReportedNotSwallowed(t *testing.T) {
	m := testModel(t)
	m.progress = &progressState{lane: store.LaneOutput, index: map[string]int{}}
	m.applyEvent(runner.Event{Kind: "error", Err: os.ErrPermission, Message: "hermes: permission denied"})
	if m.progress.err == nil {
		t.Fatal("the error must be kept for the table")
	}
	if !contains(m.progressView(), "permission denied") {
		t.Fatalf("progress view hid the error:\n%s", m.progressView())
	}
}

func TestLauncherRowsAdjust(t *testing.T) {
	m := testModel(t)
	m.openLauncher()
	// iteration 0 means "next free"; ← clamps at 0.
	m.launcher.cursor = 1
	m.updateLauncher(key("left"))
	if m.launcher.iteration != 0 {
		t.Fatalf("iteration = %d", m.launcher.iteration)
	}
	m.updateLauncher(key("right"))
	if m.launcher.iteration != 1 {
		t.Fatalf("iteration = %d", m.launcher.iteration)
	}
	// "arms" is the second row of the output form: both → with → without → both.
	m.launcher.cursor = 2
	m.updateLauncher(key("right"))
	if m.launcher.arms != "with" {
		t.Fatalf("arms = %q", m.launcher.arms)
	}
	m.updateLauncher(key("right"))
	if m.launcher.arms != "without" {
		t.Fatalf("arms = %q", m.launcher.arms)
	}
	if opts := m.optionsOf(); len(opts.Arms) != 1 || opts.Arms[0] != runner.ArmWithout {
		t.Fatalf("options arms = %v", opts.Arms)
	}
	// esc leaves without starting anything.
	m.updateLauncher(key("esc"))
	if m.mode != screenList || m.launcher != nil || m.runActive {
		t.Fatalf("esc should close the form: mode=%v launcher=%v active=%v", m.mode, m.launcher, m.runActive)
	}
}

// TestNewScreensFitTheWindow is the layout guard: every screen the run feature adds has to render
// inside the frame without a line wider than the terminal.
func TestNewScreensFitTheWindow(t *testing.T) {
	m := sized(testModel(t), 100, 40)
	m.progress = &progressState{lane: store.LaneOutput, index: map[string]int{}}
	m.applyEvent(runner.Event{Kind: runner.EventStart, Message: "iteration 3 · 2 cases × 2 arms"})
	m.applyEvent(runner.Event{Kind: runner.EventCase, Case: "sb-1-slides", Message: "sb-1-slides"})
	m.applyEvent(runner.Event{Kind: runner.EventArmStart, Case: "sb-1-slides", Arm: runner.ArmWith})
	m.openLauncher()
	m.runsList = &runsState{infos: runner.New(m.store).ListRuns()}

	for name, view := range map[string]string{
		"launcher": m.launcherView(),
		"progress": m.progressView(),
		"runs":     m.runsView(),
	} {
		if view == "" {
			t.Fatalf("%s rendered empty", name)
		}
		for _, line := range strings.Split(view, "\n") {
			if w := lipgloss.Width(line); w > 100 {
				t.Fatalf("%s line is %d wide (window 100): %q", name, w, line)
			}
		}
	}
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

func contains(s, want string) bool {
	return len(s) >= len(want) && indexOf(s, want) >= 0
}

func indexOf(s, want string) int {
	for i := 0; i+len(want) <= len(s); i++ {
		if s[i:i+len(want)] == want {
			return i
		}
	}
	return -1
}

// keep the json import honest: the fixtures above are the on-disk shapes the browser reads.
var _ = json.Marshal

// The run table carries the arm's own stream: activity lines land in the log box, remember which
// file they came from, and v opens that file.
func TestProgressShowsLiveActivity(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 120, 40
	logPath := filepath.Join(t.TempDir(), "activity.log")
	writeFile(t, logPath, "# sb-1-slides · with_skill\n+   1.0s  ▸ terminal  go run . verify\n")

	m.progress = &progressState{lane: store.LaneOutput, index: map[string]int{}}
	m.runActive = true
	m.applyEvent(runner.Event{Kind: runner.EventCase, Case: "sb-1-slides",
		Message: "sb-1-slides · slide-builder case 1"})
	m.applyEvent(runner.Event{Kind: runner.EventArmStart, Case: "sb-1-slides", Arm: runner.ArmWith})
	m.applyEvent(runner.Event{Kind: runner.EventActivity, Case: "sb-1-slides", Arm: runner.ArmWith,
		Message: "▸ terminal  go run . verify", Path: logPath})

	p := m.progress
	if len(p.log) != 1 || !strings.Contains(p.log[0], "go run . verify") {
		t.Fatalf("log = %v", p.log)
	}
	if p.logPath != logPath || p.logArm != "sb-1-slides with_skill" {
		t.Fatalf("logPath=%q logArm=%q", p.logPath, p.logArm)
	}
	if view := m.progressView(); !strings.Contains(view, "go run . verify") {
		t.Fatalf("the table does not show the activity:\n%s", view)
	}

	m.mode = screenRunProgress
	next, _ := m.updateProgress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = next.(*Model)
	if m.mode != screenViewer || m.viewer == nil || m.viewer.kind != "activity" {
		t.Fatalf("v did not open the log: mode=%v viewer=%+v", m.mode, m.viewer)
	}
	if !strings.Contains(strings.Join(m.viewer.lines, "\n"), "go run . verify") {
		t.Fatalf("viewer lines = %v", m.viewer.lines)
	}
	if !m.viewer.follow {
		t.Fatal("a running arm's log should follow")
	}

	// The same file, after the run: opens once more, but stops chasing the bottom.
	m.runActive = false
	m.openActivity(logPath, "sb-1-slides with_skill")
	if m.viewer.follow {
		t.Fatal("a finished arm's log should not follow")
	}

	// Nothing has reached the stream yet: say so instead of opening an empty pane.
	m.mode = screenRunProgress
	m.openActivity("", "")
	if m.mode != screenRunProgress || m.viewer.path != logPath {
		t.Fatal("an empty path must leave the screen where it was")
	}
}

// The form's activity row is the knob for all of this, and it defaults to full.
func TestLauncherActivityKnob(t *testing.T) {
	m := testModel(t)
	m.openLauncher()
	if m.launcher.activity != "full" || m.optionsOf().Log != runner.ActivityFull {
		t.Fatalf("default activity = %q / %v", m.launcher.activity, m.optionsOf().Log)
	}
	cursor := -1
	for i, row := range m.options() {
		if row.label == "activity" {
			cursor = i
		}
	}
	if cursor < 0 {
		t.Fatal("the run form has no activity row")
	}
	m.launcher.cursor = cursor
	m.adjustOption(1)
	if m.launcher.activity != "tools" || m.optionsOf().Log != runner.ActivityTools {
		t.Fatalf("activity = %q / %v", m.launcher.activity, m.optionsOf().Log)
	}
	m.adjustOption(1)
	if m.optionsOf().Log != runner.ActivityOff {
		t.Fatalf("activity = %q", m.launcher.activity)
	}
	m.adjustOption(1)
	if m.optionsOf().Log != runner.ActivityFull {
		t.Fatalf("activity should cycle back to full, got %q", m.launcher.activity)
	}
}

// L → a reads a finished iteration back: every arm's activity.log, labelled by arm, in one pane.
func TestRunsBrowserOpensActivity(t *testing.T) {
	m := testModel(t)
	info := runner.RunInfo{Name: "iteration-1", Lane: store.LaneOutput, Path: t.TempDir()}
	writeFile(t, filepath.Join(info.Path, "sb-1-slides", "with_skill", "activity.log"),
		"+   1.0s  ▸ terminal  go run . verify\n")
	writeFile(t, filepath.Join(info.Path, "sb-1-slides", "without_skill", "activity.log"),
		"+   1.0s  ▸ read_file  PS.md\n")

	m.openRunActivity(info)
	if m.mode != screenViewer || m.viewer == nil {
		t.Fatalf("a did not open the pane: mode=%v", m.mode)
	}
	body := strings.Join(m.viewer.lines, "\n")
	for _, want := range []string{"sb-1-slides/with_skill", "sb-1-slides/without_skill",
		"go run . verify", "read_file  PS.md"} {
		if !strings.Contains(body, want) {
			t.Fatalf("pane is missing %q:\n%s", want, body)
		}
	}
	if !strings.Contains(m.viewer.title, "2 arms") {
		t.Fatalf("title = %q", m.viewer.title)
	}

	// A run from before this existed, or with activity off: say so, do not open an empty pane.
	empty := runner.RunInfo{Name: "iteration-2", Path: t.TempDir()}
	m.mode = screenList
	m.viewer = nil
	m.openRunActivity(empty)
	if m.mode != screenList || m.viewer != nil {
		t.Fatal("a run without logs must not open an empty pane")
	}
}

// TestReportViewerRendersMarkdownAndReflows covers the two things the report pane promises: the
// report is shown as markdown, and a resize re-lays it out instead of scrolling a stale width.
func TestReportViewerRendersMarkdownAndReflows(t *testing.T) {
	m := testModel(t)
	m.width = 120
	root := m.store.Root
	report := "# Trigger eval 4\n\n## By skill\n\n| Skill | Rate |\n|---|---|\n| " +
		"slide-builder | 100% |\n\n### notes\n\n**Every query** landed on the right side of the threshold, " +
		"which is what makes this iteration comparable with the last one.\n"
	writeFile(t, filepath.Join(root, "evals", "results", "trigger-4", "meta.json"),
		`{"iteration":4,"lane":"trigger","queries":["q01-slide-builder"]}`)
	writeFile(t, filepath.Join(root, "evals", "results", "trigger-4", "report.md"), report)

	m.openRuns()
	m.updateRuns(key("enter"))
	if m.viewer == nil || m.viewer.md == "" {
		t.Fatal("the report should be held as markdown, not as pre-split lines")
	}
	body := xansi.Strip(joinLines(m.viewer.lines))
	if contains(body, "## ") || contains(body, "|---|---|") {
		t.Fatalf("the report was not rendered:\n%s", body)
	}
	if !contains(body, "Trigger eval 4") || !contains(body, "│") {
		t.Fatalf("rendered report lost its title or its table:\n%s", body)
	}

	// A narrow pane re-wraps: nothing may be wider than the pane it is painted into.
	view := xansi.Strip(m.viewer.view(60, 30))
	for _, line := range strings.Split(view, "\n") {
		if xansi.StringWidth(line) > 60 {
			t.Fatalf("viewer painted a %d-cell line into a 60-cell pane: %q",
				xansi.StringWidth(line), line)
		}
	}
	if m.viewer.mdWidth != 60 {
		t.Fatalf("mdWidth = %d, want the width it was last laid out for", m.viewer.mdWidth)
	}
	// Painting again at the same width reuses the layout, and at a new width does not.
	at60 := m.viewer.lines
	m.viewer.view(60, 30)
	if len(m.viewer.lines) != len(at60) {
		t.Fatal("the viewer re-rendered at a width it had already been laid out for")
	}
	m.viewer.view(100, 30)
	if m.viewer.mdWidth != 100 {
		t.Fatalf("a wider pane did not re-render: mdWidth = %d", m.viewer.mdWidth)
	}
}
