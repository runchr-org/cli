package gitauth

import (
	"context"
	"testing"
)

func TestIsSSHURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"SCP format", "git@github.com:org/repo.git", true},
		{"SSH protocol", "ssh://git@github.com/org/repo.git", true},
		{"HTTPS", "https://github.com/org/repo.git", false},
		{"HTTP", "http://github.com/org/repo.git", false},
		{"empty", "", false},
		{"SCP with port", "git@github.com:22:org/repo.git", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsSSHURL(tt.url); got != tt.want {
				t.Errorf("IsSSHURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestParseCredentialOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		output       string
		wantUsername string
		wantPassword string
	}{
		{
			name:         "standard output",
			output:       "protocol=https\nhost=github.com\nusername=token\npassword=ghp_xxx\n",
			wantUsername: "token",
			wantPassword: "ghp_xxx",
		},
		{
			name:         "empty output",
			output:       "",
			wantUsername: "",
			wantPassword: "",
		},
		{
			name:         "no credentials",
			output:       "protocol=https\nhost=github.com\n",
			wantUsername: "",
			wantPassword: "",
		},
		{
			name:         "only username",
			output:       "username=myuser\n",
			wantUsername: "myuser",
			wantPassword: "",
		},
		{
			name:         "password with equals sign",
			output:       "username=user\npassword=abc=def\n",
			wantUsername: "user",
			wantPassword: "abc=def",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotUser, gotPass := parseCredentialOutput(tt.output)
			if gotUser != tt.wantUsername {
				t.Errorf("username = %q, want %q", gotUser, tt.wantUsername)
			}
			if gotPass != tt.wantPassword {
				t.Errorf("password = %q, want %q", gotPass, tt.wantPassword)
			}
		})
	}
}

func TestResolveAuth_EmptyURL(t *testing.T) {
	t.Parallel()
	auth := ResolveAuth(context.Background(), "")
	if auth != nil {
		t.Error("expected nil auth for empty URL")
	}
}
