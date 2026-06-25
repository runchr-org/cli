package importclaude

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiscoverSessions_LookbackAndFilter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	writeAged := func(name string, age time.Duration) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := now.Add(-age)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	writeAged("recent.jsonl", 5*24*time.Hour)
	writeAged("old.jsonl", 60*24*time.Hour)
	writeAged("skip.txt", 1*time.Hour)

	got, err := DiscoverSessions("", dir, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "recent.jsonl" {
		t.Fatalf("lookback filter wrong: %v", got)
	}

	writeAged("abc123.jsonl", 1*24*time.Hour)
	got, err = DiscoverSessions("", dir, now, []string{"abc123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "abc123.jsonl" {
		t.Fatalf("session filter wrong: %v", got)
	}
}

func TestDiscoverSessions_MissingDirIsEmpty(t *testing.T) {
	t.Parallel()
	got, err := DiscoverSessions("", filepath.Join(t.TempDir(), "does-not-exist"), time.Now(), nil)
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}
