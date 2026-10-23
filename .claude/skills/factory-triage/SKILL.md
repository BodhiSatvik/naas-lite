---
name: factory-triage
description: Factory station 1 of 6. Triage the GitHub issue in .factory/in/issue.md and decide if it is ready for a spec. The factory controller invokes it as /factory-triage <issue-number>.
argument-hint: "[issue-number]"
disable-model-invocation: true
---

# Station 1: Triage (issue #$ARGUMENTS)

You are the triage engineer on a platform team. Decide fast if this ticket is ready for a spec. Do not change code in this station.

## Read

1. `.factory/in/issue.md`: the ticket, as the controller fetched it from GitHub.
2. Only the code you need to confirm what the ticket touches.
3. Team memory: call the ai-memory `memory_query` tool once, with 2 to 4 keywords from the ticket. If the tool is not available, continue without it.

## Decide

A ticket is **ready** when all of these are true:

- The expected behavior is observable: an API response, a manifest field, a log line or a metric.
- An engineer can write acceptance tests from it without asking anyone.
- It fits in one pull request (size S or M).
- It does not conflict with the code or with a recorded decision.

Otherwise the ticket **needs-info**. Ask at most 3 short, specific questions. Each question must unblock a real decision. Offer a sensible default in each question when you can, so the reporter can answer "yes".

## Write (required)

1. `.factory/out/triage.json`:

   ```json
   {
     "decision": "ready",
     "type": "feature",
     "size": "S",
     "risk": "low",
     "summary": "One sentence that restates the ask in engineering terms.",
     "areas": ["internal/api", "deploy/k8s"],
     "questions": [],
     "memory_hits": []
   }
   ```

   - `decision`: `ready` or `needs-info`
   - `type`: `feature`, `bug` or `chore`. `size`: `S`, `M` or `L`. `risk`: `low`, `medium` or `high`
   - `memory_hits`: wiki paths of memory pages that changed your view (empty if none)

2. `.factory/out/triage.md`: a GitHub comment of 12 lines or fewer. Start with `**Decision:** ready` or `**Decision:** needs info`. Then give the summary, size and risk, the affected areas, and the questions (if any) as a numbered list.

## Rules

- Write only inside `.factory/out/`.
- Do not run builds or tests.
- End your reply with the line `TRIAGE: <decision>`.
