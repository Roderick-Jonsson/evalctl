package runner

import (
	"encoding/json"
	"os"
	"strings"
)

// IterEvents reads every complete JSON event from a stream-json file, silently skipping partial
// lines. The file is read whole on purpose: a run is a handful of turns, and the trigger lane
// re-scans it while the child is still writing.
func IterEvents(path string) []map[string]any {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []map[string]any
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		out = append(out, event)
	}
	return out
}

// FindResult is the last `result` frame — the one carrying tokens, duration and the answer.
func FindResult(path string) map[string]any {
	var last map[string]any
	for _, event := range IterEvents(path) {
		if event["type"] == "result" {
			last = event
		}
	}
	return last
}

// ReadTools are the tool names that open a file; skill_view is Hermes's own loader.
var ReadTools = []string{"read_file", "view"}

// LoadedFromEvents reports which stage skills the agent actually opened, in order, from evidence
// only. A load is a `skill_view` tool_use naming the skill, or a read of an installed
// skills/<name>/SKILL.md. Prose in the answer is never evidence — an agent that says "using the
// slide-builder skill" without opening it has not triggered it.
func LoadedFromEvents(events []map[string]any, skills []string) []string {
	var loaded []string
	seen := map[string]bool{}
	for _, event := range events {
		if event["type"] != "tool_use" {
			continue
		}
		name, _ := event["name"].(string)
		input, _ := event["input"]
		blob, _ := json.Marshal(input)
		blobStr := string(blob)
		for _, skill := range skills {
			if seen[skill] {
				continue
			}
			if name == "skill_view" && strings.Contains(blobStr, skill) {
				seen[skill] = true
				loaded = append(loaded, skill)
				continue
			}
			for _, read := range ReadTools {
				if name == read && strings.Contains(blobStr, "skills/"+skill+"/SKILL.md") {
					seen[skill] = true
					loaded = append(loaded, skill)
					break
				}
			}
		}
	}
	return loaded
}

// HasEvidence reports whether a run did anything worth scoring: a launch that died before its
// first tool call emits only its init and result frames.
func HasEvidence(events []map[string]any) bool {
	for _, event := range events {
		switch event["type"] {
		case "tool_use", "tool_result", "text":
			return true
		}
	}
	return false
}
