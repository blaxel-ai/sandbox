package process

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Callers answer a working directory that cannot be used with a stable code,
// so every such failure is a *WorkingDirError, also once ExecuteProcess has
// wrapped it, and still says why.
func TestUnusableWorkingDirIsAWorkingDirError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{
		"path is a file":   file,
		"parent is a file": filepath.Join(file, "sub"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := GetProcessManager().ExecuteProcess("true", dir, "", nil, true, 5, nil, false, 0, false, false)
			var wdErr *WorkingDirError
			if !errors.As(err, &wdErr) {
				t.Fatalf("err = %v (%T), want a *WorkingDirError", err, err)
			}
			if wdErr.Dir != dir || !errors.Is(err, syscall.ENOTDIR) {
				t.Errorf("dir = %q, cause = %v", wdErr.Dir, wdErr.Err)
			}
		})
	}
}
