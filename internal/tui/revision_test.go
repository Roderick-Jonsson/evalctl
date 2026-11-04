package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"evalctl/internal/diff"
	"evalctl/internal/runner"
)

// proposal opens the proposal screen on a fixture whose skill is known, so the diff is the test's own.
func proposal(t *testing.T, edit func(string) string) (*Model, string, string) {
	t.Helper()
	m, root := qualityFixture(t)
	m.openQualitySkill("dummy")
	before, err := os.ReadFile(filepath.Join(root, "dummy", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	rev := runner.Revision{Skill: "dummy", Iteration: 1, N: 1,
		Why:      "the sum is stated, never shown — the reply now works it out",
		Baseline: string(before), Text: edit(string(before)),
		Path: filepath.Join(root, "dummy-workspace", "iteration-1", "revision-1.json")}
	m.openProposal(rev, runner.RevisionInputs{Failed: 1, Feedbacks: 1, Passed: 1})
	return m, root, string(before)
}

// TestProposalIsADiffWithOneKeyThatWrites: the screen's job is the diff — the reviser's own why is
// the least reliable thing it produces — and `a` is the only key that touches SKILL.md.
func TestProposalIsADiffWithOneKeyThatWrites(t *testing.T) {
	m, root, before := proposal(t, func(s string) string {
		return strings.Replace(s, "Roll them.", "Roll them and show the sum.", 1)
	})
	if m.mode != screenRevision {
		t.Fatalf("mode = %v, want the proposal screen", m.mode)
	}
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	for _, want := range []string{"PROPOSED REVISION · SKILL.md", "+1 −1 · 1 hunk", "why", "the diff",
		"@@ ", "[x]", "+1 −1", "+ Roll them and show the sum", "apply 1 hunk to SKILL.md"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the proposal is missing %q:\n%s", want, body)
		}
	}
	// Esc leaves the file alone, and the screen says so.
	press(m, "esc")
	if m.mode != screenQuality {
		t.Fatalf("esc from the proposal = %v, want the dojo", m.mode)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "dummy", "SKILL.md")); string(got) != before {
		t.Fatal("esc wrote SKILL.md")
	}
	// And `a` writes, from the same state.
	m.openProposal(m.revisionProposalFor(t, root, before), runner.RevisionInputs{})
	press(m, "a")
	after, err := os.ReadFile(filepath.Join(root, "dummy", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "show the sum") {
		t.Fatalf("a did not apply the proposal:\n%s", after)
	}
	if m.mode != screenQuality {
		t.Fatalf("after applying, mode = %v, want the dojo", m.mode)
	}
	if m.revision != nil {
		t.Fatal("the proposal stayed open after it was applied")
	}
}

// revisionProposalFor rebuilds the proposal the test applied, against the text as it still is.
func (m *Model) revisionProposalFor(t *testing.T, root, before string) runner.Revision {
	t.Helper()
	return runner.Revision{Skill: "dummy", Iteration: 1, N: 1, Baseline: before,
		Text: strings.Replace(before, "Roll them.", "Roll them and show the sum.", 1),
		Path: filepath.Join(root, "dummy-workspace", "iteration-1", "revision-1.json")}
}

// TestAFinishedCallLandsOnTheProposal: the wiring from the call to the screen, without spending one —
// the proposal is read back from disk, which is what lets it survive the call and the process.
func TestAFinishedCallLandsOnTheProposal(t *testing.T) {
	m, root := qualityFixture(t)
	m.openQualitySkill("dummy")
	before, err := os.ReadFile(filepath.Join(root, "dummy", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	rev := runner.Revision{Skill: "dummy", Iteration: 1, N: 1, Why: "show the sum",
		Baseline: string(before), Text: strings.Replace(string(before), "Roll them.", "Roll them and show the sum.", 1),
		Path: filepath.Join(root, "dummy-workspace", "iteration-1", "revision-1.json")}
	writeJSON(t, rev.Path, rev)
	m.run = &runState{kind: runRevise, scope: "revision · dummy@1", hint: ""}
	m.run.live = newLiveRun("revision · dummy@1", false)
	m.mode = screenRunLive
	m.finishRun()
	if m.mode != screenRevision || m.revision == nil {
		t.Fatalf("a finished revision call landed on mode %v (revision %v), want the proposal", m.mode, m.revision)
	}
	if m.run != nil {
		t.Fatal("the run state outlived the call")
	}
}

// TestAHunkCanBeTickedOff: the diff is cut into hunks so the operator can take the arithmetic and
// refuse the terseness rule — a single "apply everything" is a decision nobody can make on a skill.
func TestAHunkCanBeTickedOff(t *testing.T) {
	m, root := qualityFixture(t)
	// A skill long enough for two changes to be two neighbourhoods rather than one.
	body := "---\nname: dummy\ndescription: Roll dice.\n---\n\n" +
		"## Procedure\n\n1. Read the count.\n2. Roll them.\n3. Report the faces.\n" +
		"\n## Pitfalls\n\n- Inventing the faces.\n- Doing anything past the throw.\n" +
		"\n## Verification\n\n- One face per die.\n- No file was created.\n"
	write(t, filepath.Join(root, "dummy", "SKILL.md"), body)
	m.openQualitySkill("dummy")
	edit := strings.Replace(body, "3. Report the faces.", "3. Report the faces and the addition: 4 + 5 = 9.", 1)
	edit = strings.Replace(edit, "- No file was created.", "- No file was created, and no other die was offered.", 1)
	m.openProposal(runner.Revision{Skill: "dummy", Iteration: 1, N: 1, Baseline: body, Text: edit,
		Path: filepath.Join(root, "dummy-workspace", "iteration-1", "revision-1.json")},
		runner.RevisionInputs{Failed: 1})
	if n := len(m.revision.hunks); n != 2 {
		t.Fatalf("hunks = %d, want the two edits as two neighbourhoods", n)
	}
	// The cursor opens on the first hunk; n walks to the second, space refuses it.
	press(m, "n")
	if m.revision.cursor < 0 {
		t.Fatal("the cursor left the rows")
	}
	press(m, " ")
	if m.revision.selectedCount() != 1 {
		t.Fatalf("selected = %d of 2 after space", m.revision.selectedCount())
	}
	view := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	if !strings.Contains(view, "[ ]") || !strings.Contains(view, "1 of 2 kept") {
		t.Fatalf("the screen does not show the hunk as refused:\n%s", view)
	}
	press(m, "a")
	after, err := os.ReadFile(filepath.Join(root, "dummy", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "no other die was offered") {
		t.Fatalf("the refused hunk was written:\n%s", after)
	}
	if !strings.Contains(string(after), "and the addition") {
		t.Fatalf("the kept hunk was not written:\n%s", after)
	}
}

// TestProposalThatChangesNothingIsDiscarded: a reviser that answers with the same file is a fact the
// screen states, and there is nothing to apply.
func TestProposalThatChangesNothingIsDiscarded(t *testing.T) {
	m, _, _ := proposal(t, func(s string) string { return s })
	body := stripMarks(strings.Join(m.bodyLines(m.bodyHeight()), "\n"))
	if !strings.Contains(body, "no change") || !strings.Contains(body, "discard — this proposal changes nothing") {
		t.Fatalf("an unchanged proposal is not drawn as one:\n%s", body)
	}
	if diff.Changed(m.revision.lines) {
		t.Fatal("an unchanged proposal reports a change")
	}
}

// TestLineDiffIsAnEditScript: the screen and `evalctl revise` share this, so its shape is pinned.
func TestLineDiffIsAnEditScript(t *testing.T) {
	lines := diff.Lines("a\nb\nc\n", "a\nB\nc\n")
	var got []string
	for _, l := range lines {
		switch l.Kind {
		case diff.Add:
			got = append(got, "+"+l.Text)
		case diff.Del:
			got = append(got, "-"+l.Text)
		default:
			got = append(got, " "+l.Text)
		}
	}
	want := " a -b +B  c"
	if strings.Join(got, " ") != want {
		t.Fatalf("diff = %q, want %q", strings.Join(got, " "), want)
	}
	if added, removed := diff.Stat(lines); added != 1 || removed != 1 {
		t.Fatalf("stat = +%d −%d, want +1 −1", added, removed)
	}
}
