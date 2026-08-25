//go:build !linux

package archive

import "fmt"

// DefaultImageDevice has no meaning outside Linux; the export endpoint refuses
// to run there.
const DefaultImageDevice = ""

func mountImage(device, mountpoint string) error {
	return fmt.Errorf("mounting the sandbox image is only supported on linux")
}

func unmountImage(mountpoint string) error {
	return fmt.Errorf("mounting the sandbox image is only supported on linux")
}

func syncFilesystem() {}

func mounted(mountpoint string) bool { return false }
