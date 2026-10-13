package process

import (
	"fmt"
	"os"
)

// openProcessLogFiles owns only files it creates. If a later creation fails,
// remove those files without touching the preexisting path that caused failure.
func openProcessLogFiles(paths ...string) ([]*os.File, error) {
	files := make([]*os.File, 0, len(paths))
	for _, path := range paths {
		file, err := openPrivateProcessLog(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL)
		if err != nil {
			cleanupProcessLogFiles(files, true)
			return nil, fmt.Errorf("failed to create process log file: %w", err)
		}
		files = append(files, file)
	}
	return files, nil
}

// The parent closes its handles after spawn; the child retains its own copies.
// Until spawn succeeds, every created file must also be removed.
func cleanupProcessLogFiles(files []*os.File, remove bool) {
	for _, file := range files {
		_ = file.Close()
		if remove {
			_ = os.Remove(file.Name())
		}
	}
}

// Log paths belong to the API. Workloads only need the inherited writable
// stdout/stderr descriptors, never permission to reopen a log or lock its journal.
func openPrivateProcessLog(path string, flags int) (*os.File, error) {
	file, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return nil, err
	}
	// OpenFile's mode only applies on creation. Tighten files restored from an
	// older API too, before allowing the collector or restarted child to use them.
	if flags&os.O_EXCL == 0 {
		if err := file.Chmod(0600); err != nil {
			file.Close()
			return nil, fmt.Errorf("failed to secure process log file: %w", err)
		}
	}
	return file, nil
}
