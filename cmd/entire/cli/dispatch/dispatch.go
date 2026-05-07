package dispatch

import (
	"context"
	"fmt"
)

type Mode int

const (
	ModeServer Mode = iota
	ModeLocal
)

func (m Mode) String() string {
	switch m {
	case ModeServer:
		return "server"
	case ModeLocal:
		return "local"
	default:
		return fmt.Sprintf("Mode(%d)", m)
	}
}

type Options struct {
	Mode                  Mode
	RepoPaths             []string
	Since                 string
	Until                 string
	Branches              []string
	AllBranches           bool
	ImplicitCurrentBranch bool
	Voice                 string
	InsecureHTTPAuth      bool

	// Author filters checkpoints to a specific author email.
	// Cloud mode passes it through to the server; local mode matches it
	// case-insensitively against the metadata-branch commit author.
	Author string

	// Me requests filtering to the current operator. Cloud mode delegates
	// resolution to the bearer-token user; local mode resolves it to
	// `git config user.email` at run time.
	Me bool
}

// CloudRepoLimit caps how many repos the cloud mode may query in one request.
const CloudRepoLimit = 5

func Run(ctx context.Context, opts Options) (*Dispatch, error) {
	if opts.Mode == ModeServer {
		return runServer(ctx, opts)
	}
	return runLocal(ctx, opts)
}
