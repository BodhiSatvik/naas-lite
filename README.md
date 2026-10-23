# naas-lite + the AI software factory

A GitHub issue goes in. A reviewed pull request comes out. Six stations, one human gate, and the AI never gets write access to GitHub.

```
 GitHub issue ── you add the label "factory" ──▶  factory controller (Go reconcile loop)
                                                   │  worktree .worktrees/issue-N  +  herdr workspace
                                                   ▼
 1 triage ─▶ 2 spec ─▶ [ you: /approve ] ─▶ 3 build ─▶ 4 verify ─▶ 5 review ─▶ 6 ship ─▶ pull request
  claude      claude                         claude     make,       claude      claude        │
                                               ▲        no AI         │                       ▼
                                               └── fail ──┴── changes ┘            Copilot review
                                                                                   + you merge
 ai-memory captures every station session. Review writes "gotcha" pages. Spec reads them.
```

## What is in the box

| Path | What it is |
|---|---|
| `factory/` | The controller. Go, standard library only. Reconcile loop, herdr integration, live board. |
| `.claude/skills/factory-*` | The five AI stations as Claude Code skills. You can also run them by hand, for example `/factory-spec 12`. |
| `.claude/settings.json` | Guardrails. Deny `git push`, `gh`, `kubectl`, `curl`. Deny edits to `factory/`, `.claude/` and the `Makefile`. |
| `CLAUDE.md` | Conventions every station follows. |
| `cmd/`, `internal/`, `deploy/k8s/` | naas-lite: a small Namespace-as-a-Service API with Kubernetes manifests, for the factory to work on. |
| `factory/seed/` | Five demo tickets. Ticket 4 is vague on purpose. |
| `DEMO.md` | Run-of-show, talk track, fallback plan and likely questions. |

## How it works

1. You add the label `factory` to an issue.
2. The controller polls GitHub every 15 seconds. It sees the label, creates the worktree `.worktrees/issue-N` on the branch `factory/issue-N`, and opens a herdr workspace for the ticket.
3. Each station runs in its own herdr tab. An AI station is one headless Claude Code session: `claude -p "/factory-<station> N"`, with a least-privilege tool list. The tab shows a live feed of what the agent does.
4. The stage label on the issue moves with the ticket: `factory:triage` → `factory:spec` → `factory:awaiting-approval` → `factory:build` → `factory:verify` → `factory:review` → `factory:ship` → `factory:shipped`. Each station posts a comment.
5. The human gate: you comment `/approve` on the spec (from your phone works). `/reject <reason>` stops the ticket.
6. Verify is `make verify` (gofmt, vet, tests, and a Kubernetes manifest dry-run on a local kind cluster). It is not AI. A failed verify, or a review that requests changes, sends the ticket back to build with the log or the findings. After 3 attempts a human takes over.
7. Ship: the agent writes the PR text. The controller pushes the branch, opens the PR, requests a GitHub Copilot review, and comments a trace (time, turns and cost per station). You review and merge. The factory never merges.

Tickets run in parallel, each in its own worktree. A ticket that waits for your approval does not hold a slot. If the controller stops, run it again: tickets resume from their last stage.

## Setup (about 20 minutes)

**Prerequisites:** Go 1.22+, make, git, the GitHub CLI `gh` (2.88+ to request Copilot reviews), Claude Code (logged in), herdr. Optional: ai-memory, kind and kubectl.

1. Make a sandbox repo. Do not point the demo at a production repo.

   ```bash
   cd naas-lite
   git init -b main && git add -A && git commit -m "naas-lite + AI factory"
   gh repo create YOUR-ORG/naas-lite --private --source . --push
   ```

2. Build the controller and check your machine:

   ```bash
   make factory          # builds bin/factory
   ./bin/factory doctor  # tells you what is missing and how to fix it
   ```

3. Create the labels and the demo tickets:

   ```bash
   ./bin/factory init
   ./bin/factory seed    # creates 5 issues without the intake label
   ```

4. ai-memory: run `claude mcp list` and note the server name. If it is not `ai-memory`, pass `-memory-mcp NAME` to `factory run`. Open the web UI at `http://127.0.0.1:49374/web` (the server needs `--enable-web`).

5. Optional, a project board that moves by itself:

   ```bash
   gh auth refresh -s project
   ./bin/factory init -project @me/PROJECT_NUMBER   # adds a "Factory Stage" field
   ```

   In the project, set the board view's column field to **Factory Stage**. Then run the controller with `-project @me/PROJECT_NUMBER`.

6. Optional, the Kubernetes dry-run: `make kind-up` creates the `kind-factory` cluster. Verify then runs `kubectl apply --dry-run=server` against that context only. It never uses your current kubectl context.

## Run

Start herdr, then in a pane at the repo root:

```bash
# 1. Rehearse the plumbing for free: fake agents, real labels, comments, PR and make verify
./bin/factory run -fake -issue 1
./bin/factory reset -close-pr 1        # clean up after the rehearsal

# 2. The real thing: watch for the label
./bin/factory run
gh issue edit 2 --add-label factory    # or add the label on GitHub

# Or run tickets now, without the label
./bin/factory run -issue 2,4
```

Useful flags:

| Flag | What it does |
|---|---|
| `-model sonnet` | Faster stations. The default is your Claude Code default model. |
| `-auto-approve` | Skip the human gate (for unattended runs). |
| `-parallel 3` | Work on more tickets at once. |
| `-approvers alice,bob` | Only these GitHub users can `/approve`. |
| `-project @me/3` | Move cards on a GitHub Project board. |
| `-smoke` | Verify also builds the image and deploys it to the kind cluster. |
| `-dry-run` | No GitHub writes and no push. |
| `-runner local` | No herdr: stations run as background processes, logs in `.worktrees/issue-N/.factory/logs`. |

Other commands: `factory doctor`, `factory init`, `factory seed [-enqueue]`, `factory reset [-close-pr] N`.

## Guardrails

- **The AI never holds GitHub write access.** `.claude/settings.json` denies `gh`. The controller does every side effect.
- **No push, no cluster access, no network fetches.** `git push`, `kubectl`, `helm`, `curl` and `wget` are denied.
- **Agents cannot weaken their own gate.** Edits to `factory/`, `.claude/` and the `Makefile` are denied.
- **Least privilege per station.** Each station gets its own `--allowedTools` list. Only build can commit.
- **A second line of defense.** After triage, spec, review and ship, the controller reverts any file the station was not allowed to touch.
- **A deterministic gate.** `make verify` decides if code is good enough for review. The manifest dry-run uses the local `kind-factory` context only.
- **Humans decide.** A person approves every spec before code is written, and merges every PR.
- **Everything is audited.** Every station posts to the issue. The PR has a trace table. `claude --resume <session-id>` (shown at the end of each tab) lets you question any station.

## Where things are

- Station inputs: `.worktrees/issue-N/.factory/in/` (`issue.md`, `base`, `feedback.md` on retries)
- Station outputs: `.worktrees/issue-N/.factory/out/` (`triage.json`, `spec.json`, `build.json`, `verify.json` and `verify.log`, `review.json`, `pr.md`)
- Raw agent transcripts: `.worktrees/issue-N/.factory/logs/<station>.jsonl`
- Controller log: `.factory/controller.log`

## Troubleshooting

| Problem | Fix |
|---|---|
| "station did not start in herdr pane" | Your shell starts slowly. Use `-pane-warmup 3s`, or `-runner local`. |
| A station fails with denied tools | Read `.factory/logs/<station>.jsonl`. Add the command to that station's list in `factory/stages.go`, then `make factory`. |
| Memory tools are denied | The MCP server name is different. Check `claude mcp list` and pass `-memory-mcp NAME`. |
| Copilot review not requested | You need gh 2.88+ and Copilot code review turned on for the repo or the org. The PR still opens. |
| Re-run a ticket from scratch | `./bin/factory reset -close-pr N`, then add the label again. |
| A ticket failed and you fixed the cause | Remove the `factory:failed` label. The controller resumes from the stage that failed. |
| Triage said "needs info" | Answer the questions in a comment, then remove `factory:needs-info`. Triage runs again with your answers. |
| A station acts on the previous station's ai-memory handoff | ai-memory passes a handoff to the next session in the same folder. If that confuses a station, set `claim_on_session_start = false` under `[handoff]` in the ai-memory config and restart the server. |
| Your org's managed Claude Code settings block something | Stations pass `--permission-mode acceptEdits` and an allowlist. If a managed policy blocks MCP or hooks, the stations still run without memory. |

## Credits and prior art

The label state machine follows Warp's software factory guides (triage, spec, implement, review). The station skills borrow ideas from addyosmani/agent-skills (spec, plan, build, test, review, ship) and garrytan/gstack (review and ship roles). herdr runs the agents. akitaonrails/ai-memory gives them shared, cross-session memory.
