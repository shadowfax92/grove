//go:build !darwin && !linux

package inuse

import (
	"context"
	"fmt"
)

func collect(context.Context) ([]processCWD, error) {
	return nil, fmt.Errorf("process cwd snapshot unsupported on this platform")
}
