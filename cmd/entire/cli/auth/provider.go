package auth

import (
	"os"
	"strings"
)

// ProviderVersionEnvVar selects which OAuth surface this CLI talks to.
//
// Recognised values:
//
//   - "v1" (or unset / unrecognised) — current device-flow surface
//   - "v2"                            — next-generation device-flow surface
//
// This is a transition-period switch: once v2 is the universal default
// the env var goes away. Surfaces are otherwise reachable as RFC 8628
// device-flow endpoints; the only differences are paths and client_id.
const ProviderVersionEnvVar = "ENTIRE_AUTH_PROVIDER_VERSION"

// providerConfig captures the per-surface bits of OAuth wiring.
type providerConfig struct {
	clientID       string
	deviceCodePath string
	tokenPath      string
}

var providers = map[string]providerConfig{
	"v1": { //nolint:gosec // OAuth client_id and endpoint paths, not credentials
		clientID:       "entire-cli",
		deviceCodePath: "/oauth/device/code",
		tokenPath:      "/oauth/token",
	},
	"v2": { //nolint:gosec // OAuth client_id and endpoint paths, not credentials
		clientID:       "cli",
		deviceCodePath: "/api/auth/oauth/device/code",
		tokenPath:      "/api/auth/token",
	},
}

// currentProvider returns the active providerConfig, defaulting to v1
// when ENTIRE_AUTH_PROVIDER_VERSION is unset or holds an unrecognised
// value. Defaulting (rather than erroring) keeps old binaries safe if
// a future v3 ever lands.
func currentProvider() providerConfig {
	switch strings.TrimSpace(os.Getenv(ProviderVersionEnvVar)) {
	case "v2":
		return providers["v2"]
	default:
		return providers["v1"]
	}
}
