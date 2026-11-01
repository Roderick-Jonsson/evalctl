package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
	return m.splitBody(h, listW, paneW, m.laneList(), m.lanePreview(paneW, h))
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
		d.card("The two activities")
		d.fact("iteration", "the train set: run it, read the failures, revise the wording")
		d.fact("generalization", "the validation set: run every wording against it, then rank")
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
	gated  bool          // five iterations and validation not yet run
	onDisk bool          // has a description and a ledger file
	issues []store.Issue // what validation says about this skill's rows
	// The preview needs the set's own arithmetic: both labels in both splits, the holdout's size,
	// how many wordings the ledger holds and which one SKILL.md was given.
	trainYes, trainNo int
	validYes, validNo int
	holdout           int
	cands             int
	applied           int
	chars             int
	sec               int // the section the loop opens in
	step              int // the step inside it
}

func (m *Model) skillNames() []string { return m.store.KnownSkills() }

// openSkill enters one skill's loop at the section the disk says the work is in, and never at the
// sake of starting there: the loop is resumable, so the tool reads where the work stopped.
func (m *Model) openSkill(name string) {
	if m.store.Skill(name) == nil {
		m.setStatus("unknown skill "+name, true)
		return
	}
	m.skill = name
	m.flow = &flowState{sec: secIteration}
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
	// The skill opens on the choice between its two activities, with the cursor on the one the disk
	// says the work is waiting in.
	sec, _ := loopOpen(m.flow.lab)
	m.sections = sectionState{cursor: sec}
	m.mode = screenSections
}

// loadSkills fills the list. Each row needs the loop's own state, so this walks every skill's
// ledger: four skills, one read each, and no measurement is ever recomputed by it.
func (m *Model) loadSkills() {
	m.skills.rows = nil
	r := runner.New(m.store)
	// One validation pass for the whole list, grouped by skill: the block under the table is the
	// only place a finding is shown, and it must show the same findings the runner would act on.
	bySkill := map[string][]store.Issue{}
	for _, issue := range m.store.Validate() {
		bySkill[issue.Skill] = append(bySkill[issue.Skill], issue)
	}
	for _, name := range m.skillNames() {
		row := skillRow{name: name, issues: bySkill[name]}
		if lab, err := r.DescriptionLab(name); err == nil {
			row.onDisk = true
			row.iters = len(iterationsOf(lab))
			row.train = lab.TrainRows
			row.valid = lab.ValidationRows
			row.holdout = lab.HoldoutRows
			row.gated = row.iters >= iterationsRequired && lab.Applied == nil
			row.state = loopState(lab, row.iters)
			row.cands = len(lab.Candidates)
			row.chars = lab.FileChars
			if lab.Applied != nil {
				row.applied = lab.Applied.Candidate
			}
			row.sec, row.step = loopOpen(lab)
		} else if m.store.Skill(name) == nil {
			// A name the trigger set still mentions and the tree no longer has: it stays in the
			// list, because its rows are still editable, and the state column says why it has
			// nothing to show.
			row.state = "not on disk"
		}
		for _, e := range m.store.Rows(store.LaneTrigger, store.Filter{Skill: name, Split: store.SplitTrain}) {
			if e.Label == "yes" {
				row.trainYes++
			} else {
				row.trainNo++
			}
		}
		for _, e := range m.store.Rows(store.LaneTrigger, store.Filter{Skill: name, Split: store.SplitValidation}) {
			if e.Label == "yes" {
				row.validYes++
			} else {
				row.validNo++
			}
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
		return "no train queries — iteration"
	case lab.ValidationRows == 0:
		return "no validation queries — generalization"
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

// The chooser: the eval tables' own shape, with the door's pane beside it.
//
// The table keeps the properties that made the old ones readable — a two-cell gutter where the cursor
// lives, fixed columns, counts right-aligned so 1 and 47 end on one edge, a quiet header — and the
// pane says what the row under the cursor is: where its loop opens, what its two splits hold, how many
// wordings the ledger carries and what that skill's validation findings are. Same shape as the door,
// so there is one layout to learn, not two.
const (
	skillNameW  = 20
	skillTrainW = 5
	skillValidW = 5
	skillItersW = 7
)

// skillTableW is the table's width with its state column dropped: the pane carries that, and a table
// that keeps it would spend the pane's room on the same words twice.
const skillTableW = 2 + skillNameW + 1 + skillTrainW + 1 + skillValidW + 1 + skillItersW

func (m *Model) skillBody(h int) []string {
	if len(m.skills.rows) == 0 {
		m.loadSkills()
	}
	if paneW := min(96, m.width-skillTableW-2); paneW >= 44 {
		return m.skillSplit(h, paneW)
	}
	return m.skillStacked(h)
}

// skillSplit is the table on the left and the row under the cursor on the right.
func (m *Model) skillSplit(h, paneW int) []string {
	left := []string{"", m.skillHead(false)}
	room := max(1, h-len(left))
	m.clampSkills(room)
	for i := m.skills.top; i < len(m.skills.rows) && i < m.skills.top+room; i++ {
		left = append(left, m.skillTableRow(i, false))
	}
	return m.splitBody(h, skillTableW, paneW, left, m.skillPane(paneW, h))
}

// skillStacked is the chooser without a pane: the whole table, and the row under the cursor spelled
// out in a block below it, where the emptiness under four rows is.
func (m *Model) skillStacked(h int) []string {
	withState := m.width >= 72
	lines := []string{"", m.skillHead(withState)}

	// The block below the list is worth its rows only when the whole list fits above it; with more
	// skills than window, the list wins — the list is what you are choosing from.
	const blockRows = 6
	avail := h - len(lines)
	showBlock := len(m.skills.rows)+len(lines)+blockRows <= h
	if showBlock {
		avail = h - len(lines) - blockRows
	}
	room := max(1, min(len(m.skills.rows), avail))
	m.clampSkills(room)
	for i := m.skills.top; i < len(m.skills.rows) && i < m.skills.top+room; i++ {
		lines = append(lines, m.skillTableRow(i, withState))
	}
	if showBlock {
		lines = append(lines, "")
		lines = append(lines, m.skillBlock()...)
	}
	return fitRows(lines, h)
}

// clampSkills keeps the cursor inside the rows that are actually drawn.
func (m *Model) clampSkills(room int) {
	m.skills.cursor = min(m.skills.cursor, max(0, len(m.skills.rows)-1))
	if m.skills.cursor < m.skills.top {
		m.skills.top = m.skills.cursor
	}
	if m.skills.cursor >= m.skills.top+room {
		m.skills.top = m.skills.cursor - room + 1
	}
	if m.skills.top < 0 {
		m.skills.top = 0
	}
}

// skillHead is the table's header: quiet, right-aligned where the cells are numbers, and with no rule
// under it — the rule belongs to the bar, and a second one over four rows reads as a second bar.
func (m *Model) skillHead(withState bool) string {
	head := "  " + dimStyle.Render(padCell("SKILL", skillNameW, false)) + " " +
		dimStyle.Render(padCell("TRAIN", skillTrainW, true)) + " " +
		dimStyle.Render(padCell("VALID", skillValidW, true)) + " " +
		dimStyle.Render(padCell("ITERS", skillItersW, true))
	if withState {
		head += "  " + dimStyle.Render("STATE")
	}
	return head
}

// skillTableRow is one skill: the mark, the name, the three counts and — when the table has room —
// where the loop stands.
func (m *Model) skillTableRow(i int, withState bool) string {
	r := m.skills.rows[i]
	mark := "  "
	if i == m.skills.cursor {
		mark = cursorMark.Render(glyphs.Cursor)
	}
	line := mark + colSkill.Render(padCell(r.name, skillNameW, false)) + " " +
		colText.Render(padCell(fmt.Sprint(r.train), skillTrainW, true)) + " " +
		colText.Render(padCell(fmt.Sprint(r.valid), skillValidW, true)) + " " +
		colSplit.Render(padCell(fmt.Sprint(r.iters)+"/"+fmt.Sprint(iterationsRequired), skillItersW, true))
	if withState {
		state := dimStyle.Render(r.state)
		if r.gated {
			state = styleOn.Render(r.state)
		}
		line += "  " + state
	}
	if i == m.skills.cursor {
		line = selected(line)
	}
	return line
}

// skillPane is the row under the cursor, said properly, in the door's pane shape: where its loop
// opens, what its two splits hold, what the ledger carries, and what validation says about it. The
// action line is reserved before the blocks are placed, so a short window drops the ledger before it
// drops the sentence telling you what the key does.
func (m *Model) skillPane(w, h int) []string {
	r := m.skills.rows[m.skills.cursor]
	d := &docWriter{w: w, label: 12}
	var blocks [][]string
	from := len(d.lines)
	block := func() {
		blocks = append(blocks, append([]string(nil), d.lines[from:]...))
		from = len(d.lines)
	}

	d.title(strings.ToUpper(r.name))
	d.lead(fmt.Sprintf("%s · step %d of %d, %s", secNames[r.sec], r.step, len(stepsOf(r.sec)), stepNameAt(r.sec, r.step)))
	d.rule()
	block()

	d.para(stateWord(r))
	block()

	d.card("The set")
	d.fact("train", fmt.Sprintf("%s · %d trigger · %d do not", pluralOf(r.train, "query", "queries"), r.trainYes, r.trainNo))
	d.fact("validation", fmt.Sprintf("%s · %d trigger · %d do not", pluralOf(r.valid, "query", "queries"), r.validYes, r.validNo))
	d.fact("holdout", pluralOf(r.holdout, "query", "queries"))
	block()

	d.card("The ledger")
	d.fact("wordings", plural(r.cands, "candidate"))
	applied := "none yet"
	if r.applied > 0 {
		applied = fmt.Sprintf("#%d", r.applied)
	}
	d.fact("applied", applied)
	d.fact("in SKILL.md", plural(r.chars, "character"))
	block()

	if len(r.issues) > 0 {
		d.card("Findings")
		for i, issue := range r.issues {
			if i == 2 {
				d.fact("more", plural(len(r.issues)-2, "finding"))
				break
			}
			d.fact(severityWord(issue.Severity), issue.Msg)
		}
		block()
	}

	actDoc := &docWriter{w: w}
	actDoc.action("enter", fmt.Sprintf("open at %s · %s", secNames[r.sec], stepNameAt(r.sec, r.step)))
	room := h - len(actDoc.lines)
	var out []string
	for _, b := range blocks {
		if len(out)+len(b) > room {
			break
		}
		out = append(out, b...)
	}
	return fitRows(append(out, actDoc.lines...), h)
}

// severityWord is the finding's weight in a word: the fact column is too narrow for the store's own
// spelling of it, and "warn" is a verb before it is a severity.
func severityWord(s store.Severity) string {
	if s == store.SeverityError {
		return "error"
	}
	return "warning"
}

// stateWord is where the loop stands with this skill, in the case the STATE column is not there.
func stateWord(r skillRow) string {
	if r.gated {
		return "ready to generalize"
	}
	return r.state
}

// skillBlock is the narrow chooser's block: the row under the cursor said out loud, under the table.
func (m *Model) skillBlock() []string {
	if len(m.skills.rows) == 0 {
		return nil
	}
	r := m.skills.rows[m.skills.cursor]
	rule := func(n int) string { return dimStyle.Render(strings.Repeat(glyphs.Rule, max(4, n))) }
	lines := []string{
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, 2)) + " " + titleStyle.Render(r.name) + " " +
			rule(min(64, max(8, m.width-8-lipgloss.Width(r.name)))),
	}
	facts := strings.Join([]string{
		pluralOf(r.train, "train query", "train queries"),
		pluralOf(r.valid, "validation query", "validation queries"),
		fmt.Sprintf("%d of %d iterations measured", r.iters, iterationsRequired),
	}, " · ")
	for _, line := range wrapPlain(facts, max(20, m.width-4)) {
		lines = append(lines, "  "+dimStyle.Render(line))
	}
	stateStyle := dimStyle
	if r.gated {
		stateStyle = styleOn
	}
	for _, line := range wrapPlain(stateWord(r), max(20, m.width-4)) {
		lines = append(lines, "  "+stateStyle.Render(line))
	}
	for i, issue := range r.issues {
		if i == 2 {
			lines = append(lines, "  "+dimStyle.Render("and "+plural(len(r.issues)-2, "more finding")))
			break
		}
		mark := badgeWarn.Render("!")
		if issue.Severity == store.SeverityError {
			mark = statusBad.Render(glyphs.Failed)
		}
		lines = append(lines, "  "+mark+" "+dimStyle.Render(clipText(issue.Msg, max(20, m.width-6))))
	}
	if len(r.issues) == 0 {
		lines = append(lines, "  "+styleOn.Render(glyphs.Done+" no findings"))
	}
	return lines
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
