package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"

	"evalctl/internal/store"
)

// openCase puts the editor on the alpha case in a window wide enough for the whole rail.
func openCase(t *testing.T, w, h int) (*Model, *editorState) {
	t.Helper()
	m := sized(newModel(testStore(t), store.LaneOutput), w, h)
	row, ok := m.focused()
	if !ok || row.Lane != store.LaneOutput {
		t.Fatalf("no output row under the cursor: %+v", row)
	}
	m.openEditor(row, false)
	return m, m.editor
}

// TestEditorSaysWhichPageItIsOn: a form of four pages that never says so is a tunnel. The head names
// the page and the count, and the rail names every page, so how much is left is never a guess.
func TestEditorSaysWhichPageItIsOn(t *testing.T) {
	m, _ := openCase(t, 120, 30)
	view := ansi.Strip(m.View())
	for _, want := range []string{"page 1 of 4", "1 id · skill · slug", "2 prompt",
		"3 expected", "4 assertions · files"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the editor does not say %q:\n%s", want, view)
		}
	}
	// The rail is the walk: it counts pages, not fields, and the count moves with the form.
	m.Update(key("alt+2"))
	if got := ansi.Strip(m.View()); !strings.Contains(got, "page 2 of 4") {
		t.Fatalf("the page count did not follow the jump:\n%s", got)
	}
}

// TestPageJumpIsGated: alt+N goes where it says. Backwards is always free; forwards stops on a page
// that cannot be left and says what that page needs — a jump that silently does nothing teaches
// nothing.
func TestPageJumpIsGated(t *testing.T) {
	m, e := openCase(t, 120, 30)
	m.Update(key("alt+4"))
	if e.page() != 3 {
		t.Fatalf("alt+4 from page 1 landed on page %d", e.page()+1)
	}
	m.Update(key("alt+1"))
	if e.page() != 0 {
		t.Fatalf("alt+1 did not go back to the first page (page %d)", e.page()+1)
	}

	// An empty slug is what page 1 needs, so page 4 is not reachable from here.
	e.d.slug = ""
	m.Update(key("alt+4"))
	if e.page() != 0 {
		t.Fatalf("an unfinished page 1 let a jump past it (page %d)", e.page()+1)
	}
	if note := ansi.Strip(m.View()); !strings.Contains(note, "slug must be lowercase") {
		t.Fatalf("the refused jump did not say what page 1 needs:\n%s", note)
	}
}

// TestEditorPageSurvivesARefusedSave: a save that is refused must cost the typing nothing — not the
// values, and not the place in the form. It used to drop the operator back on page 1.
func TestEditorPageSurvivesARefusedSave(t *testing.T) {
	m, e := openCase(t, 120, 30)
	m.Update(key("alt+2"))
	if e.page() != 1 {
		t.Fatalf("setup: not on page 2 (%d)", e.page()+1)
	}
	e.d.slug = "not a slug" // page 1's rule, refused at save time
	m.Update(key("ctrl+s"))

	if m.mode != screenEditor || m.editor == nil {
		t.Fatal("a refused save closed the form")
	}
	if e.page() != 1 {
		t.Fatalf("a refused save moved the cursor to page %d", e.page()+1)
	}
	if !strings.Contains(ansi.Strip(m.View()), "slug must be lowercase") {
		t.Fatalf("the refusal was not shown:\n%s", ansi.Strip(m.View()))
	}
	if m.store.Skill("alpha").Output.Evals[0].Slug != "a-1" {
		t.Fatal("a refused draft was applied to the store")
	}
}

// TestCtrlSWritesTheDraft: the key bar promises ctrl+s writes, so inside the form it applies the
// draft and writes it — the same two steps the list takes, one keystroke apart.
func TestCtrlSWritesTheDraft(t *testing.T) {
	s := testStore(t)
	m := sized(newModel(s, store.LaneOutput), 120, 30)
	row, _ := m.focused()
	m.openEditor(row, false)
	m.editor.d.prompt = "a prompt typed and saved in one key"
	m.Update(key("ctrl+s"))

	if m.mode != screenList {
		t.Fatalf("ctrl+s did not apply the draft (mode %v)", m.mode)
	}
	if m.dirty {
		t.Fatal("ctrl+s applied the draft but did not write it")
	}
	body, err := os.ReadFile(filepath.Join(s.Root, "alpha", "evals", "evals.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "saved in one key") {
		t.Fatalf("the prompt is not in the file on disk:\n%s", body)
	}
}

// TestEditorFactsAreLive: the facts about a page are read off the draft on every render. The old
// form described the fixture dir once, when it was built, and went on describing the slug that had
// since been typed over.
func TestEditorFactsAreLive(t *testing.T) {
	m, e := openCase(t, 140, 30)
	m.Update(key("alt+4"))
	before := ansi.Strip(m.View())
	if !strings.Contains(before, "a-1") {
		t.Fatalf("the files page does not name the fixture dir:\n%s", before)
	}
	e.d.slug = "brand-new"
	after := ansi.Strip(m.View())
	if !strings.Contains(after, "files/brand-new") || strings.Contains(after, "files/a-1") {
		t.Fatalf("the files page still describes the old slug:\n%s", after)
	}

	// An id a sibling already owns is a warning where the number is typed, not at the end of the
	// form: the store refuses it at save time, so the page says it at typing time.
	other := m.store.Skill("alpha")
	other.Output.Evals = append(other.Output.Evals, other.Output.Evals[0])
	other.Output.Evals[1].ID = 7
	other.Output.Evals[1].Slug = "a-2"
	e.d.slug = "a-1"
	e.d.id = "7"
	m.Update(key("alt+1"))
	if got := ansi.Strip(m.View()); !strings.Contains(got, "id 7 is already case") {
		t.Fatalf("a taken id is not flagged on the page it is typed on:\n%s", got)
	}
}

// TestTriggerEditorHasItsOwnPages: the rail is the lane's own shape, not a constant. A query is
// identity, then the two facets that decide what it is, then the text — three pages, and it says so.
func TestTriggerEditorHasItsOwnPages(t *testing.T) {
	m := sized(newModel(testStore(t), store.LaneTrigger), 120, 30)
	row, _ := m.focused()
	m.openEditor(row, false)
	if got := len(m.editor.pages); got != 3 {
		t.Fatalf("a query form has %d pages, want 3", got)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"page 1 of 3", "alt+1–3 page", "1 id · skill", "2 label · split",
		"3 query · why"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the query form does not say %q:\n%s", want, view)
		}
	}
}

// TestEditorFitsTheWindowOnEveryPage: the form is the only screen that used to own no frame. However
// tall the page is, the key bar and the status line are the last two rows of the window — a footer
// that walks off the bottom takes the save key with it.
func TestEditorFitsTheWindowOnEveryPage(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {80, 22}, {64, 18}} {
		m, e := openCase(t, size[0], size[1])
		for page := 0; page < len(e.pages); page++ {
			m.Update(key("alt+" + string(rune('1'+page))))
			rows := strings.Split(ansi.Strip(m.View()), "\n")
			if len(rows) != size[1] {
				t.Fatalf("%dx%d page %d drew %d rows", size[0], size[1], page+1, len(rows))
			}
			band := rows[len(rows)-2]
			if !strings.Contains(band, "ctrl+s save") && size[0] >= 100 {
				t.Fatalf("%dx%d page %d lost the key bar: %q", size[0], size[1], page+1, band)
			}
			if !strings.Contains(rows[len(rows)-1], "") {
				t.Fatalf("%dx%d page %d has no status row", size[0], size[1], page+1)
			}
		}
	}
}

// TestFilesAreOnePerLine: the field says one staged path per line, and the list that gets written is
// the list the live count reports — so a comma is part of a name, not a separator.
func TestFilesAreOnePerLine(t *testing.T) {
	got := splitLines("a.md\nchart, final.png\n\n  b.json  ")
	want := []string{"a.md", "chart, final.png", "b.json"}
	if len(got) != len(want) {
		t.Fatalf("splitLines = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitLines = %q, want %q", got, want)
		}
	}
}

// TestKeyMapOpensFromAForm: ? types a question mark inside a form — the prompts in this set are
// questions — so the key map is alt+h, and the cap in the band says so.
func TestKeyMapOpensFromAForm(t *testing.T) {
	m, _ := openCase(t, 120, 30)
	if !strings.Contains(ansi.Strip(m.View()), "alt+h keys") {
		t.Fatalf("the form's band does not name its key map:\n%s", ansi.Strip(m.View()))
	}
	m.Update(key("?"))
	if m.help {
		t.Fatal("? opened the key map instead of typing a question mark")
	}
	m.Update(key("alt+h"))
	if !m.help {
		t.Fatal("alt+h did not open the key map")
	}
	m.Update(key("esc"))
	if m.help || m.mode != screenEditor {
		t.Fatalf("closing the key map did not come back to the form: help=%v mode=%v", m.help, m.mode)
	}
}

// TestTabbingWalksTheRail: crossing a page moves the page in the head and the fill in the rail — the
// count is the form's own, not a guess kept on the side. huh hands the move back as a command, so the
// group's public NextGroup is what a tab across a page boundary does.
func TestTabbingWalksTheRail(t *testing.T) {
	m, e := openCase(t, 120, 30)
	for page := 0; page < len(e.pages)-1; page++ {
		if moved, _ := e.step(+1); !moved {
			t.Fatalf("step %d did not cross a page boundary", page)
		}
		if e.page() != page+1 {
			t.Fatalf("after %d step(s) the form is on page %d", page+1, e.page()+1)
		}
		if got := ansi.Strip(m.View()); !strings.Contains(got, fmt.Sprintf("page %d of 4", page+2)) {
			t.Fatalf("the head does not name page %d:\n%s", page+2, got)
		}
	}
	// The rail lights the page it is on and only that one.
	rail := strings.Split(ansi.Strip(m.View()), "\n")[2]
	if !strings.Contains(rail, "4 assertions · files") {
		t.Fatalf("the rail does not name the last page: %q", rail)
	}
}

// TestSubmitOnTheLastPageApplies: crossing out of the last page is the form's submit, and submit is
// what puts the draft in memory — no ctrl+s needed for that half.
func TestSubmitOnTheLastPageApplies(t *testing.T) {
	m, e := openCase(t, 120, 30)
	e.d.prompt = "typed by walking the form"
	e.d.assert = "one\ntwo"
	for i := 0; i < 8 && e.form.State != huh.StateCompleted; i++ {
		e.form.NextGroup()
	}
	if e.form.State != huh.StateCompleted {
		t.Fatalf("walking past the last page did not submit (state %v)", e.form.State)
	}
	m.Update(key("enter"))
	if m.mode != screenList {
		t.Fatalf("a submitted form did not apply (mode %v)", m.mode)
	}
	if !m.dirty {
		t.Fatal("an applied draft is not marked as an unsaved edit")
	}
	got := m.store.Skill("alpha").Output.Evals[0]
	if got.Prompt != "typed by walking the form" || len(got.Assertions) != 2 {
		t.Fatalf("the submit applied %+v", got)
	}
}

// TestTypingUpdatesTheFacts: the facts under the form are read off the draft, and the draft is what
// the field writes as you type — so a count that was right when the form opened is right now.
func TestTypingUpdatesTheFacts(t *testing.T) {
	m, e := openCase(t, 120, 30)
	m.Update(key("alt+4"))
	if got := ansi.Strip(m.View()); !strings.Contains(got, "no assertions yet") {
		t.Fatalf("the assertions page does not start by saying there are none:\n%s", got)
	}
	for _, r := range "first check" {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if e.d.assert != "first check" {
		t.Fatalf("typing did not reach the draft: %q", e.d.assert)
	}
	if got := ansi.Strip(m.View()); !strings.Contains(got, "1 assertion") {
		t.Fatalf("the count did not follow the typing:\n%s", got)
	}
}
