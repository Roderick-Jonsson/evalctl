package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"evalctl/internal/runner"
)

// The iteration section's evidence: the failure cards, in full, for the rows the workbench lists in
// the pane. The pane shows the verdict; this shows the ask, the why it is in the set, what loaded
// instead, and the rule that a failure here is a fact about the wording and not a row to reword.

// openFailures is the evidence screen: one card per failing train row, and the rule that says a
// failure here is a fact about the wording and not a row to reword.
func (m *Model) openFailures(it *iteration) tea.Cmd {
	d := &docWriter{w: max(28, m.width-6)}
	// A document's head is drawn by the reader above the rule; the body opens with the sentence
	// that belongs to it. Two titles stacked is the one place this screen had a register of its
	// own.
	d.lead(fmt.Sprintf("candidate #%d · %s", it.Cand.ID,
		fmt.Sprintf("train %s · %d of %d pass", pct(it.Train.Train.Passed, it.Train.Train.Judged),
			it.Train.Train.Passed, it.Train.Train.Judged)))
	d.lead("A query that fails here is evidence about the description, not a query to reword. Validation is not shown: it judges the revision, so it must stay unread.")
	for _, row := range it.Cand.FailedTrain() {
		d.card(fmt.Sprintf("q%d · %s · fired %d of %d", row.ID,
			labelWord(row.ShouldTrigger), row.Triggers, row.Runs))
		d.fact("the ask", clipText(row.Query, max(40, m.width-20)))
		if row.Why != "" {
			d.fact("why it is here", clipText(row.Why, max(40, m.width-20)))
		}
		d.fact("what loaded", loadedInstead(row))
		d.fact("verdict", verdictOf(row))
	}
	m.doc = &docState{scope: "failures", title: "train failures", lines: d.lines}
	m.backTo = screenFlow
	m.mode = screenDoc
	return nil
}

// loadedInstead says what the agent reached for when the row failed: the other skills that loaded,
// or nothing at all when the wording did not reach anything.
func loadedInstead(row runner.ScoreRow) string {
	var other []string
	for _, set := range row.LoadedSets {
		other = append(other, set...)
	}
	other = append(other, row.Others...)
	if len(other) == 0 {
		return "nothing — the wording did not reach it"
	}
	return strings.Join(uniq(other), ", ")
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// verdictOf is the one sentence a failing row earns: which way the wording is wrong. It is a reading
// of the run, not a recommendation — the fix is always the description.
func verdictOf(row runner.ScoreRow) string {
	switch {
	case row.ShouldTrigger && row.Triggers == 0:
		return "too NARROW — this is the skill's job, and the wording never names that context"
	case !row.ShouldTrigger && row.Triggers > 0:
		return "too BROAD — it fired on something that is not this skill's work"
	case row.ShouldTrigger:
		return fmt.Sprintf("unstable — fired in %d of %d runs", row.Triggers, row.Runs)
	default:
		return fmt.Sprintf("unstable — failed to hold in %d of %d runs", row.Runs-row.Triggers, row.Runs)
	}
}

// idList and idListOf print a set of failing rows as ids: the pane has room for the list, not for the
// questions.
func idList(rows []runner.ScoreRow) string {
	if len(rows) == 0 {
		return "none"
	}
	var out []string
	for _, r := range rows {
		out = append(out, fmt.Sprintf("q%d", r.ID))
	}
	return strings.Join(out, " ")
}

func idListOf(ids []int) string {
	if len(ids) == 0 {
		return "none"
	}
	var out []string
	for _, id := range ids {
		out = append(out, fmt.Sprintf("q%d", id))
	}
	return strings.Join(out, " ")
}

// shortWhen is a run's start as a clock: the pane says when, not which timezone.
func shortWhen(s string) string {
	if len(s) >= 16 {
		return s[11:16]
	}
	return s
}

// needsValidation counts the measured iterations whose validation run has not happened: the work the
// validate step has to do before it can rank anything.
func (m *Model) needsValidation() int {
	if m.flow == nil {
		return 0
	}
	n := 0
	for _, it := range m.flow.iters {
		if it.Valid == nil {
			n++
		}
	}
	return n
}
