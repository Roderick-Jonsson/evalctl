package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"evalctl/internal/runner"
	"evalctl/internal/store"
)

// The run: one screen, and the keys go to it.
//
// A run is the one action in the loop that spends money and time, so it used to have a screen in
// front of it that stated the candidate, the rows, the runs per query, the agent calls and where it
// would write — and it was the only place the knobs lived. That screen is gone: the knobs live where
// the set lives, in the pane beside the two sections, and the file carries them, so the workbench's
// forward key goes straight to the run itself. What the run is doing is the screen: one line per
// query, a mark per attempt, and the arm's own stream folded in as it arrives.

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
	// cancelled is the operator's x, as opposed to an error: a batch stops at the candidate it was
	// on, and the queue behind it is dropped rather than walked through.
	cancelled bool
	logPath   string
	logArm    string
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

// ---------------------------------------------------------------- starting

// startTrainingRun launches the train run for the wording the loop is waiting on. There is nothing
// between the key and the spend: how many runs each query gets is the set's own number, changed in
// the section pane beside it, and the candidate is the one the workbench is editing. The live screen
// is the whole of the run — what it is measuring is its title and the ledger it writes.
func (m *Model) startTrainingRun() tea.Cmd {
	f := m.flow
	if f == nil || f.lab == nil {
		return nil
	}
	if f.lab.TrainRows == 0 {
		m.setStatus("the train set is empty — the section's first step defines it", true)
		return nil
	}
	cand := 0
	if pend := f.pending(); pend != nil {
		cand = pend.ID
	} else if it := f.latest(); it != nil {
		cand = it.Cand.ID
	}
	scope := "training run"
	if cand != 0 {
		scope = fmt.Sprintf("training run · candidate #%d", cand)
	}
	m.run = &runState{
		kind:  runTrain,
		cand:  cand,
		runs:  f.lab.Runs,
		dry:   f.dry,
		scope: scope,
		hint:  "the train set is what a revision may learn from — the validation set is not read here",
	}
	return m.startRun()
}

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
			msg := err.Error()
			// A cancel is an operator's decision, not a fault: the runner stopped before it scored,
			// so the only thing worth saying is that the rest of the batch was left alone.
			if errors.Is(err, context.Canceled) {
				msg = "cancelled — the remaining queries were not run and nothing was scored"
			}
			ch <- runner.Event{Kind: "error", Lane: opts.Lane, Err: err, Message: msg}
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
		if errors.Is(e.Err, context.Canceled) {
			p.cancelled = true
		}
		msg := e.Message
		if msg == "" && e.Err != nil {
			msg = e.Err.Error()
		}
		p.log = append(p.log, statusBad.Render(msg))
		m.setStatus(msg, !p.cancelled)
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
//
// A cancelled run is the one exception, and it is not a detail: the operator's x is meant to stop
// the batch, so the queue is dropped here instead of advanced. Chaining anyway turned one cancel
// into a batch that walked the rest of the queue with a live context each time — which is what a
// validation run that never stops looks like from the outside.
func (m *Model) finishRun() tea.Cmd {
	if m.run == nil || m.run.live == nil {
		return nil
	}
	m.run.live.done = true
	m.run.live.cancel = nil
	m.reload()
	if m.run.gen && m.flow != nil && m.flow.gen != nil {
		g := m.flow.gen
		if m.run.live.cancelled {
			m.flow.gen = nil
			done, total := g.at+1, len(g.queue)
			m.run.live.summary = fmt.Sprintf("batch stopped at candidate %d of %d — nothing was scored, so nothing entered the ranking", done, total)
			m.setStatus(m.run.live.summary, true)
			return nil
		}
		g.done++
		g.at++
		if g.at < len(g.queue) {
			return m.startValidationStep()
		}
		m.run.live.summary = fmt.Sprintf("validation run finished · %s measured — enter for the ranking", plural(g.done, "iteration"))
		m.flow.gen = nil
		m.setStatus(m.run.live.summary, false)
		return nil
	}
	if m.run.live.cancelled {
		m.run.live.summary = "cancelled — the run stopped before it scored, so nothing new entered the iteration table"
		m.setStatus(m.run.live.summary, true)
		return nil
	}
	if m.run.kind == runTrain {
		m.setStatus("training run finished — the result is the iteration table", false)
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
			p.cancelled = true
			p.cancel()
			m.setStatus("cancelled — the batch stops here; nothing was scored", true)
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

// leaveRun puts the flow back on the screen the run was for: a finished training run lands on the
// workbench, a finished validation batch on the ranking it just filled in — with the cursor on the
// wording the new rates recommend.
func (m *Model) leaveRun() tea.Cmd {
	kind := m.run.kind
	gen := m.run.gen
	m.run = nil
	m.mode = screenFlow
	switch {
	case gen:
		m.flow.sec, m.flow.step = secGeneralization, 2
		m.flow.enterStep(m)
	case kind == runTrain:
		m.flow.sec, m.flow.step = secIteration, 1
	default:
		m.flow.sec, m.flow.step = secGeneralization, 2
		m.flow.enterStep(m)
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

// unvalidatedIDs is the work the ranking step has to do before it can rank anything: measured
// candidates with no validation rate, or with one over part of the set — a batch stopped halfway
// still owes its remaining queries, and the ranking screen is where they are run from.
func (m *Model) unvalidatedIDs() []int {
	var out []int
	for _, it := range m.flow.iters {
		if !m.flow.complete(it.valid()) {
			out = append(out, it.Cand.ID)
		}
	}
	return out
}

func (m *Model) startGeneralization() tea.Cmd {
	if m.flow.lab == nil || m.flow.lab.ValidationRows == 0 {
		m.setStatus("no validation queries — the section's first step defines them", true)
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
		dry:   m.flow.dry,
		gen:   true,
		scope: fmt.Sprintf("validation run · candidate #%d", id),
		hint:  fmt.Sprintf("candidate %d of %d — the queue runs each measured wording in turn", g.at+1, len(g.queue)),
	}
	r.live = newLiveRun(r.scope, r.dry)
	m.run = r
	m.mode = screenRunLive
	opts := runner.Options{
		Lane:   store.LaneTrigger,
		Skills: []string{m.skill},
		Split:  store.SplitValidation,
		Runs:   r.runs,
		Budget: runner.DefaultTriggerBudget,
		DryRun: r.dry,
		Log:    runner.ActivityFull,
	}
	return m.startTriggerRun(opts)
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
