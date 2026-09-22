//go:build !linux

package securitylinux

// MaybeReexec reports the platform security result through the normal Hanami
// integration on non-Linux systems, where process re-exec is unnecessary.
func MaybeReexec(Policy) error { return nil }
