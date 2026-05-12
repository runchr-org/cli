package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProductionAuthStoreIsKeyringOnly enforces that the test-only
// file-backed auth backend stays fully gated behind //go:build authfilestore.
// The production CLI binary (no build tag) must not contain code that reads
// the test env var (ENTIRE_TEST_AUTH_STORE_FILE), and arbitrary new code in
// this package must not persist tokens to disk by accident — otherwise an
// end user could silently bypass the OS keyring.
//
// One production file is the deliberate exception: file_store.go provides
// the ENTIRE_SECRETS_PATH headless-auth store from issue #1036. It is
// explicitly opt-in (user must set the env var to an absolute path), it
// enforces 0600 permissions on read and write, and it writes via temp+rename
// with a versioned JSON schema. That file is allowlisted below.
//
// This runs as part of the regular (untagged) test suite, so any new
// production-compiled .go file in this package that mentions the forbidden
// symbols will fail CI immediately. The remediation is either to move the
// offending code into a //go:build authfilestore file (see
// store_filebackend.go) or, if the addition is another deliberate
// production exception, to add it to productionFileStoreAllowlist with a
// matching design rationale.
func TestProductionAuthStoreIsKeyringOnly(t *testing.T) {
	t.Parallel()

	// Symbols that may only appear in authfilestore-tagged files or in
	// productionFileStoreAllowlist files. Keep this list tight — these
	// are the load-bearing markers of "we are persisting auth tokens to
	// a file from production code".
	forbidden := []string{
		"ENTIRE_TEST_AUTH_STORE_FILE", // the test env-var hook
		"os.WriteFile",                // token-on-disk write
		"os.ReadFile",                 // token-on-disk read
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir auth pkg: %v", err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// _test.go files are never in the production binary, so they
		// cannot reintroduce the file backend regardless of contents.
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		src := string(data)

		if hasAuthFileStoreBuildTag(src) {
			continue
		}

		if productionFileStoreAllowlist[name] {
			// ENTIRE_TEST_AUTH_STORE_FILE is still forbidden even in
			// allowlisted production files — that symbol is the
			// test-only hook and must never leak. Other forbidden
			// symbols are permitted because the allowlisted file has
			// been signed off as a deliberate production exception.
			if strings.Contains(src, "ENTIRE_TEST_AUTH_STORE_FILE") {
				t.Errorf(
					"%s is allowlisted as a production file store but references "+
						"ENTIRE_TEST_AUTH_STORE_FILE. The test env-var hook must "+
						"never appear outside an authfilestore-tagged file.",
					name,
				)
			}
			continue
		}

		for _, sym := range forbidden {
			if strings.Contains(src, sym) {
				t.Errorf(
					"%s references %q outside a //go:build authfilestore file. "+
						"File-backed auth storage must stay gated so production "+
						"builds cannot opt into it. Move this code into a tagged "+
						"file (see store_filebackend.go), or — if this is a new "+
						"deliberate production exception — add it to "+
						"productionFileStoreAllowlist with a written rationale.",
					name, sym,
				)
			}
		}
	}
}

// productionFileStoreAllowlist names production .go files in this package
// that are permitted to do file I/O. Each entry is a deliberate exception
// to the keyring-only policy enforced by TestProductionAuthStoreIsKeyringOnly,
// and the rationale lives on the file itself.
//
// Keep this list short. Every entry expands the auth-package's disk
// footprint and must be reviewed alongside the file's own design notes.
var productionFileStoreAllowlist = map[string]bool{
	// file_store.go — the ENTIRE_SECRETS_PATH headless-auth store from
	// issue #1036. Opt-in via env var, 0600 perm enforcement, atomic
	// temp+rename writes, versioned JSON schema. See the file's package
	// doc for the full design.
	"file_store.go": true,
}

// hasAuthFileStoreBuildTag reports whether the file's build constraint
// requires the authfilestore tag. Build constraints must appear before
// the package clause, so we only scan up to that point.
func hasAuthFileStoreBuildTag(src string) bool {
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "package ") {
			return false
		}
		if strings.HasPrefix(trimmed, "//go:build ") &&
			strings.Contains(trimmed, "authfilestore") {
			return true
		}
	}
	return false
}
