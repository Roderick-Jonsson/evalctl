package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// collect wires a runner whose events land in a slice: the activity log is both a file and a
// stream of progress lines, and both have to be right.
func collect() (*Runner, *[]Event) {
	var seen []Event
	return &Runner{Emit: func(e Event) { seen = append(seen, e) }}, &seen
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, path, body string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func messages(seen []Event) []string {
	var out []string
	for _, e := range seen {
		if e.Kind == EventActivity {
			out = append(out, e.Message)
		}
	}
	return out
}

// A frame that is still being written must be held, not dropped: the tailer sees a file the child
// is halfway through appending to.
func TestActivityHoldsPartialFrame(t *testing.T) {
	dir := t.TempDir()
	stream := filepath.Join(dir, "stream.jsonl")
	write(t, stream, `{"type": "tool_use", "name": "read_file", "inp`)

	r, seen := collect()
	a := r.startActivity(stream, filepath.Join(dir, "activity.log"), "case-1", ArmWith, ActivityFull)
	if lines := messages(*seen); len(lines) != 0 {
		t.Fatalf("a half-written frame produced %v", lines)
	}

	appendTo(t, stream, `ut": {"path": "PS.md"}}`+"\n")
	a.poll()

	lines := messages(*seen)
	if len(lines) != 1 || !strings.Contains(lines[0], "read_file") || !strings.Contains(lines[0], "PS.md") {
		t.Fatalf("want one read_file line carrying the path, got %v", lines)
	}
}

// The stream is one frame per token; the log is one line per thought. 200 deltas must not become
// 200 lines.
func TestActivityCoalescesStreamedText(t *testing.T) {
	dir := t.TempDir()
	stream := filepath.Join(dir, "stream.jsonl")
	var b strings.Builder
	for _, word := range strings.Split("The outline maps to three sections and a close.", " ") {
		b.WriteString(`{"type": "text", "text": "` + word + ` "}` + "\n")
	}
	b.WriteString(`{"type": "tool_use", "name": "write_file", "input": {"path": "SL.md"}}` + "\n")
	write(t, stream, b.String())

	r, seen := collect()
	a := r.startActivity(stream, filepath.Join(dir, "activity.log"), "case-1", ArmWith, ActivityFull)
	a.poll()
	a.close()

	lines := messages(*seen)
	if len(lines) != 2 {
		t.Fatalf("want the prose on one line plus the tool call, got %d: %v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "│ ") || !strings.Contains(lines[0], "three sections") {
		t.Fatalf("want the prose line first, got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "▸ write_file") {
		t.Fatalf("want the tool call last, got %q", lines[1])
	}
}

// ActivityTools keeps the calls and drops the prose; ActivityOff writes nothing at all.
func TestActivityLevels(t *testing.T) {
	dir := t.TempDir()
	stream := filepath.Join(dir, "stream.jsonl")
	write(t, stream, `{"type": "text", "text": "thinking out loud\n"}`+"\n"+
		`{"type": "tool_use", "name": "terminal", "input": {"command": "ls -la"}}`+"\n")

	r, seen := collect()
	a := r.startActivity(stream, filepath.Join(dir, "tools.log"), "case-1", ArmWith, ActivityTools)
	a.poll()
	a.close()
	lines := messages(*seen)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "▸ terminal") {
		t.Fatalf("tools level should keep only the call, got %v", lines)
	}

	r2, seen2 := collect()
	if a2 := r2.startActivity(stream, filepath.Join(dir, "off.log"), "case-1", ArmWith, ActivityOff); a2 != nil {
		t.Fatal("ActivityOff must not open a tailer")
	}
	if len(*seen2) != 0 {
		t.Fatalf("ActivityOff emitted %v", *seen2)
	}
	if _, err := os.Stat(filepath.Join(dir, "off.log")); err == nil {
		t.Fatal("ActivityOff wrote a log file")
	}
}

// activity.log is the persisted narrative: header, then one timestamped line per event, and every
// emitted line carries the path so the TUI can open the file behind it.
func TestActivityWritesLogFile(t *testing.T) {
	dir := t.TempDir()
	stream := filepath.Join(dir, "stream.jsonl")
	logPath := filepath.Join(dir, "activity.log")
	write(t, stream, `{"type": "tool_use", "name": "skill_view", "input": {"name": "slide-builder"}}`+"\n"+
		`{"type": "tool_result", "name": "skill_view", "duration_ms": 16, "is_error": false}`+"\n")

	r, seen := collect()
	a := r.startActivity(stream, logPath, "sb-1", ArmWith, ActivityFull)
	a.poll()
	a.close()

	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	if !strings.HasPrefix(body, "# sb-1 · with_skill · ") {
		t.Fatalf("want a header naming the case and arm, got %q", body)
	}
	if !strings.Contains(body, "▸ skill_view  slide-builder") || !strings.Contains(body, "✓ skill_view  0.0s") {
		t.Fatalf("want both frames in the log, got:\n%s", body)
	}
	for _, e := range *seen {
		if e.Kind == EventActivity && e.Path != logPath {
			t.Fatalf("event %q lost its path", e.Message)
		}
	}
}

// toolDetail is what makes a line readable: the command, not the whole tool call blob.
func TestToolDetailPrefersTheCommand(t *testing.T) {
	cases := []struct {
		name  string
		input any
		want  string
	}{
		{"terminal", map[string]any{"command": "go   run .\n verify", "timeout": 5.0}, "go run . verify"},
		{"read_file", map[string]any{"path": "PS.md", "limit": 10.0}, "PS.md"},
		{"web_extract", map[string]any{"urls": []any{"https://a", "https://b"}}, "https://a https://b"},
		{"other", map[string]any{"foo": "bar"}, `{"foo":"bar"}`},
		{"empty", map[string]any{}, ""},
	}
	for _, c := range cases {
		if got := toolDetail(c.input); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// A failed tool call has to read as a failure, with the reason on the same line.
func TestActivityLineMarksFailures(t *testing.T) {
	line, ok := activityLine(map[string]any{
		"type": "tool_result", "name": "terminal", "is_error": true,
		"output": "exit status 1\ngo: no such module",
	})
	if !ok || !strings.HasPrefix(line, "✗ terminal") || !strings.Contains(line, "exit status 1") {
		t.Fatalf("want a one-line failure, got %q", line)
	}
	if strings.Contains(line, "no such module") {
		t.Fatalf("only the first line belongs on the log: %q", line)
	}
	if _, ok := activityLine(map[string]any{"type": "system"}); ok {
		t.Fatal("system frames are not activity")
	}
}

// mergeLoaded keeps the order the skills were first opened in, which is what early-stop reports.
func TestMergeLoadedKeepsFirstLoadOrder(t *testing.T) {
	got := mergeLoaded(nil, []string{"slide-builder"})
	got = mergeLoaded(got, []string{"source-research", "slide-builder"})
	if strings.Join(got, ",") != "slide-builder,source-research" {
		t.Fatalf("got %v", got)
	}
}

// Long prose wraps at a space and keeps the rest for the next line: a log that cuts words in half
// is not a log anyone reads.
func TestLongTextWrapsAtWords(t *testing.T) {
	dir := t.TempDir()
	stream := filepath.Join(dir, "stream.jsonl")
	words := strings.Repeat("alpha beta gamma delta ", 20) // 480 chars, no newline at all
	write(t, stream, `{"type": "text", "text": "`+strings.TrimSpace(words)+`"}`+"\n")

	r, seen := collect()
	a := r.startActivity(stream, filepath.Join(dir, "activity.log"), "case-1", ArmWith, ActivityFull)
	a.poll()
	a.close()

	lines := messages(*seen)
	if len(lines) < 2 {
		t.Fatalf("want the prose wrapped over several lines, got %v", lines)
	}
	for _, line := range lines {
		body := strings.TrimPrefix(line, "│ ")
		if strings.HasSuffix(body, "…") {
			continue // a genuinely long last line is clipped, not wrapped
		}
		if !strings.HasSuffix(body, "alpha") && !strings.HasSuffix(body, "beta") &&
			!strings.HasSuffix(body, "gamma") && !strings.HasSuffix(body, "delta") {
			t.Fatalf("a wrapped line ended mid-word: %q", body)
		}
	}
}
