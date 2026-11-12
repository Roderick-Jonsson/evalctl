package tui

// The door's first step, done in one screen. Most folders are not empty: they hold skills that were
// written for Hermes and never measured, and the person in them wants them declared, not duplicated.
// So the screen reads the folder first — which skills are here, which profile is named, which
// profiles exist — fills every answer in, and writes on ctrl+s:
//
//   - a folder with skills gets the eval sets those skills are missing (SKILL.md untouched);
//   - a folder with none gets a starter skill, exactly as `evalctl init` writes it;
//   - the profile it names is created if it does not exist, and linked to this folder so it can be
//     worked in by hand afterwards;
//   - then the door opens on the tree that now exists.
//
// The rules live in internal/scaffold and internal/profile, so the CLI's `init`/`adopt`/`profiles
// link` and this screen cannot disagree about what a tree is.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"evalctl/internal/profile"
	"evalctl/internal/scaffold"
	"evalctl/internal/stage"
	"evalctl/internal/theme"
)

// NewProject runs the first screen from start and returns the folder it declared. ok is false when
// the operator left without writing anything.
func NewProject(start string) (dir string, ok bool) {
	applyStyles(theme.Current())
	g, err := Glyphs(os.Getenv("EVALCTL_GLYPHS"), os.Getenv("LANG"), os.Getenv("LC_CTYPE"), os.Getenv("LC_ALL"))
	if err != nil {
		fmt.Println("evalctl tui:", err)
		return "", false
	}
	setName(g)

	final, err := tea.NewProgram(newProjectModel(start), tea.WithAltScreen()).Run()
	if err != nil {
		fmt.Println("evalctl tui:", err)
		return "", false
	}
	p, _ := final.(*projectModel)
	if p == nil || p.created == "" {
		return "", false
	}
	return p.created, true
}

// projectModel is the whole first step: the answers are read from the folder, and the write is the
// same call the CLI verbs make.
type projectModel struct {
	form       *huh.Form
	root       string
	found      []scaffold.Found
	choice     []string // multi-select: the skills already here
	skill      string   // the input: a skill to create, when none are here
	prof       string
	profileMgr *profile.Manager
	errMsg     string

	created string
	note    string
	width   int
	height  int
}

func newProjectModel(start string) *projectModel {
	root, err := filepath.Abs(start)
	if err != nil {
		root = start
	}
	p := &projectModel{root: root, width: 90, height: 30}
	// The skill names come from the folder, never from a name to retype.
	p.profileMgr = profile.New(root, nil, stage.Rules{})
	p.found = scaffold.Detect(root)
	for _, f := range p.found {
		p.choice = append(p.choice, f.Name)
	}
	p.skill = scaffold.SuggestName(root)
	p.prof = p.suggestProfile()
	p.build()
	return p
}

// suggestProfile is the profile a tree here should name: what it already declares, else a profile
// that already exists under this folder's name, else the folder's own name (which the write creates).
func (p *projectModel) suggestProfile() string {
	if declared := stage.Load(p.root).Profile; declared != "" {
		return declared
	}
	want := scaffold.Sanitize(filepath.Base(p.root))
	for _, name := range p.profileMgr.All() {
		if name == want {
			return want
		}
	}
	return want
}

// projectKeys is huh's keymap for a form that writes files: enter walks to the next field (a stray
// enter must not declare anything), ctrl+s writes, esc leaves.
var projectKeys = func() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c"))
	km.Input.Next = key.NewBinding(key.WithKeys("enter", "tab"))
	km.Input.Prev = key.NewBinding(key.WithKeys("shift+tab"))
	km.Input.Submit = key.NewBinding(key.WithKeys("ctrl+s"))
	km.MultiSelect.Next = key.NewBinding(key.WithKeys("enter", "tab"))
	km.MultiSelect.Prev = key.NewBinding(key.WithKeys("shift+tab"))
	km.MultiSelect.Submit = key.NewBinding(key.WithKeys("ctrl+s"))
	return km
}()

func (p *projectModel) build() {
	var fields []huh.Field
	if len(p.found) > 0 {
		var opts []huh.Option[string]
		for _, f := range p.found {
			opts = append(opts, huh.NewOption(f.Name, f.Name))
		}
		fields = append(fields, huh.NewMultiSelect[string]().
			Title(fmt.Sprintf("declare %d skill(s) in this folder", len(p.found))).
			Description("space toggles · each one gets the eval sets it is missing, SKILL.md is not touched").
			Options(opts...).
			Value(&p.choice))
	} else {
		fields = append(fields, huh.NewInput().
			Title("start a skill").
			Description("this folder holds none yet: a starter SKILL.md, both sets and a fixture dir").
			Value(&p.skill).
			Validate(scaffold.CheckSkillName))
	}
	fields = append(fields, huh.NewInput().
		Title("hermes profile").
		Description(p.profileHelp()).
		Value(&p.prof).
		Validate(checkProfileName))

	p.form = huh.NewForm(huh.NewGroup(fields...)).
		WithShowHelp(false).
		WithKeyMap(projectKeys).
		WithTheme(huh.ThemeBase())
	p.form.Init()
}

// profileHelp says what the answer will do, including the one case that creates something.
func (p *projectModel) profileHelp() string {
	existing := p.profileMgr.All()
	if len(existing) == 0 {
		return "measured against its SOUL.md; empty means whoever runs evalctl"
	}
	return "existing: " + strings.Join(existing, ", ") + " — a name that is not there is created"
}

// checkProfileName is Hermes's own rule (PROFILE_ID_RE) said in a field.
func checkProfileName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if name != strings.ToLower(name) {
		return fmt.Errorf("lowercase letters, digits, - and _ (like %s)", strings.ToLower(name))
	}
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
		if !ok {
			return fmt.Errorf("%q: only lowercase letters, digits, - and _", string(r))
		}
	}
	if len(name) > 64 {
		return fmt.Errorf("at most 64 characters")
	}
	return nil
}

func (p *projectModel) Init() tea.Cmd { return p.form.Init() }

func (p *projectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = msg.Width, msg.Height
		p.form.WithWidth(max(20, msg.Width-8))
		return p, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return p, tea.Quit
		case "ctrl+s":
			return p, p.create()
		}
	}
	form, cmd := p.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		p.form = f
	}
	if p.form.State == huh.StateCompleted {
		return p, p.create()
	}
	return p, cmd
}

// create is the one write: files first, then the profile (created when the name is new), then the
// link, so a tree that names a profile always names one that exists.
func (p *projectModel) create() tea.Cmd {
	prof := strings.TrimSpace(p.prof)
	if err := checkProfileName(prof); err != nil {
		p.errMsg = err.Error()
		return nil
	}
	root := p.root
	// The door is only offered from "no tree here", but the guard is cheap and the rule is the
	// same one `init` enforces: a profile is not a folder for a tree.
	if name := scaffold.HermesProfile(root); name != "" {
		p.errMsg = fmt.Sprintf("%s is the Hermes profile %q — trees live outside it", root, name)
		return nil
	}
	var report scaffold.Report
	if len(p.found) > 0 {
		if len(p.choice) == 0 {
			p.errMsg = "pick at least one skill to declare"
			return nil
		}
		r, err := scaffold.Adopt(root, p.choice, prof)
		if err != nil {
			p.errMsg = err.Error()
			return nil
		}
		report = r
	} else {
		skill := strings.TrimSpace(p.skill)
		if err := scaffold.CheckSkillName(skill); err != nil {
			p.errMsg = err.Error()
			return nil
		}
		if existing := scaffold.Existing(root, skill); existing != "" {
			p.errMsg = fmt.Sprintf("%s already exists — pick another name", existing)
			return nil
		}
		wrote, kept, err := scaffold.Write(root, scaffold.Tree(skill, prof), false)
		if err != nil {
			p.errMsg = err.Error()
			return nil
		}
		report = scaffold.Report{Skills: []string{skill}, Wrote: wrote, Kept: kept}
	}

	if prof != "" {
		line, _, err := p.profileMgr.CreateIfMissing(prof, false)
		if err != nil {
			p.errMsg = err.Error()
			return nil
		}
		if _, err := p.profileMgr.Link(prof, root, false); err != nil {
			p.errMsg = err.Error()
			return nil
		}
		if line != "" {
			p.note = line
		}
	}

	p.created = root
	p.note = strings.TrimSpace(strings.Join([]string{
		p.note,
		fmt.Sprintf("%d skill(s) · %d file(s) written · profile %s", len(report.Skills), len(report.Wrote), dashOrNone(prof)),
	}, " · "))
	return tea.Quit
}

func dashOrNone(s string) string {
	if s == "" {
		return "none (whoever runs it)"
	}
	return s
}

func (p *projectModel) View() string {
	w := max(40, min(p.width, 96))
	var b strings.Builder
	if len(p.found) > 0 {
		b.WriteString(titleStyle.Render(" declare this folder") + "\n")
	} else {
		b.WriteString(titleStyle.Render(" start a project") + "\n")
	}
	b.WriteString(laneRule(w) + "\n\n")
	for _, line := range strings.Split(strings.TrimRight(p.form.View(), "\n"), "\n") {
		b.WriteString("  " + line + "\n")
	}
	b.WriteString("\n")
	if p.errMsg != "" {
		b.WriteString("  " + statusBad.Render("! "+p.errMsg) + "\n")
	}
	b.WriteString("  " + dimStyle.Render(p.plan()) + "\n\n")
	b.WriteString(hintLine([]hint{{"enter", "the next field"}, {"ctrl+s", "write it"}, {"esc", "quit"}}))
	return b.String()
}

// plan is what ctrl+s will do, in one line, before it does it.
func (p *projectModel) plan() string {
	var what string
	if len(p.found) > 0 {
		what = fmt.Sprintf("adds the missing eval sets to %d skill(s)", len(p.choice))
	} else {
		what = "writes " + scaffold.Summary(strings.TrimSpace(p.skill))
	}
	if prof := strings.TrimSpace(p.prof); prof != "" {
		if p.profileMgr.Live(prof) {
			return what + " · links profile " + prof + " to this folder"
		}
		return what + " · creates profile " + prof + " and links it to this folder"
	}
	return what
}
