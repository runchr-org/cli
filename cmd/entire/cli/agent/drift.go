package agent

import (
	"context"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/versioninfo"
)

// devVersion is the sentinel versioninfo.Version value for local/unreleased
// builds — never produce drift warnings when running one of these.
const devVersion = "dev"

// DriftReport describes a single agent whose installed hook config was
// stamped by a CLI version older than MinCompatibleCLIVersion (or is
// missing a stamp entirely; that case normalizes to "v0.0.0" and only
// reports when the floor has been raised above "0.0.0").
type DriftReport struct {
	// Agent is the registry name of the drifted agent.
	Agent types.AgentName
	// Installed is the CLI version recorded in the agent's config. Empty
	// string means the stamp was missing or unreadable.
	Installed string
	// Required is MinCompatibleCLIVersion at the time of the check.
	Required string
}

// CheckHookDrift walks every registered agent with hooks currently
// installed and returns reports for any whose stamp normalizes below
// MinCompatibleCLIVersion. Missing/unreadable stamps normalize to
// "v0.0.0" and so report only when the floor is above "0.0.0".
//
// Returns nil for dev builds (Version == "dev") since developers run
// unreleased binaries that can't meaningfully be compared.
func CheckHookDrift(ctx context.Context) []DriftReport {
	if versioninfo.Version == devVersion {
		return nil
	}

	required := MinCompatibleCLIVersion
	requiredNorm := normalizeSemver(required)

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

		meta, _ := hv.ReadHookMeta(ctx)
		if semver.Compare(normalizeSemver(meta.CLIVersion), requiredNorm) < 0 {
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
// golang.org/x/mod/semver (leading "v", valid semver). Empty/"dev" /
// unparseable strings degrade to "v0.0.0" so they sort lowest.
func normalizeSemver(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == devVersion {
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
