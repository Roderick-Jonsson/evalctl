package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// The run: what it will cost, then what it is doing.
//
// Two screens, because a run is the one action in the loop that spends money and time. The first
// states the candidate, the rows, the runs per query, the number of agent calls and where it writes
// — and it is the only place the knobs live, so nothing about a run is changed by accident from a
// screen that was about something else. The second is the run itself: one line per query, a mark per
// attempt, and the arm's own stream folded in as it arrives.

type runKind int

const (
	runTrain runKind = iota
	runValidation
	runHoldout
)

func (k runKind) String() string {
	switch k {
	case runValidation:
		return "validation run"
	case runHoldout:
		return "holdout run"
	}
	return "training run"
}

type runState struct {
	kind  runKind
	cand  int // the candidate measured; 0 means "whatever SKILL.md holds"
	runs  int
	dry   bool
	scope string
	hint  string
	gen   bool // this run is one step of the generalization queue
	live  *liveRun
}

// liveRun is the run in flight: what has happened to each query, and the newest lines of the arm's
// own log. It is a plain struct and not a tea.Model so the event fold stays testable without a
// terminal.
type liveRun struct {
	title   string
	started time.Time
	cases   []liveCase
	index   map[string]int
	log     []string
	summary string
	err     error
	done    bool
	dry     bool
	cancel  context.CancelFunc
	logPath string
	logArm  string
}

type liveCase struct {
	name string
	runs []liveArm
}

type liveArm struct {
	name   string
	status string // running, done, failed
	detail string
}

type runEventMsg struct{ event runner.Event }
type runDoneMsg struct{}
type tickMsg time.Time

func newLiveRun(title string, dry bool) *liveRun {
	return &liveRun{title: title, started: time.Now(), index: map[string]int{}, dry: dry}
}

func (p *liveRun) caseAt(name string) *liveCase {
	if i, ok := p.index[name]; ok {
		return &p.cases[i]
	}
	p.index[name] = len(p.cases)
	p.cases = append(p.cases, liveCase{name: name})
	return &p.cases[len(p.cases)-1]
}

func (p *liveRun) stateText() string {
	if p.err != nil {
		return p.err.Error()
	}
	if p.done {
		return p.summary
	}
	return fmt.Sprintf("running %s · %s elapsed", pluralOf(len(p.cases), "query", "queries"), shortDuration(time.Since(p.started)))
}

// ---------------------------------------------------------------- the ask

// openRunAsk opens the training run for the wording the loop is waiting on.
func (m *Model) openRunAsk() tea.Cmd {
	f := m.flow
	cand := pendingID(f.pending(), f.latest())
	m.run = &runState{
		kind:  runTrain,
		cand:  cand,
		runs:  f.lab.Runs,
		scope: "training run",
		hint:  "nothing has been spent yet — enter starts it, d only prints the commands",
	}
	m.mode = screenRunAsk
	return nil
}

func (m *Model) askBody(h int) []string { return m.run.askBody(m, h) }

func (r *runState) askBody(m *Model, h int) []string {
	f := m.flow
	rows := f.lab.TrainRows
	split := store.SplitTrain
	title := fmt.Sprintf("%s · candidate #%d", r.scope, r.cand)
	switch r.kind {
	case runValidation:
		rows, split = f.lab.ValidationRows, store.SplitValidation
	case runHoldout:
		rows, split = f.lab.HoldoutRows, store.SplitHoldout
	}
	d := &docWriter{w: max(28, m.width-4)}
	d.title(title)
	d.lead(askLead(r.kind))
	d.rule()
	d.fact("scope", fmt.Sprintf("%s of %s, %d runs each = %d agent calls",
		pluralOf(rows, split+" query", split+" queries"), m.skill, r.runs, rows*r.runs))
	d.fact("budget", fmt.Sprintf("%s per run · profile eval-vcc-trigger", shortDuration(runner.DefaultTriggerBudget)))
	d.fact("writes", fmt.Sprintf("trigger-workspace/trigger-%d/", nextTrigger(m.store)))
	if r.cand != 0 && r.kind != runHoldout {
		d.fact("measures", fmt.Sprintf("candidate #%d — it is written into %s/SKILL.md first, because the router reads the file", r.cand, m.skill))
	}
	d.fact("dry run", onOff(r.dry))
	d.blank()
	d.para("The run is the only thing in this tool that costs money. Everything it records lands in the iteration directory; nothing is written into the skill itself except the description being measured.")
	d.action("enter", "start it")
	return fitRows(d.lines, h)
}

func askLead(k runKind) string {
	switch k {
	case runValidation:
		return "The validation set is run once per measured wording, and it is the only thing that ranks them. It is read after the wording is frozen — never while it is being revised."
	case runHoldout:
		return "The fresh queries are measured once, after the winner is applied. They are not part of the loop and never steer it: their only job is to say whether the shipped wording works on something nobody tuned against."
	}
	return "The train set is what a revision may learn from. Validation is not run here: it judges the wording in step 4, and measuring it now would let the loop learn from it."
}

func (r *runState) askHints() []hint {
	return []hint{
		{"← →", "runs per query"},
		{"d", "dry run"},
		{"enter", "start"},
		{"esc", "back"},
	}
}

func (m *Model) updateRunAsk(msg tea.KeyMsg) tea.Cmd {
	r := m.run
	if r == nil {
		return nil
	}
	switch msg.String() {
	case "left", "h":
		r.runs = max(1, r.runs-1)
	case "right", "l":
		r.runs = min(9, r.runs+1)
	case "d":
		r.dry = !r.dry
	case "enter":
		return m.startRun()
	}
	return nil
}

// ---------------------------------------------------------------- starting

func (m *Model) startRun() tea.Cmd {
	r := m.run
	if r == nil {
		return nil
	}
	// A candidate is measured by being in SKILL.md: the router reads the file, so this is the step
	// that puts it there. It happens when the operator asks to run, not when the form opened, so a
	// cancelled form changes nothing about the tree.
	if r.cand != 0 && !r.dry && r.kind != runHoldout {
		if msg, err := runner.New(m.store).PrepareMeasure(m.skill, r.cand); err != nil {
			m.run = nil
			m.mode = screenFlow
			m.setStatus(err.Error(), true)
			return nil
		} else if msg != "" {
			m.setStatus(msg, false)
		}
		m.reload()
	}
	split := store.SplitTrain
	switch r.kind {
	case runValidation:
		split = store.SplitValidation
	case runHoldout:
		split = store.SplitHoldout
	}
	opts := runner.Options{
		Lane:   store.LaneTrigger,
		Skills: []string{m.skill},
		Split:  split,
		Runs:   r.runs,
		Budget: runner.DefaultTriggerBudget,
		DryRun: r.dry,
		Log:    runner.ActivityFull,
	}
	r.live = newLiveRun(r.scope, r.dry)
	m.mode = screenRunLive
	return m.startTriggerRun(opts)
}

// startTriggerRun is the one place a trigger run is launched, so a training run, a validation run
// and a holdout run cannot differ in how they are wired to the event stream.
func (m *Model) startTriggerRun(opts runner.Options) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	if m.run != nil && m.run.live != nil {
		m.run.live.cancel = cancel
	} else {
		cancel()
	}
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
		if err := r.RunTrigger(ctx, opts); err != nil {
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
	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// applyEvent folds one runner event into the live view. The runner is the only thing that knows what
// happened; this function decides only how it reads.
func (m *Model) applyEvent(e runner.Event) {
	p := m.run.live
	if p == nil {
		return
	}
	switch e.Kind {
	case runner.EventStart:
		p.title = e.Message
		p.log = append(p.log, e.Message)
	case runner.EventCase:
		pc := p.caseAt(e.Case)
		p.log = append(p.log, e.Message)
		_ = pc
	case runner.EventTriggerRun, runner.EventArmStart:
		pc := p.caseAt(e.Case)
		pc.runs = append(pc.runs, liveArm{name: e.Arm, status: "running"})
	case runner.EventArmDone:
		pc := p.caseAt(e.Case)
		status := "done"
		if e.Result != nil {
			if !e.Result.Checks.Containment.OK {
				status = "failed"
			} else if e.Result.Timing.ExitCode != nil && *e.Result.Timing.ExitCode != 0 {
				status = "failed"
			}
		}
		setArm(pc, e.Arm, status, e.Message)
	case runner.EventActivity:
		if text := strings.TrimSpace(e.Message); text != "" {
			p.log = append(p.log, text)
		}
		if e.Path != "" {
			p.logPath = e.Path
			p.logArm = strings.TrimSpace(e.Case + " " + e.Arm)
		}
	case runner.EventScored, runner.EventDone:
		p.summary = e.Message
		if e.Kind == runner.EventScored {
			p.log = append(p.log, e.Message)
		}
	case runner.EventMessage:
		text := strings.TrimSpace(e.Message)
		if text == "" {
			return
		}
		// A trigger observation reports through messages: it is the only place a run's verdict on
		// one query is said in words the operator can read.
		if strings.HasPrefix(text, "run ") && len(p.cases) > 0 {
			last := &p.cases[len(p.cases)-1]
			if len(last.runs) > 0 {
				status := "done"
				if strings.Contains(text, "no evidence") {
					status = "failed"
				}
				last.runs[len(last.runs)-1].status = status
				last.runs[len(last.runs)-1].detail = text
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

func setArm(pc *liveCase, name, status, detail string) {
	for i := range pc.runs {
		if pc.runs[i].name == name {
			pc.runs[i].status, pc.runs[i].detail = status, detail
			return
		}
	}
	pc.runs = append(pc.runs, liveArm{name: name, status: status, detail: detail})
}

// finishRun is where a run ends. A generalization queue chains: the next candidate's validation run
// starts from here, because the queue is one batch and stopping between candidates would ask the
// same question three times.
func (m *Model) finishRun() tea.Cmd {
	if m.run == nil || m.run.live == nil {
		return nil
	}
	m.run.live.done = true
	m.run.live.cancel = nil
	m.reload()
	if m.run.gen && m.flow != nil && m.flow.gen != nil {
		g := m.flow.gen
		g.done++
		g.at++
		if g.at < len(g.queue) {
			return m.startValidationStep()
		}
		m.run.live.summary = fmt.Sprintf("validation run finished · %s measured", plural(g.done, "iteration"))
		m.flow.gen = nil
		m.setStatus(m.run.live.summary, false)
		return nil
	}
	if m.run.kind == runTrain {
		m.setStatus("training run finished — the result is rung 2", false)
	} else {
		m.setStatus("run finished", false)
	}
	return nil
}

func (m *Model) updateRunLive(msg tea.KeyMsg) tea.Cmd {
	r := m.run
	p := r.live
	switch msg.String() {
	case "x":
		if !p.done && p.cancel != nil {
			p.cancel()
			m.setStatus("cancelled — the queries already measured keep their results", true)
		}
	case "v":
		if p.logPath != "" {
			m.openActivityDoc(p.logPath, p.logArm)
		}
	case "r":
		if p.done {
			m.reload()
		}
	case "enter":
		if p.done {
			return m.leaveRun()
		}
	}
	return nil
}

// leaveRun puts the flow back on the rung the run was for: a finished training run lands on its
// result, a finished generalization lands on the comparison.
func (m *Model) leaveRun() tea.Cmd {
	kind := m.run.kind
	gen := m.run.gen
	m.run = nil
	m.mode = screenFlow
	switch {
	case gen:
		m.flow.step = StepGeneralize
		m.flow.res = 0
	case kind == runTrain:
		m.flow.step, m.flow.sub = StepIterate, subResult
	default:
		m.flow.step = StepGeneralize
	}
	return nil
}

func (r *runState) liveHints() []hint {
	if r.live.done {
		return []hint{{"enter", "back to the loop"}, {"v", "activity log"}}
	}
	return []hint{{"x", "cancel"}, {"v", "activity log"}, {"q", "quit"}}
}

func (r *runState) liveBody(m *Model, h int) []string {
	p := r.live
	lines := []string{
		"",
		"  " + titleStyle.Render(p.title),
		"  " + dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, m.width-4)))),
	}
	// One row per query, then the newest lines of the arm's stream underneath.
	const logRows = 8
	room := max(1, h-3-logRows)
	for i, c := range p.cases {
		if i >= room {
			break
		}
		lines = append(lines, "  "+m.liveCaseLine(c))
	}
	for len(lines) < 3+min(len(p.cases), room) {
		lines = append(lines, "")
	}
	if p.dry {
		lines = append(lines, "  "+badgeWarn.Render("dry run")+" "+dimStyle.Render("the commands above were printed; nothing was written"))
	} else {
		lines = append(lines, "  "+dimStyle.Render(strings.Repeat(glyphs.Rule, max(10, min(96, m.width-4)))))
	}
	log := p.log
	if len(log) > logRows {
		log = log[len(log)-logRows:]
	}
	for _, line := range log {
		lines = append(lines, "  "+activityStyled(line, m.width-4))
	}
	if p.err != nil {
		lines = append(lines, "  "+statusBad.Render("! "+p.err.Error()))
	}
	return fitRows(lines, h)
}

func (m *Model) liveCaseLine(c liveCase) string {
	marks := make([]string, 0, len(c.runs))
	running := false
	for _, a := range c.runs {
		switch a.status {
		case "done":
			marks = append(marks, styleOn.Render(glyphs.Done))
		case "failed":
			marks = append(marks, statusBad.Render(glyphs.Failed))
		case "running":
			marks = append(marks, cursorMark.Render(glyphs.Running))
			running = true
		default:
			marks = append(marks, dimStyle.Render("·"))
		}
	}
	_ = running
	return colText.Render(fitTo(c.name, min(30, max(12, m.width/3)))) + "  " + strings.Join(marks, " ")
}

// activityStyled colours one line of the arm's stream: a tool call and its result are the two things
// worth telling apart at a glance, and everything else is the agent's own prose.
func activityStyled(line string, width int) string {
	line = ellipsis(line, max(10, width))
	switch {
	case strings.Contains(line, glyphs.Failed):
		return statusBad.Render(line)
	case strings.Contains(line, glyphs.Done):
		return styleOn.Render(line)
	case strings.Contains(line, "▸") || strings.Contains(line, ">"):
		return activityTool.Render(line)
	}
	return dimStyle.Render(line)
}

// openActivityDoc shows a finished arm's log in the document reader.
func (m *Model) openActivityDoc(path, label string) {
	lines, err := readLog(path)
	if err != nil {
		m.setStatus(err.Error(), true)
		return
	}
	m.doc = &docState{scope: "activity", title: "activity · " + label, lines: lines}
	m.backTo = screenRunLive
	m.mode = screenDoc
}

// ---------------------------------------------------------------- the queue

// genState is the generalization batch: every measured wording that has not met the validation set
// yet, run in the order the loop wrote them.
type genState struct {
	queue []int
	at    int
	done  int
}

// unvalidatedIDs is the work step 4 has to do before it can rank anything: measured candidates with
// no validation measurement, in iteration order.
func (m *Model) unvalidatedIDs() []int {
	var out []int
	for _, it := range m.flow.iters {
		if it.Valid == nil {
			out = append(out, it.Cand.ID)
		}
	}
	return out
}

func (m *Model) startGeneralization() tea.Cmd {
	if m.flow.lab == nil || m.flow.lab.ValidationRows == 0 {
		m.setStatus("no validation queries — step 2 defines what judges the wording", true)
		return nil
	}
	ids := m.unvalidatedIDs()
	if len(ids) == 0 {
		m.setStatus("every measured iteration has been validated", false)
		return nil
	}
	m.flow.gen = &genState{queue: ids}
	return m.startValidationStep()
}

// startValidationStep writes one candidate into SKILL.md and runs the validation set against it.
func (m *Model) startValidationStep() tea.Cmd {
	g := m.flow.gen
	if g == nil || g.at >= len(g.queue) {
		return nil
	}
	id := g.queue[g.at]
	if msg, err := runner.New(m.store).PrepareMeasure(m.skill, id); err != nil {
		m.flow.gen = nil
		m.setStatus(err.Error(), true)
		return nil
	} else if msg != "" {
		m.setStatus(msg, false)
	}
	m.reload()
	r := &runState{
		kind:  runValidation,
		cand:  id,
		runs:  m.flow.lab.Runs,
		gen:   true,
		scope: fmt.Sprintf("validation run · candidate #%d", id),
		hint:  fmt.Sprintf("candidate %d of %d — the queue runs each measured wording in turn", g.at+1, len(g.queue)),
	}
	r.live = newLiveRun(r.scope, false)
	m.run = r
	m.mode = screenRunLive
	opts := runner.Options{
		Lane:   store.LaneTrigger,
		Skills: []string{m.skill},
		Split:  store.SplitValidation,
		Runs:   r.runs,
		Budget: runner.DefaultTriggerBudget,
		Log:    runner.ActivityFull,
	}
	return m.startTriggerRun(opts)
}

// onOff is a boolean as the run screen says it.
func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// shortDuration says 1200s as 20m: the operator is deciding whether to wait, not reading a spec.
func shortDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d%time.Minute == 0 && d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return d.Round(time.Minute).String()
}
