// Package investigate — see env.go for package-level rationale.
//
// prompt_yn.go is the local alias for uiform.PromptYN used by the settings
// migration and the HEAD-soft-warn.
package investigate

import (
	"context"

	"github.com/entireio/cli/cmd/entire/cli/uiform"
)

func realPromptYN(ctx context.Context, question string, def bool) (bool, error) {
	return uiform.PromptYN(ctx, question, def) //nolint:wrapcheck // uiform already wraps
}
