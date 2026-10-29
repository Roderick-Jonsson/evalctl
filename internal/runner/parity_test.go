package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"evalctl/internal/store"
)

// TestRescoreMatchesStoredTriggerScores is the parity oracle: it re-scores an iteration that the
// Python runner wrote, from the same per-run trigger.json records, and compares every verdict.
//
// It is opt-in on purpose — `evals/results/` is gitignored, so a machine without a finished
// iteration must skip rather than fail. Run it once after the port:
//
//	EVALCTL_PARITY_ROOT=/path/to/project go test ./internal/runner -run Parity -v
func TestRescoreMatchesStoredTriggerScores(t *testing.T) {
	root := os.Getenv("EVALCTL_PARITY_ROOT")
	if root == "" {
		t.Skip("set EVALCTL_PARITY_ROOT to a tree holding evals/results to compare with the Python-written scores")
	}
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	r := New(s)
	cases, err := r.LoadTriggerQueries(s.SkillNames(), nil, "all")
	if err != nil {
		t.Fatal(err)
	}

	checked := 0
	for _, info := range r.ListRuns() {
		if info.Lane != LaneTrigger {
			continue
		}
		path := filepath.Join(info.Path, "trigger.json")
		var stored struct {
			Score   TriggerScore                `json:"score"`
			Records map[string][]*TriggerRecord `json:"records"`
		}
		if err := readJSONFile(path, &stored); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		records := map[int][]*TriggerRecord{}
		for key, list := range stored.Records {
			var id int
			if _, err := json.Marshal(key); err != nil {
				t.Fatal(err)
			}
			for _, ch := range key {
				id = id*10 + int(ch-'0')
			}
			records[id] = list
		}
		var scored []TriggerCase
		for _, c := range cases {
			for _, rec := range records[c.ID] {
				if rec != nil {
					scored = append(scored, c)
					break
				}
			}
		}
		runs := stored.Score.Runs
		if runs == 0 {
			runs = info.Runs
		}
		got := r.ScoreTrigger(scored, records, runs, stored.Score.Threshold)

		if got.Overall.Judged != stored.Score.Overall.Judged ||
			got.Overall.Passed != stored.Score.Overall.Passed {
			t.Fatalf("%s: overall %+v ≠ stored %+v", info.Name, got.Overall, stored.Score.Overall)
		}
		storedRows := map[int]ScoreRow{}
		for _, row := range stored.Score.Rows {
			storedRows[row.ID] = row
		}
		for _, row := range got.Rows {
			was, ok := storedRows[row.ID]
			if !ok {
				t.Fatalf("%s: q%d is new", info.Name, row.ID)
			}
			if row.Triggers != was.Triggers || row.ValidRuns != was.ValidRuns ||
				!sameVerdict(row.Passed, was.Passed) {
				t.Fatalf("%s: q%d drifted: triggers %d/%d passed %v ≠ stored %d/%d %v",
					info.Name, row.ID, row.Triggers, row.ValidRuns, ptrBool(row.Passed),
					was.Triggers, was.ValidRuns, ptrBool(was.Passed))
			}
			if len(row.LoadedSets) != len(was.LoadedSets) {
				t.Fatalf("%s: q%d loaded sets %d ≠ %d", info.Name, row.ID, len(row.LoadedSets), len(was.LoadedSets))
			}
		}
		checked++
		t.Logf("%s: %d/%d judged, %d passed — matches the stored score",
			info.Name, got.Overall.Judged, got.Overall.Rows, got.Overall.Passed)
	}
	if checked == 0 {
		t.Skip("no trigger iterations under evals/results")
	}
}

func ptrBool(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

func sameVerdict(a, b *bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
