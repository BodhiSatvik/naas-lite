# Showcase run-of-show (about 8 minutes)

The story: "I don't use AI as autocomplete. I run a small software factory. A ticket goes in, a reviewed PR comes out, and I only do the two jobs a human must do: approve the plan and merge the code."

## Your 4-hour plan

| Time | Do this |
|---|---|
| 0:00–0:30 | Setup steps 1–4 in README.md. Run `./bin/factory doctor` until it is clean. |
| 0:30–0:45 | Rehearse the plumbing for free: `./bin/factory run -fake -issue 1`, then `./bin/factory reset -close-pr 1`. |
| 0:45–1:30 | First real run on ticket 1 (health probes). Keep its PR open: it is your backup exhibit. Fix any denied tool or flag issue now. |
| 1:30–2:15 | Second real run on ticket 3 (request IDs). Check that review writes a gotcha page and that the next spec cites memory. |
| 2:15–2:45 | Record a backup screen capture of one full run (herdr plus the GitHub issue). |
| 2:45–3:30 | Rehearse the talk below twice, with a timer. |
| 3:30–4:00 | Reset the live tickets (`./bin/factory reset -close-pr 2 4`), open the tabs, set the fonts. Done. |

Optional if you have time: `make kind-up` (manifest dry-run), and a GitHub Project board with `factory init -project`.

## Set the stage (T-10 minutes)

- [ ] herdr is open, with a pane at the repo root. Font size is large.
- [ ] Browser tabs: issue #2, issue #4, the PR from your rehearsal run, the ai-memory web UI (`http://127.0.0.1:49374/web`), and the project board if you set one up.
- [ ] GitHub mobile is open on issue #2 (you approve from the phone on stage).
- [ ] Tickets 2 and 4 are reset and have no `factory` label yet.
- [ ] Notifications off. The backup recording is ready to play.

## Talk track

**0:00 The hook (30 s).** Say the story line above. Show the empty board: `./bin/factory run`.

**0:30 One label (30 s).** Add the `factory` label to #2 (tier quota limits) and #4 ("namespaces are slow, make it better"). "That label is my whole input."

**1:00 How it works (45 s).** "The controller is a reconcile loop, like a Kubernetes controller. GitHub labels are the state. Each station is a fresh Claude Code session that runs one skill, with only the tools it needs." Point at the new herdr workspaces and tabs.

**1:45 The factory says no (45 s).** #4 comes back from triage as "needs info", with sharp questions on the issue. "It refuses to guess on a vague ticket. That is the most valuable thing a teammate can do."

**2:30 The human gate (60 s).** The spec for #2 lands as a comment. Read two acceptance criteria aloud. Point at the herdr sidebar: the ticket shows as blocked, because it needs a human. Comment `/approve` from your phone.

**3:30 Build and verify (60 s).** Open the build tab: tests first, then code. Open the verify tab: "The AI does not grade its own homework. `make verify` does: gofmt, vet, tests, and a server-side dry-run of the manifests." If verify fails, even better: show the log going back to the builder.

**4:30 Review and memory (60 s).** Open the review tab: a fresh agent that did not write the code. When it saves a lesson, open the ai-memory web UI and show the new page under `gotchas/`. "The next ticket's spec station reads this. The factory learns from its own mistakes."

**5:30 Ship (60 s).** The PR opens with a Copilot review requested. If the live run is still going, open your rehearsal PR: the acceptance-criteria checklist and the trace table with time and cost per station.

**6:30 Guardrails (45 s).** Show `.claude/settings.json`. "Agents cannot push, cannot call GitHub, cannot touch kubectl, and cannot edit the Makefile, which is the gate. Only the controller writes to GitHub. A human approves every spec and merges every PR."

**7:15 Close (45 s).** Your numbers from the rehearsal runs: minutes per ticket, dollars per ticket, how many verify loops it fixed alone. Then what comes next: run it on a slice of our real backlog in a sandbox.

## If something breaks

- **A live station stalls.** Say "here is the same run from an hour ago" and play the recording. Keep talking over it.
- **The agent goes off track.** This is a feature in the demo. Show verify failing, or review requesting changes, and the loop fixing it.
- **GitHub is slow.** The controller retries writes once and polls again. Labels catch up.
- **You need a clean slate.** `./bin/factory reset -close-pr N` removes the worktree, branch, labels and PR.

## Questions you will likely get

- **"Does our code leave the company?"** It uses the Claude Code setup that is already approved, the same path as daily use. This demo repo is a sandbox.
- **"What stops it from merging bad code?"** It cannot merge. A human approves the spec and merges the PR. In between: a deterministic verify gate, an independent AI review, and a Copilot review.
- **"What does it cost?"** Every PR has a trace with the estimated cost per station. Quote your rehearsal numbers.
- **"Why not Warp or the Copilot coding agent?"** Same idea. This one uses tools we already have approved, and our own stages, gates and guardrails. The skills are plain markdown that any agent can run.
- **"Does it scale?"** Each ticket runs in its own git worktree, in parallel. Labels are the state, so a restart resumes every ticket. Memory is shared across agents and teammates.
- **"How long did this take to build?"** An afternoon, with AI writing most of the code. That is part of the point.
