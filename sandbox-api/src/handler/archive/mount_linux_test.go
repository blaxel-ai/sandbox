//go:build linux

package archive

import "testing"

// TestTheRootCountsAsAMountPoint guards the one case the device comparison
// cannot see: / and /.. are the same directory, so the root would otherwise look
// like a plain directory — and an export comparing the root against itself, which
// is how a dry run is checked, would be rejected as a bad request.
func TestTheRootCountsAsAMountPoint(t *testing.T) {
	for _, mountpoint := range []string{"/", "//", "/."} {
		if !mounted(mountpoint) {
			t.Errorf("%s should count as a mount point", mountpoint)
		}
	}
	if mounted("/nonexistent-directory-for-the-archive-tests") {
		t.Error("a path that does not exist is not a mount point")
	}
}

// TestRootReadOnlyReadsTheMountFlags checks what a restarted API relies on to
// tell whether an archive left the filesystem frozen behind it.
func TestRootReadOnlyReadsTheMountFlags(t *testing.T) {
	readOnly, err := rootReadOnly(t.TempDir())
	if err != nil {
		t.Fatalf("failed to read the mount flags: %v", err)
	}
	if readOnly {
		t.Error("a writable directory must not be reported read-only")
	}
	if _, err := rootReadOnly("/nonexistent-directory-for-the-archive-tests"); err == nil {
		t.Error("expected a path that does not exist to be reported as an error, not as writable")
	}
}

// TestAdoptRootStateLeavesAWritableSandboxAlone guards the common startup: the
// filesystem is writable, so the sandbox serves every route.
func TestAdoptRootStateLeavesAWritableSandboxAlone(t *testing.T) {
	t.Cleanup(func() { forceResume() })

	adoptRootState(t.TempDir())
	if Quiesced() {
		t.Errorf("a sandbox on a writable filesystem must not start frozen: %+v", Status())
	}
}
