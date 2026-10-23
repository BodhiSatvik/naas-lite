package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// UI prints a station's live feed. The feed is built to be read from the
// back of a room: one short line per agent action.
type UI struct {
	mu    sync.Mutex
	out   io.Writer
	color bool
	ascii bool
}

func newUI(w io.Writer) *UI {
	return &UI{
		out:   w,
		color: os.Getenv("NO_COLOR") == "" && os.Getenv("FACTORY_PLAIN") == "",
		ascii: os.Getenv("FACTORY_ASCII") != "",
	}
}

func (u *UI) c(code, s string) string {
	if !u.color || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (u *UI) line(format string, args ...any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	fmt.Fprintf(u.out, format+"\n", args...)
}

func (u *UI) icon(emoji, ascii string) string {
	if u.ascii {
		return ascii
	}
	return emoji
}

func (u *UI) banner(stage Stage, issue int, mode string) {
	title := fmt.Sprintf("FACTORY  ▸  #%d  ▸  STATION %d · %s", issue, stationNumber(stage), strings.ToUpper(string(stage)))
	width := utf8.RuneCountInString(title) + 4
	if w := utf8.RuneCountInString(mode) + 4; w > width {
		width = w
	}
	bar := strings.Repeat("─", width)
	u.line("%s", u.c("36", "╭"+bar+"╮"))
	u.line("%s", u.c("36;1", "│  "+padRight(title, width-2)+"│"))
	u.line("%s", u.c("36", "│  "+padRight(mode, width-2)+"│"))
	u.line("%s", u.c("36", "╰"+bar+"╯"))
}

func (u *UI) footer(m StationMeta) {
	status := u.c("32;1", "■ done")
	if m.ExitCode != 0 || m.IsError {
		status = u.c("31;1", "■ failed")
	}
	parts := []string{status, fmtDuration(time.Duration(m.DurationMS) * time.Millisecond)}
	if m.NumTurns > 0 {
		parts = append(parts, fmt.Sprintf("%d turns", m.NumTurns))
	}
	if m.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.2f", m.CostUSD))
	}
	u.line("")
	u.line("%s", strings.Join(parts, " · "))
	if m.Error != "" {
		u.line("%s", u.c("31", "  "+m.Error))
	}
	if m.SessionID != "" {
		u.line("%s", u.c("2", "  ask this agent follow-up questions:  claude --resume "+m.SessionID))
	}
}

func padRight(s string, n int) string {
	if d := n - utf8.RuneCountInString(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// renderer turns Claude Code's stream-json output into the live feed.
type renderer struct {
	ui        *UI
	dir       string
	tools     map[string]toolUse
	sessionID string
	model     string
	result    *streamEvent
}

type toolUse struct{ Name, Detail string }

type streamEvent struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	SessionID       string          `json:"session_id"`
	Model           string          `json:"model"`
	Message         json.RawMessage `json:"message"`
	ParentToolUseID *string         `json:"parent_tool_use_id"`
	MCPServers      []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"mcp_servers"`
	IsError      bool    `json:"is_error"`
	Result       string  `json:"result"`
	DurationMS   int64   `json:"duration_ms"`
	NumTurns     int     `json:"num_turns"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	Attempt      int     `json:"attempt"`
	MaxRetries   int     `json:"max_retries"`
}

type message struct {
	Content json.RawMessage `json:"content"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

func newRenderer(ui *UI, dir string) *renderer {
	return &renderer{ui: ui, dir: dir, tools: map[string]toolUse{}}
}

// parseBlocks reads message content, which is either a list of blocks or a
// plain string.
func parseBlocks(raw json.RawMessage) []block {
	if len(raw) == 0 {
		return nil
	}
	var blocks []block
	if err := json.Unmarshal(raw, &blocks); err == nil {
		return blocks
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []block{{Type: "text", Text: s}}
	}
	return nil
}

func (r *renderer) handle(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	var ev streamEvent
	if err := json.Unmarshal(line, &ev); err != nil || ev.Type == "" {
		r.ui.line("%s", r.ui.c("2", truncate(string(line), 160)))
		return
	}
	indent := ""
	if ev.ParentToolUseID != nil && *ev.ParentToolUseID != "" {
		indent = r.ui.c("2", "  │ ")
	}
	switch ev.Type {
	case "system":
		r.system(ev)
	case "assistant":
		var m message
		_ = json.Unmarshal(ev.Message, &m)
		for _, b := range parseBlocks(m.Content) {
			switch b.Type {
			case "text":
				r.text(indent, b.Text)
			case "tool_use":
				r.toolUse(indent, b)
			}
		}
	case "user":
		var m message
		_ = json.Unmarshal(ev.Message, &m)
		for _, b := range parseBlocks(m.Content) {
			if b.Type == "tool_result" {
				r.toolResult(indent, b)
			}
		}
	case "result":
		e := ev
		r.result = &e
		if ev.SessionID != "" {
			r.sessionID = ev.SessionID
		}
	}
}

func (r *renderer) system(ev streamEvent) {
	switch ev.Subtype {
	case "init":
		r.sessionID = ev.SessionID
		r.model = ev.Model
		mem := r.ui.c("33", "memory: not connected")
		for _, s := range ev.MCPServers {
			if strings.Contains(strings.ToLower(s.Name), "memory") {
				if s.Status == "connected" {
					mem = r.ui.c("32", r.ui.icon("🧠", "[mem]")+" "+s.Name+" connected")
				} else {
					mem = r.ui.c("33", s.Name+": "+s.Status)
				}
			}
		}
		r.ui.line("%s", r.ui.c("2", fmt.Sprintf("● session %s · %s · ", short(ev.SessionID, 8), ev.Model))+mem)
	case "api_retry":
		r.ui.line("%s", r.ui.c("33", fmt.Sprintf("↻ API retry %d/%d", ev.Attempt, ev.MaxRetries)))
	}
}

func (r *renderer) text(indent, text string) {
	n := 0
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		prefix := "  "
		if n == 0 {
			prefix = r.ui.icon("💬", ">") + " "
		}
		r.ui.line("%s%s%s", indent, prefix, truncate(l, 150))
		n++
		if n == 3 {
			break
		}
	}
}

func (r *renderer) toolUse(indent string, b block) {
	icon, label, detail := summarizeTool(r.ui, b.Name, b.Input, r.dir)
	r.tools[b.ID] = toolUse{Name: b.Name, Detail: detail}
	r.ui.line("%s%s %s %s", indent, icon, r.ui.c("1", label), truncate(detail, 120))
}

func (r *renderer) toolResult(indent string, b block) {
	tu := r.tools[b.ToolUseID]
	text := blockText(b.Content)
	if b.IsError {
		r.ui.line("%s   %s %s", indent, r.ui.c("31", "✗"), truncate(firstLine(text), 140))
		return
	}
	if tu.Name == "Bash" {
		for _, l := range interestingLines(text, 4) {
			color := "2"
			if strings.HasPrefix(l, "FAIL") || strings.HasPrefix(l, "--- FAIL") || strings.HasPrefix(l, "panic") {
				color = "31"
			}
			r.ui.line("%s   %s", indent, r.ui.c(color, truncate(l, 140)))
		}
	}
}

// blockText reads tool_result content: a string or a list of text blocks.
func blockText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []block
	if err := json.Unmarshal(raw, &parts); err == nil {
		var sb strings.Builder
		for _, p := range parts {
			if p.Text != "" {
				sb.WriteString(p.Text)
				sb.WriteString("\n")
			}
		}
		return sb.String()
	}
	return string(raw)
}

// interestingLines keeps test and verify summaries from command output.
func interestingLines(text string, max int) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "ok "), strings.HasPrefix(t, "FAIL"), strings.HasPrefix(t, "--- FAIL"),
			t == "PASS", strings.HasPrefix(t, "panic:"), strings.HasPrefix(t, "VERIFY:"),
			strings.HasPrefix(t, "k8s-validate:"):
			out = append(out, t)
		}
		if len(out) == max {
			break
		}
	}
	return out
}

func summarizeTool(ui *UI, name string, input json.RawMessage, dir string) (icon, label, detail string) {
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	str := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := in[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	switch {
	case name == "Read":
		return ui.icon("📖", "[read]"), "Read", rel(dir, str("file_path"))
	case name == "Edit" || name == "MultiEdit":
		return ui.icon("✏️ ", "[edit]"), "Edit", rel(dir, str("file_path"))
	case name == "Write":
		return ui.icon("📝", "[write]"), "Write", rel(dir, str("file_path"))
	case name == "Bash":
		return ui.icon("⚙️ ", "[sh]"), "Bash", firstLine(str("command"))
	case name == "Grep" || name == "Glob":
		return ui.icon("🔎", "[find]"), name, str("pattern")
	case name == "TodoWrite":
		return ui.icon("☑️ ", "[todo]"), "Plan", summarizeTodos(in)
	case name == "Skill":
		return ui.icon("🧩", "[skill]"), "Skill", str("skill", "command", "name")
	case name == "Task" || name == "Agent":
		return ui.icon("🤖", "[agent]"), "Subagent", str("description", "prompt")
	case name == "WebFetch":
		return ui.icon("🌐", "[web]"), "WebFetch", str("url")
	case name == "WebSearch":
		return ui.icon("🌐", "[web]"), "WebSearch", str("query")
	case strings.HasPrefix(name, "mcp__"):
		rest := strings.TrimPrefix(name, "mcp__")
		server, tool := rest, rest
		if i := strings.Index(rest, "__"); i >= 0 {
			server, tool = rest[:i], rest[i+2:]
		}
		ic := ui.icon("🔌", "[mcp]")
		if strings.Contains(strings.ToLower(server), "memory") {
			ic = ui.icon("🧠", "[mem]")
		}
		return ic, tool, str("query", "path", "title", "handoff_id", "summary")
	}
	return ui.icon("•", "-"), name, str("file_path", "path", "query", "description", "command")
}

func summarizeTodos(in map[string]any) string {
	list, _ := in["todos"].([]any)
	done, current := 0, ""
	for _, t := range list {
		m, _ := t.(map[string]any)
		switch m["status"] {
		case "completed":
			done++
		case "in_progress":
			if s, ok := m["content"].(string); ok && current == "" {
				current = s
			}
		}
	}
	s := fmt.Sprintf("%d/%d", done, len(list))
	if current != "" {
		s += " · " + current
	}
	return s
}

func rel(dir, path string) string {
	if path == "" {
		return ""
	}
	if r, err := filepath.Rel(dir, path); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return path
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func fmtDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}
