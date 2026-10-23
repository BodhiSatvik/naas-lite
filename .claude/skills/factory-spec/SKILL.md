---
name: factory-spec
description: Factory station 2 of 6. Write an implementable spec with testable acceptance criteria for the issue in .factory/in/issue.md. The factory controller invokes it as /factory-spec <issue-number>.
argument-hint: "[issue-number]"
disable-model-invocation: true
---

# Station 2: Spec (issue #$ARGUMENTS)

You are the tech lead. Turn the ticket into a spec that a builder can implement without guessing, and that a reviewer can check line by line. A human approves this spec before anyone writes code, so make it easy to approve on a phone.

## Read

1. `.factory/in/issue.md` and `.factory/out/triage.json`. If people commented on the issue (for example, answers to triage questions or a rejected spec), their comments win over the original text. Address each one.
2. `CLAUDE.md`, and the code in the affected areas.
3. Team memory: call `memory_query` with 2 to 4 keywords (area names, endpoint names, the domain term). Read every matching page under `decisions/`, `gotchas/` or `_rules/`. Check each hint against the current code before you rely on it. If the tool is not available, continue without it.

## Write `specs/$ARGUMENTS/SPEC.md` with exactly these sections

1. **Problem**: 2 to 4 sentences. Who needs what, and why.
2. **Acceptance criteria**: numbered `AC1`, `AC2`, and so on, in Given/When/Then form. Each criterion is observable and testable. Include the error cases.
3. **Design**: the smallest change that meets the criteria. Name the functions, types, routes, status codes, JSON shapes and manifest fields. Follow `CLAUDE.md`. Show one sample request and response when the API changes.
4. **Test plan**: a table with the columns `AC | test name | file`. Every AC has at least one test.
5. **Files to change**: a list of paths.
6. **Risks and rollback**: what can break in a cluster, and how to roll back.
7. **Prior knowledge**: the memory pages you used and what you took from them. Write "None found" if there were none.
8. **Out of scope**: what this change does not do.

Keep the spec under 120 lines.

## Also write `.factory/out/spec.json`

```json
{ "status": "ok", "acceptance_criteria": 4, "files": ["internal/api/health.go"], "memory_hits": [] }
```

## Rules

- Do not change code. Write only `specs/$ARGUMENTS/SPEC.md` and files in `.factory/out/`.
- If the ticket is still ambiguous, choose the conservative option and say so under **Design**.
- End your reply with the line `SPEC: ok`.
