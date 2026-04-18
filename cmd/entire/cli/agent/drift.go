package agent

import (
	"context"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/versioninfo"
)

// DriftReport describes a single agent whose installed hook config was stamped
// by a CLI version older than the agent's declared MinCompatibleCLIVersion
// (or is missing a stamp entirely).
type DriftReport struct {
	// Agent is the registry name of the drifted agent.
	Agent types.AgentName
	// Installed is the CLI version recorded in the agent's config. Empty when Missing.
	Installed string
	// Required is the agent's MinCompatibleCLIVersion.
	Required string
	// Missing is true when the config has no entireMeta stamp at all. Treated
	// as drift so re-running `entire enable --force` stamps existing installs.
	Missing bool
}

// CheckHookDrift walks every registered agent with hooks currently installed
// and returns reports for any whose stamp is missing or below their declared
// MinCompatibleCLIVersion. Returns nil for dev builds (Version == "dev") since
// developers run unreleased binaries that can't meaningfully be compared.
//
// The check is intentionally cheap — it does a filesystem read per installed
// agent — so `entire status` and `entire enable` can call it on every run
// without concern.
func CheckHookDrift(ctx context.Context) []DriftReport {
	if versioninfo.Version == "dev" {
		return nil
	}

	var reports []DriftReport
	for _, name := range List() {
		ag, err := Get(name)
		if err != nil {
			continue
		}
		hs, ok := AsHookSupport(ag)
		if !ok || !hs.AreHooksInstalled(ctx) {
			continue
		}
		hv, ok := AsHookVersionSupport(ag)
		if !ok {
			continue
		}

		// A floor of "0.0.0" (the default) means drift warnings are off
		// globally: we've shipped the stamp mechanism but not yet raised
		// the bar on any agent. Bail before the per-agent file read.
		required := MinCompatibleCLIVersion
		if normalizeSemver(required) == "v0.0.0" {
			continue
		}

		meta, found, err := hv.ReadHookMeta(ctx)
		if err != nil || !found {
			reports = append(reports, DriftReport{
				Agent:    name,
				Required: required,
				Missing:  true,
			})
			continue
		}

		if semver.Compare(normalizeSemver(meta.CLIVersion), normalizeSemver(required)) < 0 {
			reports = append(reports, DriftReport{
				Agent:     name,
				Installed: meta.CLIVersion,
				Required:  required,
			})
		}
	}
	return reports
}

// normalizeSemver coerces a version string into the form expected by
// golang.org/x/mod/semver (leading "v", valid semver). Empty/"dev" becomes
// "v0.0.0"; unparseable strings also degrade to "v0.0.0" so they sort lowest.
func normalizeSemver(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "dev" {
		return "v0.0.0"
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return "v0.0.0"
	}
	return v
}
