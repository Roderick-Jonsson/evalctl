package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Live activity: the child already writes one JSON frame per `--format stream-json` event straight
// into <arm>/stream.jsonl, so the run can be watched by tailing that file. Nothing here changes
// how an arm is launched — a stalled reader must never block a 20-minute arm, which is exactly
// what a pipe or an in-process MultiWriter would risk.
//
// The same tail produces activity.log next to stream.jsonl: the stream is the evidence, the log
// is the narrative, written while the arm runs so a finished iteration can be read back.

// ActivityLevel is how much of the agent's own stream reaches the log.
type ActivityLevel int

const (
	ActivityUnset ActivityLevel = iota
	ActivityOff                 // no log, no events
	ActivityTools               // tool calls and their results only
	ActivityFull                // plus the assistant's running text
)

// ParseActivity reads full|tools|off; "" is the default, full.
func ParseActivity(s string) (ActivityLevel, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "full":
		return ActivityFull, nil
	case "tools":
		return ActivityTools, nil
	case "off", "none", "false":
		return ActivityOff, nil
	}
	return ActivityUnset, fmt.Errorf("--log must be full, tools or off (got %q)", s)
}

func (l ActivityLevel) String() string {
	switch l {
	case ActivityOff:
		return "off"
	case ActivityTools:
		return "tools"
	default:
		return "full"
	}
}

// activityPoll is how often the growing stream is read. At ~800 frames over two minutes a poll is
// a few KB, and the cadence is what the trigger lane already used for its early-stop check.
const activityPoll = 250 * time.Millisecond

// activityLineLimit bounds one log line: tool results carry whole files and whole web pages.
const activityLineLimit = 200

// activity tails one arm's stream while the child writes it.
type activity struct {
	r        *Runner
	path     string // stream.jsonl
	logPath  string // activity.log
	offset   int64
	rest     []byte
	started  time.Time
	file     *os.File
	level    ActivityLevel
	pending  strings.Builder // assistant text not yet broken into lines
	caseSlug string
	arm      string
}

// startActivity opens activity.log and returns the tailer, or nil when logging is off.
func (r *Runner) startActivity(stream, logPath, caseSlug, arm string, level ActivityLevel) *activity {
	if level == ActivityUnset {
		level = ActivityFull
	}
	if level == ActivityOff {
		return nil
	}
	a := &activity{r: r, path: stream, logPath: logPath, started: time.Now(),
		level: level, caseSlug: caseSlug, arm: arm}
	f, err := os.Create(logPath)
	if err != nil {
		return nil // a log that cannot be written must never fail the arm
	}
	a.file = f
	fmt.Fprintf(f, "# %s · %s · %s\n", caseSlug, arm, a.started.Format("2006-01-02 15:04:05"))
	return a
}

// poll reads what the child wrote since the last call and folds it into the log.
//
// It returns the raw frames as well, because the trigger lane decides its early stop from the
// same bytes — one read, not two.
func (a *activity) poll() []map[string]any {
	if a == nil {
		return nil
	}
	events := a.read()
	for _, e := range events {
		if e["type"] == "text" {
			if a.level < ActivityFull {
				continue
			}
			a.pending.WriteString(anyStringValue(e["text"]))
			a.flushText(false)
			continue
		}
		a.flushText(true)
		if line, ok := activityLine(e); ok {
			a.write(line)
		}
	}
	return events
}

// close drains the tail and releases the file. Safe on nil and safe to call twice.
func (a *activity) close() {
	if a == nil {
		return
	}
	a.poll()
	a.flushText(true)
	if a.file != nil {
		a.file.Close()
		a.file = nil
	}
}

// read is the tail proper: seek to the last offset, keep the unterminated remainder for next time.
func (a *activity) read() []map[string]any {
	f, err := os.Open(a.path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if _, err := f.Seek(a.offset, io.SeekStart); err != nil {
		return nil
	}
	chunk, err := io.ReadAll(f)
	if err != nil || len(chunk) == 0 {
		return nil
	}
	a.offset += int64(len(chunk))

	data := chunk
	if len(a.rest) > 0 {
		data = append(append([]byte(nil), a.rest...), chunk...)
		a.rest = nil
	}
	var out []map[string]any
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			// A half-written frame: hold it, never drop it.
			a.rest = append([]byte(nil), data...)
			break
		}
		line := bytes.TrimSpace(data[:i])
		data = data[i+1:]
		if len(line) == 0 {
			continue
		}
		var e map[string]any
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		out = append(out, e)
	}
	return out
}

// flushText emits the assistant's streamed text: complete lines always, a trailing fragment only
// when the turn ends (force) or it has grown past a paragraph.
func (a *activity) flushText(force bool) {
	if a.level < ActivityFull {
		a.pending.Reset()
		return
	}
	s := a.pending.String()
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			break
		}
		if line := strings.TrimSpace(s[:i]); line != "" {
			a.write("│ " + clipRunes(line, activityLineLimit))
		}
		s = s[i+1:]
	}
	// The remainder keeps its leading space: the stream is one frame per token, and trimming it
	// between polls would glue the words together.
	pending := strings.TrimSpace(s)
	if pending != "" {
		switch {
		case force:
			a.write("│ " + clipRunes(pending, activityLineLimit))
			s = ""
		case len([]rune(pending)) > activityLineLimit:
			// Break at a space, never mid-word: this prose is meant to be read.
			head, tail := splitAtSpace(pending, activityLineLimit)
			a.write("│ " + head)
			s = tail
		}
	}
	a.pending.Reset()
	if s != "" {
		a.pending.WriteString(s)
	}
}

// write appends one line to the log and emits it, so the TUI and the CLI see the same text.
func (a *activity) write(line string) {
	if a == nil || line == "" {
		return
	}
	if a.file != nil {
		fmt.Fprintf(a.file, "+%5.1fs  %s\n", time.Since(a.started).Seconds(), line)
	}
	a.r.emit(Event{Kind: EventActivity, Case: a.caseSlug, Arm: a.arm, Message: line, Path: a.logPath})
}

// activityLine renders one stream frame; ok is false for frames the log does not need.
func activityLine(e map[string]any) (string, bool) {
	name, _ := e["name"].(string)
	switch e["type"] {
	case "tool_use":
		if detail := toolDetail(e["input"]); detail != "" {
			return "▸ " + name + "  " + detail, true
		}
		return "▸ " + name, true
	case "tool_result":
		if isErr, _ := e["is_error"].(bool); isErr {
			return "✗ " + name + "  " + firstLine(anyStringValue(e["output"]), activityLineLimit), true
		}
		if ms := anyInt(e["duration_ms"]); ms != nil {
			return fmt.Sprintf("✓ %s  %.1fs", name, float64(*ms)/1000), true
		}
		return "✓ " + name, true
	}
	return "", false
}

// toolDetail is the one thing worth reading about a call: the command, the path, the query.
func toolDetail(input any) string {
	m, ok := input.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"command", "path", "file_path", "url", "query", "name", "slug", "skill", "pattern"} {
		if s, ok := m[key].(string); ok && s != "" {
			return clipRunes(strings.Join(strings.Fields(s), " "), activityLineLimit)
		}
	}
	for _, key := range []string{"urls", "paths", "files", "ids"} {
		list, ok := m[key].([]any)
		if !ok || len(list) == 0 {
			continue
		}
		var parts []string
		for _, item := range list {
			if s, ok := item.(string); ok && s != "" {
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 {
			return clipRunes(strings.Join(parts, " "), activityLineLimit)
		}
	}
	b, err := json.Marshal(input)
	if err != nil || len(b) <= 2 {
		return ""
	}
	return clipRunes(string(b), activityLineLimit)
}

// splitAtSpace breaks prose at the last space within limit, so a wrapped line never ends with
// half a word.
func splitAtSpace(s string, limit int) (string, string) {
	r := []rune(s)
	if len(r) <= limit {
		return s, ""
	}
	cut := limit
	for i := limit; i > limit/2; i-- {
		if r[i] == ' ' {
			cut = i
			break
		}
	}
	return string(r[:cut]), strings.TrimLeft(string(r[cut:]), " ")
}

func firstLine(s string, limit int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return clipRunes(strings.TrimSpace(s), limit)
}

func clipRunes(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}

// mergeLoaded appends newly loaded skills, keeping the order they were opened in.
func mergeLoaded(seen, next []string) []string {
	for _, s := range next {
		if !contains(seen, s) {
			seen = append(seen, s)
		}
	}
	return seen
}
