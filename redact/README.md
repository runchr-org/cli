# redact

The `redact` package implements layered secret and PII redaction for agent
transcripts stored by the Entire CLI. It is the single authoritative boundary
through which transcript bytes must pass before being persisted to a checkpoint
or returned to a caller.

## Layers

1. **Entropy-based** — high-entropy alphanumeric sequences (threshold 4.5 bits/char)
2. **Pattern-based** — betterleaks regex rules (260+ known secret formats)
3. **Credentialed URIs** — URLs containing userinfo passwords
4. **Connection strings** — JDBC, keyword DSNs, semicolon connection strings
5. **Custom rules** — user-defined patterns via `ConfigureCustomRules`
6. **Credential key/value pairs** — `DB_PASSWORD=...` style assignments
7. **PII detection** — email, phone, address patterns (opt-in via `ConfigurePII`)
8. **OpenAI Privacy Filter (OPF)** — named-entity redaction via external binary
   (opt-in via `ConfigurePrivacyFilter`; only invoked at condensation + export
   boundaries, not per-turn)

## Benchmarks

Adapter-overhead benchmark (fake runtime, fast):

    mise run bench -- ./redact/... BenchmarkRedactJSONLBytesWithPrivacyFilter

Live OPF benchmark (requires `pip install opf` and `OPF_BIN` set):

    go test -tags opf_integration -bench BenchmarkRedactJSONLBytes -benchtime=3x ./redact/...
