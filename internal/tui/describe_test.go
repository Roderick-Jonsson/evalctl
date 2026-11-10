package tui

import (
	"strings"
	"testing"

	"evalctl/internal/runner"
)

// The description pane's second state: what a proposal does to the screen, to the field, and to the
// keys. The provider call itself cannot be driven from here — it is an HTTP request and it has its
// own tests in internal/runner — so these start from a proposal already on the model, which is the
// state `finishRun` builds after the call comes back.

// descriptionProposal is a proposal as the run leaves it: the wording the failures measured, and the
// paragraph the model wrote from them.
func descriptionProposal(m *Model) runner.DescriptionProposal {
	it := m.flow.latest()
	base := it.Cand.Text
	text := strings.Replace(base, "an outline has to become slides",
		"an outline, a deck or a PO revision has to become slides", 1)
	return runner.DescriptionProposal{
		Skill: "slide-builder", Candidate: it.Cand.ID, Iteration: it.Train.Iteration,
		Baseline: base, Text: text, Why: "named the words the user actually says",
		Chars: len([]rune(text)), Model: "test-model",
	}
}

func proposalView(t *testing.T) (*Model, string) {
	t.Helper()
	m, _ := measured(t, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", true, false),
		row(3, "validation", true, true),
	})
	enterSection(m, secIteration)
	m.openDescriptionProposal(descriptionProposal(m))
	return m, stripMarks(m.View())
}

// The pane swaps what it draws, not where it draws it: the description stays in the bottom left of
// the workbench, with the failures that were used to write it still on screen above.
func TestAProposalIsDrawnInTheDescriptionPane(t *testing.T) {
	m, view := proposalView(t)
	for _, want := range []string{"THE DESCRIPTION", "FAILED QUERY", "TRAIN SET", "ITERATIONS"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the proposal lost the %q quadrant:\n%s", want, view)
		}
	}
	// The keys that decide it, and not the set's: a proposal is not the field and not the table.
	for _, want := range []string{"y keep", "p another", "esc drop"} {
		if !strings.Contains(strings.Join(strings.Fields(view), " "), want) {
			t.Errorf("the pane does not offer %q:\n%s", want, view)
		}
	}
	// The pane itself: the paragraph as it would read — the unchanged half of the sentence and the
	// words that arrived — the counts on the head, and the legend that says which colour is which.
	// Read at the pane's own width, because the frame puts a column seam between two wrapped rows.
	pane := strings.Join(strings.Fields(strings.Join(m.quadProposal(72, 16), " ")), " ")
	for _, want := range []string{"a PO revision has to become slides", "+26", "−1", "red gone, green new"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the diff does not show %q:\n%s", want, pane)
		}
	}
}

// y is the whole point: the paragraph becomes the draft, editable. Nothing is written — the save is
// still ctrl+s.
func TestKeepingAProposalPutsItInTheField(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{
		row(1, "train", true, true), row(2, "train", true, false),
	})
	enterSection(m, secIteration)
	p := descriptionProposal(m)
	m.openDescriptionProposal(p)
	before := m.flow.draft.text.Value()

	press(m, "y")
	if m.flow.proposal != nil {
		t.Fatal("y left the proposal on the pane")
	}
	if got := m.flow.draft.text.Value(); got != p.Text {
		t.Fatalf("the field holds %q, want the proposal", got)
	}
	if m.flow.focus != focusWording {
		t.Errorf("the caret should be in the description, focus = %d", m.flow.focus)
	}
	if !m.flow.draftChanged() {
		t.Error("a kept proposal is an unsaved edit; the screen must say so")
	}
	// The ledger is untouched: y is an edit, and ctrl+s is the save.
	if len(m.flow.lab.Candidates) != 1 {
		t.Errorf("keeping a proposal wrote a candidate: %d", len(m.flow.lab.Candidates))
	}
	if before == p.Text {
		t.Fatal("the fixture's proposal is not a change; the test would prove nothing")
	}
}

// esc drops it and leaves the field exactly as it was, and p puts the pane back to the field.
func TestDiscardingAProposalLeavesTheFieldAlone(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{row(1, "train", true, true), row(2, "train", true, false)})
	enterSection(m, secIteration)
	before := m.flow.draft.text.Value()
	m.openDescriptionProposal(descriptionProposal(m))

	press(m, "esc")
	if m.flow.proposal != nil {
		t.Fatal("esc did not drop the proposal")
	}
	if m.mode != screenFlow {
		t.Fatalf("esc left the workbench: mode = %d", m.mode)
	}
	if got := m.flow.draft.text.Value(); got != before {
		t.Errorf("the field changed: %q", got)
	}
	if view := stripMarks(m.View()); !strings.Contains(view, "THE DESCRIPTION") || strings.Contains(view, "keep it") {
		t.Errorf("the pane did not go back to the field:\n%s", view)
	}
}

// A proposal owns the keys: tab cannot put a caret into a text that is about to be replaced, and the
// set's letters must not walk a table the pane has covered.
func TestAProposalHoldsTheKeys(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{row(1, "train", true, true), row(2, "train", true, false)})
	enterSection(m, secIteration)
	m.openDescriptionProposal(descriptionProposal(m))

	press(m, "tab")
	if m.flow.focus != focusSet || m.flow.proposal == nil {
		t.Fatalf("tab escaped the proposal: focus = %d, proposal = %v", m.flow.focus, m.flow.proposal != nil)
	}
	if m.typing() {
		t.Error("the keys must not be going to a hidden field")
	}
	press(m, "j")
	if m.flow.proposal.top == 0 && m.flow.proposal.view > 0 && len(m.flow.proposal.segs) > 0 {
		// j scrolls the diff; on a paragraph that fits there is nothing to scroll, which is also fine.
		if len(m.flow.proposal.text) > 400 {
			t.Error("j did not scroll a long proposal")
		}
	}
}

// A workbench with nothing measured cannot ask for a proposal, and says which step is missing rather
// than spending a call to find out.
func TestAProposalNeedsAMeasuredWording(t *testing.T) {
	m, _ := measured(t, nil)
	enterSection(m, secIteration)
	press(m, "p")
	if m.mode != screenFlow || m.run != nil {
		t.Fatalf("p started something without a measurement: mode = %d", m.mode)
	}
	if !strings.Contains(strings.ToLower(m.status), "measured") && !strings.Contains(strings.ToLower(m.status), "failures") {
		t.Errorf("the refusal does not say what is missing: %q", m.status)
	}
}

// The run screen names the model and where the key came from before anything is spent — a key that
// resolves from the wrong file is the failure this line exists to make visible.
func TestTheProposalRunScreenNamesTheProvider(t *testing.T) {
	m, _ := measured(t, []runner.ScoreRow{row(1, "train", true, true), row(2, "train", true, false)})
	enterSection(m, secIteration)
	m.run = &runState{kind: runDescribe, cand: 1, scope: "description · candidate #1",
		hint: "one call to test-model · key from ~/.config/evalctl/env — the paragraph lands in the description pane"}
	m.run.live = newLiveRun(m.run.scope, false)
	m.mode = screenRunLive
	view := stripMarks(m.View())
	for _, want := range []string{"test-model", "~/.config/evalctl/env", "Nothing is written"} {
		if !strings.Contains(view, want) {
			t.Errorf("the run screen does not say %q:\n%s", want, view)
		}
	}
}
