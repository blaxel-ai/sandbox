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
