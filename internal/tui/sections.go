package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The section screen: iteration or generalization.
//
// The two halves of the loop use their two sets for opposite things — one is what a revision may
// learn from, the other is what must never shape one — so they are chosen here, explicitly, and each
// opens on its own steps. It is the same shape as the door and the skill chooser: the choice on the
// left, the one under the cursor explained on the right.

type sectionState struct{ cursor, top int }

var sectionRows = []struct {
	sec   int
	title string
}{
	{secIteration, "iteration"},
	{secGeneralization, "generalization"},
}

func (m *Model) sectionsBody(h int) []string {
	listW := min(34, max(24, m.width/3))
	paneW := min(96, m.width-listW-3)
	if paneW < 38 {
		return m.sectionsNarrow(h)
	}
	return m.splitBody(h, listW, paneW, m.sectionsList(), m.sectionPreview(paneW, h))
}

func (m *Model) sectionsList() []string {
	lines := []string{"", ""}
	for i, row := range sectionRows {
		mark := "  "
		if i == m.sections.cursor {
			mark = cursorMark.Render(glyphs.Cursor)
		}
		state := dimStyle.Render(m.sectionTag(row.sec))
		lines = append(lines, mark+titleStyle.Render(row.title)+"  "+state, "")
	}
	return lines
}

// sectionTag is the short state beside a section in the list: two or three words, because the pane
// is where the sentence lives.
func (m *Model) sectionTag(sec int) string {
	f := m.flow
	if f == nil {
		return ""
	}
	if sec == secIteration {
		return fmt.Sprintf("%d of %d", len(f.iters), iterationsRequired)
	}
	switch {
	case f.lab == nil || f.lab.ValidationRows == 0:
		return "no queries"
	case f.lab.Applied != nil && allValidated(f.lab):
		return "applied"
	case allValidated(f.lab):
		return "measured"
	case !f.gateOpen():
		return "gated"
	}
	return "to validate"
}

// sectionLead is that state as a sentence, for the pane's second line.
func (m *Model) sectionLead(sec int) string {
	f := m.flow
	if f == nil {
		return ""
	}
	if sec == secIteration {
		return fmt.Sprintf("%d of %d iterations measured", len(f.iters), iterationsRequired)
	}
	switch {
	case f.lab == nil || f.lab.ValidationRows == 0:
		return "no validation queries yet"
	case f.lab.Applied != nil && allValidated(f.lab):
		return fmt.Sprintf("#%d applied · every measured wording validated", f.lab.Applied.Candidate)
	case allValidated(f.lab):
		return "every measured wording validated"
	case !f.gateOpen():
		return fmt.Sprintf("gated — %s still to measure", plural(max(0, iterationsRequired-len(f.iters)), "iteration"))
	}
	return "ready to validate the measured wordings"
}

func (m *Model) sectionPreview(w, h int) []string {
	row := sectionRows[m.sections.cursor]
	d := &docWriter{w: w, label: 12}
	var blocks [][]string
	from := len(d.lines)
	block := func() {
		blocks = append(blocks, append([]string(nil), d.lines[from:]...))
		from = len(d.lines)
	}
	f := m.flow

	d.title(strings.ToUpper(row.title))
	d.lead(m.sectionLead(row.sec))
	d.rule()
	block()

	if row.sec == secIteration {
		d.para("The train set is the only measurement a revision may learn from. Here the wording is run against it, the failures are read one by one, and the next candidate is written from them — the validation set is never opened, so nothing the revision learns can come from it.")
		block()
		d.card("Its steps")
		d.fact("1 train set", "the queries the wording may learn from")
		d.fact("2 iterate", "run them, read the failures, revise the wording")
		block()
		d.card("The ledger")
		d.fact("wordings", plural(len(f.lab.Candidates), "candidate"))
		applied := "none yet"
		if f.lab.Applied != nil {
			applied = fmt.Sprintf("#%d", f.lab.Applied.Candidate)
		}
		d.fact("applied", applied)
		block()
	} else {
		d.para("The validation set is what judges a wording, and it never shapes one. Here it is run once against every wording the iteration produced, and the result list ranks them by it — the best wording is often not the last one written.")
		block()
		d.card("Its steps")
		d.fact("1 validation set", "the queries that judge the wording")
		d.fact("2 validate", "one run per measured wording")
		d.fact("3 results", "train beside validation, for every iteration")
		block()
		d.card("The gate")
		d.fact("measured", fmt.Sprintf("%d of %d iterations", len(f.iters), iterationsRequired))
		if !f.gateOpen() && len(f.iters) > 0 {
			d.fact("ctrl+g", "validate early, with the warning")
		}
		block()
	}

	actDoc := &docWriter{w: w}
	_, step := loopOpen(f.lab)
	if row.sec == secGeneralization {
		step = 1
		if f.lab != nil && f.lab.ValidationRows > 0 {
			step = 2
		}
	}
	if row.sec == secIteration && f.lab != nil && f.lab.TrainRows == 0 {
		step = 1
	}
	actDoc.action("enter", fmt.Sprintf("open at step %d — %s", step, stepNameAt(row.sec, step)))
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

// sectionsNarrow is the section screen without a pane: each row carries its own tag, and the one
// under the cursor carries a sentence.
func (m *Model) sectionsNarrow(h int) []string {
	lines := []string{""}
	for i, row := range sectionRows {
		mark := "  "
		if i == m.sections.cursor {
			mark = cursorMark.Render(glyphs.Cursor)
		}
		lines = append(lines, mark+titleStyle.Render(row.title)+"  "+dimStyle.Render(m.sectionTag(row.sec)))
		if i == m.sections.cursor {
			blurb := "the train set, the run, the failures, the revision"
			if row.sec == secGeneralization {
				blurb = "the validation set, one run per wording, the ranking"
			}
			for _, line := range wrapPlain(blurb, max(20, m.width-8)) {
				lines = append(lines, "    "+dimStyle.Render(line))
			}
		}
		lines = append(lines, "")
	}
	return fitRows(lines, h)
}

func (m *Model) updateSections(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "j", "down":
		m.sections.cursor = min(m.sections.cursor+1, len(sectionRows)-1)
	case "k", "up":
		m.sections.cursor = max(0, m.sections.cursor-1)
	case "g", "home":
		m.sections.cursor = 0
	case "G", "end":
		m.sections.cursor = len(sectionRows) - 1
	case "enter":
		return m.openSection(sectionRows[m.sections.cursor].sec)
	}
	return nil
}

// openSection enters one half of the loop at the step the disk says that half is at. The gate is not
// enforced here: generalization's first step is its query set, and a set can be written at any time —
// it is the run that waits.
func (m *Model) openSection(sec int) tea.Cmd {
	f := m.flow
	if f == nil {
		return nil
	}
	f.sec = sec
	f.resumeIn(m)
	m.mode = screenFlow
	return nil
}
