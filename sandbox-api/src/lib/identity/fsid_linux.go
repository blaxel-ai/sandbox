package identity

import "syscall"

// setfsuid and setfsgid wrap the raw syscalls because the syscall package
// discards their return value, which is the previous uid/gid.
func setfsuid(uid int) int {
	previous, _, _ := syscall.Syscall(syscall.SYS_SETFSUID, uintptr(uid), 0, 0)
	return int(previous)
}

func setfsgid(gid int) int {
	previous, _, _ := syscall.Syscall(syscall.SYS_SETFSGID, uintptr(gid), 0, 0)
	return int(previous)
}
