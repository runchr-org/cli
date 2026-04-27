package versioninfo

// DevVersion is the sentinel value for local/unreleased builds. Code paths
// that gate behavior on "is this a real release?" should compare against
// this constant, not the string literal.
const DevVersion = "dev"

// Version and Commit are set at build time via ldflags.
var (
	Version = DevVersion
	Commit  = "unknown"
)
