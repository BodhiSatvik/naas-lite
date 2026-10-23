---
name: factory-build
description: Factory station 3 of 6. Implement the approved spec in specs/<issue>/SPEC.md, tests first, then commit. The factory controller invokes it as /factory-build <issue-number>.
argument-hint: "[issue-number]"
disable-model-invocation: true
---

# Station 3: Build (issue #$ARGUMENTS)

You are the builder. Implement the approved spec exactly. The spec is the contract: a reviewer will check every acceptance criterion against your tests.

## Read

1. `specs/$ARGUMENTS/SPEC.md`: the approved contract.
2. `.factory/in/feedback.md`: if this file exists, this is a retry. Fix everything it lists first, then check the spec again.
3. `CLAUDE.md` for the conventions.

## Do

1. Write the tests from the spec's test plan first. Run them and see them fail.
2. Write the smallest change that makes them pass. Touch only the files the spec lists, plus tests. If you must touch another file, say why in `notes`.
3. Keep `deploy/k8s/` in sync with every port, endpoint or probe change.
4. Run `make verify` until it passes. It runs gofmt, go vet, go test, and a manifest dry-run when the local kind cluster exists.
5. Commit with a conventional message that references the issue, for example `feat(api): add readiness endpoint (#$ARGUMENTS)`. One or more commits are fine.

## Write `.factory/out/build.json`

```json
{ "status": "done", "summary": "What changed, in one or two sentences.", "tests_added": ["TestReadyz"], "notes": "" }
```

Use `"status": "blocked"` only when the spec cannot be implemented as written. Explain why in `notes`.

## Rules

- Never push, never call `gh`, never edit `factory/`, `.claude/` or the spec. The guardrails block these anyway.
- Do not add dependencies.
- End your reply with the line `BUILD: <status>`.
