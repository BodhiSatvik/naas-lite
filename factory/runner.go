package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// StationRun describes one launch of one station.
type StationRun struct {
	Key         string // display name, e.g. "3·build #2"
	Stage       Stage
	Argv        []string
	LogPath     string
	MetaPath    string
	StartedPath string
}

// Runner decides where stations run.
//
// The herdr runner gives each ticket a herdr workspace and each station its
// own tab, so you can watch every agent work live, and herdr's sidebar shows
// which ticket needs you. The local runner runs stations as background
// processes with logs in .factory/logs (useful without herdr).
type Runner interface {
	Name() string
	Open(ctx context.Context, t *Ticket) error
	Start(ctx context.Context, t *Ticket, run StationRun) error
	Mark(ctx context.Context, t *Ticket, key, symbol string)
	Attention(ctx context.Context, t *Ticket, key string, on bool, msg string)
	Notify(ctx context.Context, title, body, sound string)
	Close(ctx context.Context, t *Ticket, summary string)
}

// LocalRunner runs stations as child processes.
type LocalRunner struct {
	log func(format string, args ...any)
}

func (r *LocalRunner) Name() string                                             { return "local" }
func (r *LocalRunner) Open(context.Context, *Ticket) error                      { return nil }
func (r *LocalRunner) Mark(context.Context, *Ticket, string, string)            {}
func (r *LocalRunner) Attention(context.Context, *Ticket, string, bool, string) {}
func (r *LocalRunner) Close(context.Context, *Ticket, string)                   {}

func (r *LocalRunner) Notify(_ context.Context, title, body, _ string) {
	r.log("🔔 %s — %s", title, body)
}

func (r *LocalRunner) Start(ctx context.Context, t *Ticket, run StationRun) error {
	f, err := os.Create(run.LogPath)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, run.Argv[0], run.Argv[1:]...)
	cmd.Dir = t.Dir
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.Env = append(os.Environ(), "FACTORY_PLAIN=1")
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 20 * time.Second
	if err := cmd.Start(); err != nil {
		f.Close()
		return err
	}
	r.log("#%d %s started (log: %s)", t.Number, run.Key, relToRepo(run.LogPath))
	go func() {
		err := cmd.Wait()
		f.Close()
		// If the station died before it could report, report for it, so the
		// controller does not wait for the full timeout.
		if _, statErr := os.Stat(run.MetaPath); statErr != nil {
			_ = writeJSONAtomic(run.MetaPath, StationMeta{
				Stage: string(run.Stage), Issue: t.Number, ExitCode: max(exitCode(err), 1),
				Error: "station process exited without a result",
			})
		}
	}()
	return nil
}

// waitMeta waits until the station writes its result file.
func waitMeta(ctx context.Context, path string, timeout time.Duration) (*StationMeta, error) {
	deadline := time.Now().Add(timeout)
	for {
		if b, err := os.ReadFile(path); err == nil {
			var m StationMeta
			if json.Unmarshal(b, &m) == nil {
				return &m, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("no result from the station after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(700 * time.Millisecond):
		}
	}
}

func waitFile(ctx context.Context, path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(300 * time.Millisecond):
		}
	}
	return false
}

// shellJoin quotes argv for the interactive shell inside a herdr pane.
func shellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@%+,", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var errNoHerdr = errors.New("not running inside herdr (HERDR_ENV is not 1)")
