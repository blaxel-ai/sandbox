package drive

import (
	"errors"
	"path/filepath"
	"testing"
)

// TestUnmountDriveNotMounted: unmounting a path that was never mounted must be
// reported as ErrNotMounted so the API answers 404 instead of a server fault.
func TestUnmountDriveNotMounted(t *testing.T) {
	root := t.TempDir()

	for name, path := range map[string]string{
		"missing path":    filepath.Join(root, "missing"),
		"plain directory": root,
	} {
		t.Run(name, func(t *testing.T) {
			if err := UnmountDrive(path); !errors.Is(err, ErrNotMounted) {
				t.Fatalf("UnmountDrive(%q) = %v, want ErrNotMounted", path, err)
			}
		})
	}
}
