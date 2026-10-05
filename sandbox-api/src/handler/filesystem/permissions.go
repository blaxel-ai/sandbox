package filesystem

import "os"

// UnixPermissions converts Go permission flags to Unix octal permission bits.
func UnixPermissions(mode os.FileMode) uint32 {
	permissions := uint32(mode.Perm())
	if mode&os.ModeSticky != 0 {
		permissions |= 01000
	}
	if mode&os.ModeSetgid != 0 {
		permissions |= 02000
	}
	if mode&os.ModeSetuid != 0 {
		permissions |= 04000
	}
	return permissions
}
