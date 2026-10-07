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
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
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
