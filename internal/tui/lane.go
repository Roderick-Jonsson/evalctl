package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// The front door, and the skill behind it.
//
// There are two things a skill can be optimized for — whether it triggers, and whether its output is
// right — so the first screen is that choice and nothing else. The second screen is which skill,
// with where each one stands in the loop, because resuming is the common case: you come back to a
// skill you left at iteration three, not to a cold start.

// laneState is the front door's cursor.
type laneState struct {
	cursor int
}

// The two lanes the door offers. Output is named and described, and says plainly that it is not
// built: a menu row that opens a "coming soon" screen is a menu row that wastes a keystroke.
var laneRows = []struct {
	name, title, blurb string
	built              bool
}{
	{store.LaneTrigger, "trigger optimization", "does the right skill load? — tune the description until it does", true},
	{store.LaneOutput, "output optimization", "does the skill produce the right thing? — not built yet", false},
}

// The door: two selectors on the left, the one under the cursor explained on the right.
//
// It used to be two rows with a paragraph under each and, below them, the loop described again in
// four more lines — three blocks of prose competing for one screen, and the loop explained twice. A
// selector wants a list small enough to see whole, and a pane that answers "what is this" for the row
// you are on; that is the shape every other screen here uses, something on the left and its detail on
// the right.
func (m *Model) laneBody(h int) []string {
	// The list takes a third of the width, the pane takes the rest up to a readable measure. A list
	// wide enough for "output optimization  not built" and no wider: the pane is where the detail is,
	// and a pane squeezed to pay for empty list cells explains nothing.
	listW := min(36, max(26, m.width/3))
	paneW := min(96, m.width-listW-3)
	if paneW < 38 {
		return m.laneNarrow(h)
	}
	left, right := m.laneList(), m.lanePreview(paneW, h)
	seam := dimStyle.Render(glyphs.Seam)
	out := make([]string, h)
	for i := range out {
		var l, r string
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out[i] = fitTo(l, listW) + " " + seam + " " + fitWidth(r, paneW)
	}
	return out
}

// laneList is the whole choice: two rows, the one under the cursor marked, and whether the lane is
// built. Nothing else belongs here — a description under a row is what the pane is for.
func (m *Model) laneList() []string {
	lines := []string{"", ""}
	for i, row := range laneRows {
		mark := "  "
		if i == m.lanes.cursor {
			mark = cursorMark.Render(glyphs.Cursor)
		}
		state := styleOn.Render("built")
		if !row.built {
			state = dimStyle.Render("not built")
		}
		lines = append(lines, mark+titleStyle.Render(row.title)+"  "+state, "")
	}
	return lines
}

// lanePreview is the row under the cursor, said properly: what the lane asks, what it does, and what
// it leaves on disk. The words are the guide's, not a summary of the screen behind it.
//
// It is assembled out of blocks and laid out from the top down, with the action line reserved first:
// the pane is a column of a known height, and the last thing on it must be the key you press, not a
// sentence cut in half by the frame. A block that does not fit is dropped whole — the run's paths go
// before the loop does.
func (m *Model) lanePreview(w, h int) []string {
	row := laneRows[m.lanes.cursor]
	d := &docWriter{w: w, label: 14}
	var blocks [][]string
	from := len(d.lines)
	block := func() {
		blocks = append(blocks, append([]string(nil), d.lines[from:]...))
		from = len(d.lines)
	}

	d.title(strings.ToUpper(row.title))
	if row.built {
		d.lead("built · the description loop")
	} else {
		d.lead("not built yet")
	}
	d.rule()
	block()

	if row.built {
		d.para("A skill only helps if it gets loaded, and the description is the whole of the input — an agent decides from that one paragraph. This lane measures the wording, revises it on what the train set failed, and generalizes on a split no revision could read.")
		block()
		d.card("The loop")
		d.fact("1 train set", "the queries the wording may learn from")
		d.fact("2 validation", "the queries that judge it, held back until the end")
		d.fact("3 iterate", "run, read the failures, revise, repeat")
		d.fact("4 generalize", "validation for every measured wording, then rank")
		block()
		d.card("It writes")
		d.fact("the set", "trigger/trigger-evals.json")
		d.fact("wordings", "trigger/descriptions/<skill>.json")
		d.fact("the winner", "SKILL.md — the frontmatter key, nothing else")
		block()
	} else {
		d.para("The other half of the question: with the skill loaded, does the work come out right? Each case runs twice — with the skill and without it — and what came back is read against the case's expected output and its assertions.")
		block()
		d.card("What it will hold")
		d.fact("cases", "the ask, and the fixture each one stages")
		d.fact("assertions", "the checks a run is graded on")
		d.fact("fixtures", "evals/files/<slug>/ — the inputs, never the answers")
		d.fact("iterations", "the runs, their reports and their scores")
		block()
		d.para("Not built: the trigger loop came first, because a skill that never loads produces nothing worth grading.")
		block()
	}

	// The action, reserved before anything above it is placed.
	actDoc := &docWriter{w: w}
	if row.built {
		actDoc.action("enter", "pick the skill whose wording gets tuned")
	} else {
		actDoc.blank()
		for _, line := range wrapPlain("not available yet — the trigger loop is the one that is built", actDoc.body()) {
			actDoc.lines = append(actDoc.lines, "  "+dimStyle.Render(line))
		}
	}
	act := actDoc.lines
	room := h - len(act)
	var out []string
	for _, b := range blocks {
		if len(out)+len(b) > room {
			break
		}
		out = append(out, b...)
	}
	return fitRows(append(out, act...), h)
}

// laneNarrow is the door without its pane. Below the width that buys one, the row under the cursor
// carries its one-line explanation and nothing is hidden behind a divider that is not there.
func (m *Model) laneNarrow(h int) []string {
	lines := []string{""}
	for i, row := range laneRows {
		mark := "  "
		if i == m.lanes.cursor {
			mark = cursorMark.Render(glyphs.Cursor)
		}
		state := "built"
		if !row.built {
			state = "not built"
		}
		lines = append(lines, mark+titleStyle.Render(row.title)+"  "+dimStyle.Render(state))
		if i == m.lanes.cursor {
			for _, l := range wrapPlain(row.blurb, max(20, m.width-8)) {
				lines = append(lines, "    "+dimStyle.Render(l))
			}
		}
		lines = append(lines, "")
	}
	return fitRows(lines, h)
}

func (m *Model) updateLane(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "j", "down":
		m.lanes.cursor = min(m.lanes.cursor+1, len(laneRows)-1)
	case "k", "up":
		m.lanes.cursor = max(0, m.lanes.cursor-1)
	case "g", "home":
		m.lanes.cursor = 0
	case "G", "end":
		m.lanes.cursor = len(laneRows) - 1
	case "enter":
		if !laneRows[m.lanes.cursor].built {
			m.setStatus("output optimization is not built yet — the trigger loop comes first", false)
			return nil
		}
		m.mode = screenSkill
		m.skills = skillState{}
	}
	return nil
}

// ---------------------------------------------------------------- the skills

// skillState is the skill list's cursor and the rows it draws. The rows are recomputed on entry and
// not cached: a loop that moved on disk in another terminal must not be described from memory.
type skillState struct {
	cursor int
	top    int
	rows   []skillRow
}

// skillRow is one skill and where the loop stands with it.
type skillRow struct {
	name   string
	train  int
	valid  int
	iters  int
	state  string
	gated  bool // five iterations and validation not yet run
	onDisk bool // has a description and a ledger file
}

func (m *Model) skillNames() []string { return m.store.KnownSkills() }

// openSkill enters one skill's loop at the step the disk says it is on, and never at step 1 for the
// sake of starting there: the loop is resumable, so the tool reads where the work stopped.
func (m *Model) openSkill(name string) {
	if m.store.Skill(name) == nil {
		m.setStatus("unknown skill "+name, true)
		return
	}
	m.skill = name
	m.flow = &flowState{step: StepTrain}
	m.reload()
	// A skill with no ledger yet starts the loop from the wording SKILL.md already carries, copied
	// verbatim and recorded as candidate #1. Retyping your own description is the one step of the
	// guide a tool can do for you, and a paraphrase would measure a text nobody ships.
	if len(m.flow.lab.Candidates) == 0 {
		if _, err := runner.New(m.store).AdoptFileDescription(name); err != nil {
			m.setStatus(err.Error(), true)
		} else {
			m.reload()
			m.setStatus("candidate #1 adopted from "+name+"/SKILL.md", false)
		}
	}
	m.flow.resume(m)
	m.mode = screenFlow
}

// loadSkills fills the list. Each row needs the loop's own state, so this walks every skill's
// ledger: four skills, one read each, and no measurement is ever recomputed by it.
func (m *Model) loadSkills() {
	m.skills.rows = nil
	r := runner.New(m.store)
	for _, name := range m.skillNames() {
		row := skillRow{name: name}
		if lab, err := r.DescriptionLab(name); err == nil {
			row.onDisk = true
			row.iters = len(iterationsOf(lab))
			row.train = lab.TrainRows
			row.valid = lab.ValidationRows
			row.gated = row.iters >= iterationsRequired && lab.Applied == nil
			row.state = loopState(lab, row.iters)
		}
		m.skills.rows = append(m.skills.rows, row)
	}
	m.skills.cursor = min(m.skills.cursor, max(0, len(m.skills.rows)-1))
}

// loopState is one phrase per skill: what the loop would do next with it.
func loopState(lab *runner.Lab, iters int) string {
	switch {
	case lab == nil:
		return "—"
	case lab.TrainRows == 0:
		return "no train queries — step 1"
	case lab.ValidationRows == 0:
		return "no validation queries — step 2"
	case iters == 0:
		return "set ready — nothing measured yet"
	case lab.Applied != nil && noCandidateLeft(lab):
		return "done · #" + fmt.Sprint(lab.Applied.Candidate) + " applied"
	case allValidated(lab):
		return fmt.Sprintf("%d of %d iterations · validation measured", iters, iterationsRequired)
	case iters < iterationsRequired:
		return fmt.Sprintf("%d of %d iterations · keep iterating", iters, iterationsRequired)
	}
	return fmt.Sprintf("%d of %d iterations · ready to generalize", iters, iterationsRequired)
}

func (m *Model) skillBody(h int) []string {
	if len(m.skills.rows) == 0 {
		m.loadSkills()
	}
	lines := []string{
		"",
		"  " + headerFocus.Render(fitTo("SKILL", 20)) + "  " +
			headerFocus.Render(fitTo("TRAIN", 5)) + "  " + headerFocus.Render(fitTo("VALID", 5)) + "  " +
			headerFocus.Render(fitTo("ITERS", 7)) + "  " + headerFocus.Render("STATE"),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(88, m.width-4)))),
	}
	const head = 3
	room := max(1, h-head-2)
	top := m.skills.top
	if m.skills.cursor < top {
		top = m.skills.cursor
	}
	if m.skills.cursor >= top+room {
		top = m.skills.cursor - room + 1
	}
	m.skills.top = top
	for i := top; i < len(m.skills.rows) && len(lines) < head+room; i++ {
		r := m.skills.rows[i]
		mark := "  "
		if i == m.skills.cursor {
			mark = cursorMark.Render(glyphs.Cursor)
		}
		state := dimStyle.Render(r.state)
		if r.gated {
			state = styleOn.Render(r.state)
		}
		line := mark + colSkill.Render(fitTo(r.name, 20)) + "  " +
			colText.Render(fitTo(fmt.Sprint(r.train), 5)) + "  " +
			colText.Render(fitTo(fmt.Sprint(r.valid), 5)) + "  " +
			colSplit.Render(fitTo(fmt.Sprint(r.iters)+"/"+fmt.Sprint(iterationsRequired), 7)) + "  " + state
		if i == m.skills.cursor {
			line = selected(line)
		}
		lines = append(lines, line)
	}
	return fitRows(lines, h)
}

func (m *Model) updateSkills(msg tea.KeyMsg) tea.Cmd {
	if len(m.skills.rows) == 0 {
		m.loadSkills()
	}
	switch msg.String() {
	case "j", "down":
		m.skills.cursor = min(m.skills.cursor+1, len(m.skills.rows)-1)
	case "k", "up":
		m.skills.cursor = max(0, m.skills.cursor-1)
	case "g", "home":
		m.skills.cursor = 0
	case "G", "end":
		m.skills.cursor = len(m.skills.rows) - 1
	case "enter":
		if len(m.skills.rows) == 0 {
			return nil
		}
		m.openSkill(m.skills.rows[m.skills.cursor].name)
	}
	return nil
}
