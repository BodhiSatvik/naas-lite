package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestStationNames(t *testing.T) {
	cases := map[string]string{
		stationName(StageTriage, 1): "1·triage",
		stationName(StageBuild, 1):  "3·build",
		stationName(StageBuild, 2):  "3·build #2",
		stationName(StageShip, 1):   "6·ship",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	if stationNumber(StageApproval) != 0 {
		t.Error("the approval gate is not a station")
	}
}

func TestStationToolsLeastPrivilege(t *testing.T) {
	has := func(tools []string, want string) bool {
		for _, x := range tools {
			if x == want {
				return true
			}
		}
		return false
	}
	if !has(stationTools(StageBuild, "ai-memory"), "Bash(git commit *)") {
		t.Error("build must be able to commit")
	}
	for _, s := range []Stage{StageTriage, StageSpec, StageReview, StageShip} {
		if has(stationTools(s, "ai-memory"), "Bash(git commit *)") {
			t.Errorf("%s must not be able to commit", s)
		}
	}
	if !has(stationTools(StageSpec, "ai-memory"), "mcp__ai-memory__*") {
		t.Error("spec should reach team memory")
	}
	if has(stationTools(StageSpec, ""), "mcp____*") {
		t.Error("empty memory server name must not add a rule")
	}
}

func TestApprovalFrom(t *testing.T) {
	since := time.Date(2026, 10, 23, 18, 0, 0, 0, time.UTC)
	is := Issue{Comments: []ghComment{
		{Author: ghUser{"old"}, Body: "/approve", CreatedAt: since.Add(-time.Hour)},
		{Author: ghUser{"bot"}, Body: "### 📐 Spec ready\n\nComment /approve to start", CreatedAt: since.Add(time.Minute)},
	}}
	if ok, rej, _, _ := approvalFrom(is, since, nil); ok || rej {
		t.Fatal("an old /approve and the factory's own comment must not approve")
	}
	is.Comments = append(is.Comments, ghComment{Author: ghUser{"satvik"}, Body: "  /approve looks good", CreatedAt: since.Add(2 * time.Minute)})
	if ok, _, who, _ := approvalFrom(is, since, nil); !ok || who != "@satvik" {
		t.Fatalf("want approval by @satvik, got ok=%v who=%q", ok, who)
	}
	if ok, _, _, _ := approvalFrom(is, since, []string{"someone-else"}); ok {
		t.Fatal("an approver list must restrict who can approve")
	}
	is.Comments = append(is.Comments, ghComment{Author: ghUser{"satvik"}, Body: "/reject wrong tier table", CreatedAt: since.Add(3 * time.Minute)})
	if ok, rej, _, note := approvalFrom(is, since, nil); ok || !rej || note != "wrong tier table" {
		t.Fatalf("the newest command wins: got ok=%v rej=%v note=%q", ok, rej, note)
	}
	is.Labels = []ghLabel{{Name: labelApproved}}
	if ok, _, _, _ := approvalFrom(is, since, nil); !ok {
		t.Fatal("the approved label approves")
	}
}

func TestParsePorcelainZ(t *testing.T) {
	out := " M internal/api/server.go\x00?? specs/42/SPEC.md\x00R  new.go\x00old.go\x00?? .factory/out/x.json\x00"
	got := parsePorcelainZ(out)
	if len(got) != 3 {
		t.Fatalf("want 3 changes, got %d: %+v", len(got), got)
	}
	if got[2].Path != "new.go" || got[2].Status[0] != 'R' {
		t.Errorf("rename parsed wrong: %+v", got[2])
	}
	bad := outsideAllowed(got, []string{"specs/42/"})
	if len(bad) != 2 {
		t.Errorf("want 2 changes outside specs/42/, got %+v", bad)
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("/usr/local/bin/factory"); got != "/usr/local/bin/factory" {
		t.Errorf("safe path should stay bare, got %s", got)
	}
	if got := shellQuote("/Users/me/Work Repos/x"); got != "'/Users/me/Work Repos/x'" {
		t.Errorf("got %s", got)
	}
	if got := shellQuote("it's"); got != `'it'\''s'` {
		t.Errorf("got %s", got)
	}
}

func TestResumePoint(t *testing.T) {
	for in, want := range map[Stage]Stage{
		StageTriage: StageTriage, StageSpec: StageSpec, StageApproval: StageApproval,
		StageVerify: StageBuild, StageReview: StageBuild, StageShip: StageShip, "": StageTriage,
	} {
		if got := resumePoint(in); got != want {
			t.Errorf("resumePoint(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVerAtLeast(t *testing.T) {
	if !verAtLeast("gh version 2.89.0 (2026-03-26)", 2, 88) || verAtLeast("gh version 2.40.1", 2, 88) || !verAtLeast("gh version 3.0.0", 2, 88) {
		t.Error("version compare is wrong")
	}
}

func TestRendererFeed(t *testing.T) {
	var buf bytes.Buffer
	ui := &UI{out: &buf}
	r := newRenderer(ui, "/wt")
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"abcdef1234567890","model":"claude-opus-5-5","mcp_servers":[{"name":"ai-memory","status":"connected"}]}`,
		`{"type":"user","message":{"role":"user","content":"skill text as a plain string"},"parent_tool_use_id":null}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Reading the ticket first.\n\nThen the code."},{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/wt/.factory/in/issue.md"}}]},"parent_tool_use_id":null}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"mcp__ai-memory__memory_query","input":{"query":"readiness probe"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t3","name":"Bash","input":{"command":"go test ./...","description":"run tests"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t3","content":"ok  \texample/internal/api\t0.2s\n?   \texample/cmd\t[no test files]"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t4","name":"Bash","input":{"command":"git push"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t4","is_error":true,"content":[{"type":"text","text":"Permission to use Bash with command git push has been denied."}]}]}}`,
		`not json at all`,
		`{"type":"result","subtype":"success","is_error":false,"duration_ms":61000,"num_turns":12,"result":"TRIAGE: ready","session_id":"abcdef1234567890","total_cost_usd":0.4123}`,
	}
	for _, l := range lines {
		r.handle([]byte(l))
	}
	out := buf.String()
	for _, want := range []string{
		"session abcdef12", "ai-memory connected", "Reading the ticket first.", "Read .factory/in/issue.md",
		"memory_query readiness probe", "Bash go test ./...", "ok  \texample/internal/api", "✗ Permission to use Bash",
		"not json at all",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("feed is missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "no test files") {
		t.Error("the feed should keep only test summary lines")
	}
	if r.result == nil || r.result.NumTurns != 12 || r.sessionID != "abcdef1234567890" {
		t.Errorf("result not captured: %+v", r.result)
	}
}

func TestBoardRender(t *testing.T) {
	b := newBoard(&bytes.Buffer{}, false, nil)
	b.color = false
	b.title = "acme/naas-lite · runner: herdr"
	tk := newTicket(42)
	tk.start()
	tk.setInfo("Enforce per-tier CPU and memory limits on namespace requests", "")
	tk.setCell("triage", cellOK, 0)
	tk.setCell("spec", cellOK, 0)
	tk.setCell("gate", cellWait, 0)
	tk.setNote("waiting for /approve")
	b.snapshot = func() []snapshot { return []snapshot{tk.snap()} }
	b.Log("#42 spec posted")
	out := b.Render()
	for _, want := range []string{"AI SOFTWARE FACTORY", "#42", "Enforce per-tier", "‖", "waiting for /approve", "#42 spec posted"} {
		if !strings.Contains(out, want) {
			t.Errorf("board is missing %q\n%s", want, out)
		}
	}
}

func TestTraceMarkdown(t *testing.T) {
	tk := newTicket(7)
	tk.addTrace("1·triage", &StationMeta{DurationMS: 41000, NumTurns: 6, CostUSD: 0.08}, "ok")
	tk.setLastResult("ready")
	tk.addTrace("4·verify", &StationMeta{DurationMS: 9000}, "pass")
	md := tk.traceMarkdown()
	for _, want := range []string{"2 station runs", "$0.08", "| 1·triage | 41s | 6 | $0.08 | ready |", "| 4·verify | 9s | – | – | pass |"} {
		if !strings.Contains(md, want) {
			t.Errorf("trace is missing %q\n%s", want, md)
		}
	}
}

func TestSeedsLoad(t *testing.T) {
	seeds, err := loadSeeds()
	if err != nil {
		t.Fatal(err)
	}
	if len(seeds) < 4 {
		t.Fatalf("want at least 4 demo tickets, got %d", len(seeds))
	}
	for _, s := range seeds {
		if s.Title == "" || strings.HasPrefix(s.Title, "#") {
			t.Errorf("%s: bad title %q", s.File, s.Title)
		}
	}
}

func TestHerdrJSONHelpers(t *testing.T) {
	m := map[string]any{"result": map[string]any{
		"workspace": map[string]any{"workspace_id": "w3"},
		"root_pane": map[string]any{"pane_id": "w3:p1"},
	}}
	if dig(m, "result", "root_pane", "pane_id") != "w3:p1" || findKey(m, "workspace_id") != "w3" {
		t.Error("herdr JSON helpers failed")
	}
	if dig(m, "result", "missing", "x") != "" {
		t.Error("missing path should be empty")
	}
}
