//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd

package tty

// IsTerminal no se puede saber en esta plataforma: se responde que no, así
// las aprobaciones no se dan a ciegas.
func IsTerminal(fd uintptr) bool { return false }
