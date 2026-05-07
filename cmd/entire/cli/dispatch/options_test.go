package dispatch

import (
	"strings"
	"testing"
)

func TestResolveOptions_NormalizesScopeValues(t *testing.T) {
	t.Parallel()

	opts, err := ResolveOptions(
		false,
		"7d",
		"",
		false,
		[]string{" entireio/cli ", "", "entireio/cli"},
		"",
		false,
		"",
		false,
		func() (string, error) { return testDefaultBranchName, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.RepoPaths) != 1 || opts.RepoPaths[0] != "entireio/cli" {
		t.Fatalf("unexpected normalized repo paths: %v", opts.RepoPaths)
	}
	if opts.Branches != nil {
		t.Fatalf("cloud mode should not implicitly set branches, got %v", opts.Branches)
	}
}

func TestResolveOptions_CloudRejectsAllBranches(t *testing.T) {
	t.Parallel()

	_, err := ResolveOptions(
		false,
		"7d",
		"",
		true,
		[]string{"entireio/cli"},
		"",
		false,
		"",
		false,
		func() (string, error) { return testDefaultBranchName, nil },
	)
	if err == nil || !strings.Contains(err.Error(), "--all-branches only applies to --local") {
		t.Fatalf("expected all-branches rejection, got %v", err)
	}
}

func TestResolveOptions_CloudCapsReposAtFive(t *testing.T) {
	t.Parallel()

	repos := []string{"a/b", "c/d", "e/f", "g/h", "i/j", "k/l"}
	_, err := ResolveOptions(
		false,
		"7d",
		"",
		false,
		repos,
		"",
		false,
		"",
		false,
		func() (string, error) { return testDefaultBranchName, nil },
	)
	if err == nil || !strings.Contains(err.Error(), "supports at most 5") {
		t.Fatalf("expected 5-repo cap rejection, got %v", err)
	}
}

func TestResolveOptions_LocalSetsImplicitCurrentBranch(t *testing.T) {
	t.Parallel()

	opts, err := ResolveOptions(
		true,
		"7d",
		"",
		false,
		nil,
		"",
		false,
		"",
		false,
		func() (string, error) { return "my-feature", nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.ImplicitCurrentBranch {
		t.Fatal("expected ImplicitCurrentBranch to be true in local default")
	}
	if len(opts.Branches) != 1 || opts.Branches[0] != "my-feature" {
		t.Fatalf("expected implicit branches=[my-feature], got %v", opts.Branches)
	}
}

func TestResolveOptions_ForwardsInsecureHTTPAuth(t *testing.T) {
	t.Parallel()

	opts, err := ResolveOptions(
		false,
		"7d",
		"",
		false,
		[]string{"entireio/cli"},
		"",
		true,
		"",
		false,
		func() (string, error) { return testDefaultBranchName, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.InsecureHTTPAuth {
		t.Fatal("expected InsecureHTTPAuth=true to propagate into Options")
	}
}

func TestResolveOptions_LocalAllBranchesSkipsImplicit(t *testing.T) {
	t.Parallel()

	opts, err := ResolveOptions(
		true,
		"7d",
		"",
		true,
		nil,
		"",
		false,
		"",
		false,
		func() (string, error) { return "", nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.AllBranches {
		t.Fatal("expected AllBranches=true")
	}
	if opts.ImplicitCurrentBranch {
		t.Fatal("expected ImplicitCurrentBranch=false when AllBranches is set")
	}
	if opts.Branches != nil {
		t.Fatalf("expected nil branches when AllBranches is set, got %v", opts.Branches)
	}
}

func TestResolveOptions_PropagatesAuthor(t *testing.T) {
	t.Parallel()

	opts, err := ResolveOptions(
		false,
		"7d",
		"",
		false,
		[]string{"entireio/cli"},
		"",
		false,
		"  Teammate@Example.com  ",
		false,
		func() (string, error) { return testDefaultBranchName, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Author != "Teammate@Example.com" {
		t.Fatalf("expected trimmed Author, got %q", opts.Author)
	}
	if opts.Me {
		t.Fatal("did not expect Me=true when only --author was set")
	}
}

func TestResolveOptions_PropagatesMe(t *testing.T) {
	t.Parallel()

	opts, err := ResolveOptions(
		false,
		"7d",
		"",
		false,
		[]string{"entireio/cli"},
		"",
		false,
		"",
		true,
		func() (string, error) { return testDefaultBranchName, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Me {
		t.Fatal("expected Me=true to propagate")
	}
	if opts.Author != "" {
		t.Fatalf("expected empty Author when --me is set, got %q", opts.Author)
	}
}

func TestResolveOptions_RejectsAuthorAndMeTogether(t *testing.T) {
	t.Parallel()

	_, err := ResolveOptions(
		false,
		"7d",
		"",
		false,
		[]string{"entireio/cli"},
		"",
		false,
		"someone@example.com",
		true,
		func() (string, error) { return testDefaultBranchName, nil },
	)
	if err == nil || !strings.Contains(err.Error(), "--author and --me are mutually exclusive") {
		t.Fatalf("expected mutual-exclusion error, got %v", err)
	}
}

func TestResolveOptions_WhitespaceAuthorWithMeIsAllowed(t *testing.T) {
	t.Parallel()

	opts, err := ResolveOptions(
		false,
		"7d",
		"",
		false,
		[]string{"entireio/cli"},
		"",
		false,
		"   ",
		true,
		func() (string, error) { return testDefaultBranchName, nil },
	)
	if err != nil {
		t.Fatalf("whitespace --author should normalize to empty and not collide with --me, got %v", err)
	}
	if !opts.Me {
		t.Fatal("expected Me=true to propagate")
	}
	if opts.Author != "" {
		t.Fatalf("expected empty Author after trimming whitespace, got %q", opts.Author)
	}
}

func TestResolveOptions_CloudRejectsInvalidRepoSlug(t *testing.T) {
	t.Parallel()

	_, err := ResolveOptions(
		false,
		"7d",
		"",
		false,
		[]string{"../../etc/passwd"},
		"",
		false,
		"",
		false,
		func() (string, error) { return testDefaultBranchName, nil },
	)
	if err == nil || !strings.Contains(err.Error(), `invalid repo "../../etc/passwd": expected owner/repo`) {
		t.Fatalf("expected repo slug validation error, got %v", err)
	}
}
