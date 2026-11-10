package footprint

import (
	"context"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// entryVisitor consumes metadata while it is owned by the directory reader.
// A nil private size asks account to try the per-file fallback when requested.
type entryVisitor func(string, *unix.Stat_t, *int64)

func readEntriesPortable(ctx context.Context, directory *os.File, visit entryVisitor) error {
	fd := int(directory.Fd())
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		names, err := directory.Readdirnames(256)
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return err
			}
			var st unix.Stat_t
			if statErr := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); statErr != nil {
				// Nil metadata tells the visitor to report an unreadable entry, retaining
				// its name instead of turning one failed lstat into a failed worktree.
				visit(name, nil, nil)
				continue
			}
			visit(name, &st, nil)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
