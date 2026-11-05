package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"evalctl/internal/model"
)

// The case's assertion list, written where it is read.
//
// Setup is the section that shows what a case declares, so it is the section the list is edited from:
// `e` turns the assertion block into the list itself, in place. It was a huh text area, and a text
// area is the wrong shape for this list — a line of it is an assertion, and in a text area the line
// is only a newline inside one string: the marker in front of it is the field's prompt (editable-looking
// chrome, drawn per display row, so a wrapped assertion grows a second marker and a second assertion
// looks like the same one), and the field scrolls once it has more lines than the height it was given,
// which hides the assertions above the one being typed.
//
// So the list is drawn as the list it is: one bullet per assertion, drawn by the pane and never part of
// the text; the caret is a bar inside the sentence; the block grows until the window runs out of rows,
// and only then does it scroll — with the assertion being typed kept in view.
//
// It is hand-rolled and the huh fields are not, which is a real cost: the caret, the wrap and the
// scrolling are this file's now. The reason it is worth it is that only a list can say "this is the
// second assertion" — the one thing the operator is doing when they press enter.

type assertState struct {
	active bool
	slug   string
	id     int
	path   string   // <skill>/evals/evals.json, as the head and the status row name it
	lines  []string // one assertion per entry, exactly as the file will hold them
	cur    int      // which assertion the caret is in
	col    int      // the caret's position in that assertion, in runes
	top    int      // first row drawn, when the window cannot hold the list
	width  int
	errMsg string
}

func (a *assertState) build(lines []string) {
	a.lines = append([]string(nil), lines...)
	if len(a.lines) == 0 {
		a.lines = []string{""}
	}
	a.cur = len(a.lines) - 1
	a.col = len([]rune(a.lines[a.cur]))
}

// pane draws the list: the head names the file it is written to, then one bulleted assertion per line
// and the caret bar where the next character goes. The block takes the rows it needs; scroll only
// happens when the window itself is too short for the list.
func (a *assertState) pane(w, h int) []string {
	a.width = w
	lines := []string{"", regionHead(w, "ASSERTIONS", a.path, true), laneRule(w)}
	body, caretRow := a.rows(w)
	room := max(1, h-len(lines)-boolRows(a.errMsg != ""))
	if len(body) > room {
		// The window follows the assertion being typed, and only rows are lost — never the structure:
		// every visible row still belongs to a bullet above or below it.
		if caretRow < a.top {
			a.top = caretRow
		}
		if caretRow >= a.top+room {
			a.top = caretRow - room + 1
		}
		a.top = min(max(0, a.top), len(body)-room)
		body = body[a.top : a.top+room]
	} else {
		a.top = 0
	}
	lines = append(lines, body...)
	if a.errMsg != "" {
		lines = append(lines, "  "+statusBad.Render("! "+a.errMsg))
	}
	return fitRows(lines, h)
}

// boolRows is one row when the value is true, so the error line is paid for before the list is
// measured against the window.
func boolRows(b bool) int {
	if b {
		return 1
	}
	return 0
}

// rows is the whole list as display rows, unfitted: each assertion's bullet, then its sentence
// wrapped under it with the caret bar drawn in the row it lands on. It also says which row the caret
// stands on, so the window can keep it in view.
func (a *assertState) rows(w int) ([]string, int) {
	textW := max(12, w-6)
	var out []string
	caretRow := 0
	for i, line := range a.lines {
		mark := "  " + dimStyle.Render(glyphs.Bullet) + " "
		if i == a.cur {
			mark = "  " + styleOn.Render(glyphs.Bullet) + " "
		}
		wrapped, at := caretRows(line, caretOf(i, a.cur, a.col), textW)
		for k, row := range wrapped {
			if i == a.cur && k == at {
				caretRow = len(out)
			}
			if k == 0 {
				out = append(out, mark+row)
				continue
			}
			out = append(out, "    "+row)
		}
	}
	return out, caretRow
}

// caretOf is the caret's offset inside one assertion: it is the caret only when that assertion holds
// it, and every other line is drawn without one.
func caretOf(i, cur, col int) int {
	if i == cur {
		return col
	}
	return -1
}

func (a *assertState) hints() []hint {
	return []hint{{"enter", "the next assertion"}, {"↑ ↓", "the assertions"}, {"ctrl+s", "write"}, {"esc", "discard"}}
}

func (a *assertState) update(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+s":
		return a.apply(m)
	case "esc":
		m.assertEdit = nil
		m.setStatus("the list is the file's again — nothing was written", false)
		return nil
	case "enter":
		a.split()
	case "backspace", "ctrl+h":
		a.backspace()
	case "up":
		a.moveLine(-1)
	case "down":
		a.moveLine(1)
	case "left":
		a.moveCol(-1)
	case "right":
		a.moveCol(1)
	case "home", "ctrl+a":
		a.col = 0
	case "end", "ctrl+e":
		a.col = a.lineLen()
	default:
		if msg.Type == tea.KeySpace {
			a.insert(" ")
			return nil
		}
		if msg.Type == tea.KeyRunes || msg.Paste {
			a.insertText(string(msg.Runes))
		}
	}
	return nil
}

// updateMsg is the app's pass-through for non-key messages. The list owns its caret, so there is
// nothing to hand on — it is here because every editor in the app answers the same interface.
func (a *assertState) updateMsg(msg tea.Msg) tea.Cmd { return nil }

func (a *assertState) lineLen() int { return len([]rune(a.lines[a.cur])) }

// split cuts the assertion at the caret and starts the next one there: enter is how a second
// assertion is written, and it is a break in the list and not a newline inside a sentence.
func (a *assertState) split() {
	runes := []rune(a.lines[a.cur])
	col := min(max(0, a.col), len(runes))
	head, tail := string(runes[:col]), string(runes[col:])
	a.lines[a.cur] = head
	a.lines = append(a.lines[:a.cur+1], append([]string{tail}, a.lines[a.cur+1:]...)...)
	a.cur++
	a.col = 0
}

// backspace deletes the character before the caret, and at the head of an assertion it joins the two:
// the empty assertion above is not a row the list can hold.
func (a *assertState) backspace() {
	runes := []rune(a.lines[a.cur])
	switch {
	case a.col > 0:
		a.lines[a.cur] = string(runes[:a.col-1]) + string(runes[a.col:])
		a.col--
	case a.cur > 0:
		prev := len([]rune(a.lines[a.cur-1]))
		a.lines[a.cur-1] += a.lines[a.cur]
		a.lines = append(a.lines[:a.cur], a.lines[a.cur+1:]...)
		a.cur--
		a.col = prev
	}
	if len(a.lines) == 0 {
		a.lines = []string{""}
		a.cur, a.col = 0, 0
	}
}

// insertText types at the caret. A paste that carries newlines is a list, not a line: every newline in
// it is a break between assertions, which is what the same text means in the file.
func (a *assertState) insertText(s string) {
	for i, part := range strings.Split(s, "\n") {
		if i > 0 {
			a.split()
		}
		a.insert(part)
	}
}

func (a *assertState) insert(s string) {
	if s == "" {
		return
	}
	runes := []rune(a.lines[a.cur])
	col := min(max(0, a.col), len(runes))
	a.lines[a.cur] = string(runes[:col]) + s + string(runes[col:])
	a.col = col + len([]rune(s))
}

func (a *assertState) moveLine(delta int) {
	next := min(max(0, a.cur+delta), len(a.lines)-1)
	if next == a.cur {
		return
	}
	a.cur = next
	a.col = min(a.col, a.lineLen())
}

// moveCol walks the caret, and crosses the assertion boundaries: a right arrow at the end of an
// assertion is the start of the next one, which is what the reader expects of a list.
func (a *assertState) moveCol(delta int) {
	switch {
	case delta < 0 && a.col > 0:
		a.col--
	case delta < 0 && a.cur > 0:
		a.cur--
		a.col = a.lineLen()
	case delta > 0 && a.col < a.lineLen():
		a.col++
	case delta > 0 && a.cur < len(a.lines)-1:
		a.cur++
		a.col = 0
	}
}

// apply writes the list through the store, which backs the file up first. Blank assertions are not
// assertions: they are the empty rows left behind by an enter, and the file keeps the sentences only.
// An empty list is not an empty array — the key is dropped, the shape the format gives a case whose
// assertions are still unknown.
func (a *assertState) apply(m *Model) tea.Cmd {
	sk := m.store.Skill(m.skill)
	idx := -1
	if sk != nil {
		for i, c := range sk.Output.Evals {
			if c.Slug == a.slug {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		a.errMsg = a.slug + " is no longer in " + a.path + " — esc leaves the editor"
		return nil
	}
	cse := sk.Output.Evals[idx]
	cse.Assertions = model.SplitAssertions(strings.Join(a.lines, "\n"))
	if _, err := m.store.UpdateOutput(m.skill, idx, cse); err != nil {
		a.errMsg = err.Error()
		return nil
	}
	if err := m.store.SaveSkill(m.skill); err != nil {
		a.errMsg = "could not write " + a.path + ": " + err.Error()
		return nil
	}
	m.assertEdit = nil
	m.reloadQuality()
	switch n := len(cse.Assertions); n {
	case 0:
		m.setStatus(a.slug+" declares no assertions now — nothing for the judge to grade", false)
	case 1:
		m.setStatus("wrote 1 assertion · the grades on screen were read against the old list — g re-grades", false)
	default:
		m.setStatus(fmt.Sprintf("wrote %d assertions · the grades on screen were read against the old list — g re-grades", n), false)
	}
	return nil
}

// openAssertEditor opens the list of the case under the cursor. It reads the list from the file
// rather than from the verdicts: an assertion written since the last grade is one the judge has never
// seen, and it still belongs in the list.
func (m *Model) openAssertEditor() tea.Cmd {
	q := m.quality
	if q == nil || q.caseAt() == nil {
		m.setStatus("no case on screen — n makes one, and its assertions are written here", true)
		return nil
	}
	c := q.caseAt()
	a := &assertState{
		slug:  c.Slug,
		id:    c.ID,
		path:  m.skill + "/evals/evals.json",
		width: m.width,
	}
	a.build(m.caseAssertions(c.Slug))
	a.active = true
	m.assertEdit = a
	if len(model.SplitAssertions(strings.Join(a.lines, "\n"))) == 0 {
		m.setStatus(c.Slug+" declares no assertion yet — one per bullet, ctrl+s writes them", false)
	} else {
		m.setStatus(fmt.Sprintf("%s · one assertion per bullet · ctrl+s writes %s", c.Slug, a.path), false)
	}
	return nil
}

// caseAssertions is the declared list as the file has it.
func (m *Model) caseAssertions(slug string) []string {
	sk := m.store.Skill(m.skill)
	if sk == nil {
		return nil
	}
	for _, c := range sk.Output.Evals {
		if c.Slug == slug {
			return append([]string(nil), c.Assertions...)
		}
	}
	return nil
}

func (m *Model) assertEditActive() bool { return m.assertEdit != nil && m.assertEdit.active }

// caretRows wraps one assertion at w cells and draws the caret bar in the row the caret falls in. The
// wrap is greedy on spaces and the offsets are the text's own, so the bar sits on the character the
// next keystroke will go before — a bar that moved with the wrap would be a bar in the wrong place.
func caretRows(text string, caret, w int) ([]string, int) {
	runes := []rune(text)
	if caret > len(runes) {
		caret = len(runes)
	}
	type span struct{ from, to int }
	var spans []span
	for start := 0; start < len(runes); {
		width, end, space := 0, start, -1
		for end < len(runes) {
			cell := lipgloss.Width(string(runes[end]))
			if width+cell > w && end > start {
				break
			}
			width += cell
			if runes[end] == ' ' {
				space = end
			}
			end++
		}
		if end < len(runes) && space >= start {
			end = space + 1
		}
		spans = append(spans, span{start, end})
		start = end
	}
	if len(spans) == 0 {
		spans = []span{{0, 0}}
	}
	out := make([]string, 0, len(spans))
	at := -1
	for _, s := range spans {
		shown := []rune(strings.TrimRight(string(runes[s.from:s.to]), " "))
		if caret >= 0 && caret >= s.from && caret <= s.to {
			pos := min(caret-s.from, len(shown))
			at = len(out)
			out = append(out, string(shown[:pos])+cursorMark.Render(glyphs.Caret)+string(shown[pos:]))
			continue
		}
		out = append(out, string(shown))
	}
	return out, at
}
