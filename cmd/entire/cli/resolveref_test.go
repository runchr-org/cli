package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/entireio/cli/internal/coreapi"
)

// Valid ULID-shaped fixtures (26 Crockford base32 chars, no I/L/O/U) so the
// resolver tests exercise the ULID short-circuit instead of a name lookup.
const (
	ulidOrgAcme        = "0123456789ABCDEFGHJKMNPQR1"
	ulidOrgGlobex      = "0123456789ABCDEFGHJKMNPQR2"
	ulidProjectWidgets = "0123456789ABCDEFGHJKMNPQR3"
	ulidAccount        = "0123456789ABCDEFGHJKMNPQR4"
	ulidResolvedAcct   = "0123456789ABCDEFGHJKMNPQR9"
)

// resolveTestClient builds a coreapi client pointed at a test server whose
// handler is h, and returns the client plus a counter of HTTP requests seen.
// It lets the resolver tests assert the load-bearing invariant from
// resolveref.go's doc comment: a ULID ref makes zero network calls, a name ref
// makes exactly one.
func resolveTestClient(t *testing.T, h http.HandlerFunc) (*coreapi.Client, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	c, err := coreapi.NewWithBearer(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewWithBearer: %v", err)
	}
	return c, &calls
}

func TestResolveOrgRef(t *testing.T) {
	t.Parallel()
	orgs := &coreapi.ListOrgsOutputBody{Orgs: []coreapi.Org{
		{ID: ulidOrgAcme, Name: "acme"},
		{ID: ulidOrgGlobex, Name: "globex"},
	}}

	t.Run("ULID passes through without a network call", func(t *testing.T) {
		t.Parallel()
		c, calls := resolveTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			t.Error("unexpected HTTP call for a ULID ref")
			w.WriteHeader(http.StatusInternalServerError)
		})
		got, err := resolveOrgRef(context.Background(), c, ulidOrgGlobex)
		if err != nil {
			t.Fatalf("resolveOrgRef: %v", err)
		}
		if got != ulidOrgGlobex {
			t.Errorf("resolveOrgRef = %q, want the ULID unchanged", got)
		}
		if n := calls.Load(); n != 0 {
			t.Errorf("ULID ref made %d HTTP calls, want 0", n)
		}
	})

	t.Run("name resolves via exactly one list call", func(t *testing.T) {
		t.Parallel()
		c, calls := resolveTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			if err := writeJSON(w, orgs); err != nil {
				t.Errorf("encode orgs: %v", err)
			}
		})
		got, err := resolveOrgRef(context.Background(), c, "globex")
		if err != nil {
			t.Fatalf("resolveOrgRef: %v", err)
		}
		if got != ulidOrgGlobex {
			t.Errorf("resolveOrgRef = %q, want globex id", got)
		}
		if n := calls.Load(); n != 1 {
			t.Errorf("name ref made %d HTTP calls, want 1", n)
		}
	})

	t.Run("name match is case-insensitive", func(t *testing.T) {
		t.Parallel()
		c, _ := resolveTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			if err := writeJSON(w, orgs); err != nil {
				t.Errorf("encode orgs: %v", err)
			}
		})
		got, err := resolveOrgRef(context.Background(), c, "ACME")
		if err != nil {
			t.Fatalf("resolveOrgRef: %v", err)
		}
		if got != ulidOrgAcme {
			t.Errorf("resolveOrgRef(ACME) = %q, want acme id", got)
		}
	})
}

func TestResolveProjectRef(t *testing.T) {
	t.Parallel()
	projects := &coreapi.ListProjectsOutputBody{Projects: []coreapi.Project{
		{ID: ulidProjectWidgets, Name: "widgets", OwnerId: ulidOrgAcme, OwnerType: coreapi.ProjectOwnerTypeOrg},
	}}

	t.Run("ULID passes through without a network call", func(t *testing.T) {
		t.Parallel()
		c, calls := resolveTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			t.Error("unexpected HTTP call for a ULID ref")
			w.WriteHeader(http.StatusInternalServerError)
		})
		got, err := resolveProjectRef(context.Background(), c, ulidProjectWidgets)
		if err != nil {
			t.Fatalf("resolveProjectRef: %v", err)
		}
		if got != ulidProjectWidgets {
			t.Errorf("resolveProjectRef = %q, want the ULID unchanged", got)
		}
		if n := calls.Load(); n != 0 {
			t.Errorf("ULID ref made %d HTTP calls, want 0", n)
		}
	})

	t.Run("name resolves via exactly one list call", func(t *testing.T) {
		t.Parallel()
		c, calls := resolveTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			if err := writeJSON(w, projects); err != nil {
				t.Errorf("encode projects: %v", err)
			}
		})
		got, err := resolveProjectRef(context.Background(), c, "widgets")
		if err != nil {
			t.Fatalf("resolveProjectRef: %v", err)
		}
		if got != ulidProjectWidgets {
			t.Errorf("resolveProjectRef = %q, want widgets id", got)
		}
		if n := calls.Load(); n != 1 {
			t.Errorf("name ref made %d HTTP calls, want 1", n)
		}
	})

	t.Run("name match is case-insensitive", func(t *testing.T) {
		t.Parallel()
		c, _ := resolveTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			if err := writeJSON(w, projects); err != nil {
				t.Errorf("encode projects: %v", err)
			}
		})
		got, err := resolveProjectRef(context.Background(), c, "Widgets")
		if err != nil {
			t.Fatalf("resolveProjectRef: %v", err)
		}
		if got != ulidProjectWidgets {
			t.Errorf("resolveProjectRef(Widgets) = %q, want widgets id", got)
		}
	})
}

func TestResolveAccountRef(t *testing.T) {
	t.Parallel()

	t.Run("ULID passes through without a network call", func(t *testing.T) {
		t.Parallel()
		c, calls := resolveTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			t.Error("unexpected HTTP call for a ULID ref")
			w.WriteHeader(http.StatusInternalServerError)
		})
		got, err := resolveAccountRef(context.Background(), c, ulidAccount)
		if err != nil {
			t.Fatalf("resolveAccountRef: %v", err)
		}
		if got != ulidAccount {
			t.Errorf("resolveAccountRef = %q, want the ULID unchanged", got)
		}
		if n := calls.Load(); n != 0 {
			t.Errorf("ULID ref made %d HTTP calls, want 0", n)
		}
	})

	t.Run("handle resolves via exactly one call", func(t *testing.T) {
		t.Parallel()
		c, calls := resolveTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			if err := writeJSON(w, &coreapi.ResolvedIdentity{AccountId: ulidResolvedAcct, Provider: "github", Handle: "alice"}); err != nil {
				t.Errorf("encode identity: %v", err)
			}
		})
		got, err := resolveAccountRef(context.Background(), c, "github:alice")
		if err != nil {
			t.Fatalf("resolveAccountRef: %v", err)
		}
		if got != ulidResolvedAcct {
			t.Errorf("resolveAccountRef = %q, want resolved account id", got)
		}
		if n := calls.Load(); n != 1 {
			t.Errorf("handle ref made %d HTTP calls, want 1", n)
		}
	})

	t.Run("empty resolved account id is an error", func(t *testing.T) {
		t.Parallel()
		c, _ := resolveTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			if err := writeJSON(w, &coreapi.ResolvedIdentity{AccountId: "", Provider: "github", Handle: "alice"}); err != nil {
				t.Errorf("encode identity: %v", err)
			}
		})
		if _, err := resolveAccountRef(context.Background(), c, "github:alice"); err == nil {
			t.Error("resolveAccountRef expected error for empty account id")
		}
	})

	t.Run("non-qualified handle fails before any network call", func(t *testing.T) {
		t.Parallel()
		c, calls := resolveTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			t.Error("unexpected HTTP call for an invalid handle")
			w.WriteHeader(http.StatusInternalServerError)
		})
		if _, err := resolveAccountRef(context.Background(), c, "alice"); err == nil {
			t.Error("resolveAccountRef expected error for non-qualified handle")
		}
		if n := calls.Load(); n != 0 {
			t.Errorf("invalid handle made %d HTTP calls, want 0", n)
		}
	})
}

func TestLooksLikeULID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want bool
	}{
		{in: "01J0ABCDEFGHJKMNPQRSTVWXYZ", want: true}, // 26 chars, valid alphabet
		{in: "01j0abcdefghjkmnpqrstvwxyz", want: true}, // lowercase accepted
		{in: "acme", want: false},                      // short name
		{in: "my-project", want: false},                // hyphen not in alphabet
		{in: "", want: false},                          // empty
		{in: "01J0ABCDEFGHJKMNPQRSTVWXY", want: false}, // 25 chars
		{in: "01J0ABCDEFGHJKMNPQRSTVWXYZ0", want: false},
		{in: "01J0ABCDEFGHIKMNPQRSTVWXYZ", want: false}, // contains I
		{in: "01J0ABCDEFGHLKMNPQRSTVWXYZ", want: false}, // contains L
		{in: "01J0ABCDEFGHOKMNPQRSTVWXYZ", want: false}, // contains O
		{in: "01J0ABCDEFGHUKMNPQRSTVWXYZ", want: false}, // contains U
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			if got := looksLikeULID(tt.in); got != tt.want {
				t.Errorf("looksLikeULID(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseQualifiedHandle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in           string
		wantProvider string
		wantHandle   string
		wantErr      bool
	}{
		{in: "github:alice", wantProvider: "github", wantHandle: "alice"},
		{in: "github:alice:bob", wantProvider: "github", wantHandle: "alice:bob"}, // only first colon splits
		{in: "alice", wantErr: true},                                              // no provider prefix
		{in: "github:", wantErr: true},                                            // empty handle
		{in: ":alice", wantErr: true},                                             // empty provider
		{in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			provider, handle, err := parseQualifiedHandle(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseQualifiedHandle(%q) expected error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseQualifiedHandle(%q): %v", tt.in, err)
			}
			if provider != tt.wantProvider || handle != tt.wantHandle {
				t.Errorf("parseQualifiedHandle(%q) = (%q, %q), want (%q, %q)", tt.in, provider, handle, tt.wantProvider, tt.wantHandle)
			}
		})
	}
}

func TestPickOrg(t *testing.T) {
	t.Parallel()
	orgs := []coreapi.Org{
		{ID: ulidOrgAcme, Name: "acme"},
		{ID: ulidOrgGlobex, Name: "globex"},
	}

	t.Run("unique match", func(t *testing.T) {
		t.Parallel()
		got, err := pickOrg(orgs, "globex")
		if err != nil {
			t.Fatalf("pickOrg: %v", err)
		}
		if got != ulidOrgGlobex {
			t.Errorf("pickOrg = %q, want globex id", got)
		}
	})

	t.Run("no match", func(t *testing.T) {
		t.Parallel()
		if _, err := pickOrg(orgs, "missing"); err == nil {
			t.Error("pickOrg expected error for unknown name")
		}
	})

	t.Run("ambiguous", func(t *testing.T) {
		t.Parallel()
		dupes := []coreapi.Org{
			{ID: "01J0ORG000000000000000000A", Name: "dup"},
			{ID: "01J0ORG000000000000000000B", Name: "dup"},
		}
		if _, err := pickOrg(dupes, "dup"); err == nil {
			t.Error("pickOrg expected error for ambiguous name")
		}
	})
}

func TestPickProject(t *testing.T) {
	t.Parallel()
	projects := []coreapi.Project{
		{ID: ulidProjectWidgets, Name: "widgets", OwnerId: ulidOrgAcme, OwnerType: coreapi.ProjectOwnerTypeOrg},
		{ID: "01J0PRJ0000000000000000002", Name: "gadgets", OwnerId: ulidOrgAcme},
	}

	t.Run("unique match", func(t *testing.T) {
		t.Parallel()
		got, err := pickProject(projects, "gadgets")
		if err != nil {
			t.Fatalf("pickProject: %v", err)
		}
		if got != "01J0PRJ0000000000000000002" {
			t.Errorf("pickProject = %q, want gadgets id", got)
		}
	})

	t.Run("no match", func(t *testing.T) {
		t.Parallel()
		if _, err := pickProject(projects, "missing"); err == nil {
			t.Error("pickProject expected error for unknown name")
		}
	})

	t.Run("ambiguous across owners", func(t *testing.T) {
		t.Parallel()
		dupes := []coreapi.Project{
			{ID: "01J0PRJ000000000000000000A", Name: "shared", OwnerId: ulidOrgAcme},
			{ID: "01J0PRJ000000000000000000B", Name: "shared", OwnerId: ulidOrgGlobex},
		}
		if _, err := pickProject(dupes, "shared"); err == nil {
			t.Error("pickProject expected error for ambiguous name")
		}
	})
}

func TestFilterProjectsByName(t *testing.T) {
	t.Parallel()
	projects := []coreapi.Project{
		{ID: "1", Name: "a"},
		{ID: "2", Name: "b"},
		{ID: "3", Name: "a"},
	}

	t.Run("empty name returns all", func(t *testing.T) {
		t.Parallel()
		if got := filterProjectsByName(projects, ""); len(got) != 3 {
			t.Errorf("len = %d, want 3", len(got))
		}
	})

	t.Run("exact filter", func(t *testing.T) {
		t.Parallel()
		got := filterProjectsByName(projects, "a")
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
		for _, p := range got {
			if p.Name != "a" {
				t.Errorf("unexpected project %q", p.Name)
			}
		}
	})

	t.Run("no match", func(t *testing.T) {
		t.Parallel()
		if got := filterProjectsByName(projects, "z"); len(got) != 0 {
			t.Errorf("len = %d, want 0", len(got))
		}
	})
}
