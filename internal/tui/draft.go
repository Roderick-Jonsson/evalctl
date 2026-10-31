package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// The revision: the one screen in the loop where something is typed.
//
// Everything else in the flow can be computed, but the wording cannot, so this screen gives the
// parent text a caret and keeps the evidence beside it — which rows are failing, what the guide
// allows (broaden, narrow, never paste a query), and how many characters are left of the format's
// 1024. The note is not decoration: it is what the next session reads to know why this wording
// exists, and the ledger records it beside the parent it came from.

type draftState struct {
	parent int
	text   textarea.Model
	note   textinput.Model
	focus  int // 0 the wording, 1 the note
	errMsg string

	width, height int
}

func (m *Model) openDraft() tea.Cmd {
	it := m.flow.latest()
	if it == nil {
		m.setStatus("nothing measured yet — run the train set first", true)
		return nil
	}
	ta := textarea.New()
	ta.Placeholder = "the description the router will read"
	ta.SetValue(it.Cand.Text)
	ta.CharLimit = store.DescriptionLimit
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.SetWidth(max(30, m.width-6))
	ta.SetHeight(max(4, min(12, m.bodyHeight()/3)))
	ta.Focus()

	ni := textinput.New()
	ni.Placeholder = "what changed, and why — one line"
	ni.CharLimit = 240
	ni.Width = max(20, m.width-24)

	m.draft = &draftState{parent: it.Cand.ID, text: ta, note: ni,
		width: m.width, height: m.bodyHeight()}
	m.mode = screenDraft
	return nil
}

func (d *draftState) value() string { return strings.TrimSpace(d.text.Value()) }

func (d *draftState) body(m *Model, h int) []string {
	it := m.flow.latest()
	n := len([]rune(d.value()))
	counter := fmt.Sprintf("%d / %d", n, store.DescriptionLimit)
	style := dimStyle
	if n > store.DescriptionLimit {
		style = statusBad
	} else if n > store.DescriptionLimit*4/5 {
		style = badgeWarn
	}
	title := fmt.Sprintf("candidate #%d from #%d", d.parent+1, d.parent)
	lines := []string{
		"",
		"  " + titleStyle.Render(title) + "  " + style.Render(counter),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, m.width-4)))),
	}
	for _, line := range strings.Split(strings.TrimRight(d.text.View(), "\n"), "\n") {
		lines = append(lines, "  "+line)
	}
	lines = append(lines, "  "+dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, m.width-4)))),
		"  "+dimStyle.Render("why this change  ")+d.note.View())
	if d.errMsg != "" {
		lines = append(lines, "  "+statusBad.Render("! "+d.errMsg))
	} else {
		lines = append(lines, "")
	}
	lines = append(lines, "  "+dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, m.width-4)))))
	if it != nil {
		d := &docWriter{w: max(28, m.width-4)}
		d.fact("still failing", idList(it.Cand.FailedTrain()))
		if len(it.Train.Fixed) > 0 || len(it.Train.Broke) > 0 {
			d.fact("last revision", fmt.Sprintf("fixed %s · broke %s",
				idListOf(it.Train.Fixed), idListOf(it.Train.Broke)))
		}
		if lifted := liftedWords(m, it.Cand.Text); lifted != "" {
			d.fact("lifted words", lifted)
		}
		lines = append(lines, d.lines...)
	}
	return fitRows(lines, h)
}

// liftedWords names the query words that have appeared in the draft: the guide's one hard stylistic
// rule is not to copy the test's own vocabulary, because that is how a wording learns the train set
// and nothing else.
func liftedWords(m *Model, draft string) string {
	it := m.flow.latest()
	if it == nil {
		return ""
	}
	words := runner.LiftedWords(draft, it.Cand.Text, it.Cand.FailedTrain())
	if len(words) == 0 {
		return ""
	}
	return fmt.Sprintf("%s — say intent, not keywords", strings.Join(words, ", "))
}

func (d *draftState) stateText() string {
	return "the wording is the only thing that changes here; the parent stays in the ledger"
}

func (d *draftState) hints() []hint {
	return []hint{
		{"tab", "wording / note"},
		{"ctrl+s", "save as the next candidate"},
		{"esc", "discard"},
	}
}

func (d *draftState) update(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return tea.Quit
	case "tab":
		if d.focus == 0 {
			d.focus = 1
			d.text.Blur()
			d.note.Focus()
		} else {
			d.focus = 0
			d.note.Blur()
			d.text.Focus()
		}
		return nil
	case "ctrl+s":
		return d.save(m)
	case "esc":
		m.draft, m.mode = nil, screenFlow
		m.setStatus("discarded — the wording was not written", false)
		return nil
	}
	var cmd tea.Cmd
	if d.focus == 0 {
		d.text, cmd = d.text.Update(msg)
	} else {
		d.note, cmd = d.note.Update(msg)
	}
	return cmd
}

// save writes the candidate into the ledger. It does not write SKILL.md: the file is what the router
// reads, and a wording that has never been measured should not be in it — the training run of the
// next iteration is what puts it there.
func (d *draftState) save(m *Model) tea.Cmd {
	text := d.value()
	if text == "" {
		d.errMsg = "a description is required"
		return nil
	}
	if len([]rune(text)) > store.DescriptionLimit {
		d.errMsg = fmt.Sprintf("%d characters — the format's ceiling is %d", len([]rune(text)), store.DescriptionLimit)
		return nil
	}
	note := strings.TrimSpace(d.note.Value())
	cand, err := runner.New(m.store).SaveDescriptionCandidate(m.skill, text, note, d.parent)
	if err != nil {
		d.errMsg = err.Error()
		return nil
	}
	m.draft, m.mode = nil, screenFlow
	m.reload()
	m.flow.sub = subRun
	m.setStatus(fmt.Sprintf("candidate #%d saved — it becomes iteration %d after the run",
		cand.ID, len(m.flow.iters)+1), false)
	return nil
}
