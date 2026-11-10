//go:build !darwin || !cgo

package footprint

import (
	"context"
	"os"
)

func readEntries(ctx context.Context, directory *os.File, _ bool, visit entryVisitor) error {
	return readEntriesPortable(ctx, directory, visit)
}
