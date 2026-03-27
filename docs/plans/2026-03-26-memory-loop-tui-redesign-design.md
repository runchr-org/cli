# Memory Loop TUI Redesign Design

## Summary

Redesign the memory-loop TUI so it feels like an intentional terminal application instead of a set of lightly styled tables. Build two usable layouts on top of the existing memory-loop state and actions:

- `Inspector`: a keyboard-first operational view for reviewing and acting on individual memories
- `Dashboard`: a higher-level overview for scanning memory health, recent activity, and system status

Both layouts should share the same data model, commands, lifecycle actions, and styling primitives so the user can compare them in actual use and decide which direction to keep.

## Goals

- Make the TUI visually coherent and easier to scan.
- Preserve fast keyboard-driven workflows for reviewing memories.
- Let the user compare `Inspector` and `Dashboard` layouts in the same command.
- Surface the most important memory-loop information without forcing tab-hopping.
- Improve hierarchy, spacing, and panel structure without changing memory-loop semantics.

## Non-Goals

- No change to memory-loop storage or record lifecycle semantics.
- No rewrite of refresh, ranking, or injection logic.
- No new remote state or shared backend.
- No irreversible decision about final layout in this change; the user will compare both.

## Current Problems

The current TUI works, but the presentation is weak:

- the shell is mostly a thin tab bar plus plain content
- most screens read like raw tables rather than an application layout
- related information is split across separate full-screen tabs
- there is little top-level summary or visual orientation
- selected-state, metadata, and actions do not have a strong hierarchy
- settings consume a full screen even though they are low-frequency controls

This makes the interface feel unfinished even when the underlying functionality is useful.

## Design Options Considered

### Option 1: Polish the Existing 4-Tab Layout

Pros:

- smallest change
- preserves current navigation model

Cons:

- keeps the same weak screen structure
- does not solve the “disconnected screens” feeling
- still makes the TUI feel table-first instead of task-first

### Option 2: Two-Mode Shell with Shared Panels

Pros:

- supports direct A/B comparison of two layouts
- reuses the same state, actions, and rendering primitives
- lets the product discover whether the better default is operational or overview-first

Cons:

- more rendering work up front
- requires some root-level navigation reshaping

### Option 3: Separate Commands for Separate Layouts

Pros:

- clean conceptual split

Cons:

- duplicates navigation and maintenance cost
- awkward for quick comparison
- too heavy for what is fundamentally one product decision

## Recommendation

Implement **Option 2: one TUI with two top-level layout modes**.

This gives the user a real comparison without fragmenting the feature. The redesign should replace the current tab-first shell with a stronger application frame and treat memories, injection, history, and settings as composable panels.

## Information Architecture

### Top-Level Modes

The TUI should have two primary layout modes:

- `Inspector`
- `Dashboard`

`Inspector` should be the default because the core memory-loop workflow is operational: scan records, inspect one, and apply lifecycle actions quickly.

### Shared App Frame

Both layouts should share a consistent frame:

- header
- summary strip
- main content area
- footer help/status bar

The header should show the current layout mode plus current memory-loop controls such as mode and activation policy. The summary strip should expose small high-value metrics at a glance.

### Shared Data Panels

The app should treat the following as reusable panels instead of separate full-screen destinations:

- memory list
- selected memory detail
- injection tester
- recent injections
- refresh history
- settings controls

Each layout can arrange these panels differently while reusing the same rendering logic and action messages.

## Layout Designs

### Inspector Layout

`Inspector` is a working view for acting on memories.

Recommended arrangement:

- left pane: searchable/filterable memory list
- right pane: selected memory detail card
- right pane lower sections: lifecycle actions, provenance, usage, and outcome metadata
- optional compact secondary panel for injection testing or recent injections

The list should dominate the layout. The right side should feel like an inspector card rather than a dump of fields.

### Dashboard Layout

`Dashboard` is a read-first overview for scanning system state.

Recommended arrangement:

- top row: metric cards
- upper body: status distribution and recent activity panels
- lower body: compact memory list with selection support
- side or lower panel: selected memory summary/detail

The dashboard should still allow selection and action, but it should prioritize “what is happening?” over “work through this queue.”

## Interaction Model

### Navigation

- top-level navigation switches between `Inspector` and `Dashboard`
- core list navigation remains keyboard-first
- selection stays stable when changing layouts whenever possible
- actions should dispatch the same lifecycle messages in either layout

### Memory Workflow

In `Inspector`:

- arrows or `j`/`k` move through memories
- `/` enters search
- `f` cycles status filters
- `enter` expands or collapses long detail content
- single-keystroke lifecycle actions remain available

In `Dashboard`:

- arrow keys move between panels or list rows
- selecting a memory updates the detail panel in place
- the user should not need to jump to another screen to understand the selected record

### Settings

Settings should no longer be a dedicated full-screen view. They should become a lower-priority panel or drawer that keeps controls available without taking over the whole interface.

### Injection Testing

Injection testing should remain in the TUI, but integrated as a panel:

- compact in `Inspector`
- richer and more visible in `Dashboard`

This keeps it attached to the memory system rather than feeling like a disconnected tool.

## Visual Language

The TUI should move from “styled output” to “intentional dashboard.”

### Visual Principles

- restrained slate/gray base
- warm amber accent for focus and navigation
- green/yellow/red reserved for status semantics
- strong whitespace and panel separation
- more hierarchy, fewer cramped columns

### Typography and Hierarchy

Use:

- bold section titles
- uppercase micro-labels for panel headings
- dim metadata for lower-priority text
- stronger selected state for rows and cards

### Panels and Cards

Prefer framed panels and compact cards over raw full-width tables. Tables can still be used where appropriate, but the main experience should rely on:

- metric cards
- memory summary rows
- detail cards
- scoped footer hints

## Implementation Boundaries

This redesign should remain a presentation and navigation refactor.

### Must Reuse

- existing memory-loop state loading/saving
- existing lifecycle action messages
- existing injection test behavior
- existing settings mutation behavior

### Should Change

- root model layout mode and app frame
- rendering composition
- panel structure
- keyboard help and mode switching
- list/detail presentation

### Should Not Change

- memory-loop JSON schema
- generator and ranking behavior
- refresh command semantics
- lifecycle rules for candidate, active, suppressed, and archived records

## Testing Strategy

The risk is mostly in rendering and navigation regressions, so tests should focus on:

- root view mode switching
- panel rendering with representative state
- stable selection and filter behavior across layouts
- settings and lifecycle action dispatch staying wired correctly

Because the package currently has little direct TUI coverage, add focused tests around render helpers and layout behavior instead of trying to snapshot the entire app in one giant assertion.

## Rollout

Ship both layouts in the same command and let the user compare them in real use. After feedback:

- choose the better default
- keep both if they serve distinct workflows
- or remove the weaker layout once the preferred direction is clear
