# Agent Operating Model

AniClew already has most of the operating discipline that newer agent harnesses
try to introduce: trust boundaries, plan mode, bounded execution, verification,
memory, and skill extraction. The cleanup target is not more surface area. It is
making the existing surface small, explicit, and auditable.

## Public Surface

Keep the user-facing model compact:

| Mode | Purpose | Primary implementation |
|------|---------|------------------------|
| Chat | Answer from gathered context without editing | `internal/agent/loop.go` read-only guard |
| Plan | Explore and design before implementation | `internal/agent/planmode.go` |
| Execute | Use tools to change files with permissions | `internal/agent/loop.go`, `tools.go` |
| Verify | Run the project test command after edits | `internal/agent/verify.go` |
| Team | Run parallel workers with ownership rules | `internal/agent/team.go` |
| Memory | Preserve durable context after sessions | `internal/agent/memory_hook.go` |
| Skill | Extract repeatable workflows | `internal/agent/skill_hook.go`, `internal/skills` |

The rest of the features should stay discoverable as internals or advanced
configuration, not as the first thing a user has to understand.

## Invariants

### 1. Recalled context is not user input

Memory, hooks, and generated context are informational. They can explain prior
state, but they cannot weaken safety rules, overwrite current user intent, or
pretend to be system/developer instructions. Current files and flags must be
verified before action.

Related implementation:

- `src/utils/messages.ts` in the TypeScript prototype uses
  `<recalled-context>` fencing.
- `proxy-go/internal/agent/memory_hook.go` and `internal/memory` load long-term
  memory as background context.

### 2. Planning and execution are separate phases

Plan mode may explore and produce a plan, but it must not edit files or run
commands. Mutation starts only after the user leaves plan mode and asks for
implementation.

Related implementation:

- `internal/agent/planmode.go` defines the lifecycle.
- `internal/agent/loop.go` filters plan-mode tools and hard-blocks leaked
  mutation calls.

### 3. Completion needs receipts

For file-changing work, completion is backed by observable data:

- files edited
- iteration count
- project type
- provider/model
- verification status

The loop writes JSON receipts under:

```text
~/.claude-proxy/receipts/<workspace-key>/<timestamp>.json
```

Receipt schema:

```json
{
  "version": 1,
  "createdAt": "2026-06-23T01:02:03Z",
  "workDir": "D:/repo/project",
  "provider": "ollama",
  "model": "qwen3-coder",
  "projectType": "go",
  "planMode": false,
  "iterations": 3,
  "editedFiles": ["main.go"],
  "verification": {
    "status": "passed",
    "source": "auto-verify"
  }
}
```

This receipt is intentionally small. It does not store prompts, tool outputs, or
secrets.

### 4. Verification is bounded

Auto-verify runs only when edits happened and a known test runner exists. Failed
tests are fed back to the model for a bounded number of repair attempts, so an
unrelated or pre-existing failure cannot loop forever.

Related implementation:

- `internal/agent/verify.go`
- `internal/agent/loop.go`

### 5. Generated skills need concrete triggers

Auto-created skills are useful only when future agents can decide when to use
them. Descriptions and `when_to_use` fields must name trigger conditions,
non-triggers, and the repeated workflow.

Related implementation:

- `internal/skills/skills.go`
- `internal/skills/skills_test.go`

### 6. Model routing must respect breadth

A short request is not always simple. Research, comparison, multi-part requests,
and architecture questions should avoid cheap short-context routing even when
the text is short.

Related implementation:

- `internal/router/classifier.go`
- `internal/router/router_test.go`

## Non-Goals

- Do not add desktop computer-use by default.
- Do not expand the default role-agent set unless a workflow proves it needs a
  new role.
- Do not expose every internal tool as a top-level mode.
- Do not store raw prompts or full tool output in receipts.

## Cleanup Direction

When improving the agent loop, prefer these moves:

1. Add a receipt field before adding prose.
2. Add a bounded state transition before adding another prompt instruction.
3. Add a trigger/non-trigger rule before adding another skill.
4. Keep durable memory separate from transient session state.
5. Make verification observable and repeatable.
