package drive

import (
	"sync"
	"testing"
)

func TestNormalizeDrivePath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"root stays root", "/", "/"},
		{"missing leading slash", "sub", "/sub"},
		{"trailing slash trimmed", "/sub/", "/sub"},
		{"nested trailing slash trimmed", "/a/b/", "/a/b"},
		{"empty becomes root", "", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeDrivePath(tt.input); got != tt.want {
				t.Errorf("normalizeDrivePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestMountLockForSameKey verifies the same mutex is returned for paths that
// clean to the same key, so concurrent mounts on the same target serialize.
func TestMountLockForSameKey(t *testing.T) {
	a := mountLockFor("/mnt/test")
	b := mountLockFor("/mnt/test/")
	c := mountLockFor("/mnt/other")
	if a != b {
		t.Errorf("expected identical lock for equivalent paths")
	}
	if a == c {
		t.Errorf("expected distinct locks for different paths")
	}
}

// TestMountLockForConcurrent ensures concurrent lookups for the same key never
// hand out two different mutexes (which would defeat serialization).
func TestMountLockForConcurrent(t *testing.T) {
	const n = 50
	locks := make([]*sync.Mutex, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			locks[idx] = mountLockFor("/mnt/concurrent")
		}(i)
	}
	wg.Wait()
	first := locks[0]
	for i := 1; i < n; i++ {
		if locks[i] != first {
			t.Fatalf("mountLockFor returned different mutexes for the same key")
		}
	}
}
