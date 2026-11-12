package config

import (
	"fmt"
	"os"
	"syscall"

	"git-mirror/internal/datadir"
)

// Lock acquires an exclusive advisory lock on the data directory. The returned
// function releases it. Locks are per-process (flock), so both the server and
// concurrent CLI invocations serialize their config/state updates.
func Lock(d datadir.Dir) (func(), error) {
	f, err := os.OpenFile(d.LockPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", d.LockPath(), err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
