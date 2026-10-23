package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Cell states on the board.
const (
	cellPending = ""
	cellRun     = "run"
	cellOK      = "ok"
	cellFail    = "fail"
	cellWait    = "wait"
	cellInfo    = "info"
	cellSkip    = "skip"
)

// boardColumns are the board's station columns, in order. "gate" is the human
// spec approval.
var boardColumns = []string{"triage", "spec", "gate", "build", "verify", "review", "ship"}

func columnFor(s Stage) string {
	if s == StageApproval {
		return "gate"
	}
	return string(s)
}

type cell struct {
	State   string
	Attempt int
}

// TraceRow records one station run, for the board and the PR description.
type TraceRow struct {
	Station  string
	Duration time.Duration
	Turns    int
	CostUSD  float64
	Result   string
}

// Ticket is one GitHub issue in flight.
type Ticket struct {
	mu sync.Mutex

	Number  int
	Title   string
	URL     string
	Branch  string
	Dir     string
	Base    string
	Stage   Stage
	Attempt int
	PR      string
	Outcome string
	Note    string
	Started time.Time
	Ended   time.Time
	CostUSD float64
	Queued  bool

	cells      map[string]cell
	trace      []TraceRow
	needsHuman bool
	useLocal   bool
	resumed    Stage
	holdsSlot  bool

	// herdr ids
	ws, rootTab, rootPane string
	rootUsed              bool
	tabs, panes           map[string]string
}

func newTicket(n int) *Ticket {
	return &Ticket{Number: n, Queued: true, cells: map[string]cell{}, tabs: map[string]string{}, panes: map[string]string{}}
}

func (t *Ticket) start() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Queued = false
	t.Started = time.Now()
}

func (t *Ticket) end(outcome string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Ended = time.Now()
	if outcome != "" {
		t.Outcome = outcome
	}
}

func (t *Ticket) setInfo(title, url string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Title, t.URL = title, url
}

func (t *Ticket) TitleSnapshot() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.Title
}

func (t *Ticket) setCell(column, state string, attempt int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cells[column] = cell{State: state, Attempt: attempt}
}

func (t *Ticket) setNote(format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Note = fmt.Sprintf(format, args...)
}

func (t *Ticket) setPane(key, tab, pane string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tabs[key], t.panes[key] = tab, pane
}

func (t *Ticket) pane(key string) (tab, pane string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.tabs[key], t.panes[key]
}

func (t *Ticket) addTrace(station string, m *StationMeta, result string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.CostUSD += m.CostUSD
	t.trace = append(t.trace, TraceRow{
		Station:  station,
		Duration: time.Duration(m.DurationMS) * time.Millisecond,
		Turns:    m.NumTurns,
		CostUSD:  m.CostUSD,
		Result:   result,
	})
}

// setLastResult replaces the result of the latest trace row with what the
// station decided, for example "needs info" or "request_changes".
func (t *Ticket) setLastResult(result string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.trace) > 0 {
		t.trace[len(t.trace)-1].Result = result
	}
}

func (t *Ticket) outPath(name string) string { return filepath.Join(t.Dir, ".factory", "out", name) }
func (t *Ticket) inPath(name string) string  { return filepath.Join(t.Dir, ".factory", "in", name) }

func (t *Ticket) specRel() string {
	return filepath.ToSlash(filepath.Join("specs", fmt.Sprint(t.Number), "SPEC.md"))
}

// ticketState is saved in the worktree so a restarted controller can resume
// a ticket where it stopped.
type ticketState struct {
	Stage   Stage     `json:"stage"`
	Attempt int       `json:"attempt"`
	Updated time.Time `json:"updated"`
}

func (t *Ticket) saveState() {
	t.mu.Lock()
	st := ticketState{Stage: t.Stage, Attempt: t.Attempt, Updated: time.Now()}
	dir := t.Dir
	t.mu.Unlock()
	if dir == "" {
		return
	}
	_ = writeJSONAtomic(filepath.Join(dir, ".factory", "state.json"), st)
}

func loadState(dir string) (ticketState, bool) {
	var st ticketState
	b, err := os.ReadFile(filepath.Join(dir, ".factory", "state.json"))
	if err != nil || json.Unmarshal(b, &st) != nil {
		return st, false
	}
	return st, true
}

// resumePoint maps the last saved stage to the step to restart from. A
// ticket that failed during build, verify or review restarts at build.
func resumePoint(st Stage) Stage {
	switch st {
	case StageSpec:
		return StageSpec
	case StageApproval:
		return StageApproval
	case StageBuild, StageVerify, StageReview:
		return StageBuild
	case StageShip:
		return StageShip
	}
	return StageTriage
}

// traceMarkdown renders the factory trace for the PR body and the issue.
func (t *Ticket) traceMarkdown() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var total time.Duration
	for _, r := range t.trace {
		total += r.Duration
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<details><summary>🏭 Factory trace: %d station runs · %s of agent time · $%.2f</summary>\n\n", len(t.trace), fmtDuration(total), t.CostUSD)
	b.WriteString("| Station | Time | Turns | Cost | Result |\n|---|---|---|---|---|\n")
	for _, r := range t.trace {
		turns, cost := "–", "–"
		if r.Turns > 0 {
			turns = fmt.Sprint(r.Turns)
		}
		if r.CostUSD > 0 {
			cost = fmt.Sprintf("$%.2f", r.CostUSD)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", r.Station, fmtDuration(r.Duration), turns, cost, r.Result)
	}
	b.WriteString("\nStations: triage → spec → human approval → build → verify (make verify, no AI) → review → ship. ")
	b.WriteString("Agents ran as headless Claude Code sessions with least-privilege tools; only the controller pushed or called GitHub.\n</details>\n")
	return b.String()
}

// snapshot is a copy of the ticket for the board.
type snapshot struct {
	Number  int
	Title   string
	Stage   Stage
	Cells   map[string]cell
	Outcome string
	Note    string
	PR      string
	Started time.Time
	Ended   time.Time
	CostUSD float64
	Queued  bool
}

func (t *Ticket) snap() snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	cells := make(map[string]cell, len(t.cells))
	for k, v := range t.cells {
		cells[k] = v
	}
	return snapshot{
		Number: t.Number, Title: t.Title, Stage: t.Stage, Cells: cells, Outcome: t.Outcome,
		Note: t.Note, PR: t.PR, Started: t.Started, Ended: t.Ended, CostUSD: t.CostUSD, Queued: t.Queued,
	}
}
