package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Config holds the controller settings.
type Config struct {
	Repo           string
	GHRepo         string
	Base           string
	Label          string
	Parallel       int
	Poll           time.Duration
	AutoApprove    bool
	Approvers      []string
	MaxAttempts    int
	StationTimeout time.Duration
	Model          string
	MemoryMCP      string
	Project        string
	CopilotReview  bool
	Draft          bool
	Runner         string
	Issues         []int
	Once           bool
	DryRun         bool
	Fake           bool
	Smoke          bool
	NoBoard        bool
	ClaudeBin      string
	HerdrBin       string
	PaneWarmup     time.Duration
	Self           string
}

func parseRunFlags(args []string) (*Config, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfg := &Config{}
	fs.StringVar(&cfg.Repo, "repo", ".", "path to the main checkout of the target repo")
	fs.StringVar(&cfg.GHRepo, "gh-repo", "", "GitHub repo OWNER/NAME (default: detected with gh)")
	fs.StringVar(&cfg.Base, "base", "main", "base branch for worktrees and PRs")
	fs.StringVar(&cfg.Label, "label", "factory", "intake label: open issues with this label enter the factory")
	fs.IntVar(&cfg.Parallel, "parallel", 2, "tickets worked at the same time (tickets waiting for approval do not count)")
	fs.DurationVar(&cfg.Poll, "poll", 15*time.Second, "how often to poll GitHub for new tickets")
	fs.BoolVar(&cfg.AutoApprove, "auto-approve", false, "skip the human spec-approval gate")
	approvers := fs.String("approvers", "", "comma-separated GitHub logins allowed to /approve (default: anyone who can comment)")
	fs.IntVar(&cfg.MaxAttempts, "max-attempts", 3, "build → verify → review attempts before a human takes over")
	fs.DurationVar(&cfg.StationTimeout, "station-timeout", 20*time.Minute, "timeout for one station run")
	fs.StringVar(&cfg.Model, "model", "", "model for the agent stations, passed to claude --model (default: your Claude Code default)")
	fs.StringVar(&cfg.MemoryMCP, "memory-mcp", "ai-memory", "MCP server name of ai-memory in Claude Code (check with: claude mcp list); empty disables")
	fs.StringVar(&cfg.Project, "project", "", "optional GitHub Project OWNER/NUMBER whose \"Factory Stage\" field follows each ticket")
	fs.BoolVar(&cfg.CopilotReview, "copilot-review", true, "request a GitHub Copilot code review on each PR")
	fs.BoolVar(&cfg.Draft, "draft", false, "open PRs as drafts")
	fs.StringVar(&cfg.Runner, "runner", "auto", "where stations run: auto (herdr when inside herdr), herdr, or local")
	issues := fs.String("issue", "", "comma-separated issue numbers to run now, without the intake label")
	fs.BoolVar(&cfg.Once, "once", false, "poll once, finish those tickets, then exit")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "no GitHub writes and no push: log them instead")
	fs.BoolVar(&cfg.Fake, "fake", false, "fake agents with canned outputs: rehearse the plumbing without AI calls")
	fs.BoolVar(&cfg.Smoke, "smoke", false, "verify also deploys to the local kind cluster (make smoke)")
	fs.BoolVar(&cfg.NoBoard, "no-board", false, "print plain log lines instead of the live board")
	fs.StringVar(&cfg.ClaudeBin, "claude", "claude", "Claude Code binary")
	fs.StringVar(&cfg.HerdrBin, "herdr", "herdr", "herdr binary")
	fs.DurationVar(&cfg.PaneWarmup, "pane-warmup", 1500*time.Millisecond, "wait after creating a herdr tab before typing into its shell")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	for _, a := range splitList(*approvers) {
		cfg.Approvers = append(cfg.Approvers, strings.TrimPrefix(a, "@"))
	}
	for _, s := range splitList(*issues) {
		n, err := strconv.Atoi(strings.TrimPrefix(s, "#"))
		if err != nil {
			return nil, fmt.Errorf("bad issue number %q", s)
		}
		cfg.Issues = append(cfg.Issues, n)
	}
	if cfg.Parallel < 1 {
		cfg.Parallel = 1
	}
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 1
	}
	return cfg, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Controller is the factory's reconcile loop.
type Controller struct {
	cfg    *Config
	gh     *GH
	runner Runner
	local  Runner
	proj   *Project
	board  *Board

	mu      sync.Mutex
	tickets map[int]*Ticket
	order   []int
	active  map[int]bool
	done    map[int]bool
	sem     chan struct{}
	wg      sync.WaitGroup
}

func cmdRun(args []string) error {
	cfg, err := parseRunFlags(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c, err := newController(ctx, cfg)
	if err != nil {
		return err
	}
	return c.Run(ctx)
}

func newController(ctx context.Context, cfg *Config) (*Controller, error) {
	root, err := repoRoot(ctx, cfg.Repo)
	if err != nil {
		return nil, fmt.Errorf("-repo must point at a git checkout: %w", err)
	}
	cfg.Repo = root
	if cfg.GHRepo == "" {
		if cfg.GHRepo, err = detectRepo(ctx, root); err != nil {
			return nil, err
		}
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if s, err := filepath.EvalSymlinks(self); err == nil {
		self = s
	}
	cfg.Self = self
	if err := os.MkdirAll(filepath.Join(root, ".factory"), 0o755); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(filepath.Join(root, ".factory", "controller.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	board := newBoard(os.Stdout, !cfg.NoBoard && isTTY(os.Stdout), logFile)
	board.color = isTTY(os.Stdout) && os.Getenv("NO_COLOR") == ""
	c := &Controller{
		cfg:     cfg,
		board:   board,
		tickets: map[int]*Ticket{},
		active:  map[int]bool{},
		done:    map[int]bool{},
		sem:     make(chan struct{}, cfg.Parallel),
	}
	c.gh = &GH{Repo: cfg.GHRepo, Dir: root, DryRun: cfg.DryRun, Log: board.Log}
	c.local = &LocalRunner{log: board.Log}
	herdrRunner := &HerdrRunner{h: &Herdr{Bin: cfg.HerdrBin}, warmup: cfg.PaneWarmup, log: board.Log}
	inHerdr := os.Getenv("HERDR_ENV") == "1"
	switch cfg.Runner {
	case "local":
		c.runner = c.local
	case "herdr":
		if !inHerdr {
			return nil, fmt.Errorf("%w: start the controller from a herdr pane, or use -runner local", errNoHerdr)
		}
		c.runner = herdrRunner
	case "auto", "":
		if inHerdr {
			c.runner = herdrRunner
		} else {
			c.runner = c.local
		}
	default:
		return nil, fmt.Errorf("unknown -runner %q (use auto, herdr or local)", cfg.Runner)
	}
	if cfg.Project != "" {
		if c.proj, err = loadProject(ctx, c.gh, cfg.Project); err != nil {
			board.Log("project board updates are off: %v", err)
		}
	}
	var flags []string
	for name, on := range map[string]bool{"auto-approve": cfg.AutoApprove, "fake agents": cfg.Fake, "dry-run": cfg.DryRun, "kind smoke": cfg.Smoke} {
		if on {
			flags = append(flags, name)
		}
	}
	sort.Strings(flags)
	board.title = fmt.Sprintf("%s · runner: %s · intake label: %s", cfg.GHRepo, c.runner.Name(), cfg.Label)
	if len(flags) > 0 {
		board.title += " · " + strings.Join(flags, ", ")
	}
	board.snapshot = c.snapshot
	return c, nil
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// Run is the reconcile loop: watch GitHub, start tickets, repeat.
func (c *Controller) Run(ctx context.Context) error {
	boardCtx, stopBoard := context.WithCancel(context.Background())
	boardDone := make(chan struct{})
	go func() { c.board.Loop(boardCtx); close(boardDone) }()
	finish := func() {
		stopBoard()
		<-boardDone
		c.board.Final()
	}

	c.board.Log("factory up · repo %s · %d station slots · stations run in %s", c.cfg.GHRepo, c.cfg.Parallel, c.runner.Name())
	if c.runner.Name() == "local" {
		c.board.Log("stations run as background processes; logs in .worktrees/issue-N/.factory/logs (start inside herdr for live panes)")
	}
	if len(c.cfg.Issues) > 0 {
		for _, n := range c.cfg.Issues {
			c.launch(ctx, n)
		}
		c.wg.Wait()
		finish()
		return nil
	}
	c.board.Log("watching for open issues labeled %q every %s", c.cfg.Label, c.cfg.Poll)
	c.reconcile(ctx)
	if c.cfg.Once {
		c.wg.Wait()
		finish()
		return nil
	}
	tick := time.NewTicker(c.cfg.Poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			c.board.Log("shutting down: waiting for local stations to stop (herdr stations keep running in their panes)")
			c.wg.Wait()
			finish()
			return nil
		case <-tick.C:
			c.reconcile(ctx)
		}
	}
}

func (c *Controller) reconcile(ctx context.Context) {
	issues, err := c.gh.ListIssues(ctx, c.cfg.Label)
	if err != nil {
		if ctx.Err() == nil {
			c.board.Log("GitHub poll failed: %v", err)
		}
		return
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	for _, is := range issues {
		if _, terminal := is.TerminalStage(); terminal {
			continue
		}
		c.launch(ctx, is.Number)
	}
}

// launch starts a ticket unless it is already in flight.
func (c *Controller) launch(ctx context.Context, n int) {
	c.mu.Lock()
	if c.active[n] || (c.cfg.DryRun && c.done[n]) {
		c.mu.Unlock()
		return
	}
	c.active[n] = true
	t, ok := c.tickets[n]
	if !ok || !t.Ended.IsZero() {
		t = newTicket(n)
		if !ok {
			c.order = append(c.order, n)
		}
		c.tickets[n] = t
	}
	c.mu.Unlock()

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.process(ctx, t)
		c.mu.Lock()
		delete(c.active, n)
		c.done[n] = true
		c.mu.Unlock()
	}()
}

func (c *Controller) snapshot() []snapshot {
	c.mu.Lock()
	ts := make([]*Ticket, 0, len(c.order))
	for _, n := range c.order {
		ts = append(ts, c.tickets[n])
	}
	c.mu.Unlock()
	out := make([]snapshot, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.snap())
	}
	return out
}

// acquire takes a station slot for the ticket. Tickets give the slot back
// while they wait for a human, so a pending approval never blocks the line.
func (c *Controller) acquire(ctx context.Context, t *Ticket) bool {
	select {
	case c.sem <- struct{}{}:
		t.mu.Lock()
		t.holdsSlot = true
		t.mu.Unlock()
		return true
	case <-ctx.Done():
		return false
	}
}

func (c *Controller) release(t *Ticket) {
	t.mu.Lock()
	held := t.holdsSlot
	t.holdsSlot = false
	t.mu.Unlock()
	if held {
		<-c.sem
	}
}

// process drives one ticket through the line.
func (c *Controller) process(ctx context.Context, t *Ticket) {
	if !c.acquire(ctx, t) {
		return
	}
	defer c.release(t)
	t.start()

	is, err := c.gh.ViewIssue(ctx, t.Number)
	if err != nil {
		c.board.Log("#%d cannot read the issue: %v", t.Number, err)
		t.end("✗ cannot read the issue")
		return
	}
	t.setInfo(is.Title, is.URL)
	c.board.Log("#%d picked up: %s", t.Number, is.Title)

	t.Branch = fmt.Sprintf("factory/issue-%d", t.Number)
	t.Dir = filepath.Join(c.cfg.Repo, ".worktrees", fmt.Sprintf("issue-%d", t.Number))
	created, base, err := ensureWorktree(ctx, c.cfg.Repo, t.Dir, t.Branch, c.cfg.Base)
	if err != nil {
		c.fail(ctx, t, StageTriage, "cannot create the worktree: %v", err)
		return
	}
	t.Base = base
	if err := writeInputs(t, is); err != nil {
		c.fail(ctx, t, StageTriage, "cannot write the ticket inputs: %v", err)
		return
	}
	if err := writeJSONAtomic(stationDefaultsPath(t.Dir), stationDefaults{
		Timeout: c.cfg.StationTimeout.String(), MemoryMCP: c.cfg.MemoryMCP, Claude: c.cfg.ClaudeBin,
		Model: c.cfg.Model, Fake: c.cfg.Fake, Smoke: c.cfg.Smoke,
	}); err != nil {
		c.fail(ctx, t, StageTriage, "cannot write the station settings: %v", err)
		return
	}

	from := StageTriage
	if !created {
		if st, ok := loadState(t.Dir); ok {
			from = resumePoint(st.Stage)
		}
	}
	if from != StageTriage {
		t.resumed = from
		c.board.Log("#%d resuming at %s (its worktree already exists; run 'factory reset %d' to start over)", t.Number, from, t.Number)
		for _, col := range boardColumns {
			if col == columnFor(from) {
				break
			}
			t.setCell(col, cellOK, 0)
		}
	}

	if err := c.runner.Open(ctx, t); err != nil {
		t.useLocal = true
		c.board.Log("#%d herdr workspace not created (%v); its stations run as background processes", t.Number, err)
	}

	steps := []struct {
		at Stage
		fn func(context.Context, *Ticket) bool
	}{
		{StageTriage, c.triage},
		{StageSpec, c.spec},
		{StageApproval, c.approval},
		{StageBuild, c.buildLoop},
		{StageShip, c.ship},
	}
	started := false
	for _, s := range steps {
		if s.at == from {
			started = true
		}
		if !started {
			continue
		}
		if ctx.Err() != nil {
			t.end("stopped")
			return
		}
		if !s.fn(ctx, t) {
			return
		}
	}
}

type triageResult struct {
	Decision   string   `json:"decision"`
	Type       string   `json:"type"`
	Size       string   `json:"size"`
	Risk       string   `json:"risk"`
	Summary    string   `json:"summary"`
	Areas      []string `json:"areas"`
	Questions  []string `json:"questions"`
	MemoryHits []string `json:"memory_hits"`
}

type specResult struct {
	Status             string   `json:"status"`
	AcceptanceCriteria int      `json:"acceptance_criteria"`
	Files              []string `json:"files"`
	MemoryHits         []string `json:"memory_hits"`
}

type buildResult struct {
	Status     string   `json:"status"`
	Summary    string   `json:"summary"`
	TestsAdded []string `json:"tests_added"`
	Notes      string   `json:"notes"`
}

type reviewFinding struct {
	Severity string `json:"severity"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Message  string `json:"message"`
}

type reviewResult struct {
	Verdict  string          `json:"verdict"`
	Summary  string          `json:"summary"`
	Findings []reviewFinding `json:"findings"`
	Lessons  []string        `json:"lessons"`
}

type shipResult struct {
	Title string `json:"title"`
}

// Station 1: triage.
func (c *Controller) triage(ctx context.Context, t *Ticket) bool {
	key := stationName(StageTriage, 1)
	meta, err := c.runStation(ctx, t, StageTriage, 1)
	if !c.stationOK(ctx, t, StageTriage, key, meta, err) {
		return false
	}
	c.guard(ctx, t, StageTriage)
	var tr triageResult
	if err := readJSON(t.outPath("triage.json"), &tr); err != nil {
		return c.fail(ctx, t, StageTriage, "the triage station wrote no valid .factory/out/triage.json (%v)", err)
	}
	c.comment(ctx, t, "🔎 Triage", readText(t.outPath("triage.md"), "**Decision:** "+tr.Decision+"\n\n"+tr.Summary))
	if strings.EqualFold(strings.TrimSpace(tr.Decision), "needs-info") {
		t.setLastResult("needs info")
		t.setCell("triage", cellInfo, 0)
		c.runner.Mark(ctx, t, key, "?")
		c.board.Log("#%d needs info: triage asked %d question(s) on the issue instead of guessing", t.Number, len(tr.Questions))
		c.setStage(ctx, t, StageNeedsInfo)
		c.runner.Notify(ctx, fmt.Sprintf("#%d needs info", t.Number), "Triage asked questions on the issue", "request")
		c.closeTicket(ctx, t, "? needs info")
		return false
	}
	t.setLastResult("ready")
	c.markOK(ctx, t, "triage", key, 0)
	c.board.Log("#%d triage: ready · %s · size %s · risk %s", t.Number, orDash(tr.Type), orDash(tr.Size), orDash(tr.Risk))
	return true
}

// Station 2: spec.
func (c *Controller) spec(ctx context.Context, t *Ticket) bool {
	key := stationName(StageSpec, 1)
	meta, err := c.runStation(ctx, t, StageSpec, 1)
	if !c.stationOK(ctx, t, StageSpec, key, meta, err) {
		return false
	}
	c.guard(ctx, t, StageSpec, filepath.ToSlash(filepath.Dir(t.specRel()))+"/")
	if _, err := os.Stat(filepath.Join(t.Dir, t.specRel())); err != nil {
		c.runner.Mark(ctx, t, key, "✗")
		return c.fail(ctx, t, StageSpec, "the spec station wrote no %s", t.specRel())
	}
	var sr specResult
	_ = readJSON(t.outPath("spec.json"), &sr)
	t.setLastResult(fmt.Sprintf("%d ACs", sr.AcceptanceCriteria))
	c.markOK(ctx, t, "spec", key, 0)
	c.board.Log("#%d spec: %d acceptance criteria · %d team-memory page(s) used", t.Number, sr.AcceptanceCriteria, len(sr.MemoryHits))
	return true
}

// The human gate: a person approves the spec before any code is written.
func (c *Controller) approval(ctx context.Context, t *Ticket) bool {
	specText, err := os.ReadFile(filepath.Join(t.Dir, t.specRel()))
	if err != nil {
		return c.fail(ctx, t, StageApproval, "the spec is missing: %v", err)
	}
	specKey := stationName(StageSpec, 1)
	if c.cfg.AutoApprove {
		c.comment(ctx, t, "📐 Spec (auto-approved)", specComment(t, string(specText), false))
		t.setCell("gate", cellSkip, 0)
		c.board.Log("#%d spec auto-approved (-auto-approve)", t.Number)
	} else {
		c.setStage(ctx, t, StageApproval)
		since := time.Now().Add(-45 * time.Second)
		if t.resumed == StageApproval {
			if fi, err := os.Stat(filepath.Join(t.Dir, t.specRel())); err == nil {
				since = fi.ModTime().Add(-45 * time.Second)
			}
		} else {
			c.comment(ctx, t, "📐 Spec ready for your approval", specComment(t, string(specText), true))
		}
		t.setCell("gate", cellWait, 0)
		t.setNote("waiting for /approve")
		c.runner.Attention(ctx, t, specKey, true, "Approve the spec on GitHub: comment /approve")
		c.runner.Notify(ctx, fmt.Sprintf("#%d spec ready", t.Number), "Comment /approve on the issue to start the build", "request")
		c.board.Log("#%d spec posted on GitHub · waiting for /approve (or the %s label)", t.Number, labelApproved)

		c.release(t) // free the slot while a human decides
		ok, who, note, err := c.waitApproval(ctx, t, since)
		c.runner.Attention(ctx, t, specKey, false, "")
		if err != nil {
			t.end("stopped")
			return false
		}
		if !c.acquire(ctx, t) {
			t.end("stopped")
			return false
		}
		t.setNote("")
		if !ok {
			msg := "Spec rejected by " + who
			if note != "" {
				msg += ": " + note
			}
			// A retry after a rejection rewrites the spec: the spec station
			// reads the rejection comment from .factory/in/issue.md.
			t.mu.Lock()
			t.Stage = StageSpec
			t.mu.Unlock()
			t.saveState()
			return c.fail(ctx, t, StageApproval, "%s", msg)
		}
		t.setCell("gate", cellOK, 0)
		c.board.Log("#%d spec approved by %s · build starts", t.Number, who)
	}
	if _, err := commitPaths(ctx, t.Dir, fmt.Sprintf("docs(spec): approved spec for #%d", t.Number), filepath.Dir(t.specRel())); err != nil {
		c.board.Log("#%d could not commit the spec: %v", t.Number, err)
	}
	return true
}

func (c *Controller) waitApproval(ctx context.Context, t *Ticket, since time.Time) (approved bool, who, note string, err error) {
	if c.cfg.DryRun {
		c.board.Log("#%d dry-run: approving the spec automatically in 5s", t.Number)
		select {
		case <-ctx.Done():
			return false, "", "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
		return true, "dry-run", "", nil
	}
	tick := time.NewTicker(8 * time.Second)
	defer tick.Stop()
	for {
		is, err := c.gh.ViewIssue(ctx, t.Number)
		if err == nil {
			ok, rejected, who, note := approvalFrom(is, since, c.cfg.Approvers)
			if ok {
				return true, who, "", nil
			}
			if rejected {
				return false, who, note, nil
			}
		} else if ctx.Err() == nil {
			c.board.Log("#%d approval poll failed: %v", t.Number, err)
		}
		select {
		case <-ctx.Done():
			return false, "", "", ctx.Err()
		case <-tick.C:
		}
	}
}

// Stations 3-5: build, verify, review, with loops back to build.
func (c *Controller) buildLoop(ctx context.Context, t *Ticket) bool {
	for attempt := 1; attempt <= c.cfg.MaxAttempts; attempt++ {
		t.mu.Lock()
		t.Attempt = attempt
		t.mu.Unlock()

		// Build: an agent writes tests first, then code, then commits.
		key := stationName(StageBuild, attempt)
		meta, err := c.runStation(ctx, t, StageBuild, attempt)
		if !c.stationOK(ctx, t, StageBuild, key, meta, err) {
			return false
		}
		_ = os.Remove(t.inPath("feedback.md"))
		var br buildResult
		_ = readJSON(t.outPath("build.json"), &br)
		if strings.EqualFold(br.Status, "blocked") {
			c.comment(ctx, t, "🧱 Build blocked", orDash(br.Notes))
			return c.fail(ctx, t, StageBuild, "the builder says the spec cannot be built as written: %s", orDash(br.Notes))
		}
		if made, err := commitAll(ctx, t.Dir, fmt.Sprintf("chore: commit remaining build changes (#%d)", t.Number)); err != nil {
			c.board.Log("#%d could not commit leftover changes: %v", t.Number, err)
		} else if made {
			c.board.Log("#%d committed changes the builder left uncommitted", t.Number)
		}
		t.setLastResult("built")
		c.markOK(ctx, t, "build", key, attempt)

		// Verify: a deterministic gate, not an AI opinion.
		key = stationName(StageVerify, attempt)
		meta, err = c.runStation(ctx, t, StageVerify, attempt)
		if !c.stationOK(ctx, t, StageVerify, key, meta, err) {
			return false
		}
		var vr verifyResult
		if err := readJSON(t.outPath("verify.json"), &vr); err != nil {
			return c.fail(ctx, t, StageVerify, "verify wrote no result: %v", err)
		}
		if vr.Status != "pass" {
			t.setLastResult("fail")
			t.setCell("verify", cellFail, attempt)
			c.runner.Mark(ctx, t, key, "✗")
			if attempt == c.cfg.MaxAttempts {
				return c.fail(ctx, t, StageVerify, "make verify still fails after %d attempts. See .factory/out/verify.log in the worktree.", attempt)
			}
			c.board.Log("#%d verify failed (attempt %d of %d) · the log goes back to the builder", t.Number, attempt, c.cfg.MaxAttempts)
			_ = writeFeedback(t, "Verification failed",
				"`make verify` failed after your change. Fix the cause, not the symptom, then run `make verify` again.\n\nLast lines of the log:\n\n```\n"+tailFile(t.outPath("verify.log"), 80)+"\n```")
			continue
		}
		res := "pass"
		if vr.KindDryRun == "skipped" {
			res = "pass (kind dry-run skipped)"
		}
		t.setLastResult(res)
		c.markOK(ctx, t, "verify", key, attempt)

		// Review: a fresh agent that did not write the code.
		key = stationName(StageReview, attempt)
		meta, err = c.runStation(ctx, t, StageReview, attempt)
		if !c.stationOK(ctx, t, StageReview, key, meta, err) {
			return false
		}
		c.guard(ctx, t, StageReview)
		var rr reviewResult
		if err := readJSON(t.outPath("review.json"), &rr); err != nil {
			return c.fail(ctx, t, StageReview, "the review station wrote no valid .factory/out/review.json (%v)", err)
		}
		c.comment(ctx, t, fmt.Sprintf("🧐 Review, attempt %d: %s", attempt, rr.Verdict), readText(t.outPath("review.md"), rr.Summary))
		t.setLastResult(rr.Verdict)
		if strings.EqualFold(rr.Verdict, "approve") {
			c.markOK(ctx, t, "review", key, attempt)
			c.board.Log("#%d review: approved (%d finding(s), %d lesson(s) saved to team memory)", t.Number, len(rr.Findings), len(rr.Lessons))
			return true
		}
		t.setCell("review", cellFail, attempt)
		c.runner.Mark(ctx, t, key, "✗")
		if attempt == c.cfg.MaxAttempts {
			c.board.Log("#%d review still requests changes after %d attempts · shipping a draft PR for a human", t.Number, attempt)
			t.mu.Lock()
			t.needsHuman = true
			t.mu.Unlock()
			return true
		}
		c.board.Log("#%d review requested changes (%d finding(s)) · back to build", t.Number, len(rr.Findings))
		_ = writeFeedback(t, "Reviewer requested changes", formatFindings(rr))
	}
	return true
}

// Station 6: ship. The agent writes the PR text; the controller pushes and
// opens the PR.
func (c *Controller) ship(ctx context.Context, t *Ticket) bool {
	key := stationName(StageShip, 1)
	meta, err := c.runStation(ctx, t, StageShip, 1)
	if !c.stationOK(ctx, t, StageShip, key, meta, err) {
		return false
	}
	c.guard(ctx, t, StageShip)
	t.setLastResult("PR written")
	var sr shipResult
	_ = readJSON(t.outPath("ship.json"), &sr)
	title := strings.TrimSpace(sr.Title)
	if title == "" {
		title = fmt.Sprintf("%s (#%d)", t.TitleSnapshot(), t.Number)
	}
	t.mu.Lock()
	needsHuman := t.needsHuman
	t.mu.Unlock()
	if needsHuman {
		title = "[needs human] " + title
	}
	body := readText(t.outPath("pr.md"), "")
	if !strings.Contains(body, fmt.Sprintf("#%d", t.Number)) {
		body += fmt.Sprintf("\n\nCloses #%d\n", t.Number)
	}
	if needsHuman {
		body = "> ⚠️ The review station still requested changes after the last attempt. A human should take this over.\n\n" + body
	}
	body += "\n\n" + t.traceMarkdown()
	bodyPath := t.outPath("pr-final.md")
	if err := os.WriteFile(bodyPath, []byte(body), 0o644); err != nil {
		return c.fail(ctx, t, StageShip, "cannot write the PR body: %v", err)
	}
	if _, err := commitAll(ctx, t.Dir, fmt.Sprintf("chore: commit remaining changes (#%d)", t.Number)); err != nil {
		c.board.Log("#%d could not commit leftover changes: %v", t.Number, err)
	}
	if n, err := commitsAhead(ctx, t.Dir, t.Base); err == nil && n == 0 {
		return c.fail(ctx, t, StageShip, "the branch has no commits ahead of %s", t.Base)
	}

	url := ""
	if c.cfg.DryRun {
		c.board.Log("#%d dry-run: would push %s and open a PR titled %q", t.Number, t.Branch, title)
		url = "(dry-run, no PR)"
	} else {
		if err := pushBranch(ctx, t.Dir, t.Branch); err != nil {
			return c.fail(ctx, t, StageShip, "git push failed: %v", err)
		}
		url, _ = c.gh.FindPR(ctx, t.Branch)
		if url == "" {
			if url, err = c.gh.CreatePR(ctx, c.cfg.Base, t.Branch, title, bodyPath, c.cfg.Draft || needsHuman); err != nil {
				return c.fail(ctx, t, StageShip, "gh pr create failed: %v", err)
			}
		} else if err := c.gh.EditPR(ctx, url, title, bodyPath); err != nil {
			c.board.Log("#%d could not update the existing PR: %v", t.Number, err)
		}
		if c.cfg.CopilotReview {
			if err := c.gh.RequestReview(ctx, url, "@copilot"); err != nil {
				c.board.Log("#%d Copilot review not requested (needs gh 2.88+ and Copilot code review): %v", t.Number, err)
			} else {
				c.board.Log("#%d GitHub Copilot review requested", t.Number)
			}
		}
	}
	t.mu.Lock()
	t.PR = url
	t.mu.Unlock()
	c.markOK(ctx, t, "ship", key, 0)
	c.comment(ctx, t, "🚀 Shipped", fmt.Sprintf("Pull request: %s\n\nA human reviews and merges. The factory never merges.\n\n%s", url, t.traceMarkdown()))
	c.setStage(ctx, t, StageShipped)
	c.board.Log("#%d shipped · %s", t.Number, url)
	c.runner.Notify(ctx, fmt.Sprintf("#%d shipped", t.Number), url, "done")
	c.closeTicket(ctx, t, "✓ "+shortPR(url))
	return true
}

// runStation starts one station and waits for its result.
func (c *Controller) runStation(ctx context.Context, t *Ticket, st Stage, attempt int) (*StationMeta, error) {
	c.setStage(ctx, t, st)
	key := stationName(st, attempt)
	_ = os.Remove(metaPath(t.Dir, st))
	_ = os.Remove(startedPath(t.Dir, st))
	// Station settings live in .factory/station.json, so the command typed
	// into the pane stays short and readable.
	runID := fmt.Sprintf("%x", time.Now().UnixNano()&0xffffffffff)
	argv := []string{c.cfg.Self, "station", "-dir", t.Dir, "-run-id", runID, string(st), strconv.Itoa(t.Number)}
	run := StationRun{
		Key:         key,
		Stage:       st,
		Argv:        argv,
		LogPath:     filepath.Join(t.Dir, ".factory", "logs", fmt.Sprintf("%s-%d.log", st, attempt)),
		MetaPath:    metaPath(t.Dir, st),
		StartedPath: startedPath(t.Dir, st),
	}
	t.setCell(columnFor(st), cellRun, attempt)
	t.setNote(key + " running")
	c.board.Log("#%d → %s", t.Number, key)

	runner := c.runner
	if t.useLocal {
		runner = c.local
	}
	if err := runner.Start(ctx, t, run); err != nil {
		if runner == c.local {
			return nil, err
		}
		c.board.Log("#%d %s: herdr launch failed (%v); running it as a background process", t.Number, key, err)
		t.useLocal = true
		if err := c.local.Start(ctx, t, run); err != nil {
			return nil, err
		}
	}
	return waitMeta(ctx, run.MetaPath, c.cfg.StationTimeout+2*time.Minute)
}

func (c *Controller) stationOK(ctx context.Context, t *Ticket, st Stage, key string, meta *StationMeta, err error) bool {
	if err != nil {
		if ctx.Err() != nil {
			t.end("stopped")
			return false
		}
		c.runner.Mark(ctx, t, key, "✗")
		return c.fail(ctx, t, st, "%s did not finish: %v", key, err)
	}
	ok := meta.ExitCode == 0 && !meta.IsError
	result := "ok"
	if !ok {
		result = "error"
	}
	t.addTrace(key, meta, result)
	if !ok {
		c.runner.Mark(ctx, t, key, "✗")
		return c.fail(ctx, t, st, "%s failed: %s", key,
			firstNonEmpty(meta.Error, truncate(meta.Result, 400), fmt.Sprintf("exit code %d", meta.ExitCode)))
	}
	return true
}

// guard reverts edits a station is not allowed to make. Triage, review and
// ship must not change code; spec may only write its spec. The deny rules in
// .claude/settings.json are the first line; this check is the second.
func (c *Controller) guard(ctx context.Context, t *Ticket, st Stage, allow ...string) {
	files, err := changedFiles(ctx, t.Dir)
	if err != nil {
		c.board.Log("#%d guardrail check skipped: %v", t.Number, err)
		return
	}
	bad := outsideAllowed(files, allow)
	if len(bad) == 0 {
		return
	}
	names := make([]string, 0, len(bad))
	for _, f := range bad {
		names = append(names, f.Path)
	}
	c.board.Log("#%d guardrail: the %s station may not edit %s · change discarded", t.Number, st, strings.Join(names, ", "))
	if err := discardChanges(ctx, t.Dir, bad); err != nil {
		c.board.Log("#%d could not discard the change: %v", t.Number, err)
	}
}

func (c *Controller) setStage(ctx context.Context, t *Ticket, st Stage) {
	t.mu.Lock()
	t.Stage = st
	url := t.URL
	t.mu.Unlock()
	if !isTerminal(st) {
		t.saveState()
	}
	if err := c.gh.SetStage(ctx, t.Number, st); err != nil && ctx.Err() == nil {
		c.board.Log("#%d label update failed: %v", t.Number, err)
	}
	if c.proj != nil {
		if err := c.proj.SetStage(ctx, url, st); err != nil && ctx.Err() == nil {
			c.board.Log("#%d project board update failed: %v", t.Number, err)
		}
	}
}

func (c *Controller) markOK(ctx context.Context, t *Ticket, column, key string, attempt int) {
	t.setCell(column, cellOK, attempt)
	c.runner.Mark(ctx, t, key, "✓")
}

const commentFooter = "Posted by the factory controller"

func (c *Controller) comment(ctx context.Context, t *Ticket, title, body string) {
	full := fmt.Sprintf("### %s\n\n%s\n\n<sub>🏭 %s · branch `%s`</sub>\n", title, strings.TrimSpace(body), commentFooter, t.Branch)
	if err := c.gh.Comment(ctx, t.Number, full); err != nil && ctx.Err() == nil {
		c.board.Log("#%d comment failed: %v", t.Number, err)
	}
}

// fail stops a ticket, tells the humans why, and leaves the worktree in place
// so the ticket can resume.
func (c *Controller) fail(ctx context.Context, t *Ticket, st Stage, format string, args ...any) bool {
	msg := fmt.Sprintf(format, args...)
	col := columnFor(st)
	t.mu.Lock()
	attempt := t.cells[col].Attempt
	t.mu.Unlock()
	t.setCell(col, cellFail, attempt)
	c.board.Log("#%d ✗ stopped at %s: %s", t.Number, st, msg)
	if t.Branch != "" {
		c.comment(ctx, t, "🛑 Factory stopped at "+string(st), fmt.Sprintf(
			"%s\n\nTo retry, fix the cause and remove the `%s` label. The controller resumes from this stage. To start over, run `factory reset %d`.",
			msg, StageFailed.Label(), t.Number))
	}
	c.setStage(ctx, t, StageFailed)
	c.runner.Notify(ctx, fmt.Sprintf("#%d needs a human", t.Number), truncate(msg, 120), "request")
	c.closeTicket(ctx, t, "✗ "+string(st))
	return false
}

func (c *Controller) closeTicket(ctx context.Context, t *Ticket, summary string) {
	t.end(summary)
	t.setNote("")
	c.runner.Close(ctx, t, summary)
}

// writeInputs gives the stations the ticket as files, so no agent needs gh.
func writeInputs(t *Ticket, is Issue) error {
	for _, d := range []string{"in", "out", "logs"} {
		if err := os.MkdirAll(filepath.Join(t.Dir, ".factory", d), 0o755); err != nil {
			return err
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", is.Title)
	fmt.Fprintf(&b, "- Issue: #%d (%s)\n- Author: @%s\n- Labels: %s\n- Branch: %s (base: %s)\n\n",
		is.Number, is.URL, is.Author.Login, strings.Join(is.LabelNames(), ", "), t.Branch, t.Base)
	b.WriteString("## Body\n\n")
	if body := strings.TrimSpace(is.Body); body != "" {
		b.WriteString(body + "\n")
	} else {
		b.WriteString("(empty)\n")
	}
	var human []ghComment
	for _, cm := range is.Comments {
		if !strings.Contains(cm.Body, commentFooter) {
			human = append(human, cm)
		}
	}
	if len(human) > 0 {
		b.WriteString("\n## Comments from people\n")
		for _, cm := range human {
			fmt.Fprintf(&b, "\n### @%s, %s\n\n%s\n", cm.Author.Login, cm.CreatedAt.Format("2006-01-02 15:04"), strings.TrimSpace(cm.Body))
		}
	}
	if err := os.WriteFile(t.inPath("issue.md"), []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.WriteFile(t.inPath("base"), []byte(t.Base+"\n"), 0o644)
}

func writeFeedback(t *Ticket, title, body string) error {
	return os.WriteFile(t.inPath("feedback.md"), []byte("# "+title+"\n\n"+body+"\n"), 0o644)
}

func formatFindings(rr reviewResult) string {
	var b strings.Builder
	b.WriteString("The review station requested changes. Fix every high and medium finding, then run `make verify` again.\n\n")
	for i, f := range rr.Findings {
		loc := f.File
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		fmt.Fprintf(&b, "%d. [%s] %s: %s\n", i+1, orDash(f.Severity), orDash(loc), f.Message)
	}
	if rr.Summary != "" {
		b.WriteString("\nReviewer summary: " + rr.Summary + "\n")
	}
	return b.String()
}

func specComment(t *Ticket, spec string, ask bool) string {
	var b strings.Builder
	if ask {
		fmt.Fprintf(&b, "Comment **`/approve`** to start the build (or add the `%s` label). Comment `/reject <reason>` to stop.\n\n", labelApproved)
	}
	fmt.Fprintf(&b, "<details open><summary><code>%s</code></summary>\n\n%s\n\n</details>", t.specRel(), strings.TrimSpace(spec))
	return b.String()
}

func readText(path, fallback string) string {
	b, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return fallback
	}
	return string(b)
}

func tailFile(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "(no log)"
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "–"
	}
	return s
}

func shortPR(url string) string {
	if i := strings.LastIndex(url, "/pull/"); i >= 0 {
		return "PR #" + url[i+len("/pull/"):]
	}
	return url
}

func relToRepo(path string) string {
	if wd, err := os.Getwd(); err == nil {
		if r, err := filepath.Rel(wd, path); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
	}
	return path
}
