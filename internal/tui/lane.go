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

func (m *Model) laneBody(h int) []string {
	lines := []string{""}
	for i, row := range laneRows {
		mark := "  "
		if i == m.lanes.cursor {
			mark = cursorMark.Render(glyphs.Cursor)
		}
		state := styleOn.Render("built")
		if !row.built {
			state = dimStyle.Render("not built")
		}
		lines = append(lines, mark+titleStyle.Render(row.title)+"  "+state)
		for _, line := range wrapPlain(row.blurb, max(20, m.width-10)) {
			lines = append(lines, "       "+dimStyle.Render(line))
		}
		lines = append(lines, "")
	}
	lines = append(lines, "",
		"  "+titleStyle.Render("the trigger loop, in four steps"),
		dimStyle.Render("    1 train set        the queries the wording may learn from"),
		dimStyle.Render("    2 validation set    the queries that judge it, held back until the end"),
		dimStyle.Render("    3 iterate           run the train set, read the failures, revise, repeat"),
		dimStyle.Render("    4 generalize        run the validation set for every iteration and compare"),
	)
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
