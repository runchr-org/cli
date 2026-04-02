# Summary Browser Redesign Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Redesign `entire summary` so the TUI defaults to the current branch, matches the repo's amber TUI style, paginates filtered sessions, fills the terminal correctly, and shows complete session metadata plus all summary and facet details.

**Architecture:** Keep the existing `entire summary` command and `summarytui` package, but extend the root Bubble Tea model with branch filters, paginator state, and explicit `WindowSizeMsg` sizing. Replace the static detail page with a viewport-backed renderer that shows all metadata and content from `insightsdb.SessionRow`, while preserving JSON and accessible text fallbacks in `summary_cmd.go`.

**Tech Stack:** Go 1.26.x, Cobra, Bubble Tea, Bubbles table/help/key/paginator/viewport, Lip Gloss, existing `insightsdb`, testify, `go test`, `mise`.

---

### Task 1: Lock the current behavior with failing summary command tests

**Files:**
- Modify: `cmd/entire/cli/summary_cmd_test.go`
- Modify: `cmd/entire/cli/summary_cmd.go`

**Step 1: Write the failing tests**

Add tests for:
- accessible text output including author metadata fields
- accessible text output rendering every facet section heading
- `loadSummarySessions` continuing to honor explicit `--branch` filtering while the TUI later applies its own default current-branch filter

**Step 2: Run test to verify it fails**

Run: `go test ./cmd/entire/cli -run 'TestRenderSummaryText_|TestLoadSummarySessions_'`

Expected: FAIL because the text renderer does not currently print author metadata or the full desired detail set.

**Step 3: Write minimal implementation**

Update the summary text renderer and any supporting helpers in
`cmd/entire/cli/summary_cmd.go` to:
- print author metadata when present
- render all summary/facet sections consistently
- keep current JSON behavior unchanged

**Step 4: Run test to verify it passes**

Run: `go test ./cmd/entire/cli -run 'TestRenderSummaryText_|TestLoadSummarySessions_'`

Expected: PASS.

**Step 5: Commit**

```bash
git add cmd/entire/cli/summary_cmd.go cmd/entire/cli/summary_cmd_test.go
git commit -m "test: cover summary browser text metadata"
```

### Task 2: Add failing tests for root-model filtering and pagination

**Files:**
- Modify: `cmd/entire/cli/summarytui/root_test.go`
- Modify: `cmd/entire/cli/summarytui/root.go`
- Modify: `cmd/entire/cli/summarytui/styles.go`
- Create: `cmd/entire/cli/summarytui/keys.go`

**Step 1: Write the failing tests**

Add tests for:
- default filter is `Current Branch`
- filter choices are `Current Branch`, `Main`, and `All`
- filtering rebuilds the visible row set from the full session list
- pagination splits filtered rows into multiple pages
- switching filters resets the current page to page 1

Use table/test fixtures with mixed branches and enough rows to force more than
one page.

**Step 2: Run test to verify it fails**

Run: `go test ./cmd/entire/cli/summarytui -run 'TestRoot(Filter|Pagination|View)'`

Expected: FAIL because the current root model has no filter or paginator state.

**Step 3: Write minimal implementation**

Implement in `cmd/entire/cli/summarytui/root.go`:
- branch filter enum/state
- current-branch default wiring
- filtered row slice
- paginator state
- helpers to rebuild the current page's table rows

Implement in `cmd/entire/cli/summarytui/keys.go`:
- key bindings for filter changes and page navigation

Implement in `cmd/entire/cli/summarytui/styles.go`:
- amber filter chip and status-bar styles aligned with the repo's other TUIs

**Step 4: Run test to verify it passes**

Run: `go test ./cmd/entire/cli/summarytui -run 'TestRoot(Filter|Pagination|View)'`

Expected: PASS.

**Step 5: Commit**

```bash
git add cmd/entire/cli/summarytui/root.go cmd/entire/cli/summarytui/root_test.go cmd/entire/cli/summarytui/styles.go cmd/entire/cli/summarytui/keys.go
git commit -m "feat: add summary browser filters and pagination"
```

### Task 3: Add failing tests for runtime table sizing and visible-row count

**Files:**
- Modify: `cmd/entire/cli/summarytui/root_test.go`
- Modify: `cmd/entire/cli/summarytui/root.go`

**Step 1: Write the failing tests**

Add tests for:
- `tea.WindowSizeMsg` setting table width and height
- large terminals showing more than the default table body row count
- resizing preserving the selected row when possible

**Step 2: Run test to verify it fails**

Run: `go test ./cmd/entire/cli/summarytui -run 'TestRoot(WindowSize|Resizes|VisibleRows)'`

Expected: FAIL because the root model currently never sizes the table from the terminal.

**Step 3: Write minimal implementation**

Update `cmd/entire/cli/summarytui/root.go` to:
- handle `tea.WindowSizeMsg`
- compute header/footer chrome height
- set `table.SetWidth(...)`
- set `table.SetHeight(...)`
- rebuild rows after resize if page layout changes

**Step 4: Run test to verify it passes**

Run: `go test ./cmd/entire/cli/summarytui -run 'TestRoot(WindowSize|Resizes|VisibleRows)'`

Expected: PASS.

**Step 5: Commit**

```bash
git add cmd/entire/cli/summarytui/root.go cmd/entire/cli/summarytui/root_test.go
git commit -m "fix: size summary browser table from terminal"
```

### Task 4: Add failing tests for the redesigned detail viewport

**Files:**
- Modify: `cmd/entire/cli/summarytui/detail_page.go`
- Create: `cmd/entire/cli/summarytui/detail_page_test.go`
- Modify: `cmd/entire/cli/summarytui/root.go`
- Modify: `cmd/entire/cli/summarytui/root_test.go`

**Step 1: Write the failing tests**

Add tests for:
- detail page rendering author/model metadata
- detail page rendering all summary sections
- detail page rendering all facet sections even when empty
- detail page scroll behavior through a viewport
- `Esc` returning from detail to the table while preserving the selected row

**Step 2: Run test to verify it fails**

Run: `go test ./cmd/entire/cli/summarytui -run 'TestDetail|TestRootUpdate_(Enter|Escape)'`

Expected: FAIL because the current detail page is a static string renderer with incomplete metadata.

**Step 3: Write minimal implementation**

Replace the current detail model with a viewport-backed detail page that:
- stores `width` and `height`
- builds structured, fully-populated content text
- includes `owner_id`, `owner_name`, `owner_email`, `model`, `created_at`,
  `tokens`, and `turns`
- supports scroll keys in detail mode

Update `cmd/entire/cli/summarytui/root.go` to pass size updates and key events
through to the detail page while in `stateDetail`.

**Step 4: Run test to verify it passes**

Run: `go test ./cmd/entire/cli/summarytui -run 'TestDetail|TestRootUpdate_(Enter|Escape)'`

Expected: PASS.

**Step 5: Commit**

```bash
git add cmd/entire/cli/summarytui/detail_page.go cmd/entire/cli/summarytui/detail_page_test.go cmd/entire/cli/summarytui/root.go cmd/entire/cli/summarytui/root_test.go
git commit -m "feat: redesign summary browser detail page"
```

### Task 5: Restyle the browser shell to match other TUIs

**Files:**
- Modify: `cmd/entire/cli/summarytui/styles.go`
- Modify: `cmd/entire/cli/summarytui/root.go`
- Modify: `cmd/entire/cli/summarytui/detail_page.go`
- Modify: `cmd/entire/cli/summarytui/root_test.go`

**Step 1: Write the failing tests**

Add tests for:
- header/title text and filter chip rendering
- footer key-hint rendering
- selected-row emphasis present in the root view output

Keep assertions textual and stable rather than snapshotting ANSI output.

**Step 2: Run test to verify it fails**

Run: `go test ./cmd/entire/cli/summarytui -run 'TestRootView_|TestDetailView_'`

Expected: FAIL because the current shell view is plain text with minimal styling structure.

**Step 3: Write minimal implementation**

Update `styles.go`, `root.go`, and `detail_page.go` to:
- use the repo's amber-accent visual language
- add a title row
- add filter chips
- add a status/footer bar with page/filter hints
- keep colorless terminals readable when color is disabled

**Step 4: Run test to verify it passes**

Run: `go test ./cmd/entire/cli/summarytui -run 'TestRootView_|TestDetailView_'`

Expected: PASS.

**Step 5: Commit**

```bash
git add cmd/entire/cli/summarytui/styles.go cmd/entire/cli/summarytui/root.go cmd/entire/cli/summarytui/detail_page.go cmd/entire/cli/summarytui/root_test.go
git commit -m "style: align summary browser with repo tui style"
```

### Task 6: Wire current-branch defaults from the command layer

**Files:**
- Modify: `cmd/entire/cli/summary_cmd.go`
- Modify: `cmd/entire/cli/summary_cmd_test.go`
- Modify: `cmd/entire/cli/summarytui/root.go`

**Step 1: Write the failing tests**

Add tests for:
- TUI launch path passing the current git branch into the root model
- explicit `--branch` continuing to work for non-TUI output
- no regressions in `--json` and `ACCESSIBLE=1` modes

**Step 2: Run test to verify it fails**

Run: `go test ./cmd/entire/cli ./cmd/entire/cli/summarytui -run 'TestRunSummary_|TestRoot'`

Expected: FAIL because the current TUI constructor does not know the current branch.

**Step 3: Write minimal implementation**

Update `cmd/entire/cli/summary_cmd.go` to:
- resolve the current branch when launching the TUI
- pass it into `summarytui.Run(...)`
- leave non-TUI outputs unchanged except for the richer text rendering from Task 1

Update `cmd/entire/cli/summarytui/root.go` to use that current-branch value as
the default filter selection.

**Step 4: Run test to verify it passes**

Run: `go test ./cmd/entire/cli ./cmd/entire/cli/summarytui -run 'TestRunSummary_|TestRoot'`

Expected: PASS.

**Step 5: Commit**

```bash
git add cmd/entire/cli/summary_cmd.go cmd/entire/cli/summary_cmd_test.go cmd/entire/cli/summarytui/root.go
git commit -m "feat: default summary browser to current branch"
```

### Task 7: Run focused verification and format

**Files:**
- Modify: any touched files from previous tasks

**Step 1: Run focused package tests**

Run: `go test ./cmd/entire/cli ./cmd/entire/cli/summarytui`

Expected: PASS.

**Step 2: Run formatting**

Run: `mise run fmt`

Expected: no diff or formatting-only changes.

**Step 3: Re-run focused package tests**

Run: `go test ./cmd/entire/cli ./cmd/entire/cli/summarytui`

Expected: PASS.

**Step 4: Commit**

```bash
git add cmd/entire/cli/summary_cmd.go cmd/entire/cli/summary_cmd_test.go cmd/entire/cli/summarytui
git commit -m "feat: redesign summary browser tui"
```

### Task 8: Run broader required verification before completion

**Files:**
- Modify: `AGENTS.md` and `CLAUDE.md` only if command behavior documentation needs updating

**Step 1: Run lint**

Run: `mise run lint`

Expected: PASS.

**Step 2: Run CI-equivalent test suite**

Run: `mise run test:ci`

Expected: PASS.

**Step 3: Review the final diff**

Check that:
- the list defaults to current branch
- the three branch filters are present and obvious
- pagination applies to filtered rows
- the table fills the terminal instead of showing only the default body size
- the detail view shows author metadata and all summary/facet sections
- accessible and JSON modes remain intact

**Step 4: Final commit**

```bash
git add cmd/entire/cli/summary_cmd.go cmd/entire/cli/summary_cmd_test.go cmd/entire/cli/summarytui docs/plans/2026-04-02-summary-browser-design.md docs/plans/2026-04-02-summary-browser.md AGENTS.md CLAUDE.md
git commit -m "feat: redesign summary browser tui"
```
