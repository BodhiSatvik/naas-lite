// Command factory runs an AI software factory for one GitHub repo.
//
// GitHub issues go in. Reviewed pull requests come out. Each ticket moves
// through six stations: triage, spec, build, verify, review and ship. The
// AI stations are headless Claude Code sessions that each run one skill
// from .claude/skills. Verify is a plain "make verify" gate. A human
// approves every spec before any code is written, and merges every PR.
//
// The controller is a reconcile loop, like a Kubernetes controller: GitHub
// labels hold the state, and the loop drives each ticket toward "shipped".
// Only the controller writes to GitHub. The agents never push and never
// call gh.
//
// See README.md at the repo root.
package main

import (
	"fmt"
	"os"
)

const version = "0.1.0"

const usageText = `factory - ticket-to-PR software factory (GitHub + herdr + Claude Code)

Usage:
  factory doctor                      check prerequisites
  factory init [-project OWNER/NUM]   create the labels (and an optional project field)
  factory seed [-enqueue]             create the demo tickets
  factory run [flags]                 start the controller (reconcile loop)
  factory reset [-close-pr] ISSUE...  delete a ticket's worktree, branch and stage labels
  factory station [flags] STAGE ISSUE run one station (the controller calls this)
  factory version

Run "factory run -h" for controller flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	var err error
	args := os.Args[2:]
	switch os.Args[1] {
	case "run":
		err = cmdRun(args)
	case "station":
		err = cmdStation(args)
	case "init":
		err = cmdInit(args)
	case "seed":
		err = cmdSeed(args)
	case "doctor":
		err = cmdDoctor(args)
	case "reset":
		err = cmdReset(args)
	case "version", "-v", "--version":
		fmt.Println("factory", version)
	case "help", "-h", "--help":
		fmt.Print(usageText)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usageText)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "factory:", err)
		os.Exit(1)
	}
}
