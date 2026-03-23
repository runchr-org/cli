package filter

import (
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/settings"
)

func TestFilter_Clean(t *testing.T) {
	t.Parallel()
	f := Filter{Match: "/home/user/project", Replace: "__ent__/repo"}
	got := f.Clean([]byte("file at /home/user/project/src/main.go"))
	want := "file at __ent__/repo/src/main.go"
	if string(got) != want {
		t.Errorf("Clean() = %q, want %q", got, want)
	}
}

func TestFilter_Smudge(t *testing.T) {
	t.Parallel()
	f := Filter{Match: "/home/user/project", Replace: "__ent__/repo"}
	got := f.Smudge([]byte("file at __ent__/repo/src/main.go"))
	want := "file at /home/user/project/src/main.go"
	if string(got) != want {
		t.Errorf("Smudge() = %q, want %q", got, want)
	}
}

func TestFilter_RoundTrip(t *testing.T) {
	t.Parallel()
	f := Filter{Match: "/Users/soph/Work/repo", Replace: "__ent__/repo"}
	original := []byte("editing /Users/soph/Work/repo/README.md and /Users/soph/Work/repo/go.mod")
	cleaned := f.Clean(original)
	restored := f.Smudge(cleaned)
	if string(restored) != string(original) {
		t.Errorf("round trip failed: got %q, want %q", restored, original)
	}
}

func TestPipeline_Clean_MostSpecificFirst(t *testing.T) {
	t.Parallel()
	// repoRoot is under homeDir — the more specific match must be applied first
	p, err := NewPipeline("/home/user/project", "/home/user", nil)
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("repo=/home/user/project home=/home/user")
	got := string(p.Clean(input))
	want := "repo=__ent__/repo home=__ent__/home"
	if got != want {
		t.Errorf("Clean() = %q, want %q", got, want)
	}
}

func TestPipeline_Smudge_ReverseOrder(t *testing.T) {
	t.Parallel()
	p, err := NewPipeline("/home/user/project", "/home/user", nil)
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("repo=__ent__/repo home=__ent__/home")
	got := string(p.Smudge(input))
	want := "repo=/home/user/project home=/home/user"
	if got != want {
		t.Errorf("Smudge() = %q, want %q", got, want)
	}
}

func TestPipeline_RoundTrip(t *testing.T) {
	t.Parallel()
	p, err := NewPipeline("/home/user/project", "/home/user", nil)
	if err != nil {
		t.Fatal(err)
	}
	original := []byte("path=/home/user/project/src and home=/home/user/.config")
	cleaned := p.Clean(original)
	restored := p.Smudge(cleaned)
	if string(restored) != string(original) {
		t.Errorf("round trip failed:\n  original: %q\n  cleaned:  %q\n  restored: %q", original, cleaned, restored)
	}
}

func TestPipeline_UserFilters(t *testing.T) {
	t.Parallel()
	userFilters := []settings.TranscriptFilter{
		{Match: "acme-corp.internal", Key: "hostname"},
	}
	p, err := NewPipeline("/home/user/project", "/home/user", userFilters)
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("connecting to acme-corp.internal:8080")
	got := string(p.Clean(input))
	want := "connecting to __ent_user__/hostname:8080"
	if got != want {
		t.Errorf("Clean() = %q, want %q", got, want)
	}

	restored := string(p.Smudge([]byte(got)))
	if restored != string(input) {
		t.Errorf("Smudge() = %q, want %q", restored, input)
	}
}

func TestPipeline_EmptyPaths(t *testing.T) {
	t.Parallel()
	p, err := NewPipeline("", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("nothing to filter here")
	got := string(p.Clean(input))
	if got != string(input) {
		t.Errorf("Clean() should be no-op with empty paths, got %q", got)
	}
}

func TestPipeline_CleanString(t *testing.T) {
	t.Parallel()
	p, err := NewPipeline("/home/user/project", "/home/user", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := p.CleanString("path=/home/user/project/file.go")
	want := "path=__ent__/repo/file.go"
	if got != want {
		t.Errorf("CleanString() = %q, want %q", got, want)
	}
}

func TestPipeline_SmudgeString(t *testing.T) {
	t.Parallel()
	p, err := NewPipeline("/home/user/project", "/home/user", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := p.SmudgeString("path=__ent__/repo/file.go")
	want := "path=/home/user/project/file.go"
	if got != want {
		t.Errorf("SmudgeString() = %q, want %q", got, want)
	}
}

func TestPipeline_NilSafe(t *testing.T) {
	t.Parallel()
	var p *Pipeline
	input := []byte("should pass through unchanged")
	if got := string(p.Clean(input)); got != string(input) {
		t.Errorf("nil Clean() = %q, want %q", got, input)
	}
	if got := string(p.Smudge(input)); got != string(input) {
		t.Errorf("nil Smudge() = %q, want %q", got, input)
	}
	if got := p.CleanString("test"); got != "test" {
		t.Errorf("nil CleanString() = %q, want %q", got, "test")
	}
	if got := p.SmudgeString("test"); got != "test" {
		t.Errorf("nil SmudgeString() = %q, want %q", got, "test")
	}
}

func TestPipeline_Idempotent(t *testing.T) {
	t.Parallel()
	p, err := NewPipeline("/home/user/project", "/home/user", nil)
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("path=/home/user/project/src")

	// Cleaning twice should produce the same result
	cleaned := p.Clean(input)
	doubleCleaned := p.Clean(cleaned)
	if string(cleaned) != string(doubleCleaned) {
		t.Errorf("not idempotent:\n  first:  %q\n  second: %q", cleaned, doubleCleaned)
	}
}
