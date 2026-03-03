# Plugin Architecture Research

Research into making the Entire CLI more modular and plugin-oriented, with two goals:

1. **External agents** can be used without merging into the core repo (and forever maintaining them)
2. **Internal commands** that aren't released publicly can be baked into internal CLI builds easily

## Current Architecture

### How Agents Work Today

Agents use a **factory-based registry with `init()` self-registration**:

```
cmd/entire/cli/agent/
├── agent.go              # Agent interface (19 methods) + 7 optional interfaces
├── registry.go           # Factory registry: Register(), Get(), List(), Detect()
├── types/agent.go        # AgentName, AgentType type aliases
├── claudecode/            # Each agent is a subpackage
│   ├── claude.go         #   init() { agent.Register("claude-code", NewClaudeCodeAgent) }
│   ├── hooks.go
│   ├── lifecycle.go
│   └── transcript.go
├── cursor/
├── geminicli/
├── opencode/
└── factoryaidroid/
```

**Registration flow:**
1. Each agent package has `init()` calling `agent.Register(name, factory)`
2. `hooks_cmd.go` has blank imports (`_ "...agent/claudecode"`) to trigger `init()`
3. At runtime, `agent.List()` / `agent.Get()` / `agent.Detect()` query the registry
4. `newHooksCmd()` dynamically creates subcommands from registered agents

**Architecture enforcement:**
- `architecture_test.go` verifies each agent calls `Register()` in `init()`
- Agents are forbidden from importing `strategy/`, `checkpoint/`, `session/`, or `cli/`
- Agents are "passive data providers" — framework calls them, never vice versa

### How Commands Work Today

Commands are hardcoded in `root.go`:

```go
func NewRootCmd() *cobra.Command {
    cmd.AddCommand(newRewindCmd())
    cmd.AddCommand(newResumeCmd())
    cmd.AddCommand(newCleanCmd())
    // ... 10+ commands explicitly wired
}
```

### Build System

- **goreleaser** for cross-platform builds with `CGO_ENABLED=0`
- **ldflags** inject version, commit hash, telemetry keys
- Build tags already used: `integration`, `e2e`, `unix`
- No existing plugin or conditional compilation mechanism

---

## Analysis: What's Already Good

The agent architecture is **already 80% of the way to a plugin system**:

- Clean `Agent` interface with optional interfaces via type assertions
- Factory registry pattern with name-based lookup
- Self-registration via `init()` — no central "list of all agents"
- Architecture tests enforce decoupling — agents can't import framework internals
- Dynamic hook subcommand generation from registered agents

The main coupling points that prevent external agents today:
1. Agent packages must live inside the `cmd/entire/cli/agent/` directory
2. Blank imports in `hooks_cmd.go` are the only way to activate an agent
3. `AgentName` constants are defined in `registry.go` — adding a new agent requires modifying core code
4. The binary is statically compiled — no runtime loading

---

## Option 1: External Agent Executables (kubectl/gh-style)

**Pattern:** Agents are standalone executables discovered on `$PATH`.

### How It Would Work

```
# External agent is a standalone binary
$ entire-agent-aider --capabilities    # Reports what it implements
$ entire-agent-aider --parse-hook ...  # Translates hook into Event JSON
$ entire-agent-aider --detect          # Reports if present in repo

# Discovery
$ ls ~/.entire/agents/                 # Or scan PATH for entire-agent-* prefix
entire-agent-aider
entire-agent-windsurf
```

**Protocol:** JSON over stdin/stdout (similar to how hooks already work):

```json
// entire-agent-aider --capabilities
{
  "name": "aider",
  "type": "Aider",
  "description": "Aider AI pair programmer",
  "is_preview": true,
  "protected_dirs": [".aider"],
  "supports": ["hook_support", "transcript_analyzer", "file_watcher"],
  "hook_names": ["session-start", "session-end"]
}

// entire-agent-aider --parse-hook session-start < stdin_data
{
  "type": "session_start",
  "session_id": "abc123",
  "session_ref": "/path/to/transcript",
  "timestamp": "2026-03-03T12:00:00Z"
}
```

### Pros
- **Zero coupling** — agents developed in any language, any repo
- **Independent release cycles** — agent updates don't require CLI release
- **Battle-tested pattern** — kubectl, gh, git all use this successfully
- **No CGO needed** — stays `CGO_ENABLED=0`
- **Community friendly** — anyone can write an agent

### Cons
- **Performance overhead** — process spawn per hook invocation (10-50ms each)
- **Protocol versioning** — must maintain backward-compatible JSON schema
- **Error handling complexity** — stderr parsing, exit codes, timeouts
- **Distribution burden** — each agent needs its own install/update mechanism
- **Testing complexity** — integration tests need the agent binary available
- **Loss of type safety** — JSON serialization/deserialization vs. Go interfaces

### Implementation Effort
**Medium-High.** Requires:
- Defining a stable JSON protocol for all 7+ interfaces
- Writing a `ProcessAgent` adapter that implements `agent.Agent` by shelling out
- Agent discovery logic (PATH scan, `~/.entire/agents/` directory)
- Protocol version negotiation
- Good error messages when agent binary is missing/broken

---

## Option 2: Compile-Time Plugins via Build Tags

**Pattern:** Agent code lives in external repos but is compiled in via build tags and Go module `replace` directives or a "plugin manifest" build step.

### How It Would Work

**For internal commands (unreleased features):**

```go
// cmd/entire/cli/internal_commands.go
//go:build internal

package cli

func init() {
    internalCommands = append(internalCommands,
        newSecretInternalCmd(),
        newBetaFeatureCmd(),
    )
}
```

```go
// root.go (modified)
var internalCommands []*cobra.Command // populated by build-tagged files

func NewRootCmd() *cobra.Command {
    // ... existing commands ...
    for _, cmd := range internalCommands {
        cmd.AddCommand(cmd)
    }
}
```

Internal build: `go build -tags=internal ./cmd/entire`

**For external agents in separate repos:**

```go
// In github.com/someone/entire-agent-aider (external repo):
package aider

import "github.com/entireio/cli/cmd/entire/cli/agent"

func init() {
    agent.Register("aider", NewAiderAgent)
}

type AiderAgent struct{}
func (a *AiderAgent) Name() types.AgentName { return "aider" }
// ... implement Agent interface ...
```

```go
// Custom build entrypoint: cmd/entire-custom/main.go
package main

import (
    "github.com/entireio/cli/cmd/entire/cli"
    _ "github.com/someone/entire-agent-aider"  // External agent
)

func main() {
    // Same as cmd/entire/main.go
    rootCmd := cli.NewRootCmd()
    rootCmd.Execute()
}
```

### Pros
- **Zero runtime overhead** — same as today, single static binary
- **Full type safety** — compile-time verification of all interfaces
- **Already works with current architecture** — `init()` + `Register()` pattern is ready
- **Internal builds are trivial** — just add `-tags=internal` to goreleaser
- **No protocol design needed** — reuses existing Go interfaces directly
- **Testable** — standard Go testing, no process management

### Cons
- **External agents need Go** — must be written in Go
- **Version coupling** — external agents must compile against a specific CLI version
- **Not truly "plug and play"** — requires rebuilding the binary to add agents
- **Module management** — custom builds need `go.mod` with `require` for external agents
- **API stability pressure** — changing `Agent` interface breaks all external agents

### Implementation Effort
**Low for internal commands.** Just add build-tagged files and a goreleaser variant.

**Medium for external agents.** Need to:
- Extract `agent` package interfaces into a stable, versioned SDK module
- Document the "custom build" pattern
- Possibly provide a `cmd/entire-custom/` template
- Add semantic versioning guarantees for the agent interfaces

---

## Option 3: HashiCorp-Style gRPC Plugins

**Pattern:** Agents are separate processes communicating via gRPC, managed by `hashicorp/go-plugin`.

### How It Would Work

```protobuf
// agent.proto
service AgentPlugin {
  rpc GetCapabilities(Empty) returns (Capabilities);
  rpc DetectPresence(DetectRequest) returns (DetectResponse);
  rpc ParseHookEvent(HookEventRequest) returns (Event);
  rpc ReadTranscript(ReadRequest) returns (TranscriptData);
  rpc ExtractModifiedFiles(ExtractRequest) returns (FileList);
  // ...
}
```

```go
// Plugin side (agent binary)
func main() {
    plugin.Serve(&plugin.ServeConfig{
        HandshakeConfig: handshake,
        Plugins: map[string]plugin.Plugin{
            "agent": &AgentGRPCPlugin{Impl: &AiderAgent{}},
        },
        GRPCServer: plugin.DefaultGRPCServer,
    })
}
```

```go
// Host side (CLI)
client := plugin.NewClient(&plugin.ClientConfig{
    HandshakeConfig: handshake,
    Plugins:         pluginMap,
    Cmd:             exec.Command("entire-agent-aider"),
    AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
})
defer client.Kill()

rpcClient, _ := client.Client()
raw, _ := rpcClient.Dispense("agent")
agent := raw.(Agent)
```

### Pros
- **Process isolation** — plugin crash doesn't take down CLI
- **Language agnostic** — any language with gRPC support
- **Versioned protocol** — protobuf handles schema evolution well
- **Connection reuse** — single process for multiple calls per session
- **Battle-tested** — Terraform has thousands of providers using this
- **Health checking** — built-in keepalive and restart

### Cons
- **Heavy dependency** — pulls in gRPC, protobuf, `hashicorp/go-plugin`
- **Complexity** — significant boilerplate for plugin authors
- **Startup latency** — gRPC handshake adds ~100-200ms per plugin
- **Overkill for this use case** — agents are called infrequently (hook events)
- **CGO concerns** — some gRPC features may want CGO (though pure-Go works)
- **Developer friction** — writing a Terraform provider is notoriously painful

### Implementation Effort
**High.** Requires:
- Protobuf schema design for all agent interfaces
- gRPC server/client boilerplate
- Plugin discovery and lifecycle management
- SDK package for plugin authors
- Significant documentation

---

## Option 4: Hybrid Approach (Recommended)

Combine **build tags for internal commands** with **executable-based external agents**, leveraging the existing architecture.

### Design

```
┌──────────────────────────────────────────┐
│              Entire CLI Binary            │
│                                          │
│  ┌─────────────────────────────────────┐ │
│  │         Agent Registry              │ │
│  │  ┌─────────┐ ┌─────────┐           │ │
│  │  │ Claude  │ │ Cursor  │  Built-in  │ │
│  │  │ Code    │ │         │  Agents    │ │
│  │  └─────────┘ └─────────┘           │ │
│  │  ┌─────────┐ ┌─────────┐           │ │
│  │  │ Gemini  │ │OpenCode │           │ │
│  │  └─────────┘ └─────────┘           │ │
│  │  ┌──────────────────────┐           │ │
│  │  │ ExternalAgentAdapter │──── JSON ──┼─┼──► entire-agent-aider
│  │  │ (implements Agent)   │  protocol  │ │
│  │  └──────────────────────┘           │ │
│  └─────────────────────────────────────┘ │
│                                          │
│  ┌─────────────────────────────────────┐ │
│  │         Command Registry            │ │
│  │  rewind, resume, enable, ...        │ │
│  │  ┌─────────────────────────────┐    │ │
│  │  │ //go:build internal         │    │ │
│  │  │ secret-cmd, beta-feature    │    │ │
│  │  └─────────────────────────────┘    │ │
│  └─────────────────────────────────────┘ │
└──────────────────────────────────────────┘
```

### Part A: Internal Commands via Build Tags

**Minimal changes, high value.**

1. Add a `var additionalCommands []func() *cobra.Command` in `root.go`
2. Build-tagged files in `cmd/entire/cli/commands/internal/` register commands
3. Goreleaser gets a second build config for internal releases with `-tags=internal`
4. Internal commands can import everything — they're compiled in

```go
// root.go change
var additionalCommands []func() *cobra.Command

func NewRootCmd() *cobra.Command {
    // ... existing commands ...
    for _, cmdFn := range additionalCommands {
        cmd.AddCommand(cmdFn())
    }
}
```

```go
// cmd/entire/cli/internal_billing.go
//go:build internal

package cli

func init() {
    additionalCommands = append(additionalCommands, newBillingCmd)
}

func newBillingCmd() *cobra.Command { ... }
```

### Part B: External Agent Protocol

**Moderate changes, enables community agents.**

1. Define a JSON-over-stdin/stdout protocol covering the `Agent` interface
2. Create an `ExternalAgent` adapter struct that implements `Agent` by executing the external binary
3. Discovery: scan `~/.entire/agents/` and `$PATH` for `entire-agent-*` binaries
4. External agents register themselves into the existing registry at startup
5. Keep the process alive for the duration of a hook invocation (not persistent daemon)

**Protocol design (minimal viable):**

An external agent binary must support these subcommands:

| Subcommand | Maps to | Required |
|---|---|---|
| `capabilities` | Identity methods | Yes |
| `detect` | `DetectPresence()` | Yes |
| `parse-hook <name>` | `ParseHookEvent()` (stdin: hook data, stdout: Event JSON) | If HookSupport |
| `install-hooks` | `InstallHooks()` | If HookSupport |
| `uninstall-hooks` | `UninstallHooks()` | If HookSupport |
| `read-transcript <ref>` | `ReadTranscript()` | Yes |
| `extract-files <ref> --offset N` | `ExtractModifiedFilesFromOffset()` | Optional |
| `extract-prompts <ref> --offset N` | `ExtractPrompts()` | Optional |

**Capabilities response declares what's supported:**

```json
{
  "protocol_version": 1,
  "name": "aider",
  "type": "Aider",
  "description": "Aider AI pair programmer",
  "is_preview": true,
  "protected_dirs": [".aider"],
  "interfaces": {
    "hook_support": {
      "hook_names": ["session-start", "session-end"]
    },
    "transcript_analyzer": true,
    "file_watcher": {
      "watch_paths": [".aider/sessions/*.json"]
    }
  }
}
```

**The adapter:**

```go
// cmd/entire/cli/agent/external/adapter.go
type ExternalAgent struct {
    BinaryPath   string
    caps         *Capabilities  // cached from capabilities call
}

func (e *ExternalAgent) Name() types.AgentName { return types.AgentName(e.caps.Name) }
func (e *ExternalAgent) Type() types.AgentType  { return types.AgentType(e.caps.Type) }
func (e *ExternalAgent) DetectPresence(ctx context.Context) (bool, error) {
    out, err := e.exec(ctx, "detect")
    // parse JSON bool response
}
func (e *ExternalAgent) ParseHookEvent(ctx context.Context, hookName string, stdin io.Reader) (*Event, error) {
    out, err := e.execWithStdin(ctx, stdin, "parse-hook", hookName)
    // parse JSON Event response
}
// ... etc
```

### Part C: Agent Interface SDK (Future)

For compile-time external agents (Option 2), extract the agent interfaces into a separate Go module:

```
github.com/entireio/agent-sdk/     # Stable, semver'd
├── agent.go                        # Agent interface
├── types.go                        # AgentName, AgentType, Event, etc.
├── registry.go                     # Register(), Get(), List()
└── testing/                        # Test helpers for agent authors
```

This is a future enhancement that provides compile-time type safety for Go agent authors who want tighter integration than the JSON protocol.

---

## Recommendation: Phased Rollout

### Phase 1: Internal Build Tags (1-2 days)

- Add `additionalCommands` slice to `root.go`
- Add `additionalAgents` slice pattern for build-tagged agent registration
- Create `.goreleaser.internal.yaml` with `-tags=internal`
- Add one example internal command behind `//go:build internal`
- **Result:** Internal team can ship unreleased commands immediately

### Phase 2: External Agent Protocol (1-2 weeks)

- Design JSON protocol (subcommand-based, as described above)
- Implement `ExternalAgent` adapter in `cmd/entire/cli/agent/external/`
- Add agent discovery (scan `~/.entire/agents/` + PATH)
- Register discovered external agents in the existing registry
- Write a reference external agent (e.g., re-implement a simple agent as external)
- Document "How to write an external agent"
- **Result:** Anyone can write an agent without touching this repo

### Phase 3: Agent SDK Module (future, if needed)

- Extract agent interfaces into `github.com/entireio/agent-sdk`
- Version the interfaces with semver guarantees
- Provide `cmd/entire-custom/main.go` template for custom builds
- **Result:** Go developers get compile-time type safety for agent development

---

## Risk Assessment

| Risk | Mitigation |
|---|---|
| Protocol changes break external agents | Version field + backward-compat commitment |
| External agents are slow (process spawn) | Cache capabilities; most hooks are infrequent |
| Agent interface changes | Phase 3 SDK with semver; Phase 2 protocol versioning |
| Build tag complexity | Keep it simple: one tag (`internal`), one extra goreleaser config |
| External agent security | Document trust model; agents run with user's permissions (same as any CLI tool) |
| Discovery confusion | Clear precedence: built-in > `~/.entire/agents/` > PATH |

---

## Comparable Systems

| System | Plugin Mechanism | Lesson for Us |
|---|---|---|
| **kubectl** | `kubectl-*` executables on PATH | Simple, community-loved, but no structured protocol |
| **gh (GitHub CLI)** | `gh-*` extensions with manifest | Added `gh extension` manager for install/update |
| **Terraform** | hashicorp/go-plugin (gRPC) | Powerful but heavy; good for complex providers, overkill for agents |
| **VS Code** | JavaScript extensions with typed API | SDK approach works at scale; versioned API is key |
| **Git** | Subcommands on PATH (`git-*`) | Simplest possible; has stood the test of time |
| **Docker** | CLI plugins in `~/.docker/cli-plugins/` | JSON metadata + executable; good middle ground |

The Docker CLI plugin model is closest to what makes sense here: lightweight JSON metadata + executable, with a well-defined discovery path.
