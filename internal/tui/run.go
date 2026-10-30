package tui

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"path/filepath"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"evalctl/internal/profile"
	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// runEventMsg carries one progress event out of a running lane.
type runEventMsg struct{ event runner.Event }

// runDoneMsg is the channel closing: the lane returned (see progress.err for why).
type runDoneMsg struct{}

// tickMsg re-renders the elapsed clock; nobody else refreshes a long arm.
type tickMsg time.Time

// ---------------------------------------------------------------- launcher

type launcherState struct {
	lane     string
	skills   []string // "" entry means every skill
	caseIDs  []int
	queryIDs []int
	target   string

	iteration int
	arms      string // "both", "with", "without"
	resume    bool
	force     bool
	verify    bool
	isolate   bool
	dryRun    bool
	split     string
	activity  string // full, tools or off — how much of the agent's stream is shown
	runs      int
	threshold float64
	maxTurns  int
	budget    int
	model     string

	cursor  int
	editing bool
	input   textinput.Model
}

// openLauncher prefills the form from the current selection (or the focused row, or the whole
// lane), so "run these three cases" is one key from the list.
func (m *Model) openLauncher() {
	// Selected rows are the target; with nothing selected the whole lane is, because a run that
	// silently measured only the focused row would be a lie about what was graded.
	var targets []store.Entry
	for _, e := range m.selectedAcrossLanes() {
		if e.Lane == m.lane {
			targets = append(targets, e)
		}
	}
	lane := m.lane
	l := &launcherState{
		lane: lane, arms: "both", resume: false, force: false, verify: true,
		isolate: true, split: m.filter.Split, runs: 0, threshold: 0, maxTurns: 0,
		activity: "full",
	}
	if l.split == "" {
		l.split = "all"
	}
	if lane == store.LaneOutput {
		l.budget = 1200
	} else {
		l.budget = 240
	}
	// Show the file's own knobs, not this file's copy of the defaults: the form is a claim about
	// what the run will measure.
	cfg := runner.New(m.store).TriggerConfigOf()
	l.runs = cfg.Runs
	l.threshold = cfg.Threshold
	l.maxTurns = runner.DefaultTriggerTurns

	skillSeen := map[string]bool{}
	if len(targets) > 0 {
		for _, e := range targets {
			if !skillSeen[e.Skill] {
				skillSeen[e.Skill] = true
				l.skills = append(l.skills, e.Skill)
			}
			if lane == store.LaneOutput {
				if sk := m.store.Skill(e.Skill); sk != nil && e.Index < len(sk.Output.Evals) {
					l.caseIDs = append(l.caseIDs, sk.Output.Evals[e.Index].ID)
				}
			} else {
				l.queryIDs = append(l.queryIDs, e.ID)
			}
		}
	}
	if len(targets) == 0 && m.skill != "" {
		// Nothing selected in a skill's view means that skill's whole lane: the list on screen is
		// already the filter, and running every other stage from here would grade far more than
		// the form said.
		l.skills = []string{m.skill}
	}
	switch n := len(targets); {
	case n == 0 && m.skill != "":
		l.target = "every row of " + m.skill + " in this lane"
	case n == 0:
		l.target = "every row in this lane"
	case n == 1:
		l.target = "the focused row"
	default:
		l.target = fmt.Sprintf("%d selected rows", n)
	}
	if len(l.skills) == len(m.store.SkillNames()) {
		l.skills = nil
	}

	ti := textinput.New()
	ti.Placeholder = "default model"
	ti.CharLimit = 64
	l.input = ti
	l.cursor = 1 // "target" above is a read-out, not a knob
	m.launcher = l
	m.mode = screenRunLauncher
	m.setStatus("", false)
}

// options is the form's row list: label plus the current value and how to change it.
func (m *Model) options() []struct{ label, value string } {
	l := m.launcher
	next := "next free"
	if l.iteration > 0 {
		next = fmt.Sprintf("%d", l.iteration)
	}
	onOff := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	rows := []struct{ label, value string }{
		{"target", l.target},
		{"iteration", next},
	}
	if l.lane == store.LaneTrigger {
		rows = append(rows,
			struct{ label, value string }{"split", l.split},
			struct{ label, value string }{"runs per query", fmt.Sprintf("%d", l.runs)},
			struct{ label, value string }{"threshold", fmt.Sprintf("%.2f", l.threshold)},
			struct{ label, value string }{"max turns", fmt.Sprintf("%d", l.maxTurns)},
			struct{ label, value string }{"resume", onOff(l.resume)},
			struct{ label, value string }{"force", onOff(l.force)},
			struct{ label, value string }{"dry run", onOff(l.dryRun)},
			struct{ label, value string }{"model", orDashText(l.model)},
			struct{ label, value string }{"activity", l.activity},
			struct{ label, value string }{"budget", fmt.Sprintf("%ds", l.budget)},
		)
		return rows
	}
	arms := "with_skill + without_skill"
	switch l.arms {
	case "with":
		arms = "with_skill only"
	case "without":
		arms = "without_skill only"
	}
	rows = append(rows,
		struct{ label, value string }{"arms", arms},
		struct{ label, value string }{"resume", onOff(l.resume)},
		struct{ label, value string }{"force", onOff(l.force)},
		struct{ label, value string }{"verify", onOff(l.verify)},
		struct{ label, value string }{"isolate", onOff(l.isolate)},
		struct{ label, value string }{"dry run", onOff(l.dryRun)},
		struct{ label, value string }{"model", orDashText(l.model)},
		struct{ label, value string }{"activity", l.activity},
		struct{ label, value string }{"budget", fmt.Sprintf("%ds", l.budget)},
	)
	return rows
}

func orDashText(s string) string {
	if s == "" {
		return "(default)"
	}
	return s
}

func (m *Model) rowIsModel() bool {
	l := m.launcher
	if l == nil {
		return false
	}
	rows := m.options()
	return rows[l.cursor].label == "model"
}

func (m *Model) updateLauncher(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	l := m.launcher
	if l == nil {
		m.mode = screenList
		return m, nil
	}
	if l.editing {
		switch msg.String() {
		case "esc":
			l.editing = false
			l.input.Blur()
			return m, nil
		case "enter":
			l.model = strings.TrimSpace(l.input.Value())
			l.editing = false
			l.input.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		l.input, cmd = l.input.Update(msg)
		return m, cmd
	}

	rows := m.options()
	switch msg.String() {
	case "esc", "q":
		m.launcher = nil
		m.mode = screenList
		m.setStatus("run cancelled — nothing started", false)
		return m, nil
	case "up", "k":
		if l.cursor > 0 {
			l.cursor--
		}
		return m, nil
	case "down", "j":
		if l.cursor < len(rows)-1 {
			l.cursor++
		}
		return m, nil
	case "enter":
		if m.rowIsModel() {
			l.editing = true
			l.input.SetValue(l.model)
			l.input.Focus()
			return m, textinput.Blink
		}
		return m, m.startRun()
	case "left", "h":
		m.adjustOption(-1)
		return m, nil
	case "right", "l", " ":
		m.adjustOption(1)
		return m, nil
	}
	return m, nil
}

func (m *Model) adjustOption(dir int) {
	l := m.launcher
	rows := m.options()
	label := rows[l.cursor].label
	toggle := func(b *bool) {
		if dir != 0 {
			*b = !*b
		}
	}
	switch label {
	case "iteration":
		l.iteration += dir
		if l.iteration < 0 {
			l.iteration = 0
		}
	case "arms":
		order := []string{"both", "with", "without"}
		index := 0
		for i, name := range order {
			if name == l.arms {
				index = i
			}
		}
		index = (index + dir + len(order)) % len(order)
		l.arms = order[index]
	case "resume":
		toggle(&l.resume)
	case "force":
		toggle(&l.force)
	case "verify":
		toggle(&l.verify)
	case "isolate":
		toggle(&l.isolate)
	case "dry run":
		toggle(&l.dryRun)
	case "budget":
		l.budget += 60 * dir
		if l.budget < 30 {
			l.budget = 30
		}
	case "split":
		order := []string{"all", "train", "validation"}
		index := 0
		for i, name := range order {
			if name == l.split {
				index = i
			}
		}
		l.split = order[(index+dir+len(order))%len(order)]
	case "runs per query":
		l.runs += dir
		if l.runs < 1 {
			l.runs = 1
		}
	case "threshold":
		l.threshold += 0.05 * float64(dir)
		if l.threshold < 0.05 {
			l.threshold = 0.05
		}
		if l.threshold > 0.95 {
			l.threshold = 0.95
		}
	case "activity":
		order := []string{"full", "tools", "off"}
		index := 0
		for i, name := range order {
			if name == l.activity {
				index = i
			}
		}
		l.activity = order[(index+dir+len(order))%len(order)]
	case "max turns":
		l.maxTurns += dir
		if l.maxTurns < 1 {
			l.maxTurns = 1
		}
	}
}

func (m *Model) launcherView() string {
	l := m.launcher
	if l == nil {
		return ""
	}
	var b strings.Builder
	laneName := "output lane — with skill vs without"
	if l.lane == store.LaneTrigger {
		laneName = "trigger lane — does the right skill load?"
	}
	b.WriteString(titleStyle.Render("run · "+laneName) + "\n\n")
	if l.lane == store.LaneTrigger && len(l.queryIDs) > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  queries: %v\n", l.queryIDs)))
	} else if l.lane == store.LaneOutput && len(l.caseIDs) > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  cases: %v · skills: %s\n", l.caseIDs,
			orDashText(strings.Join(l.skills, ", ")))))
	}
	b.WriteString("\n")
	for i, row := range m.options() {
		mark := "  "
		if i == l.cursor {
			mark = styleOn.Render("▸ ")
		}
		label := fmt.Sprintf("%-16s", row.label)
		value := row.value
		if l.editing && i == l.cursor && row.label == "model" {
			value = l.input.View()
		}
		if i == l.cursor {
			value = styleOn.Render(value)
		}
		b.WriteString(mark + label + " " + value + "\n")
	}
	b.WriteString("\n")
	if l.editing {
		b.WriteString(dimStyle.Render("  type the model, enter keeps it, esc drops it"))
	} else {
		b.WriteString(dimStyle.Render("  j k move · ← → change · enter run · esc cancel"))
	}
	b.WriteString("\n")
	if l.dryRun {
		b.WriteString(colDupe.Render("  dry run: prints the hermes commands and writes nothing"))
	}
	return box.Width(min(m.width-2, 100)).Render(b.String())
}

// optionsOf builds the runner's options from the form.
func (m *Model) optionsOf() runner.Options {
	l := m.launcher
	opts := runner.Options{
		Lane: l.lane, Skills: l.skills, CaseIDs: l.caseIDs, QueryIDs: l.queryIDs,
		Split: l.split, Model: l.model, Budget: time.Duration(l.budget) * time.Second,
		Resume: l.resume, Force: l.force, NoVerify: !l.verify, Isolate: l.isolate,
		DryRun: l.dryRun, Iteration: l.iteration, Runs: l.runs, Threshold: l.threshold,
		MaxTurns: l.maxTurns, Log: activityLevel(l.activity),
	}
	switch l.arms {
	case "with":
		opts.Arms = []string{runner.ArmWith}
	case "without":
		opts.Arms = []string{runner.ArmWithout}
	default:
		opts.Arms = append([]string(nil), runner.AllArms...)
	}
	return opts
}

// ---------------------------------------------------------------- progress

type progressArm struct {
	name   string
	status string // pending, running, done, failed, skipped
	detail string
}

type progressCase struct {
	name  string
	skill string
	arms  []progressArm
}

type progressState struct {
	lane    string
	title   string
	runRef  string
	started time.Time
	cases   []progressCase
	index   map[string]int
	log     []string
	logPath string // activity.log of the arm whose lines are newest
	logArm  string
	cancel  context.CancelFunc
	err     error
	summary string
}

// activityLevel reads the form's verbosity; "" is full, the same default the CLI uses.
func activityLevel(s string) runner.ActivityLevel {
	level, err := runner.ParseActivity(s)
	if err != nil || level == runner.ActivityUnset {
		return runner.ActivityFull
	}
	return level
}

func (p *progressState) caseAt(name string) *progressCase {
	if i, ok := p.index[name]; ok {
		return &p.cases[i]
	}
	p.index[name] = len(p.cases)
	p.cases = append(p.cases, progressCase{name: name})
	return &p.cases[len(p.cases)-1]
}

func (m *Model) startRun() tea.Cmd {
	l := m.launcher
	opts := m.optionsOf()
	ctx, cancel := context.WithCancel(context.Background())
	p := &progressState{lane: l.lane, started: time.Now(), index: map[string]int{}, cancel: cancel}
	p.title = fmt.Sprintf("%s lane · %s", l.lane, l.target)
	m.progress = p
	m.launcher = nil
	m.mode = screenRunProgress
	m.runActive = true

	ch := make(chan runner.Event, 128)
	m.runEvents = ch
	r := runner.New(m.store)
	r.Emit = func(e runner.Event) {
		select {
		case ch <- e:
		case <-ctx.Done():
		}
	}
	go func() {
		defer close(ch)
		var err error
		if opts.Lane == store.LaneTrigger {
			err = r.RunTrigger(ctx, opts)
		} else {
			err = r.RunQuality(ctx, opts)
		}
		if err != nil {
			ch <- runner.Event{Kind: "error", Lane: opts.Lane, Err: err, Message: err.Error()}
		}
	}()
	return tea.Batch(waitForEvent(ch), tickEvery())
}

func waitForEvent(ch chan runner.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return runDoneMsg{}
		}
		return runEventMsg{event: e}
	}
}

func tickEvery() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// applyEvent folds one progress event into the table. The runner is the source of truth for what
// happened; the table only mirrors it.
func (m *Model) applyEvent(e runner.Event) {
	p := m.progress
	if p == nil {
		return
	}
	switch e.Kind {
	case runner.EventStart:
		p.title = e.Message
		p.log = append(p.log, e.Message)
	case runner.EventCase:
		pc := p.caseAt(e.Case)
		pc.skill = e.Message
	case runner.EventArmStart:
		pc := p.caseAt(e.Case)
		pc.arms = append(pc.arms, progressArm{name: e.Arm, status: "running"})
	case runner.EventTriggerRun:
		pc := p.caseAt(e.Case)
		pc.arms = append(pc.arms, progressArm{name: e.Arm, status: "running"})
	case runner.EventArmDone:
		pc := p.caseAt(e.Case)
		status := "done"
		detail := e.Message
		if e.Result != nil {
			if !e.Result.Checks.Containment.OK {
				status = "failed"
			} else if e.Result.Timing.ExitCode != nil && *e.Result.Timing.ExitCode != 0 {
				status = "failed"
			}
			detail = e.Message
		}
		updateArm(pc, e.Arm, status, detail)
	case runner.EventActivity:
		// The arm's own stream: tool calls, results and its running text, folded straight into
		// the log box. Lines are kept plain and coloured at render time so a clipped line never
		// cuts an escape sequence.
		if text := strings.TrimSpace(e.Message); text != "" {
			p.log = append(p.log, text)
		}
		if e.Path != "" {
			p.logPath = e.Path
			p.logArm = strings.TrimSpace(e.Case + " " + e.Arm)
		}
	case runner.EventScored, runner.EventDone:
		p.summary = e.Message
		if e.Case != "" {
			p.runRef = e.Case
		}
		if e.Kind == runner.EventScored {
			p.log = append(p.log, e.Message)
		}
	case runner.EventMessage:
		text := strings.TrimSpace(e.Message)
		if text == "" {
			return
		}
		// A trigger observation reports through messages; keep the newest few lines only.
		if strings.HasPrefix(text, "run ") {
			if len(p.cases) > 0 {
				last := &p.cases[len(p.cases)-1]
				if len(last.arms) > 0 {
					last.arms[len(last.arms)-1].status = "done"
					last.arms[len(last.arms)-1].detail = text
					if strings.Contains(text, "no evidence") {
						last.arms[len(last.arms)-1].status = "failed"
					}
				}
			}
		}
		p.log = append(p.log, text)
	case "error":
		p.err = e.Err
		msg := e.Message
		if msg == "" && e.Err != nil {
			msg = e.Err.Error()
		}
		p.log = append(p.log, statusBad.Render(msg))
		m.setStatus(msg, true)
	}
	if len(p.log) > 200 {
		p.log = p.log[len(p.log)-200:]
	}
}

func updateArm(pc *progressCase, name, status, detail string) {
	for i := range pc.arms {
		if pc.arms[i].name == name {
			pc.arms[i].status = status
			pc.arms[i].detail = detail
			return
		}
	}
	pc.arms = append(pc.arms, progressArm{name: name, status: status, detail: detail})
}

func (m *Model) updateProgress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.progress
	if p == nil {
		m.mode = screenList
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		// The run keeps going: detaching from the table must not kill a 20-minute arm.
		m.mode = screenList
		if m.runActive {
			m.setStatus("run continues in the background — R opens the table, L the results", false)
		}
		return m, nil
	case "x":
		if m.runActive && p.cancel != nil {
			p.cancel()
			m.setStatus("cancelling — the running arm is killed, finished arms are kept", true)
		}
		return m, nil
	case "o":
		if !m.runActive {
			m.openReportFor(p.runRef)
		}
		return m, nil
	case "v":
		m.openActivity(p.logPath, p.logArm)
		return m, nil
	}
	return m, nil
}

// openActivity shows one arm's activity.log in full, following it while the arm still writes.
func (m *Model) openActivity(path, label string) {
	if path == "" {
		m.setStatus("no activity yet — nothing has reached the stream", false)
		return
	}
	text, err := os.ReadFile(path)
	if err != nil {
		m.setStatus("cannot read "+path+": "+err.Error(), true)
		return
	}
	v := &viewerState{kind: "activity", path: path, title: label + " · activity.log",
		lines: textLines(string(text))}
	v.refresh()
	v.follow = m.runActive
	m.viewer = v
	m.mode = screenViewer
}

func (m *Model) progressView() string {
	p := m.progress
	if p == nil {
		return ""
	}
	elapsed := time.Duration(0)
	if !p.started.IsZero() {
		elapsed = time.Since(p.started).Round(time.Second)
	}
	state := "running"
	if !m.runActive {
		state = "finished"
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("evalctl run · %s · %s", state, elapsed)) + "\n")
	b.WriteString(dimStyle.Render(p.title) + "\n\n")

	done, total, failures := 0, 0, 0
	for _, pc := range p.cases {
		name := pc.name
		if name == "" {
			name = "…"
		}
		line := fmt.Sprintf("  %-30s ", clipLine(name, 30))
		for _, arm := range pc.arms {
			total++
			switch arm.status {
			case "done":
				done++
				line += styleOn.Render("✓") + " "
			case "failed":
				done++
				failures++
				line += statusBad.Render("✗") + " "
			case "running":
				line += colDupe.Render("⟳") + " "
			default:
				line += dimStyle.Render("·") + " "
			}
			line += fmt.Sprintf("%-13s ", arm.name)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	counter := fmt.Sprintf("  %d/%d arms done", done, total)
	if failures > 0 {
		counter += fmt.Sprintf(" · %d failure(s)", failures)
	}
	b.WriteString(styleOn.Render(counter) + "\n")

	if len(p.log) > 0 {
		b.WriteString("\n" + dimStyle.Render("  activity") + "\n")
		start := max(0, len(p.log)-8)
		for _, line := range p.log[start:] {
			b.WriteString("  " + activityStyled(line, max(30, m.width-6)) + "\n")
		}
	}
	if p.err != nil {
		b.WriteString("\n" + statusBad.Render("  "+p.err.Error()) + "\n")
	} else if p.summary != "" && !m.runActive {
		b.WriteString("\n" + styleOn.Render("  "+p.summary) + "\n")
	}
	foot := "  esc back · o open report · v activity"
	if m.runActive {
		foot = "  esc back (run keeps going) · x cancel · v activity"
	}
	b.WriteString("\n" + dimStyle.Render(foot))
	return b.String()
}

// activityStyled clips a plain activity line and then colours it: clipping an already-coloured
// line would cut an escape sequence in half. Lines the runner did not produce are left alone.
func activityStyled(line string, width int) string {
	clipped := clipLine(line, width)
	switch {
	case strings.HasPrefix(clipped, "▸"):
		return activityTool.Render(clipped)
	case strings.HasPrefix(clipped, "✓"):
		return statusOk.Render(clipped)
	case strings.HasPrefix(clipped, "✗"):
		return statusBad.Render(clipped)
	case strings.HasPrefix(clipped, "│"):
		return dimStyle.Render(clipped)
	}
	return clipped
}

// openReportFor shows the report.md the run just wrote.
func (m *Model) openReportFor(ref string) {
	if ref == "" {
		m.setStatus("no report yet — the run did not finish an iteration", true)
		return
	}
	r := runner.New(m.store)
	lane := ""
	if m.progress != nil {
		lane = m.progress.lane
	}
	info, err := r.ResolveRun(ref, lane)
	if err != nil {
		m.setStatus(err.Error(), true)
		return
	}
	text, err := runner.ReadReport(r.ReportPath(info))
	if err != nil {
		m.setStatus("no report.md yet — the iteration may not have finished", true)
		return
	}
	m.viewer = newMarkdownViewer(info.Ref()+" · report.md", text, m.width)
	m.mode = screenViewer
}

// ---------------------------------------------------------------- results browser

type runsState struct {
	infos  []runner.RunInfo
	cursor int
}

func (m *Model) openRuns() {
	m.runsList = &runsState{infos: runner.New(m.store).ListRuns()}
	m.mode = screenRuns
}

func (m *Model) updateRuns(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	list := m.runsList
	if list == nil {
		m.mode = m.back()
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.mode = m.back()
		return m, nil
	case "r":
		list.infos = runner.New(m.store).ListRuns()
		m.setStatus("results reloaded", false)
		return m, nil
	case "up", "k":
		if list.cursor > 0 {
			list.cursor--
		}
		return m, nil
	case "down", "j":
		if list.cursor < len(list.infos)-1 {
			list.cursor++
		}
		return m, nil
	case "g":
		list.cursor = 0
		return m, nil
	case "G":
		list.cursor = max(0, len(list.infos)-1)
		return m, nil
	case "enter", "v":
		if list.cursor < len(list.infos) {
			info := list.infos[list.cursor]
			text, err := runner.ReadReport(runner.New(m.store).ReportPath(info))
			if err != nil {
				m.setStatus(info.Ref()+" has no report.md yet", true)
				return m, nil
			}
			m.viewer = newMarkdownViewer(info.Ref()+" · report.md", text, m.width)
			m.mode = screenViewer
		}
		return m, nil
	case "a":
		if list.cursor < len(list.infos) {
			m.openRunActivity(list.infos[list.cursor])
		}
		return m, nil
	}
	return m, nil
}

// openRunActivity concatenates every arm's activity.log under an iteration: a finished run can be
// read back the way it was watched.
func (m *Model) openRunActivity(info runner.RunInfo) {
	var files []string
	for _, pattern := range []string{"*/*/activity.log", "*/*/*/activity.log"} {
		if matches, err := filepath.Glob(filepath.Join(info.Path, pattern)); err == nil {
			files = append(files, matches...)
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		m.setStatus(info.Ref()+" has no activity.log — run it again with activity on", true)
		return
	}
	lines := []string{}
	for _, file := range files {
		label, err := filepath.Rel(info.Path, file)
		if err != nil {
			label = file
		}
		lines = append(lines, "", "  "+titleStyle.Render(strings.TrimSuffix(label, "/activity.log")))
		if b, err := os.ReadFile(file); err == nil {
			lines = append(lines, textLines(string(b))...)
		}
	}
	m.viewer = &viewerState{kind: "run-activity",
		title: fmt.Sprintf("%s · activity · %d arms", info.Ref(), len(files)), lines: lines}
	m.mode = screenViewer
}

func (m *Model) runsView() string {
	list := m.runsList
	if list == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("results · %d iteration(s)", len(list.infos))) + "\n")
	b.WriteString(dimStyle.Render("  one workspace per skill — enter opens the report, r reloads") + "\n\n")
	if len(list.infos) == 0 {
		b.WriteString(dimStyle.Render("  nothing yet — R starts a run") + "\n")
	} else {
		b.WriteString(dimStyle.Render(fmt.Sprintf("     %-16s %-16s %-8s %-6s %-6s %-14s %s",
			"WORKSPACE", "RUN", "LANE", "CASES", "RUNS", "PASS", "STARTED")) + "\n")
		for i, info := range list.infos {
			mark := "  "
			if i == list.cursor {
				mark = styleOn.Render("▸ ")
			}
			pass := "—"
			if info.PassRate != nil {
				pass = fmt.Sprintf("%.0f%% (%d/%d)", *info.PassRate*100, info.Passed, info.Judged)
			}
			workspace := info.Skill
			if workspace == "" {
				workspace = "—"
			}
			line := fmt.Sprintf("%s%-16s %-16s %-8s %-6d %-6d %-14s %s", mark, workspace, info.Name,
				info.Lane, info.Cases, info.Runs, pass, info.Started)
			if i == list.cursor {
				line = selected(line)
			}
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("\n" + dimStyle.Render("  esc back · enter report · a activity · r reload"))
	return b.String()
}

// textLines turns a log (activity.log, a live tail) into viewable lines. Logs are plain text by
// nature, so they skip the markdown renderer; a *report* goes through newMarkdownViewer instead.
func textLines(text string) []string {
	raw := strings.Split(strings.TrimRight(text, "\n"), "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		out = append(out, "  "+strings.ReplaceAll(line, "\t", "  "))
	}
	return out
}

// ---------------------------------------------------------------- profiles

// ensureProfiles brings all four profiles to their intended state. Only reachable from the
// profiles viewer, and only ever on the four known names.
func (m *Model) ensureProfiles() string {
	man := profile.New(m.store.Root, m.store.SkillNames())
	count := 0
	for _, name := range []string{profile.Real, profile.With, profile.Without, profile.Trigger} {
		if _, err := man.Ensure(name, false); err != nil {
			return "ensure failed: " + err.Error()
		}
		count++
	}
	return fmt.Sprintf("ensure: %d profile(s) provisioned — P shows the re-check", count)
}

// openProfiles shows the four profiles as check judges them; `u` runs ensure on whatever fails.
func (m *Model) openProfiles() {
	man := profile.New(m.store.Root, m.store.SkillNames())
	rows, problems := man.Check([]string{profile.Real, profile.With, profile.Without, profile.Trigger})
	var lines []string
	lines = append(lines, "  "+lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("%-24s %-7s %-9s %-9s %s",
		"PROFILE", "SKILLS", "MEMORIES", "CONTRACT", "CWD")))
	for _, row := range rows {
		contract := statusBad.Render("STALE")
		if row.ContractCurrent {
			contract = statusOk.Render("ok")
		}
		cwd := "—"
		if row.TerminalCwd != "" {
			cwd = "pinned"
		}
		lines = append(lines, fmt.Sprintf("  %-24s %-7d %-9d %-9s %s", row.Profile, len(row.Skills),
			len(row.Memories), contract, cwd))
	}
	lines = append(lines, "")
	if len(problems) == 0 {
		lines = append(lines, "  "+statusOk.Render("all profiles in their intended state"))
	} else {
		for _, p := range problems {
			lines = append(lines, "  "+statusBad.Render("✗ ")+p)
		}
		lines = append(lines, "", "  "+dimStyle.Render("u runs ensure: creates what is missing, renders, syncs, scrubs"))
	}
	m.viewer = &viewerState{kind: "profiles", title: "profiles · check", lines: lines}
	m.mode = screenViewer
}
