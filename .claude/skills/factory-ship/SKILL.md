---
name: factory-ship
description: Factory station 6 of 6. Write the pull request title and description for the finished branch. The factory controller invokes it as /factory-ship <issue-number>; the controller then pushes and opens the PR.
argument-hint: "[issue-number]"
disable-model-invocation: true
---

# Station 6: Ship (issue #$ARGUMENTS)

You are the release engineer. Write the pull request that a busy reviewer will read on a phone. The controller pushes the branch and opens the PR. You only write text.

## Read

- `specs/$ARGUMENTS/SPEC.md`
- Read `.factory/in/base` to get the base ref, then run `git log --oneline <base>..HEAD` and `git diff --stat <base>...HEAD` with that literal ref.
- `.factory/out/triage.json`, `.factory/out/build.json`, `.factory/out/verify.json` and `.factory/out/review.json`

## Write

1. `.factory/out/ship.json`: `{ "title": "feat(api): <what changed> (#$ARGUMENTS)" }`. Use a conventional-commit title of 72 characters or fewer.
2. `.factory/out/pr.md` with these sections:
   - **Summary**: 2 to 3 sentences. End with `Closes #$ARGUMENTS`.
   - **Changes**: 3 to 7 bullets, by behavior, not by file.
   - **Acceptance criteria**: a checklist with every AC from the spec. Tick each one and name the test that proves it.
   - **Verification**: what `make verify` ran and its result. Say if the kind manifest dry-run was skipped.
   - **Review**: the review verdict, and how the findings were resolved.
   - **Risk and rollback**: from the spec, updated if needed.
3. If this change set a convention that future work must follow, call `memory_write_page` to record it as `decisions/<short-slug>.md` with kind `decision` (context, decision, consequences). Skip this when it did not.

## Rules

- Write only inside `.factory/out/` (and through the memory tool).
- End your reply with the line `SHIP: ready`.
