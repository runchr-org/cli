package cli

import (
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/api"
)

func TestTrailWebURL(t *testing.T) {
	t.Setenv(api.BaseURLEnvVar, "https://entire.io")

	cases := []struct {
		name   string
		target trailReviewTarget
		want   string
	}{
		{
			name: "full target",
			target: trailReviewTarget{
				Host:  "gh",
				Owner: "entireio",
				Repo:  "cli",
				Trail: api.TrailResource{Number: 466, Branch: "review-profiles"},
			},
			want: "https://entire.io/gh/entireio/cli/trails/466/review-profiles",
		},
		{
			name: "no trail number yields no link",
			target: trailReviewTarget{
				Host:  "gh",
				Owner: "entireio",
				Repo:  "cli",
				Trail: api.TrailResource{Branch: "review-profiles"},
			},
			want: "",
		},
		{
			name: "missing forge yields no link",
			target: trailReviewTarget{
				Owner: "entireio",
				Repo:  "cli",
				Trail: api.TrailResource{Number: 1, Branch: "main"},
			},
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := trailWebURL(c.target); got != c.want {
				t.Errorf("trailWebURL() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestTrailWebURL_HonorsCustomBase(t *testing.T) {
	t.Setenv(api.BaseURLEnvVar, "https://entire.example.com/")
	target := trailReviewTarget{
		Host:  "gh",
		Owner: "acme",
		Repo:  "app",
		Trail: api.TrailResource{Number: 7, Branch: "feat/x"},
	}
	want := "https://entire.example.com/gh/acme/app/trails/7/feat/x"
	if got := trailWebURL(target); got != want {
		t.Errorf("trailWebURL() = %q, want %q", got, want)
	}
}
