# Memory Loop Handoff

Repo: `/Users/alisha/.superset/worktrees/cli/agent-improvements`

Plan docs:
- [design](/Users/alisha/.superset/worktrees/cli/agent-improvements/docs/plans/2026-03-26-memory-loop-heavyweight-design.md)
- [implementation plan](/Users/alisha/.superset/worktrees/cli/agent-improvements/docs/plans/2026-03-26-memory-loop-heavyweight.md)

Design/plan commit:
- `48bf34ac docs: add heavyweight memory loop design and plan`

## What Is Done

### Task 1

Done.

Implemented in [memoryloop.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memoryloop/memoryloop.go):
- expanded memory-loop state model
- added `Mode`, `ActivationPolicy`, richer `Status`, scope/origin/outcome/history fields
- added backward-compatible load/save migration from old `snapshot` JSON
- added `LegacyInferred` handling for migrated legacy records

Tests in [memoryloop_test.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memoryloop/memoryloop_test.go).

### Task 2

Effectively done.

Implemented:
- generator emits raw candidate/generated records in [generator.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memoryloop/generator.go)
- reconciliation logic in [memoryloop.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memoryloop/memoryloop.go)
- refresh now reconciles instead of replacing store wholesale in [memory_loop_cmd.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memory_loop_cmd.go)
- refresh history is appended
- show/status no longer label all records as active

Reconciliation now handles:
- exact fingerprint matches
- reworded matches via bounded token-overlap fallback
- duplicate/near-duplicate generated records in one refresh
- legacy personal records with empty `scope_value`
- suppressed/archived preservation without resurrection
- scope-aware personal vs repo records

Key tests:
- [memoryloop_test.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memoryloop/memoryloop_test.go)
- [memory_loop_cmd_test.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memory_loop_cmd_test.go)

### Task 3

Mostly done. The last spec review had passed, and the last quality review found only two issues which were then fixed locally:
- settings validation for invalid `memory_loop.mode` / `activation_policy`
- hide zero `Last refresh` for control-only pre-refresh stores

Task 3 changes:
- replaced old enable/disable model with:
  - `entire memory-loop mode off|manual|auto`
  - `entire memory-loop policy review|auto`
- mode-first settings in [settings.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/settings/settings.go)
- lifecycle treats persisted store mode as authoritative once store exists in [lifecycle.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/lifecycle.go)
- pre-store behavior is also mode-first
- legacy compatibility mapping is now:
  - `enabled=false` => `off`
  - `enabled=true` + `claude_injection_enabled=false` => `manual`
  - `enabled=true` + `claude_injection_enabled=true` => `auto`
- pre-store `mode` and `policy` commands create a minimal local store instead of erroring
- partial local `memory_loop` overrides now deep-merge instead of replacing the whole struct
- settings validation added for invalid mode/policy
- zero refresh time hidden for control-only store

Key Task 3 files:
- [memory_loop_cmd.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memory_loop_cmd.go)
- [memory_loop_cmd_test.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memory_loop_cmd_test.go)
- [memory_loop_settings_test.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memory_loop_settings_test.go)
- [settings.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/settings/settings.go)
- [lifecycle.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/lifecycle.go)
- [lifecycle_test.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/lifecycle_test.go)
- [memoryloop.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memoryloop/memoryloop.go)
- [memoryloop_test.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memoryloop/memoryloop_test.go)

## Latest Local Verification That Passed

Task 3 settings/lifecycle focused:
- `go test ./cmd/entire/cli -run 'TestLoadEntireSettings_MemoryLoop(LocalOverrideMergesFields|Config|LegacyEnabledAndInjectionMapping|ModeAndPolicy|ExplicitModeOverridesLegacyEnabled)'`
- `go test ./cmd/entire/cli -run 'Test(EffectiveMemoryLoopMode_PreStore(UsesModeDerivedFromSettings|ExplicitModeBeatsLegacyEnabled)|HandleLifecycleTurnStart_(InjectsMemoryForClaude|StoreModeOverridesLegacySettingsGate)|SetMemoryLoop(Mode_CreatesStoreBeforeRefresh|Policy_CreatesStoreBeforeRefresh|Mode_PersistsAuthoritativeMode|Policy_PersistsActivationPolicy)|RunMemoryLoopShow_ReportsActualStatuses|FilterMemoryLoopRows)'`
- `go test ./cmd/entire/cli -run 'Test(LoadEntireSettings_(InvalidMemoryLoop(Mode|ActivationPolicy)|MemoryLoop(LocalOverrideMergesFields|Config|LegacyEnabledAndInjectionMapping|ModeAndPolicy|ExplicitModeOverridesLegacyEnabled))|RunMemoryLoopShow_(ReportsActualStatuses|HidesZeroRefreshTimeForControlOnlyStore))'`

Task 2 / memoryloop focused:
- `go test ./cmd/entire/cli/memoryloop -run 'Test(BuildGeneratedRecords_.*|ReconcileGeneratedRecords_.*|.*State|.*Select|.*Format)'`

## Important Current Status

Task 3 spec review had passed after the deep-merge fix.

Then the final Task 3 quality review found:
- missing validation for settings mode/policy
- zero-time refresh display

Those were fixed locally and the related tests pass.

What did not happen before handoff:
- no final post-fix code-quality sign-off result was received after the last validation/zero-time fixes

So the next agent should:
1. run the focused Task 3 tests listed above
2. request one final Task 3 code-quality review
3. if clean, mark Task 3 done and move to Task 4

## Likely Files Currently Changed In Worktree

Main memory-loop related files touched:
- [memory_loop_cmd.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memory_loop_cmd.go)
- [memory_loop_cmd_test.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memory_loop_cmd_test.go)
- [memory_loop_settings_test.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memory_loop_settings_test.go)
- [memoryloop.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memoryloop/memoryloop.go)
- [generator.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memoryloop/generator.go)
- [memoryloop_test.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memoryloop/memoryloop_test.go)
- [settings.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/settings/settings.go)
- [lifecycle.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/lifecycle.go)
- [lifecycle_test.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/lifecycle_test.go)

Earlier owner/scope changes also exist in:
- [session/state.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/session/state.go)
- [checkpoint/checkpoint.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/checkpoint/checkpoint.go)
- [checkpoint/committed.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/checkpoint/committed.go)
- [checkpoint/v2_committed.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/checkpoint/v2_committed.go)
- [insights_cmd.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/insights_cmd.go)
- [insightsdb/cache.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/insightsdb/cache.go)
- [insightsdb/db.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/insightsdb/db.go)
- [insightsdb/queries.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/insightsdb/queries.go)

Those owner/scope changes were already implemented and verified earlier.

## What Still Needs To Be Done

### Immediate Next Step

Finish Task 3:
1. run the focused Task 3 tests above
2. request one final Task 3 code-quality review
3. if clean, mark Task 3 done

### Remaining Plan Tasks

Task 4:
- add refresh progress output and richer summaries
- main file: [memory_loop_cmd.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memory_loop_cmd.go)
- tests: [memory_loop_cmd_test.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memory_loop_cmd_test.go)

Task 5:
- lifecycle management commands:
  - `activate`
  - `promote`
  - `suppress`
  - `unsuppress`
  - `archive`
- files:
  - [memory_loop_cmd.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memory_loop_cmd.go)
  - [memoryloop.go](/Users/alisha/.superset/worktrees/cli/agent-improvements/cmd/entire/cli/memoryloop/memoryloop.go)
  - tests in command + memoryloop test files

Task 6:
- manual memory entry:
  - `entire memory-loop add --kind ... --title ... --body ... [--scope me|repo]`

Task 7:
- improve `show` / `status` and retrieval visibility
- note: some truthful status work already got pulled into Task 2/3; build on it rather than redoing it

Task 8:
- outcome tracking and pruning
  - inject/match counts
  - `prune`
  - basic derived outcome fields

Task 9:
- broader verification
  - likely `go test ./cmd/entire/cli/...`
  - likely `mise run fmt`
  - likely `mise run lint`

## Settled Product Decisions

These were explicitly chosen with the user:
- heavyweight direction, not lightweight snapshot-only
- unified file model, not separate current/history stores
- statuses:
  - `candidate`
  - `active`
  - `suppressed`
  - `archived`
- mode:
  - `off|manual|auto`
- activation policy:
  - `review|auto`
- layered personal + repo memory
- repo-scoped generated memories do not auto-activate
- repo/shared governance is explicit promotion, not silent sharing
- personal memories can be active locally
- project/repo memory should remain inspectable and user-controlled

## Caution

There were many iterative review-driven fixes in Task 2 and Task 3.

Another agent should not assume the initial plan’s task boundaries perfectly match the current code. Some Task 7-style truthful status rendering already got pulled into Task 2/3 because earlier behavior would otherwise have been dishonest.
