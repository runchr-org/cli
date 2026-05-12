// Package investigate — see env.go for package-level rationale.
//
// prompt_yn.go provides the production y/N prompt used by the settings
// migration and the HEAD-soft-warn. Mirrors review.realPromptYN.
package investigate

import (
	"context"
	"errors"
	"fmt"

	"charm.land/huh/v2"
)

func realPromptYN(ctx context.Context, question string, def bool) (bool, error) {
	answer := def
	form := newAccessibleForm(huh.NewGroup(
		huh.NewConfirm().
			Title(question).
			Value(&answer),
	))
	if err := form.RunWithContext(ctx); err != nil {
		if errors.Is(err, huh.ErrUserAborted) || errors.Is(err, context.Canceled) {
			return false, nil
		}
		return false, fmt.Errorf("confirm form: %w", err)
	}
	return answer, nil
}
