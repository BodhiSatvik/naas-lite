package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GH wraps the GitHub CLI. Every GitHub side effect in the factory goes
// through this type, inside the controller. The AI stations never get gh:
// .claude/settings.json denies it.
type GH struct {
	Repo   string // OWNER/NAME
	Dir    string // working directory for gh
	DryRun bool
	Log    func(format string, args ...any)
}

type ghLabel struct {
	Name string `json:"name"`
}

type ghUser struct {
	Login string `json:"login"`
}

type ghComment struct {
	Author    ghUser    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

// Issue is the subset of a GitHub issue the factory reads.
type Issue struct {
	Number   int         `json:"number"`
	Title    string      `json:"title"`
	Body     string      `json:"body"`
	URL      string      `json:"url"`
	State    string      `json:"state"`
	Author   ghUser      `json:"author"`
	Labels   []ghLabel   `json:"labels"`
	Comments []ghComment `json:"comments"`
}

// HasLabel reports whether the issue carries the label.
func (is Issue) HasLabel(name string) bool {
	for _, l := range is.Labels {
		if strings.EqualFold(l.Name, name) {
			return true
		}
	}
	return false
}

// LabelNames returns the issue's label names.
func (is Issue) LabelNames() []string {
	out := make([]string, 0, len(is.Labels))
	for _, l := range is.Labels {
		out = append(out, l.Name)
	}
	return out
}

// TerminalStage returns the terminal stage label on the issue, if any.
func (is Issue) TerminalStage() (Stage, bool) {
	for _, s := range []Stage{StageShipped, StageNeedsInfo, StageFailed} {
		if is.HasLabel(s.Label()) {
			return s, true
		}
	}
	return "", false
}

func (g *GH) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = g.Dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("gh %s: %s", shortArgs(args), msg)
	}
	return stdout.Bytes(), nil
}

// write runs a mutating gh command once more on failure, or only logs it in
// dry-run mode.
func (g *GH) write(ctx context.Context, args ...string) ([]byte, error) {
	if g.DryRun {
		g.Log("dry-run: gh %s", shortArgs(args))
		return nil, nil
	}
	out, err := g.run(ctx, args...)
	if err != nil && ctx.Err() == nil {
		time.Sleep(2 * time.Second)
		out, err = g.run(ctx, args...)
	}
	return out, err
}

func shortArgs(args []string) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if len(a) > 60 {
			a = a[:57] + "..."
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

// detectRepo asks gh which GitHub repo the checkout belongs to.
func detectRepo(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", "repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("cannot detect the GitHub repo (pass -gh-repo OWNER/NAME): %w", err)
	}
	repo := strings.TrimSpace(string(out))
	if repo == "" {
		return "", fmt.Errorf("cannot detect the GitHub repo (pass -gh-repo OWNER/NAME)")
	}
	return repo, nil
}

// ListIssues returns open issues that carry the label.
func (g *GH) ListIssues(ctx context.Context, label string) ([]Issue, error) {
	out, err := g.run(ctx, "issue", "list", "--repo", g.Repo, "--label", label,
		"--state", "open", "--limit", "50", "--json", "number,title,url,labels")
	if err != nil {
		return nil, err
	}
	var issues []Issue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("parse gh issue list: %w", err)
	}
	return issues, nil
}

// ViewIssue reads one issue with its comments.
func (g *GH) ViewIssue(ctx context.Context, n int) (Issue, error) {
	out, err := g.run(ctx, "issue", "view", strconv.Itoa(n), "--repo", g.Repo,
		"--json", "number,title,body,url,state,author,labels,comments")
	if err != nil {
		return Issue{}, err
	}
	var is Issue
	if err := json.Unmarshal(out, &is); err != nil {
		return Issue{}, fmt.Errorf("parse gh issue view: %w", err)
	}
	return is, nil
}

// Comment posts a comment on the issue.
func (g *GH) Comment(ctx context.Context, n int, body string) error {
	f, err := os.CreateTemp("", "factory-comment-*.md")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		return err
	}
	f.Close()
	_, err = g.write(ctx, "issue", "comment", strconv.Itoa(n), "--repo", g.Repo, "--body-file", f.Name())
	return err
}

// SetStage leaves exactly one factory stage label on the issue. It keeps the
// intake label and factory:approved.
func (g *GH) SetStage(ctx context.Context, n int, s Stage) error {
	is, err := g.ViewIssue(ctx, n)
	if err != nil {
		return err
	}
	args := []string{"issue", "edit", strconv.Itoa(n), "--repo", g.Repo}
	changed := false
	for _, st := range allStages {
		if st != s && is.HasLabel(st.Label()) {
			args = append(args, "--remove-label", st.Label())
			changed = true
		}
	}
	if !is.HasLabel(s.Label()) {
		args = append(args, "--add-label", s.Label())
		changed = true
	}
	if !changed {
		return nil
	}
	_, err = g.write(ctx, args...)
	return err
}

// RemoveLabels removes the labels that are present on the issue.
func (g *GH) RemoveLabels(ctx context.Context, n int, labels ...string) error {
	is, err := g.ViewIssue(ctx, n)
	if err != nil {
		return err
	}
	args := []string{"issue", "edit", strconv.Itoa(n), "--repo", g.Repo}
	changed := false
	for _, l := range labels {
		if is.HasLabel(l) {
			args = append(args, "--remove-label", l)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	_, err = g.write(ctx, args...)
	return err
}

// EnsureLabel creates the label or updates its color and description.
func (g *GH) EnsureLabel(ctx context.Context, d labelDef) error {
	_, err := g.write(ctx, "label", "create", d.Name, "--repo", g.Repo,
		"--color", d.Color, "--description", d.Desc, "--force")
	return err
}

// CreateIssue opens an issue and returns its URL.
func (g *GH) CreateIssue(ctx context.Context, title, body string, labels []string) (string, error) {
	f, err := os.CreateTemp("", "factory-issue-*.md")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	f.WriteString(body)
	f.Close()
	args := []string{"issue", "create", "--repo", g.Repo, "--title", title, "--body-file", f.Name()}
	for _, l := range labels {
		args = append(args, "--label", l)
	}
	out, err := g.write(ctx, args...)
	return lastURL(out), err
}

// FindPR returns the URL of the open PR for the head branch, or "".
func (g *GH) FindPR(ctx context.Context, head string) (string, error) {
	out, err := g.run(ctx, "pr", "list", "--repo", g.Repo, "--head", head,
		"--state", "open", "--json", "url", "--limit", "1")
	if err != nil {
		return "", err
	}
	var prs []struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(out, &prs); err != nil || len(prs) == 0 {
		return "", nil
	}
	return prs[0].URL, nil
}

// CreatePR opens a pull request and returns its URL.
func (g *GH) CreatePR(ctx context.Context, base, head, title, bodyFile string, draft bool) (string, error) {
	args := []string{"pr", "create", "--repo", g.Repo, "--base", base, "--head", head,
		"--title", title, "--body-file", bodyFile}
	if draft {
		args = append(args, "--draft")
	}
	out, err := g.write(ctx, args...)
	if err != nil {
		return "", err
	}
	return lastURL(out), nil
}

// EditPR updates the title and body of an existing pull request.
func (g *GH) EditPR(ctx context.Context, url, title, bodyFile string) error {
	_, err := g.write(ctx, "pr", "edit", url, "--repo", g.Repo, "--title", title, "--body-file", bodyFile)
	return err
}

// RequestReview asks a reviewer to review the PR. "@copilot" requests a
// GitHub Copilot code review (needs gh 2.88 or later and Copilot code review
// enabled for the repo).
func (g *GH) RequestReview(ctx context.Context, url, reviewer string) error {
	_, err := g.write(ctx, "pr", "edit", url, "--repo", g.Repo, "--add-reviewer", reviewer)
	return err
}

// ClosePR closes the PR and deletes its branch.
func (g *GH) ClosePR(ctx context.Context, url string) error {
	_, err := g.write(ctx, "pr", "close", url, "--repo", g.Repo, "--delete-branch")
	return err
}

var urlRE = regexp.MustCompile(`https://\S+`)

func lastURL(out []byte) string {
	all := urlRE.FindAllString(string(out), -1)
	if len(all) == 0 {
		return strings.TrimSpace(string(out))
	}
	return all[len(all)-1]
}

// approvalFrom reads the human gate. A factory:approved label, or a comment
// that starts with /approve, approves the spec. A comment that starts with
// /reject stops the ticket. Only comments newer than "since" count. When
// approvers is not empty, only those GitHub logins count.
func approvalFrom(is Issue, since time.Time, approvers []string) (approved, rejected bool, who, note string) {
	if is.HasLabel(labelApproved) {
		return true, false, "label " + labelApproved, ""
	}
	for i := len(is.Comments) - 1; i >= 0; i-- {
		cm := is.Comments[i]
		if cm.CreatedAt.Before(since) {
			continue
		}
		if len(approvers) > 0 && !containsFold(approvers, cm.Author.Login) {
			continue
		}
		body := strings.TrimSpace(cm.Body)
		lower := strings.ToLower(body)
		switch {
		case strings.HasPrefix(lower, "/approve"):
			return true, false, "@" + cm.Author.Login, ""
		case strings.HasPrefix(lower, "/reject"):
			return false, true, "@" + cm.Author.Login, strings.TrimSpace(body[len("/reject"):])
		}
	}
	return false, false, "", ""
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimPrefix(x, "@"), s) {
			return true
		}
	}
	return false
}
