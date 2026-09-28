//go:build linux

package tty

import (
	"syscall"
	"unsafe"
)

// IsTerminal pregunta al kernel por los atributos de terminal del descriptor.
func IsTerminal(fd uintptr) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
