package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Herdr drives the herdr CLI. Most herdr commands print JSON, so the
// controller reads ids from the responses instead of guessing them.
type Herdr struct {
	Bin string
}

func (h *Herdr) call(ctx context.Context, args ...string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("herdr %s: %s", strings.Join(firstN(args, 2), " "), msg)
	}
	var m map[string]any
	if b := bytes.TrimSpace(stdout.Bytes()); len(b) > 0 {
		_ = json.Unmarshal(b, &m)
	}
	return m, nil
}

func firstN(s []string, n int) []string {
	if len(s) < n {
		return s
	}
	return s[:n]
}

// dig reads a nested string (or number) from a decoded JSON object.
func dig(m map[string]any, path ...string) string {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[p]
	}
	switch v := cur.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// findKey returns the first string value stored under key anywhere in the
// tree. It is a fallback in case a herdr version shapes a response
// differently from the docs.
func findKey(v any, key string) string {
	switch t := v.(type) {
	case map[string]any:
		if s, ok := t[key].(string); ok && s != "" {
			return s
		}
		for _, child := range t {
			if s := findKey(child, key); s != "" {
				return s
			}
		}
	case []any:
		for _, child := range t {
			if s := findKey(child, key); s != "" {
				return s
			}
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// HerdrRunner runs each station in its own herdr tab.
type HerdrRunner struct {
	h      *Herdr
	warmup time.Duration
	log    func(format string, args ...any)
}

func (r *HerdrRunner) Name() string { return "herdr" }

// Open creates the ticket's herdr workspace, rooted in its worktree.
func (r *HerdrRunner) Open(ctx context.Context, t *Ticket) error {
	m, err := r.h.call(ctx, "workspace", "create", "--cwd", t.Dir, "--label", workspaceLabel(t, ""), "--no-focus")
	if err != nil {
		return err
	}
	ws := firstNonEmpty(dig(m, "result", "workspace", "workspace_id"), findKey(m, "workspace_id"))
	tab := firstNonEmpty(dig(m, "result", "tab", "tab_id"), findKey(m, "tab_id"))
	pane := firstNonEmpty(dig(m, "result", "root_pane", "pane_id"), findKey(m, "pane_id"))
	if ws == "" || pane == "" {
		return errors.New("herdr workspace create: no workspace or pane id in the response")
	}
	t.mu.Lock()
	t.ws, t.rootTab, t.rootPane, t.rootUsed = ws, tab, pane, false
	t.mu.Unlock()
	return nil
}

func (r *HerdrRunner) Start(ctx context.Context, t *Ticket, run StationRun) error {
	t.mu.Lock()
	ws, useRoot := t.ws, !t.rootUsed && t.rootPane != ""
	tab, pane := t.rootTab, t.rootPane
	if useRoot {
		t.rootUsed = true
	}
	t.mu.Unlock()
	if ws == "" {
		return errors.New("no herdr workspace for this ticket")
	}
	if useRoot {
		if tab != "" {
			_, _ = r.h.call(ctx, "tab", "rename", tab, run.Key)
		}
	} else {
		m, err := r.h.call(ctx, "tab", "create", "--workspace", ws, "--cwd", t.Dir, "--label", run.Key, "--no-focus")
		if err != nil {
			return err
		}
		tab = firstNonEmpty(dig(m, "result", "tab", "tab_id"), findKey(m, "tab_id"))
		pane = firstNonEmpty(dig(m, "result", "root_pane", "pane_id"), findKey(m, "pane_id"))
		if pane == "" {
			return errors.New("herdr tab create: no pane id in the response")
		}
		// Give the new shell a moment to print its prompt.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.warmup):
		}
	}
	t.setPane(run.Key, tab, pane)
	cmdline := shellJoin(run.Argv)
	if _, err := r.h.call(ctx, "pane", "run", pane, cmdline); err != nil {
		return err
	}
	if waitFile(ctx, run.StartedPath, 20*time.Second) {
		return nil
	}
	// The shell may have dropped the command while it started up. Send it
	// again; the run id makes a double launch harmless.
	r.log("#%d %s: no start signal yet, resending the command", t.Number, run.Key)
	if _, err := r.h.call(ctx, "pane", "run", pane, cmdline); err != nil {
		return err
	}
	if !waitFile(ctx, run.StartedPath, 30*time.Second) {
		return fmt.Errorf("station did not start in herdr pane %s", pane)
	}
	return nil
}

// Mark puts a status symbol on the station's tab, for example "2·spec ✓".
func (r *HerdrRunner) Mark(ctx context.Context, t *Ticket, key, symbol string) {
	if tab, _ := t.pane(key); tab != "" {
		_, _ = r.h.call(ctx, "tab", "rename", tab, strings.TrimSpace(key+" "+symbol))
	}
}

// Attention flags a pane as blocked in herdr's sidebar while the ticket waits
// for a human (the spec approval), and clears it afterwards.
func (r *HerdrRunner) Attention(ctx context.Context, t *Ticket, key string, on bool, msg string) {
	_, pane := t.pane(key)
	if pane == "" {
		return
	}
	var err error
	if on {
		_, err = r.h.call(ctx, "pane", "report-agent", pane, "--source", "factory", "--agent", "factory",
			"--state", "blocked", "--message", msg)
	} else {
		_, err = r.h.call(ctx, "pane", "release-agent", pane, "--source", "factory", "--agent", "factory")
	}
	if err != nil {
		r.log("herdr attention flag skipped: %v", err)
	}
}

func (r *HerdrRunner) Notify(ctx context.Context, title, body, sound string) {
	args := []string{"notification", "show", title, "--body", body}
	if sound != "" {
		args = append(args, "--sound", sound)
	}
	if _, err := r.h.call(ctx, args...); err != nil {
		r.log("herdr notification skipped: %v", err)
	}
}

func (r *HerdrRunner) Close(ctx context.Context, t *Ticket, summary string) {
	t.mu.Lock()
	ws := t.ws
	t.mu.Unlock()
	if ws != "" {
		_, _ = r.h.call(ctx, "workspace", "rename", ws, workspaceLabel(t, summary))
	}
}

func workspaceLabel(t *Ticket, suffix string) string {
	title := truncate(t.TitleSnapshot(), 26)
	label := fmt.Sprintf("#%d %s", t.Number, title)
	if suffix != "" {
		label = fmt.Sprintf("#%d %s", t.Number, suffix)
	}
	return label
}
