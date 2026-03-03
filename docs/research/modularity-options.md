# Modularity Research: External Agents & Internal-Only Commands

## Current Architecture

The CLI already has strong foundations for modularity:

- **Agent registry** (`agent/registry.go`): Thread-safe `Register(name, factory)` + `Get(name)` pattern
- **Self-registration via `init()`**: Each agent calls `agent.Register()` in its `init()` function
- **Blank imports**: Two files (`hooks_cmd.go`, `config.go`) pull in agents via `_ "github.com/.../claudecode"`
- **Strict import boundaries**: Architecture tests enforce that agents can't import framework internals
- **Composable interfaces**: Core `Agent` (14 methods) + 7 optional interfaces (`HookSupport`, `TranscriptAnalyzer`, etc.)
- **Dynamic command generation**: Hook subcommands are built from `agent.List()` at startup

The coupling points are narrow: two blank-import sites and the agent name/type constants in `registry.go`.

---

## Idea 1: External Agent Repos

### Goal

Let third parties (or ourselves) maintain agent integrations in separate repos so the core CLI doesn't need to merge PRs for every new agent or agent update.

### Approach A: Compile-Time Composition (Recommended)

**How it works**: The `database/sql` driver pattern. Agent implementations live in separate Go modules. The main binary pulls them in via blank imports. Different "editions" of the binary can include different agent sets.

```
# External repo: github.com/example/entire-agent-neovim
# Implements agent.Agent + agent.HookSupport
# Has init() that calls agent.Register("neovim", NewNeovimAgent)

# In the main CLI's go.mod:
require github.com/example/entire-agent-neovim v1.2.0

# In a registration file (e.g., cmd/entire/cli/agents_external.go):
import _ "github.com/example/entire-agent-neovim"
```

**What needs to change**:

1. **Extract agent contract to a standalone module** — Create `github.com/entireio/cli-agent-sdk` (or `github.com/entireio/cli/agent/sdk`) containing:
   - `Agent` interface
   - `HookSupport`, `TranscriptAnalyzer`, and other optional interfaces
   - `types` package (`AgentName`, `AgentType`, `HookInput`, `Event`, etc.)
   - `Register()` function and registry
   - Utility packages agents are allowed to import (`logging`, `paths`, `jsonutil`, etc.)

2. **External agents depend only on the SDK module**, not the full CLI

3. **Main CLI imports the SDK + blank-imports each agent** — External agents are just `go get` dependencies

**Pros**:
- Zero runtime overhead (same as today's in-process pattern)
- Type-safe — compiler catches interface mismatches
- External maintainers own their agent lifecycle (releases, tests)
- Versioned via Go modules (semver, `go.sum` integrity)
- Architecture test still works (just check imports against the SDK module)
- No cross-platform issues (works on Windows, macOS, Linux)

**Cons**:
- SDK module becomes a public API surface — breaking changes need semver discipline
- Upgrading an agent still requires a CLI release (new `go.mod` entry → new binary)
- External agents must be compiled with the same Go version (minor, not exact like `plugin`)

**Real-world precedents**:
- `database/sql` drivers (`_ "github.com/lib/pq"`)
- `golangci-lint` custom linters (module plugin mode)
- OpenTelemetry collector contributions (`otelcol-contrib` vs `otelcol-core`)
- Caddy server modules (`xcaddy build --with github.com/...`)

### Approach B: External Binary Plugins (kubectl/gh-style)

**How it works**: Plugins are standalone executables discovered on `$PATH`. The CLI finds `entire-agent-<name>` binaries and delegates to them.

```
# User installs: /usr/local/bin/entire-agent-neovim
# CLI discovers it via PATH scan
# Communication via stdin/stdout (JSON protocol) or gRPC
```

**What needs to change**:

1. **Define a plugin protocol** — JSON-over-stdio is simplest:
   - CLI sends: `{"command": "parse-hook-event", "hook": "stop", "stdin": "..."}`
   - Plugin responds: `{"event": {"type": "TurnEnd", ...}}`
   - Need protocol for each Agent interface method

2. **Plugin discovery** — Scan `$PATH` for `entire-agent-*` binaries at startup

3. **Proxy agent implementation** — An `ExternalAgent` struct that calls out to the binary for each interface method

**Pros**:
- Fully decoupled — no recompilation needed to add agents
- Language-agnostic (agents could be written in Python, Rust, etc.)
- Independent release cycles — users install/update agents separately

**Cons**:
- **Significant performance cost** — Every hook invocation spawns a subprocess (hooks are latency-sensitive, called on every user prompt)
- **Protocol versioning** — Must maintain backwards-compatible JSON schema
- **Error handling complexity** — Process crashes, timeouts, version mismatches
- **Testing burden** — Need integration tests for the protocol layer
- **Distribution story** — Need a package manager or install mechanism (`entire agent install <name>`)
- **More moving parts for users** — Binary version mismatches, PATH issues

**Real-world precedents**:
- kubectl plugins (`kubectl-<name>` on PATH)
- gh CLI extensions (`gh extension install`)
- Docker CLI plugins (`docker-<name>`)
- Git subcommands (`git-<name>`)

### Approach C: HashiCorp go-plugin (gRPC)

**How it works**: Plugins are separate binaries that communicate with the host via gRPC over a local socket. HashiCorp's `go-plugin` library handles the lifecycle.

**Pros**:
- Process isolation (plugin crash doesn't kill CLI)
- Cross-language support
- Battle-tested (Terraform, Vault, Nomad)
- Bidirectional communication

**Cons**:
- **Massive overkill for this use case** — Terraform providers do heavy I/O (cloud API calls). Agent hooks do lightweight event parsing
- Adds gRPC dependency chain (~5MB binary size increase)
- Still needs a distribution story
- Complex to debug

### Recommendation for External Agents

**Start with Approach A (compile-time composition)**. It's the lowest-effort, highest-value path:

1. Extract an SDK module from the existing `agent/` package — this is mostly a packaging exercise since the interfaces and import boundaries already exist
2. Move Factory AI Droid (or any partner agent) to an external repo as the first proof
3. Keep the blank-import pattern — it's simple and it works
4. Offer a `xcaddy`-like builder for custom builds: `entirex build --with github.com/example/entire-agent-foo`

If there's future demand for non-Go agents or fully decoupled distribution, **layer Approach B on top** — the registry pattern supports both internal and external agents simultaneously.

---

## Idea 2: Internal-Only Subcommands

### Goal

Some commands should only be available to Entire team members (debug tools, admin commands, experimental features) without shipping them to all users.

### Approach A: Build Tags (Recommended)

**How it works**: Use Go build tags to conditionally compile internal commands.

```go
// cmd/entire/cli/internal_commands.go
//go:build entire_internal

package cli

func init() {
    internalCommands = append(internalCommands,
        newDebugShadowBranchCmd(),
        newInspectCheckpointCmd(),
        newBenchmarkCmd(),
    )
}
```

```go
// cmd/entire/cli/internal_commands_default.go
//go:build !entire_internal

package cli

// No internal commands in public builds
```

```go
// root.go — register internal commands
var internalCommands []*cobra.Command

func NewRootCmd() *cobra.Command {
    // ... existing commands ...
    for _, cmd := range internalCommands {
        root.AddCommand(cmd)
    }
}
```

**Build**:
```bash
# Public release (default)
go build ./cmd/entire

# Internal build (includes debug commands)
go build -tags entire_internal ./cmd/entire
```

**Pros**:
- Zero overhead in public builds — internal code isn't even compiled in
- Simple and idiomatic Go
- Already using build tags for `integration` and `e2e` tests
- No runtime check overhead
- Internal commands can import anything (no API boundary needed)
- Works with goreleaser — just don't add the tag to release builds

**Cons**:
- Internal users need a separate build (or a `mise run build:internal` task)
- Can't toggle at runtime — need to rebuild

### Approach B: Runtime Feature Flag

**How it works**: Check an environment variable or config file at startup to gate commands.

```go
func NewRootCmd() *cobra.Command {
    // ...
    if os.Getenv("ENTIRE_INTERNAL") == "1" || isInternalUser() {
        cmd.AddCommand(newDebugShadowBranchCmd())
        cmd.AddCommand(newInspectCheckpointCmd())
    }
}

func isInternalUser() bool {
    // Check ~/.entire/internal.json or org membership
    home, _ := os.UserHomeDir()
    _, err := os.Stat(filepath.Join(home, ".entire", "internal"))
    return err == nil
}
```

**Pros**:
- Same binary for everyone — simpler distribution
- Users can opt-in without rebuilding
- Can be toggled on/off instantly

**Cons**:
- Internal code ships to everyone (larger binary, potential information leak)
- Commands visible via `strings` on the binary
- Risk of accidental activation by external users

### Approach C: Separate Binary / Plugin

**How it works**: Internal commands live in a separate `entire-internal` binary or plugin.

```bash
# Internal users install an additional binary
entire-internal debug shadow-branch
entire-internal inspect checkpoint abc123
```

Or as a CLI plugin:
```bash
entire internal debug shadow-branch  # delegates to entire-internal binary
```

**Pros**:
- Clean separation — no internal code in public binary
- Internal tool can have its own release cycle
- Can import CLI internals as a library

**Cons**:
- Another binary to distribute and maintain
- Version synchronization between CLI and internal tool
- Users need to keep both in sync

### Approach D: Hidden Commands with Auth Check

**How it works**: Commands are always compiled in but hidden and require authentication.

```go
func newInternalCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:    "internal",
        Hidden: true,
        PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
            if !verifyInternalAccess() {
                return fmt.Errorf("this command requires internal access")
            }
            return nil
        },
    }
    cmd.AddCommand(newDebugCmd())
    cmd.AddCommand(newInspectCmd())
    return cmd
}
```

**Pros**:
- Single binary, single distribution
- Proper access control (can verify org membership via API)

**Cons**:
- Code ships to everyone
- Needs an auth mechanism (API call, token file, etc.)
- Auth adds latency and failure modes

### Recommendation for Internal Commands

**Use Approach A (build tags)** for truly internal-only tools (debug commands, inspection tools). It's the simplest, most secure (code not in public binary), and follows existing patterns in the codebase.

**Combine with Approach B (runtime flag)** for experimental features that might graduate to public — use `ENTIRE_EXPERIMENTAL=1` to gate features that are being tested with early adopters before general availability.

Concretely:

```
//go:build entire_internal     → Debug/admin tools (never public)
ENTIRE_EXPERIMENTAL=1          → Upcoming features (will become public)
Hidden: true                   → Already used for hooks, analytics (implementation detail)
```

---

## Idea 3: Additional Modularity Ideas

### Strategy Plugins

The `Strategy` interface already supports multiple implementations. Could allow external strategy implementations the same way as agents — extract a strategy SDK, let external repos implement the interface.

### Hook Protocol Plugins

Instead of making entire agents external, make just the **hook parsing** pluggable. A lightweight protocol where external tools can emit lifecycle events in a standard format:

```bash
# Any tool can emit events to entire via a standard protocol
entire hooks emit --event turn-end --session-id abc123 --agent-type "My Tool"
```

This would let any tool integrate without implementing the full Agent interface.

### Subcommand Plugins (gh-style)

Allow external commands without the full agent interface:

```bash
# entire-ext-dashboard → becomes `entire dashboard`
# entire-ext-metrics   → becomes `entire metrics`
```

Useful for companion tools (web dashboards, analytics, etc.) that aren't agents.

---

## Implementation Priority

| Priority | Item | Effort | Value |
|----------|------|--------|-------|
| 1 | Build tags for internal commands | Small (1-2 days) | High — immediate need, simple pattern |
| 2 | Extract agent SDK module | Medium (3-5 days) | High — unblocks external agents |
| 3 | Move one agent to external repo | Small (1-2 days) | Medium — proves the pattern |
| 4 | `ENTIRE_EXPERIMENTAL` flag | Small (1 day) | Medium — useful for feature rollout |
| 5 | External binary plugin support | Large (1-2 weeks) | Low — only if non-Go agents needed |
| 6 | Hook emit protocol | Medium (3-5 days) | Medium — lowers integration barrier |
