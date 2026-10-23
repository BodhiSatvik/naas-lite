package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// StationMeta is what a station run reports back to the controller. The
// station writes it last, atomically, so the controller never reads half a
// file.
type StationMeta struct {
	Stage      string    `json:"stage"`
	Issue      int       `json:"issue"`
	RunID      string    `json:"run_id"`
	ExitCode   int       `json:"exit_code"`
	StartedAt  time.Time `json:"started_at"`
	DurationMS int64     `json:"duration_ms"`
	CostUSD    float64   `json:"cost_usd"`
	NumTurns   int       `json:"num_turns"`
	SessionID  string    `json:"session_id,omitempty"`
	IsError    bool      `json:"is_error"`
	Result     string    `json:"result,omitempty"`
	Error      string    `json:"error,omitempty"`
}

func outDir(dir string) string { return filepath.Join(dir, ".factory", "out") }

// stationDefaults are the station settings the controller writes to
// .factory/station.json in each worktree.
type stationDefaults struct {
	Timeout   string `json:"timeout"`
	MemoryMCP string `json:"memory_mcp"`
	Claude    string `json:"claude"`
	Model     string `json:"model"`
	Fake      bool   `json:"fake"`
	Smoke     bool   `json:"smoke"`
}

func stationDefaultsPath(dir string) string { return filepath.Join(dir, ".factory", "station.json") }

func loadStationDefaults(dir string) (stationDefaults, bool) {
	var d stationDefaults
	if err := readJSON(stationDefaultsPath(dir), &d); err != nil {
		return d, false
	}
	return d, true
}

func metaPath(dir string, s Stage) string {
	return filepath.Join(outDir(dir), string(s)+".meta.json")
}

func startedPath(dir string, s Stage) string {
	return filepath.Join(outDir(dir), string(s)+".started")
}

// cmdStation runs one station inside a ticket's worktree. The controller
// starts it in a herdr tab (or as a background process), then waits for the
// meta file.
func cmdStation(args []string) error {
	fs := flag.NewFlagSet("station", flag.ContinueOnError)
	dir := fs.String("dir", ".", "ticket worktree to run in")
	model := fs.String("model", "", "model for claude --model")
	timeout := fs.Duration("timeout", 20*time.Minute, "station timeout")
	memoryMCP := fs.String("memory-mcp", "ai-memory", "MCP server name of ai-memory")
	claudeBin := fs.String("claude", "claude", "claude binary")
	smoke := fs.Bool("smoke", false, "verify also runs make smoke (kind deploy)")
	fake := fs.Bool("fake", false, "fake agent: canned outputs, no Claude calls")
	runID := fs.String("run-id", "", "unique id of this run (guards against double launches)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: factory station [flags] STAGE ISSUE")
	}
	stage := Stage(fs.Arg(0))
	if stationNumber(stage) == 0 {
		return fmt.Errorf("unknown station %q", stage)
	}
	issue, err := strconv.Atoi(fs.Arg(1))
	if err != nil {
		return fmt.Errorf("bad issue number %q", fs.Arg(1))
	}
	wd, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	// The controller writes the station settings into the worktree, so the
	// command typed into each herdr pane stays short. Flags still win.
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if d, ok := loadStationDefaults(wd); ok {
		if v, err := time.ParseDuration(d.Timeout); err == nil && !set["timeout"] {
			*timeout = v
		}
		if !set["memory-mcp"] {
			*memoryMCP = d.MemoryMCP
		}
		if d.Claude != "" && !set["claude"] {
			*claudeBin = d.Claude
		}
		if !set["model"] {
			*model = d.Model
		}
		if !set["fake"] {
			*fake = d.Fake
		}
		if !set["smoke"] {
			*smoke = d.Smoke
		}
	}
	for _, d := range []string{outDir(wd), filepath.Join(wd, ".factory", "logs")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	// A herdr pane can receive the same command twice if the shell was slow
	// to start. The run id makes the second launch a no-op.
	if *runID != "" {
		lock := filepath.Join(outDir(wd), fmt.Sprintf(".%s.%s.lock", stage, *runID))
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Println("factory: this station run already started; ignoring the duplicate launch")
			return nil
		}
		f.Close()
	}
	_ = os.WriteFile(startedPath(wd, stage), []byte(*runID), 0o644)

	ui := newUI(os.Stdout)
	mode := "agent: claude (headless) · skill /factory-" + string(stage)
	switch {
	case *fake:
		mode = "agent: fake (rehearsal mode, no AI calls)"
	case stage == StageVerify:
		mode = "deterministic gate: make verify (no AI)"
	}
	if *model != "" && !*fake && stage != StageVerify {
		mode += " · model " + *model
	}
	ui.banner(stage, issue, mode)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	meta := StationMeta{Stage: string(stage), Issue: issue, RunID: *runID, StartedAt: time.Now()}
	var runErr error
	switch {
	case stage == StageVerify:
		runErr = runVerify(ctx, wd, *smoke, ui, &meta)
	case *fake:
		runErr = runFake(ctx, stage, issue, wd, ui, &meta)
	default:
		runErr = runAgent(ctx, stage, issue, wd, *claudeBin, *model, *memoryMCP, ui, &meta)
	}
	meta.DurationMS = time.Since(meta.StartedAt).Milliseconds()
	if runErr != nil {
		meta.Error = runErr.Error()
		if meta.ExitCode == 0 {
			meta.ExitCode = 1
		}
	}
	if err := writeJSONAtomic(metaPath(wd, stage), meta); err != nil {
		ui.line("cannot write station result: %v", err)
	}
	ui.footer(meta)
	fmt.Printf("FACTORY_EXIT=%d\n", meta.ExitCode)
	return nil
}

// runAgent runs one headless Claude Code session that invokes the station's
// skill, and renders its stream-json output as a live feed.
func runAgent(ctx context.Context, stage Stage, issue int, dir, claudeBin, model, memoryMCP string, ui *UI, meta *StationMeta) error {
	prompt := fmt.Sprintf("/factory-%s %d", stage, issue)
	args := []string{
		"-p", prompt,
		"--permission-mode", "acceptEdits",
		"--allowedTools", strings.Join(stationTools(stage, memoryMCP), ","),
		"--output-format", "stream-json",
		"--verbose",
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	ui.line("%s", ui.c("2", fmt.Sprintf("$ %s -p %q --permission-mode acceptEdits --output-format stream-json …", claudeBin, prompt)))

	cmd := exec.CommandContext(ctx, claudeBin, args...)
	cmd.Dir = dir
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 15 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = &dimWriter{ui: ui}
	raw, err := os.Create(filepath.Join(dir, ".factory", "logs", string(stage)+".jsonl"))
	if err != nil {
		return err
	}
	defer raw.Close()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w (is Claude Code installed and on PATH?)", claudeBin, err)
	}

	r := newRenderer(ui, dir)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		raw.Write(line)
		raw.Write([]byte{'\n'})
		r.handle(line)
	}
	waitErr := cmd.Wait()

	meta.SessionID = r.sessionID
	if res := r.result; res != nil {
		meta.CostUSD = res.TotalCostUSD
		meta.NumTurns = res.NumTurns
		meta.IsError = res.IsError
		meta.Result = truncate(res.Result, 2000)
	}
	meta.ExitCode = exitCode(waitErr)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errors.New("station timed out")
	}
	if meta.IsError && meta.ExitCode == 0 {
		meta.ExitCode = 1
	}
	if r.result == nil && meta.ExitCode == 0 {
		return errors.New("claude exited without a result event")
	}
	checkOutputs(stage, issue, dir, ui)
	return nil
}

// checkOutputs warns in the feed when a station skipped a required file. The
// controller makes the real decision.
func checkOutputs(stage Stage, issue int, dir string, ui *UI) {
	want := map[Stage][]string{
		StageTriage: {".factory/out/triage.json", ".factory/out/triage.md"},
		StageSpec:   {fmt.Sprintf("specs/%d/SPEC.md", issue), ".factory/out/spec.json"},
		StageBuild:  {".factory/out/build.json"},
		StageReview: {".factory/out/review.json", ".factory/out/review.md"},
		StageShip:   {".factory/out/ship.json", ".factory/out/pr.md"},
	}[stage]
	for _, f := range want {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			ui.line("%s", ui.c("33", "⚠ missing output: "+f))
		}
	}
}

type verifyStep struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
}

type verifyResult struct {
	Status     string       `json:"status"`
	Steps      []verifyStep `json:"steps"`
	KindDryRun string       `json:"kind_dry_run"`
	Log        string       `json:"log"`
}

// runVerify is the deterministic gate: plain make targets, no AI. The AI
// writes the code; this decides if it is good enough to review.
func runVerify(ctx context.Context, dir string, smoke bool, ui *UI, meta *StationMeta) error {
	logPath := filepath.Join(outDir(dir), "verify.log")
	logf, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer logf.Close()
	var steps []verifyStep
	run := func(name string, argv ...string) int {
		ui.line("%s", ui.c("1", "$ "+strings.Join(argv, " ")))
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = dir
		w := io.MultiWriter(&lockedWriter{ui: ui}, logf)
		cmd.Stdout = w
		cmd.Stderr = w
		start := time.Now()
		code := exitCode(cmd.Run())
		steps = append(steps, verifyStep{Name: name, Command: strings.Join(argv, " "), ExitCode: code, DurationMS: time.Since(start).Milliseconds()})
		return code
	}
	code := run("verify", "make", "verify")
	if code == 0 && smoke {
		code = run("smoke", "make", "smoke")
	}
	logText, _ := os.ReadFile(logPath)
	res := verifyResult{Status: "pass", Steps: steps, KindDryRun: "ran", Log: ".factory/out/verify.log"}
	if bytes.Contains(logText, []byte("k8s-validate: SKIPPED")) {
		res.KindDryRun = "skipped"
	}
	if code != 0 {
		res.Status = "fail"
		ui.line("%s", ui.c("31;1", "✗ gate failed: the log goes back to the builder"))
	} else {
		ui.line("%s", ui.c("32;1", "✓ gate passed: on to review"))
	}
	return writeJSONAtomic(filepath.Join(outDir(dir), "verify.json"), res)
}

// runFake writes canned station outputs. Use it to rehearse the GitHub and
// herdr plumbing without spending tokens: factory run -fake.
func runFake(ctx context.Context, stage Stage, issue int, dir string, ui *UI, meta *StationMeta) error {
	issueText, _ := os.ReadFile(filepath.Join(dir, ".factory", "in", "issue.md"))
	vague := !bytes.Contains(bytes.ToLower(issueText), []byte("acceptance"))
	say := func(lines ...string) error {
		for _, l := range lines {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(400 * time.Millisecond):
			}
			ui.line("%s", l)
		}
		return nil
	}
	out := outDir(dir)
	switch stage {
	case StageTriage:
		decision, questions := "ready", []string{}
		if vague {
			decision = "needs-info"
			questions = []string{"Which endpoint is slow, and what latency do you see?", "What target do you want (for example p95 under 200 ms)?"}
		}
		if err := say("📖 Read .factory/in/issue.md", "🔎 Grep internal/", "💬 Fake triage decision: "+decision); err != nil {
			return err
		}
		md := "**Decision:** " + decision + "\n\nFake triage (rehearsal mode)."
		for i, q := range questions {
			md += fmt.Sprintf("\n%d. %s", i+1, q)
		}
		_ = writeJSONAtomic(filepath.Join(out, "triage.json"), map[string]any{
			"decision": decision, "type": "feature", "size": "S", "risk": "low",
			"summary": "Fake triage for a rehearsal run.", "areas": []string{"internal/api"},
			"questions": questions, "memory_hits": []string{},
		})
		return os.WriteFile(filepath.Join(out, "triage.md"), []byte(md+"\n"), 0o644)
	case StageSpec:
		if err := say("🧠 memory_query (fake)", "📝 Write specs/"+strconv.Itoa(issue)+"/SPEC.md"); err != nil {
			return err
		}
		specDir := filepath.Join(dir, "specs", strconv.Itoa(issue))
		if err := os.MkdirAll(specDir, 0o755); err != nil {
			return err
		}
		spec := fmt.Sprintf("# Spec for #%d (rehearsal)\n\n## Problem\nFake spec.\n\n## Acceptance criteria\n- AC1: Given a rehearsal, when the factory runs, then a PR opens.\n\n## Test plan\n| AC | test | file |\n|---|---|---|\n| AC1 | n/a | n/a |\n", issue)
		_ = writeJSONAtomic(filepath.Join(out, "spec.json"), map[string]any{"status": "ok", "acceptance_criteria": 1, "files": []string{}, "memory_hits": []string{}})
		return os.WriteFile(filepath.Join(specDir, "SPEC.md"), []byte(spec), 0o644)
	case StageBuild:
		if err := say("📖 Read specs/"+strconv.Itoa(issue)+"/SPEC.md", "✏️  Edit rehearsal/issue-"+strconv.Itoa(issue)+".md", "⚙️  Bash git commit"); err != nil {
			return err
		}
		f := filepath.Join(dir, "rehearsal", fmt.Sprintf("issue-%d.md", issue))
		_ = os.MkdirAll(filepath.Dir(f), 0o755)
		if err := os.WriteFile(f, []byte(fmt.Sprintf("Rehearsal build for #%d at %s\n", issue, time.Now().Format(time.RFC3339))), 0o644); err != nil {
			return err
		}
		if _, err := commitAll(ctx, dir, fmt.Sprintf("chore: rehearsal build (#%d)", issue)); err != nil {
			return err
		}
		return writeJSONAtomic(filepath.Join(out, "build.json"), map[string]any{"status": "done", "summary": "Fake build.", "tests_added": []string{}, "notes": ""})
	case StageReview:
		if err := say("⚙️  Bash git diff", "💬 Fake review: approve"); err != nil {
			return err
		}
		_ = writeJSONAtomic(filepath.Join(out, "review.json"), map[string]any{"verdict": "approve", "summary": "Fake review.", "findings": []any{}, "lessons": []string{}})
		return os.WriteFile(filepath.Join(out, "review.md"), []byte("**Verdict:** approve (rehearsal)\n"), 0o644)
	case StageShip:
		if err := say("📝 Write .factory/out/pr.md"); err != nil {
			return err
		}
		_ = writeJSONAtomic(filepath.Join(out, "ship.json"), map[string]any{"title": fmt.Sprintf("chore: factory rehearsal (#%d)", issue)})
		return os.WriteFile(filepath.Join(out, "pr.md"), []byte(fmt.Sprintf("## Summary\nRehearsal run of the factory. Safe to close.\n\nCloses #%d\n", issue)), 0o644)
	}
	return nil
}

// dimWriter shows agent stderr in the feed, dimmed.
type dimWriter struct{ ui *UI }

func (w *dimWriter) Write(p []byte) (int, error) {
	for _, l := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			w.ui.line("%s", w.ui.c("2", truncate(l, 160)))
		}
	}
	return len(p), nil
}

// lockedWriter writes command output through the UI lock.
type lockedWriter struct{ ui *UI }

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.ui.mu.Lock()
	defer w.ui.mu.Unlock()
	return w.ui.out.Write(p)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if c := ee.ExitCode(); c >= 0 {
			return c
		}
	}
	return 1
}

func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
