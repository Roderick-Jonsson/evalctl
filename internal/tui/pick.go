package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// The work list: one screen, and the only one that chooses.
//
// It replaces four screens — the door, the two skill choosers and the skill menu — with the same
// question asked once: what is there to do, and what does the disk say about it? A row is one
// lane of one skill, so a skill that is worked in both lanes is two rows: the lane is a badge on
// the row, not a screen you pass through first. The list is sorted by where the work is (a run
// that failed, then a loop that was never measured, then a set that is clean), because the common
// case is resuming and not starting.
//
// The right-hand pane is the row said properly: the numbers that decide it, the one errand the
// disk is waiting for, and the key that walks in. Nothing here is a level: enter goes straight to
// the work, and esc comes straight back.

type pickRow struct {
	skill string
	lane  string // store.LaneTrigger | store.LaneOutput
	badge string // "description" | "output"
	state string // one line, the numbers that decide it
	next  string // the one errand, in the lab's own words
	// urgent orders the list: 0 a run failed, 1 nothing done yet, 2 clean. The loop's own rule,
	// "read the failures first", applied to the list instead of to a screen.
	urgent int
	// action is the one key that walks in, in the row's own terms — "n makes the first case" is a
	// different sentence from "read the failures", and the row is what knows which it is.
	action string
}

type pickState struct {
	rows   []pickRow
	cursor int
	top    int
	err    string
}

func (r pickRow) key() string { return r.skill + "/" + r.lane }

// skills is how many groups the list has, for the head's count: a skill is a group, never a row.
func (p *pickState) skills() int {
	n, prev := 0, ""
	for _, r := range p.rows {
		if r.skill != prev {
			n++
			prev = r.skill
		}
	}
	return n
}

// loadPick reads every skill's two lanes. Both are always rows: a skill is a description to tune and
// a set to declare, and neither needs a run behind it to be worked on. A skill with nothing measured
// is the ordinary case for a new one, and hiding it until a case exists would hide the way in.
func (m *Model) loadPick() {
	p := &m.pick
	keep := ""
	if p.cursor >= 0 && p.cursor < len(p.rows) {
		keep = p.rows[p.cursor].key()
	}
	p.rows, p.err = nil, ""
	r := runner.New(m.store)
	groups := make([][]pickRow, 0, len(m.store.SkillNames()))
	for _, name := range m.store.SkillNames() {
		// Both lanes of one skill are adjacent, in the same order for every skill: the list is read
		// by skill, and a lane that moves around the screen is a lane you have to hunt for.
		groups = append(groups, []pickRow{triggerPickRow(r, m.store, name), outputPickRow(r, name)})
	}
	// The group's urgency is its most urgent lane, so a skill with a failing run sits high with both
	// of its lanes beside it — and never with one lane up top and the other at the bottom.
	sort.SliceStable(groups, func(i, j int) bool {
		ui, uj := groupUrgency(groups[i]), groupUrgency(groups[j])
		if ui != uj {
			return ui < uj
		}
		return groups[i][0].skill < groups[j][0].skill
	})
	for _, g := range groups {
		p.rows = append(p.rows, g...)
	}
	p.cursor = min(max(0, p.cursor), max(0, len(p.rows)-1))
	for i, row := range p.rows {
		if row.key() == keep {
			p.cursor = i
			break
		}
	}
}

// groupUrgency is the most urgent of a skill's two lanes: 0 a run failed, 1 nothing done yet, 2 clean.
func groupUrgency(g []pickRow) int {
	u := 2
	for _, r := range g {
		if r.urgent < u {
			u = r.urgent
		}
	}
	return u
}

// triggerPickRow is the description lane's row: the loop's own numbers, or the fact that there is
// nothing to measure yet — which is a place to start, not a reason to hide the row.
func triggerPickRow(r *runner.Runner, s *store.Store, name string) pickRow {
	row := pickRow{skill: name, lane: store.LaneTrigger, badge: "description"}
	if strings.TrimSpace(s.SkillDescription(name)) == "" {
		row.state, row.urgent = "no description yet", 1
		row.next = "the description is the whole of the trigger: the wording block starts empty, and ctrl+s writes it into SKILL.md"
		row.action = "the wording — type a sentence, ctrl+s saves it"
		return row
	}
	lab, err := r.DescriptionLab(name)
	if err != nil || lab == nil {
		row.state, row.urgent = "the skill's files did not re-read", 1
		row.next = err.Error()
		return row
	}
	row.next = lab.Next.Line
	switch {
	case lab.Iterations == 0:
		row.state, row.urgent = "nothing measured yet", 1
		row.action = "the loop — R runs the train set"
	case len(failingOf(lab)) > 0:
		// The row says the one number that sends you in; where the loop stands is the pane's sentence.
		row.state = fmt.Sprintf("%s failing", plural(len(failingOf(lab)), "train row"))
		row.urgent = 0
		row.action = "the loop — read the failures, revise, R again"
	default:
		row.state = triggerRates(lab)
		row.urgent = 2
		row.action = "the loop — the wordings measured, and the pick"
	}
	return row
}

// failingOf counts the train rows the newest measured candidate fails: the only number that says
// "there is something to revise".
func failingOf(lab *runner.Lab) []runner.ScoreRow {
	if lab == nil || len(lab.Candidates) == 0 {
		return nil
	}
	last := lab.Candidates[len(lab.Candidates)-1]
	m := last.MeasureOn(store.SplitTrain)
	if m == nil {
		return nil
	}
	return last.FailedTrain()
}

// triggerRates is the line a finished loop earns: the best wording's two rates.
func triggerRates(lab *runner.Lab) string {
	if lab.Best == nil {
		return fmt.Sprintf("%s · no validation yet", plural(lab.Iterations, "wording"))
	}
	t, v := lab.Best.MeasureOn(store.SplitTrain), lab.Best.MeasureOn(store.SplitValidation)
	line := fmt.Sprintf("%s · best #%d", plural(lab.Iterations, "wording"), lab.Best.ID)
	if t != nil {
		line += fmt.Sprintf(" · train %d/%d", t.Train.Passed, t.Train.Judged)
	}
	if v != nil {
		line += fmt.Sprintf(" · validation %d/%d", v.Validation.Passed, v.Validation.Judged)
	}
	return line
}

// outputPickRow is the output lane's row: the cases, and the newest iteration's verdicts — or the
// fact that the set is still to be declared, which is the first thing a new skill needs.
func outputPickRow(r *runner.Runner, name string) pickRow {
	row := pickRow{skill: name, lane: store.LaneOutput, badge: "output"}
	lab, err := r.QualityLab(name)
	if err != nil || lab == nil {
		row.state, row.urgent = "the set did not re-read", 1
		if err != nil {
			row.next = err.Error()
		}
		return row
	}
	row.next = lab.Next.Line
	switch {
	case lab.Cases == 0:
		row.state, row.urgent = "no cases yet", 1
		row.next = "a case is a prompt, the expected output, its fixtures and the assertions a judge reads"
		row.action = "the set — n makes the first case, S opens evals.json"
	case lab.InitPending && len(lab.Iterations) == 0:
		row.state, row.urgent = fmt.Sprintf("%s · nothing run yet", plural(lab.Cases, "case")), 1
		row.action = "the set — the loop is closed until one run grades it"
	case lab.Unasserted > 0:
		row.state = fmt.Sprintf("%s · %d carry no assertion", plural(lab.Cases, "case"), lab.Unasserted)
		row.urgent = 0
		row.action = "the set — e writes the assertions"
	default:
		last := lab.Iterations[len(lab.Iterations)-1]
		line := plural(len(lab.Iterations), "iteration")
		if lab.Best > 0 {
			line += fmt.Sprintf(" · best #%d", lab.Best)
		}
		if n := len(last.Failed); n > 0 {
			// The row says the one number that sends you in: what failed. The rates are the pane's
			// and the table's, and a row too narrow for them prints an ellipsis instead of a fact.
			line = fmt.Sprintf("%s failing", plural(n, "assertion"))
			row.urgent = 0
			row.action = "the cases, and what failed"
		} else {
			line += fmt.Sprintf(" · with %d/%d · without %d/%d",
				last.With.Passed, last.With.Judged, last.Without.Passed, last.Without.Judged)
			row.urgent = 2
			row.action = "the cases and the trend"
		}
		row.state = line
	}
	return row
}

// ---------------------------------------------------------------- drawing

func (m *Model) pickListWidth() int { return min(54, max(38, m.width*2/5)) }

func (m *Model) pickBody(h int) []string {
	listW := m.pickListWidth()
	paneW := min(96, m.width-listW-3)
	if paneW < 40 {
		return m.pickNarrow(h)
	}
	return m.splitBody(h, listW, paneW, m.pickList(listW), m.pickPane(paneW, h))
}

func (m *Model) pickList(w int) []string {
	p := &m.pick
	lines := []string{"", regionHead(w, "THE WORK", fmt.Sprintf("%d", p.skills()), true),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, w-4)))}
	if len(p.rows) == 0 {
		lines = append(lines, "", "  "+dimStyle.Render("no skill in this tree"),
			"  "+dimStyle.Render("nothing to tune yet"))
		return lines
	}
	const badgeW = 12
	prev := ""
	for i, r := range p.rows {
		if r.skill != prev {
			if prev != "" {
				lines = append(lines, "")
			}
			// The skill is the group's head and takes no keys: the cursor walks the lanes, and a
			// header that could be selected would be a row that opens nothing.
			// The skill is the group's head and takes no keys. Its two lanes hang under it behind one
			// seam, so "these belong together" is drawn rather than counted.
			lines = append(lines, "  "+titleStyle.Render(ellipsis(r.skill, w-4)))
			prev = r.skill
		}
		mark := " "
		if i == p.cursor {
			mark = cursorMark.Render(glyphs.Cursor)
		}
		badge := padCell(r.badge, badgeW, false)
		state := ellipsis(r.state, max(8, w-5-badgeW-1))
		stem := mark + "  " + dimStyle.Render(glyphs.Seam) + " "
		line := stem + titleStyle.Render(badge) + " " + dimStyle.Render(state)
		if r.urgent == 0 && i != p.cursor {
			// The words that say a run left something behind, in the colour the failure pane uses:
			// the list's job is "where is the work", and a failure is the loudest answer.
			line = stem + titleStyle.Render(badge) + " " + failMark.Render(state)
		}
		if i == p.cursor {
			line = selected(line)
		}
		lines = append(lines, line)
	}
	return lines
}

func (m *Model) pickNarrow(h int) []string {
	p := &m.pick
	lines := []string{"", regionHead(m.width, "THE WORK", fmt.Sprintf("%d", p.skills()), true),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, m.width-4)))}
	prev := ""
	for i, r := range p.rows {
		if r.skill != prev {
			if prev != "" {
				lines = append(lines, "")
			}
			lines = append(lines, "  "+titleStyle.Render(r.skill))
			prev = r.skill
		}
		mark := " "
		if i == p.cursor {
			mark = cursorMark.Render(glyphs.Cursor)
		}
		line := mark + "   " + titleStyle.Render(r.badge)
		if i == p.cursor {
			line = selected(line)
		}
		lines = append(lines, line, "     "+dimStyle.Render(r.state))
	}
	return fitRows(lines, h)
}

// pickPane is the row under the cursor said properly: what the lane has measured, what the disk is
// waiting for, and the one key that walks in. The action is reserved first, so a short window
// loses the explanation and never the key.
func (m *Model) pickPane(w, h int) []string {
	p := &m.pick
	d := &docWriter{w: w, label: 12}
	if len(p.rows) == 0 {
		d.head("THE WORK", false)
		d.rule()
		d.para("A skill appears here when it has a description in SKILL.md or cases in evals/evals.json. " +
			"Make one with `evalctl add`, or point --root at a tree that has one.")
		return fitRows(d.lines, h)
	}
	r := p.rows[p.cursor]
	from := len(d.lines)
	// The head names the row, not just the skill: with the lanes stacked under one name, "which one am
	// I looking at" must be answerable from the pane as well as from the cursor.
	d.head(strings.ToUpper(r.skill)+" · "+strings.ToUpper(r.badge), false)
	d.rule()
	d.lead(lanePurpose(r.lane))
	d.labelled("state", paneLabel, r.state)
	block := append([]string(nil), d.lines[from:]...)
	d.lines = nil
	if r.next != "" {
		d.para(r.next)
	}
	body := d.lines

	act := &docWriter{w: w}
	sentence := r.action
	if sentence == "" {
		switch r.lane {
		case store.LaneOutput:
			sentence = "the cases and what failed — R runs the set, p revises"
		default:
			sentence = "the loop — the set, the wording, the wordings measured"
		}
	}
	act.action("enter", sentence)
	room := h - len(act.lines)
	out := append([]string{}, block...)
	if len(out)+len(body) <= room {
		out = append(out, body...)
	}
	return fitRows(append(out, act.lines...), h)
}

// ---------------------------------------------------------------- keys

func (m *Model) updatePick(msg tea.KeyMsg) tea.Cmd {
	p := &m.pick
	switch msg.String() {
	case "j", "down":
		p.cursor = min(p.cursor+1, max(0, len(p.rows)-1))
	case "k", "up":
		p.cursor = max(0, p.cursor-1)
	case "g", "home":
		p.cursor = 0
	case "G", "end":
		p.cursor = max(0, len(p.rows)-1)
	case "enter":
		if len(p.rows) > 0 {
			m.openWork(p.rows[p.cursor])
		}
	}
	return nil
}

// laneOrDefault is the lane a `--skill` opens: the one the flag named, or the description loop,
// which is the lane every skill has.
func laneOrDefault(lane string) string {
	if lane == store.LaneOutput {
		return store.LaneOutput
	}
	return store.LaneTrigger
}

// lanePurpose is the lane's one sentence, the same words the door used to print: the badge names the
// lane, and this says what the lane is for without a paragraph about it.
func lanePurpose(lane string) string {
	if lane == store.LaneOutput {
		return "does the work come out right? — run both arms, grade, revise, rerun"
	}
	return "does the right skill load? — tune the description until it does"
}

// openSkill opens a skill's description loop on the work surface: its trigger runs, the rows of the split
// on screen, and candidate #1 adopted from SKILL.md when the loop has never started — retyping your
// own description is the one step of the guide a tool can do for you, and a paraphrase would measure
// a text nobody ships.
func (m *Model) openSkill(name string) {
	if m.store.Skill(name) == nil {
		m.setStatus("unknown skill "+name, true)
		return
	}
	m.skill = name
	m.flow = &flowState{sec: secIteration, step: 1}
	m.reload()
	if m.flow.lab == nil {
		return
	}
	if len(m.flow.lab.Candidates) == 0 {
		switch {
		case strings.TrimSpace(m.store.SkillDescription(name)) == "":
			// A skill with no description is the first day of one: the wording block is empty and the
			// sentence typed into it is the first draft.
			m.setStatus("no description yet — type it in the wording block, ctrl+s stages it", false)
		default:
			// The wording SKILL.md carries is a candidate as soon as it is measured: there is nothing
			// to adopt, only something to run.
			m.setStatus("no trigger runs yet — enter measures the wording "+name+"/SKILL.md carries", false)
		}
	}
	// The wording block is seeded here and not on the first frame: a skill with no description opens on
	// an empty field, and the keys work before anything has been drawn.
	m.ensureDraft()
	m.mode = screenFlow
}

// openWork walks into a row: the lane is already decided by the row, so there is no screen between
// the list and the work.
func (m *Model) openWork(r pickRow) {
	m.skill, m.lane = r.skill, r.lane
	switch r.lane {
	case store.LaneOutput:
		m.openQualitySkill(r.skill)
		if m.quality == nil || m.quality.lab == nil {
			return
		}
		switch {
		case m.quality.lab.InitPending:
			// Nothing declared, or nothing run: the set is the work, and the declaration is what opens
			// the loop. The disk decides it, and the list only says which skill.
			m.setQualitySection(qSetup)
		default:
			m.setQualitySection(qDojo)
			// The cursor lands on a failing case: the surface's job is what failed, and a cursor
			// resting on a case that passed makes the reader find the failure themselves.
			m.quality.selectFirstFailed()
		}
		m.mode = screenQuality
		m.setStatus("", false)
	default:
		m.openSkill(r.skill)
		if m.flow == nil {
			return
		}
		// The loop's home is the workbench: the three blocks hold the set, the wording and the
		// wordings measured, which is where a revision is read and written. The split toggle is a
		// key on that screen now, not a screen in front of it.
		m.flow.sec, m.flow.step = secIteration, 1
		m.flow.focus = focusSet
		m.mode = screenFlow
		m.reload()
		m.setStatus("", false)
	}
}
