//go:build !linux

package identity

// setfsuid(2) and setfsgid(2) are Linux-only. Elsewhere they report a value the
// caller can never match, so Do fails closed instead of running filesystem
// operations with the privileges it was asked to drop. Sandboxes only ever run
// on Linux; these builds exist for the CLI.
func setfsuid(uid int) int { return -1 }

func setfsgid(gid int) int { return -1 }
