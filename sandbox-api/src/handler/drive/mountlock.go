package drive

import (
	"path/filepath"
	"sync"
)

var (
	mountLocksMu sync.Mutex
	mountLocks   = make(map[string]*sync.Mutex)
)

// mountLockFor returns the mutex dedicated to a given mount path, creating it
// on first use. Serializing on the normalized mount path prevents two
// concurrent mount operations from racing on the same target, which otherwise
// spawns duplicate blfs processes that fight over the same LevelDB cache locks
// and trip the filesystem's own double-mount guard.
func mountLockFor(mountPath string) *sync.Mutex {
	key := filepath.Clean(mountPath)
	mountLocksMu.Lock()
	defer mountLocksMu.Unlock()
	l, ok := mountLocks[key]
	if !ok {
		l = &sync.Mutex{}
		mountLocks[key] = l
	}
	return l
}
