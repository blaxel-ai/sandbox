//go:build !linux

package identity

// Do is a no-op on non-Linux platforms: setfsuid/setfsgid are Linux-specific
// syscalls. Without them, the sandbox cannot enforce per-thread filesystem
// identity, so fn runs under the process's existing credentials.
func (id *Identity) Do(fn func() error) error {
	return fn()
}
