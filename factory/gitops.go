package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func gitCmd(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return strings.TrimSpace(stdout.String()), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func repoRoot(ctx context.Context, dir string) (string, error) {
	return gitCmd(ctx, dir, "rev-parse", "--show-toplevel")
}

// ensureWorktree gives a ticket its own checkout on its own branch, so many
// tickets can be in flight at once without touching each other or your main
// checkout. It reuses an existing worktree, which lets a ticket resume. It
// returns whether it created the worktree, and the base ref to diff against.
func ensureWorktree(ctx context.Context, repo, path, branch, base string) (created bool, baseRef string, err error) {
	baseRef = "origin/" + base
	if _, err := gitCmd(ctx, repo, "fetch", "--quiet", "origin", base); err != nil {
		// Offline is fine for a demo: fall back to the local branch.
		if _, verr := gitCmd(ctx, repo, "rev-parse", "--verify", "--quiet", baseRef); verr != nil {
			baseRef = base
		}
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return false, baseRef, nil
	}
	_, _ = gitCmd(ctx, repo, "worktree", "prune")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, baseRef, err
	}
	if _, err := gitCmd(ctx, repo, "worktree", "add", "-B", branch, path, baseRef); err != nil {
		return false, baseRef, err
	}
	return true, baseRef, nil
}

// removeWorktree deletes a ticket's worktree and local branch.
func removeWorktree(ctx context.Context, repo, path, branch string) error {
	var errs []string
	if _, err := os.Stat(path); err == nil {
		if _, err := gitCmd(ctx, repo, "worktree", "remove", "--force", path); err != nil {
			errs = append(errs, err.Error())
		}
	}
	_, _ = gitCmd(ctx, repo, "worktree", "prune")
	if _, err := gitCmd(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		if _, err := gitCmd(ctx, repo, "branch", "-D", branch); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

type fileChange struct {
	Status string // two-letter porcelain status, e.g. " M", "??", "A "
	Path   string
}

// changedFiles lists uncommitted changes in the worktree. Ignored files (the
// .factory/ run folder) never show up.
func changedFiles(ctx context.Context, dir string) ([]fileChange, error) {
	out, err := gitCmdRaw(ctx, dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return parsePorcelainZ(out), nil
}

func parsePorcelainZ(out string) []fileChange {
	var files []fileChange
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		fc := fileChange{Status: e[:2], Path: e[3:]}
		if fc.Status[0] == 'R' || fc.Status[0] == 'C' {
			i++ // the next entry is the original path
		}
		if strings.HasPrefix(fc.Path, ".factory/") {
			continue
		}
		files = append(files, fc)
	}
	return files
}

func gitCmdRaw(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// outsideAllowed returns the changes that are not under any allowed prefix.
func outsideAllowed(files []fileChange, allow []string) []fileChange {
	var bad []fileChange
	for _, f := range files {
		ok := false
		for _, a := range allow {
			if strings.HasPrefix(f.Path, a) {
				ok = true
				break
			}
		}
		if !ok {
			bad = append(bad, f)
		}
	}
	return bad
}

// discardChanges reverts the listed changes in the worktree.
func discardChanges(ctx context.Context, dir string, files []fileChange) error {
	for _, f := range files {
		var err error
		switch {
		case f.Status == "??":
			err = os.RemoveAll(filepath.Join(dir, f.Path))
		case f.Status[0] == 'A':
			_, err = gitCmd(ctx, dir, "rm", "-f", "--quiet", "--", f.Path)
		default:
			_, err = gitCmd(ctx, dir, "checkout", "HEAD", "--", f.Path)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// commitAll commits every change in the worktree. It reports whether it made
// a commit.
func commitAll(ctx context.Context, dir, msg string) (bool, error) {
	files, err := changedFiles(ctx, dir)
	if err != nil || len(files) == 0 {
		return false, err
	}
	if _, err := gitCmd(ctx, dir, "add", "-A"); err != nil {
		return false, err
	}
	if _, err := gitCmd(ctx, dir, "commit", "--quiet", "-m", msg); err != nil {
		return false, err
	}
	return true, nil
}

// commitPaths commits changes under the given paths only.
func commitPaths(ctx context.Context, dir, msg string, paths ...string) (bool, error) {
	args := append([]string{"add", "--"}, paths...)
	if _, err := gitCmd(ctx, dir, args...); err != nil {
		return false, err
	}
	if _, err := gitCmd(ctx, dir, "diff", "--cached", "--quiet"); err == nil {
		return false, nil // nothing staged
	}
	if _, err := gitCmd(ctx, dir, "commit", "--quiet", "-m", msg); err != nil {
		return false, err
	}
	return true, nil
}

func commitsAhead(ctx context.Context, dir, base string) (int, error) {
	out, err := gitCmd(ctx, dir, "rev-list", "--count", base+"..HEAD")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

func pushBranch(ctx context.Context, dir, branch string) error {
	_, err := gitCmd(ctx, dir, "push", "--quiet", "--force-with-lease", "-u", "origin", branch)
	return err
}
