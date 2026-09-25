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

func setRootReadOnly(root string, readOnly bool) error {
	return fmt.Errorf("remounting the sandbox root is only supported on linux")
}

func rootReadOnly(root string) (bool, error) { return false, nil }

func syncFilesystem() {}

func mounted(mountpoint string) bool { return false }

func mountedFromImage(mountpoint, device string) bool { return false }

func deviceHoldsImage(device string) bool { return false }

func detectImageDevice() string { return DefaultImageDevice }
