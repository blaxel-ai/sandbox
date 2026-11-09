package filesystem

import (
	"errors"
	"runtime"
	"testing"
)

// /proc files report a size of 0, so only the read itself can enforce MaxBytes.
func TestReadTreeEnforcesMaxBytesWhileReading(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs /proc")
	}
	_, err := NewFilesystem("/").ReadTree("/proc/self", TreeOptions{Content: true, Patterns: []string{"status"}, MaxBytes: 1})
	if !errors.Is(err, ErrTreeLimit) {
		t.Fatalf("err = %v, want ErrTreeLimit", err)
	}

	dir, err := NewFilesystem("/").ReadTree("/proc/self", TreeOptions{Content: true, Patterns: []string{"status"}, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(dir.Files) != 1 || dir.Files[0].Content == nil || len(*dir.Files[0].Content) == 0 {
		t.Fatalf("want /proc/self/status with content, got %+v", dir.Files)
	}
}
