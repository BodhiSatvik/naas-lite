package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Board is the controller's live "factory floor" view: one row per ticket,
// one column per station, and the latest events underneath.
type Board struct {
	mu       sync.Mutex
	out      io.Writer
	live     bool
	color    bool
	title    string
	snapshot func() []snapshot
	events   []string
	logFile  *os.File
	frame    int
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func newBoard(out io.Writer, live bool, logFile *os.File) *Board {
	return &Board{
		out:     out,
		live:    live,
		color:   os.Getenv("NO_COLOR") == "",
		logFile: logFile,
	}
}

// Log records an event: on the board, in .factory/controller.log, and on
// stdout when the board is off.
func (b *Board) Log(format string, args ...any) {
	line := time.Now().Format("15:04:05") + "  " + fmt.Sprintf(format, args...)
	b.mu.Lock()
	b.events = append(b.events, line)
	if len(b.events) > 300 {
		b.events = b.events[len(b.events)-300:]
	}
	live := b.live
	b.mu.Unlock()
	if b.logFile != nil {
		fmt.Fprintln(b.logFile, line)
	}
	if !live {
		fmt.Fprintln(b.out, line)
	}
}

// Loop redraws the board until ctx ends.
func (b *Board) Loop(ctx context.Context) {
	if !b.live {
		return
	}
	fmt.Fprint(b.out, "\x1b[?25l\x1b[2J") // hide cursor, clear screen
	defer fmt.Fprint(b.out, "\x1b[?25h")
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		b.draw()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (b *Board) draw() {
	b.mu.Lock()
	b.frame++
	b.mu.Unlock()
	frame := b.Render()
	fmt.Fprint(b.out, "\x1b[H"+strings.ReplaceAll(frame, "\n", "\x1b[K\n")+"\x1b[J")
}

// Final prints the board once more without the live redraw.
func (b *Board) Final() {
	if b.live {
		fmt.Fprint(b.out, "\x1b[H\x1b[2J\x1b[?25h")
	}
	fmt.Fprint(b.out, b.Render())
}

func (b *Board) c(code, s string) string {
	if !b.color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

const (
	colTitle = 30
	colCell  = 7
)

// Render returns the board as text.
func (b *Board) Render() string {
	b.mu.Lock()
	frame := b.frame
	events := append([]string(nil), b.events...)
	title := b.title
	b.mu.Unlock()
	var tickets []snapshot
	if b.snapshot != nil {
		tickets = b.snapshot()
	}

	var sb strings.Builder
	head := fmt.Sprintf(" 🏭 AI SOFTWARE FACTORY  ·  %s  ·  %s ", title, time.Now().Format("15:04:05"))
	sb.WriteString(b.c("1;37;44", head) + "\n")
	sb.WriteString(b.c("2", " issue → triage → spec → [you approve] → build → verify → review → ship → PR") + "\n\n")

	sb.WriteString(b.c("1", fmt.Sprintf(" %-6s %s", "TICKET", padRight("TITLE", colTitle))))
	for _, col := range boardColumns {
		sb.WriteString(b.c("1", padRight(strings.ToUpper(col), colCell)))
	}
	sb.WriteString(b.c("1", fmt.Sprintf("%-8s %-6s %s", "TIME", "COST", "OUTCOME")) + "\n")

	if len(tickets) == 0 {
		sb.WriteString(b.c("2", "  waiting for tickets: add the intake label to a GitHub issue") + "\n")
	}
	for _, t := range tickets {
		sb.WriteString(fmt.Sprintf(" %-6s %s", fmt.Sprintf("#%d", t.Number), padRight(truncate(t.Title, colTitle-2), colTitle)))
		for _, col := range boardColumns {
			sb.WriteString(b.cellText(t.Cells[col], frame))
		}
		elapsed := ""
		if !t.Started.IsZero() {
			end := t.Ended
			if end.IsZero() {
				end = time.Now()
			}
			elapsed = fmtDuration(end.Sub(t.Started))
		}
		cost := "–"
		if t.CostUSD > 0 {
			cost = fmt.Sprintf("$%.2f", t.CostUSD)
		}
		outcome := t.Outcome
		switch {
		case t.Queued:
			outcome = b.c("2", "queued")
		case t.PR != "" && strings.HasPrefix(outcome, "✓"):
			outcome = b.c("32", outcome)
		case strings.HasPrefix(outcome, "✗"):
			outcome = b.c("31", outcome)
		case strings.HasPrefix(outcome, "?"):
			outcome = b.c("33", outcome)
		case outcome == "" && t.Note != "":
			outcome = b.c("2", t.Note)
		}
		sb.WriteString(fmt.Sprintf("%-8s %-6s %s\n", elapsed, cost, outcome))
	}

	sb.WriteString("\n" + b.c("2", " ✓ done  "+spinner[0]+" running  ‖ waiting for you  ✗ failed (retries show as ✗2)  ? needs info  – skipped") + "\n")
	sb.WriteString(b.c("2", strings.Repeat("─", 100)) + "\n")
	n := 12
	if len(events) < n {
		n = len(events)
	}
	for _, e := range events[len(events)-n:] {
		sb.WriteString(" " + truncate(e, 140) + "\n")
	}
	return sb.String()
}

func (b *Board) cellText(c cell, frame int) string {
	sym, color := "·", "2"
	switch c.State {
	case cellRun:
		sym, color = spinner[frame%len(spinner)], "33;1"
	case cellOK:
		sym, color = "✓", "32;1"
	case cellFail:
		sym, color = "✗", "31;1"
	case cellWait:
		sym, color = "‖", "35;1"
	case cellInfo:
		sym, color = "?", "33;1"
	case cellSkip:
		sym, color = "–", "2"
	}
	if c.Attempt > 1 {
		sym += fmt.Sprint(c.Attempt)
	}
	pad := colCell - utf8.RuneCountInString(sym)
	if pad < 1 {
		pad = 1
	}
	return b.c(color, sym) + strings.Repeat(" ", pad)
}
