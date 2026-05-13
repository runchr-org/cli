package redact

// GetOPFConfigForTest exposes the package-private global to tests in other
// packages via the exported GetOPFConfig function. This file documents the
// cross-package test seam; the actual implementation lives in opf.go.
//
// The "_test.go" suffix means this file is excluded from production builds.
// External tests should use redact.GetOPFConfig() and redact.ResetOPFConfig()
// directly.
func GetOPFConfigForTest() *OPFConfig { return getOPFConfig() }

// ResetOPFConfigForTest nils the package-private global so tests in other
// packages can return to "never configured" state. Mirrors the
// resetOPFConfig helper used inside redact's own _test.go files.
func ResetOPFConfigForTest() {
	opfConfigMu.Lock()
	defer opfConfigMu.Unlock()
	opfConfig = nil
}
