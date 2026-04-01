# Memory Loop File Materialization Design

## Goal

Unify memory adoption and file materialization around `memory-loop` so any memory can be adopted into any scope, and approved memories can be written into project or personal instruction files, after which the file becomes the canonical source of truth.

## Problem

The current system splits related behavior across two concepts:

- `memory-loop` stores generated and manual memories, including `skill_patch`
- `entire skill` is a separate analytics/improvement workflow for skill files

This creates product and workflow gaps:

- a repo memory cannot be cleanly adopted as a personal or branch-scoped memory
- `skill_patch` exists as a memory kind, but there is no direct workflow to apply it into the corresponding `SKILL.md`
- non-skill memories can inject repeatedly even when the user wants to materialize them into durable instruction files instead
- there is no single review flow in the TUI for “keep as memory” versus “write into files”

## Desired Behavior

- Any memory can be adopted into any memory scope: `repo`, `me`, or `branch`.
- Scope adoption must not mutate the original memory in place; it should create or reconcile a scoped copy.
- A repo-derived memory can be kept personal if the user wants it without affecting teammates.
- `skill_patch` applies only to `SKILL.md`.
- All other memory kinds apply only to instruction files such as `AGENTS.md` and `CLAUDE.md`.
- File application is independent of memory scope.
- When applying to project files, write to all applicable project instruction files.
- When applying to personal files, write to all applicable installed personal agent instruction files.
- After successful file application, archive or suppress the memory unconditionally so files are the sole source of truth.

## Recommendation

Make `memory-loop` the sole lifecycle system for memory review, scope adoption, and file materialization. Keep the TUI as the primary interaction surface and add a wizard off the selected memory instead of adding new top-level commands.

## Information Model

### Memory Records

Existing `memoryloop.MemoryRecord` remains the durable store for:

- candidate/active/suppressed/archived state
- scope (`repo`, `me`, `branch`)
- origin (`generated`, `manual`)
- history

The file materialization flow should add history detail indicating:

- action type (`applied_to_files`)
- target location (`project` or `personal`)
- concrete file paths written
- whether the application targeted skill files or general instruction files

### Scope Portability

Introduce a generic adoption action in the TUI:

- any selected memory may be adopted into `repo`, `me`, or `branch`
- adoption creates or reconciles a separate scoped record keyed by the same fingerprint plus scope
- the original memory remains intact unless the user explicitly archives/suppresses it

This allows a repo-scoped candidate to become a personal active memory without changing team-visible behavior.

### Canonical Source Of Truth

Before file application, the memory record is the source of truth.

After successful file application:

- the target files become canonical
- the originating memory is archived or suppressed unconditionally
- future prompt injection should rely on file content rather than the archived memory

## TUI UX

### Entry Point

Do not add new user-facing top-level commands for this workflow. Extend the existing `entire memory-loop tui` experience.

### Wizard Flow

When a memory is selected, the TUI should open a wizard with explicit user intent selection:

1. `Adopt to scope`
2. `Apply to files`
3. `Suppress`
4. `Archive`

#### Adopt To Scope

If the user chooses `Adopt to scope`, the wizard should:

- ask for target scope: `repo`, `me`, or `branch`
- show the resulting scoped record summary
- confirm creation or reconciliation

#### Apply To Files

If the user chooses `Apply to files`, the wizard should:

- ask for file location: `project` or `personal`
- resolve concrete target files
- show a preview or diff for each target
- confirm writes
- archive the memory on success

## File Resolution Rules

### Skill Patch Memories

`skill_patch` memories only apply to `SKILL.md`.

#### Project Location

- Prefer the repo-local skill file associated with the memory's originating skill signal/path.
- If no concrete project skill target can be resolved, fail without guessing.

#### Personal Location

- Resolve matching personal skill files across installed agent roots.
- “Installed” means the corresponding home config root exists, such as `~/.claude`, `~/.codex`, or equivalent supported agent directories.
- Apply to all matching personal skill targets that exist.

### Non-Skill Memories

All non-`skill_patch` kinds apply only to instruction files.

#### Project Location

- Target repo-root `AGENTS.md` and `CLAUDE.md`.
- If both exist, apply to both.
- If only one exists, apply to the one that exists.
- If neither exists, fail before any write.

#### Personal Location

- Target all supported personal instruction-file locations for installed agents.
- “Installed” means the corresponding home config root exists.
- Apply to all matching existing personal targets.
- If none exist, fail before any write.

## Edit Strategy

### Patch-Based Updates

File application must be patch-based rather than blind append.

- `skill_patch` should generate or apply a minimal patch against the target `SKILL.md`
- non-skill memories should generate or apply a minimal patch against each instruction file
- patch generation should reconcile nearby existing content when possible to avoid duplicate or conflicting guidance

### Preview Requirement

The wizard should always show a preview before writing. The user should see:

- resolved target files
- the exact text change or diff per file
- whether the operation will archive the memory after completion

### Multi-File Consistency

For a multi-file apply:

- if any target write fails, stop and report the failing file
- do not archive the memory unless all requested writes succeed

## Relationship To `entire skill`

`entire skill` should stop being a separate source of truth. It may remain temporarily as a thin entry point into the same memory-backed review/apply flow, but the durable behavior should live in `memory-loop`.

## Non-Goals

- No new shared remote state or team-wide backend
- No automatic writing to files without explicit TUI confirmation
- No guessing of skill targets when the source skill file cannot be resolved
- No direct application of `skill_patch` to general instruction files

## Risks

- Ambiguous personal target mapping across supported agents
- Duplicate or awkward prose if patch generation is too naive
- Confusion if adoption and application are presented as the same action

The mitigation is to keep the wizard explicit, show resolved targets before writing, and make file application always archive the memory afterward.
