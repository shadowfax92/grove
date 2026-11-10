//go:build !darwin

package activity

import (
	"context"
	"time"
)

func directoryTime(ctx context.Context, dir string, names map[string]bool) (time.Time, error) {
	return statTime(ctx, dir, names)
}
