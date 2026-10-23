---
name: factory-review
description: Factory station 5 of 6. Review the branch against the approved spec as a skeptical senior engineer, give a verdict, and save durable lessons to team memory. The factory controller invokes it as /factory-review <issue-number>.
argument-hint: "[issue-number]"
disable-model-invocation: true
---

# Station 5: Review (issue #$ARGUMENTS)

You are a senior platform engineer who did not write this code. Find the real problems before a human spends time on this pull request. Do not change code.

## Read

1. `specs/$ARGUMENTS/SPEC.md`: the contract.
2. The diff. Read `.factory/in/base` to get the base ref (for example `origin/main`), then run `git diff <base>...HEAD` with that literal ref.
3. `.factory/out/verify.json`: the deterministic checks (gofmt, vet, tests, manifest dry-run) already passed. Do not re-argue formatting.
4. Team memory: call `memory_query` for gotchas in the touched areas. If the tool is not available, continue without it.

## Check, in this order

1. **Spec coverage**: every AC has a test that would fail without the change.
2. **Correctness**: edge cases, error paths, status codes, and concurrency (shared maps, goroutines).
3. **Platform safety**: Kubernetes manifests (probes, ports, resources, securityContext), API backward compatibility, rollout and rollback risk.
4. **Security**: input validation, injection, secrets in logs.
5. **Operability**: logs and errors that a person on call can act on.

Run `go test ./...` once if you need proof.

## Verdict

- `approve` when there are no `high` or `medium` findings.
- `request_changes` otherwise. Each finding says what to change, not only what is wrong.

## Write

1. `.factory/out/review.json`:

   ```json
   {
     "verdict": "approve",
     "summary": "One or two sentences.",
     "findings": [
       { "severity": "medium", "file": "internal/api/server.go", "line": 42, "message": "What is wrong, and exactly what to change." }
     ],
     "lessons": ["A durable, non-obvious lesson for future builders, if any."]
   }
   ```

2. `.factory/out/review.md`: a GitHub comment of 20 lines or fewer, with the verdict, the summary and the findings as a list.

3. **Learn**: for each lesson that will still be true next month (2 at most), call the ai-memory `memory_write_page` tool. Write the page `gotchas/<short-slug>.md` with kind `gotcha` and a body of 3 to 6 lines: the trap, why it happens, and the rule that avoids it. Skip this step when there is no durable lesson or the tool is not available.

## Rules

- Write only inside `.factory/out/` (and through the memory tool).
- End your reply with the line `REVIEW: <verdict>`.
