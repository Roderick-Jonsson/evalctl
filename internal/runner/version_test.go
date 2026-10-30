package runner

import (
	"encoding/json"
	"strings"
	"testing"

	"evalctl/internal/version"
)

// The artifact is what survives the terminal: a report that does not name the build cannot be
// compared with the next iteration's, because a different harness invalidates the delta.
func TestReportsNameTheBuild(t *testing.T) {
	want := "evalctl " + version.Resolve().String()

	report := Report(nil, Benchmark{Generated: "2026-10-29T12:00:00+0100"}, "slide-builder", 3)
	if !strings.Contains(report, want) {
		t.Errorf("output report does not name the build:\n%s", report)
	}

	trigger := TriggerReport(TriggerScore{Threshold: 0.5, Runs: 3}, 2, nil)
	if !strings.Contains(trigger, want) {
		t.Errorf("trigger report does not name the build:\n%s", trigger)
	}
}

func TestToolInfoMatchesTheBinary(t *testing.T) {
	info := version.Resolve()
	tool := toolInfo()
	if tool.Version != info.Version || tool.Commit != info.Commit || tool.Dirty != info.Dirty ||
		tool.BuiltAt != info.BuiltAt {
		t.Errorf("meta tool = %+v, binary = %+v", tool, info)
	}
	if tool.Version == "" {
		t.Error("meta tool has no version")
	}
}

// The meta file is read by whoever compares two iterations, so the JSON path is part of the
// contract: provenance.tool.version has to be where they look.
func TestMetaJSONCarriesTheTool(t *testing.T) {
	meta := metaQuality{}
	meta.Provenance.Tool = toolInfo()
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Provenance struct {
			Tool struct {
				Version string `json:"version"`
				Commit  string `json:"commit"`
				Dirty   bool   `json:"dirty"`
			} `json:"tool"`
		} `json:"provenance"`
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Provenance.Tool.Version != version.Resolve().Version {
		t.Errorf("meta.json provenance.tool = %+v\n%s", back.Provenance.Tool, raw)
	}
}
