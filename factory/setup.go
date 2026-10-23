package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed seed/*.md
var seedFS embed.FS

func commonGH(ctx context.Context, fs *flag.FlagSet, args []string) (*GH, string, error) {
	repo := fs.String("repo", ".", "path to the main checkout")
	ghRepo := fs.String("gh-repo", "", "GitHub repo OWNER/NAME (default: detected with gh)")
	if err := fs.Parse(args); err != nil {
		return nil, "", err
	}
	root, err := repoRoot(ctx, *repo)
	if err != nil {
		return nil, "", fmt.Errorf("-repo must point at a git checkout: %w", err)
	}
	if *ghRepo == "" {
		if *ghRepo, err = detectRepo(ctx, root); err != nil {
			return nil, "", err
		}
	}
	return &GH{Repo: *ghRepo, Dir: root, Log: func(f string, a ...any) { fmt.Printf(f+"\n", a...) }}, root, nil
}

// cmdInit creates the factory labels, and optionally the project field.
func cmdInit(args []string) error {
	ctx := context.Background()
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	label := fs.String("label", "factory", "intake label")
	project := fs.String("project", "", "optional GitHub Project OWNER/NUMBER to add the \"Factory Stage\" field to")
	gh, _, err := commonGH(ctx, fs, args)
	if err != nil {
		return err
	}
	for _, d := range labelDefs(*label) {
		if err := gh.EnsureLabel(ctx, d); err != nil {
			return err
		}
		fmt.Printf("label %-26s ok\n", d.Name)
	}
	if *project != "" {
		if err := createProjectField(ctx, gh, *project); err != nil {
			return fmt.Errorf("project field: %w (does your gh token have the project scope? run: gh auth refresh -s project)", err)
		}
	}
	fmt.Printf("\nlabels ready in %s\n", gh.Repo)
	return nil
}

type seedIssue struct {
	File, Title, Body string
}

func loadSeeds() ([]seedIssue, error) {
	entries, err := fs.ReadDir(seedFS, "seed")
	if err != nil {
		return nil, err
	}
	var seeds []seedIssue
	for _, e := range entries {
		b, err := seedFS.ReadFile("seed/" + e.Name())
		if err != nil {
			return nil, err
		}
		text := strings.TrimSpace(string(b))
		title, body, _ := strings.Cut(text, "\n")
		seeds = append(seeds, seedIssue{File: e.Name(), Title: strings.TrimSpace(strings.TrimPrefix(title, "#")), Body: strings.TrimSpace(body) + "\n"})
	}
	sort.Slice(seeds, func(i, j int) bool { return seeds[i].File < seeds[j].File })
	return seeds, nil
}

// cmdSeed creates the demo tickets.
func cmdSeed(args []string) error {
	ctx := context.Background()
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	enqueue := fs.Bool("enqueue", false, "also add the intake label, so the factory picks the tickets up at once")
	label := fs.String("label", "factory", "intake label")
	list := fs.Bool("list", false, "only list the demo tickets")
	gh, _, err := commonGH(ctx, fs, args)
	if err != nil {
		return err
	}
	seeds, err := loadSeeds()
	if err != nil {
		return err
	}
	for _, s := range seeds {
		if *list {
			fmt.Printf("%s  %s\n", s.File, s.Title)
			continue
		}
		var labels []string
		if *enqueue {
			labels = []string{*label}
		}
		url, err := gh.CreateIssue(ctx, s.Title, s.Body, labels)
		if err != nil {
			return err
		}
		fmt.Printf("created %s  %s\n", url, s.Title)
	}
	if !*list && !*enqueue {
		fmt.Printf("\nAdd the %q label to a ticket (on GitHub, or: gh issue edit N --add-label %s) to put it on the line.\n", *label, *label)
	}
	return nil
}

// cmdReset removes a ticket's worktree, branch and factory stage labels, so
// you can run it again from scratch. Handy between rehearsals.
func cmdReset(args []string) error {
	ctx := context.Background()
	fs := flag.NewFlagSet("reset", flag.ContinueOnError)
	closePR := fs.Bool("close-pr", false, "also close the ticket's open PR and delete its remote branch")
	gh, root, err := commonGH(ctx, fs, args)
	if err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("usage: factory reset [-close-pr] ISSUE...")
	}
	for _, a := range fs.Args() {
		n, err := strconv.Atoi(strings.TrimPrefix(a, "#"))
		if err != nil {
			return fmt.Errorf("bad issue number %q", a)
		}
		branch := fmt.Sprintf("factory/issue-%d", n)
		if *closePR {
			if url, _ := gh.FindPR(ctx, branch); url != "" {
				if err := gh.ClosePR(ctx, url); err != nil {
					fmt.Printf("#%d: could not close %s: %v\n", n, url, err)
				} else {
					fmt.Printf("#%d: closed %s\n", n, url)
				}
			}
		}
		path := filepath.Join(root, ".worktrees", fmt.Sprintf("issue-%d", n))
		if err := removeWorktree(ctx, root, path, branch); err != nil {
			fmt.Printf("#%d: %v\n", n, err)
		}
		labels := []string{labelApproved}
		for _, s := range allStages {
			labels = append(labels, s.Label())
		}
		if err := gh.RemoveLabels(ctx, n, labels...); err != nil {
			fmt.Printf("#%d: could not remove labels: %v\n", n, err)
		}
		fmt.Printf("#%d: reset (worktree, branch and stage labels removed; the intake label stays)\n", n)
	}
	return nil
}

// cmdDoctor checks the prerequisites and says how to fix what is missing.
func cmdDoctor(args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	repo := fs.String("repo", ".", "path to the main checkout")
	memoryMCP := fs.String("memory-mcp", "ai-memory", "MCP server name of ai-memory")
	label := fs.String("label", "factory", "intake label")
	if err := fs.Parse(args); err != nil {
		return err
	}
	problems := 0
	ok := func(name, detail string) { fmt.Printf("  ✓ %-22s %s\n", name, detail) }
	warn := func(name, detail string) { fmt.Printf("  ! %-22s %s\n", name, detail) }
	bad := func(name, detail string) { problems++; fmt.Printf("  ✗ %-22s %s\n", name, detail) }
	out := func(name string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = *repo
		b, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(b)), err
	}

	fmt.Println("factory doctor")
	root, err := repoRoot(ctx, *repo)
	if err != nil {
		bad("git repo", "run this from the repo checkout (or pass -repo)")
		return fmt.Errorf("%d problem(s)", problems)
	}
	ok("git repo", root)

	if v, err := out("gh", "--version"); err != nil {
		bad("gh", "install the GitHub CLI: https://cli.github.com")
	} else {
		ver := firstLine(v)
		if verAtLeast(ver, 2, 88) {
			ok("gh", ver)
		} else {
			warn("gh", ver+" (2.88+ is needed to request a Copilot review from the CLI)")
		}
		if _, err := out("gh", "auth", "status"); err != nil {
			bad("gh auth", "run: gh auth login")
		} else {
			ok("gh auth", "logged in")
		}
		if r, err := detectRepo(ctx, root); err != nil {
			bad("GitHub repo", "push this checkout to GitHub first (gh repo create)")
		} else {
			ok("GitHub repo", r)
			labels, _ := out("gh", "label", "list", "--repo", r, "--limit", "200", "--json", "name", "-q", ".[].name")
			have := map[string]bool{}
			for _, l := range strings.Split(labels, "\n") {
				have[strings.TrimSpace(l)] = true
			}
			if have[*label] && have[StageApproval.Label()] && have[StageShipped.Label()] {
				ok("labels", "factory labels exist")
			} else {
				bad("labels", "run: factory init")
			}
		}
	}

	if v, err := out("claude", "--version"); err != nil {
		bad("claude", "install Claude Code and log in")
	} else {
		ok("claude", firstLine(v))
		if *memoryMCP != "" {
			mcp, _ := out("claude", "mcp", "list")
			if strings.Contains(mcp, *memoryMCP) {
				ok("ai-memory MCP", *memoryMCP+" is registered in Claude Code")
			} else {
				warn("ai-memory MCP", fmt.Sprintf("no %q server in 'claude mcp list'; pass -memory-mcp NAME, or run: ai-memory install-mcp --client claude-code --apply", *memoryMCP))
			}
		}
	}
	if v, err := out("ai-memory", "status"); err != nil {
		warn("ai-memory", "ai-memory CLI or server not reachable (memory is optional, but it powers the 'factory learns' part)")
	} else {
		ok("ai-memory", firstLine(v))
	}

	if v, err := out("herdr", "--version"); err != nil {
		warn("herdr", "not found; stations will run as background processes")
	} else if os.Getenv("HERDR_ENV") != "1" {
		warn("herdr", firstLine(v)+" installed, but this shell is not inside a herdr pane (start herdr, then run factory there)")
	} else {
		ok("herdr", firstLine(v)+", inside a pane")
	}

	if v, err := out("go", "version"); err != nil {
		bad("go", "install Go 1.22+")
	} else {
		ok("go", v)
	}
	if _, err := exec.LookPath("make"); err != nil {
		bad("make", "install make (Xcode command line tools on macOS)")
	} else {
		ok("make", "found")
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		warn("kubectl", "not found; the manifest dry-run in make verify is skipped")
	} else if _, err := out("kubectl", "config", "get-contexts", "kind-factory"); err != nil {
		warn("kind cluster", "no kind-factory context; manifest dry-run is skipped (create one: make kind-up)")
	} else {
		ok("kind cluster", "kind-factory context found (manifest dry-run is on)")
	}

	missing := 0
	for _, s := range []string{"triage", "spec", "build", "review", "ship"} {
		p := filepath.Join(root, ".claude", "skills", "factory-"+s, "SKILL.md")
		if _, err := os.Stat(p); err != nil {
			missing++
			bad("skill factory-"+s, "missing "+p)
		}
	}
	if missing == 0 {
		ok("skills", "5 factory skills present")
	}
	if gi, err := os.ReadFile(filepath.Join(root, ".gitignore")); err != nil || !strings.Contains(string(gi), ".worktrees/") || !strings.Contains(string(gi), ".factory/") {
		bad(".gitignore", "add .worktrees/ and .factory/")
	} else {
		ok(".gitignore", ".worktrees/ and .factory/ are ignored")
	}

	fmt.Println()
	if problems > 0 {
		return fmt.Errorf("%d problem(s) to fix", problems)
	}
	fmt.Println("ready. Start herdr, then: factory run")
	return nil
}

var verRE = regexp.MustCompile(`(\d+)\.(\d+)`)

func verAtLeast(s string, major, minor int) bool {
	m := verRE.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	return a > major || (a == major && b >= minor)
}
