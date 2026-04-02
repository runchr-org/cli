# Summary Browser Redesign

## Goal

Redesign the `entire summary` TUI so the session browser matches the repo's
other TUIs, defaults to the current branch, exposes explicit branch filters,
supports pagination, and shows complete session metadata plus all summary and
facet content in a readable detail view.

## Problem

The first-pass `summarytui` works functionally but does not meet the CLI's TUI
bar for readability or navigation.

Current issues:

- The detail page is plain text with no visual hierarchy, so long summaries and
  facets become difficult to scan.
- The list view uses the default `bubbles/table` height behavior instead of
  sizing from the terminal, which is why only about nine visible rows appear in
  the browser even when more sessions are loaded.
- The browser has no branch-scoped filter controls, so it drops users into a
  global recent-history view instead of the branch they are actively working on.
- The detail page omits useful metadata already present in `insightsdb`, such as
  author fields and model name.

## Desired Behavior

- Keep `bubbles/table` for the session list.
- Match the amber-accent styling and status/help patterns used by
  `memorylooptui` and `skilltui`.
- Default the list to `Current Branch`.
- Support three branch filters:
  - `Current Branch`
  - `Main`
  - `All`
- Apply pagination after filtering, with explicit page indicators and next/prev
  navigation.
- Fill the available terminal height instead of stopping at the table's default
  visible row count.
- Open a scrollable detail page that shows all available metadata, summary
  content, and facet content for the selected session.
- Preserve explicit empty states for missing summary/facet sections.

## Proposed Changes

### 1. Keep the existing command and data-loading path

`entire summary` should continue to:

- load session rows from `.entire/insights.db`
- reuse the existing summary/facet backfill path
- keep `--last`, `--agent`, `--branch`, `--me`, and `--json`

The redesign is focused on the interactive browser behavior and presentation,
not on changing the storage model.

### 2. Add a branch-filtered, paginated root model

The TUI root model should manage:

- the full loaded row set
- the active branch filter
- the filtered row slice
- the paginator state
- the table rows for the current page

The default filter should be the current git branch resolved from the repo root.
`Main` should match literal branch `main`. `All` should disable branch
filtering.

The paginator should operate on filtered results. Changing the filter should
reset the page back to the first page.

### 3. Size the table from `tea.WindowSizeMsg`

The list must stop relying on the default `bubbles/table` size.

On resize:

- store terminal `width` and `height`
- compute available vertical space after header, footer, and filter rows
- set table width and height explicitly
- rebuild the current page rows if needed

This fixes the current "why are there only 9?" behavior. The browser is not
loading only nine sessions; it is only rendering about nine visible rows because
the table height is never configured from the terminal size.

### 4. Restyle the root view to match other TUIs

The session browser should adopt the existing TUI visual language:

- bold amber app title
- amber active state / selected row emphasis
- dim inactive text and status hints
- chip-style filter controls with an active/inactive appearance
- footer status bar with key hints and page/filter status

The list should remain table-based, but the framing around it should feel like
the rest of the CLI rather than a raw Bubble Tea default table.

### 5. Replace the static detail page with a scrollable viewport

The detail page should be rendered through a `viewport` so long content can be
read comfortably.

The top metadata block should include:

- Agent
- Author
  - prefer `owner_id`
  - include `owner_name` and `owner_email` when present
- Model
- Branch
- Checkpoint
- Session
- Created timestamp
- Tokens
- Turns

The body should include all summary and facet information already carried by
`insightsdb.SessionRow`.

Summary section:

- Intent
- Outcome
- Friction
- Learnings

Facet section:

- Repeated Instructions
- Missing Context
- Failure Loops
- Skill Signals
- Review-Derived Rules
- Repo Gotchas
- Workflow Gaps

No summary or facet subsection should be silently omitted.

### 6. Preserve accessible and JSON fallbacks

The redesign should not change the existing non-TUI paths:

- `--json` still returns structured session data
- `ACCESSIBLE=1` still prints plain text instead of launching Bubble Tea

The new TUI fields should be mirrored in accessible text output where practical,
especially author metadata and the complete set of facet sections.

## Architecture

The implementation should stay within the current split:

- `cmd/entire/cli/summary_cmd.go`
  - command wiring
  - session loading
  - accessible / JSON rendering
- `cmd/entire/cli/summarytui/root.go`
  - root model state
  - filtering
  - pagination
  - list/detail navigation
- `cmd/entire/cli/summarytui/detail_page.go`
  - detail viewport model
  - complete metadata rendering
- `cmd/entire/cli/summarytui/styles.go`
  - shared amber-accent styling aligned with the repo's other TUIs

This should remain a narrow browser package rather than becoming a generic
session-management framework.

## Testing Strategy

Add or update tests for:

- branch filter defaults and branch-filtered row selection
- paginator behavior across filtered result sets
- runtime table sizing from window size updates
- detail page rendering of author/model metadata
- detail page rendering of all summary and facet sections
- detail viewport scroll/back navigation
- accessible text output including author metadata and full facet headings

## Out Of Scope

- editing summary or facet content
- fetching more rows interactively from SQLite after startup
- adding free-text search
- changing how summaries or facets are generated

## Risks

- Overcomplicating the table layer when the current package is intentionally
  small.
- Drifting stylistically from the existing TUIs if new styles are invented
  instead of reusing the repo's established amber palette.
- Accidentally hiding fields in the detail view when trying to reduce visual
  noise.

Mitigations:

- keep `bubbles/table`
- keep filter choices fixed and small
- render every summary/facet section explicitly, even when empty
- derive styles from the same Lip Gloss conventions already used elsewhere in
  the repo
