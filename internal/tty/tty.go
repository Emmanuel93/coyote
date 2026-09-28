// Package tty informa si la entrada estándar es una terminal interactiva. Las
// aprobaciones se dan desde una terminal: un agente corre sus comandos con la
// entrada conectada a un pipe o a /dev/null.
package tty

import "os"

// Stdin informa si la entrada estándar es una terminal.
func Stdin() bool { return IsTerminal(os.Stdin.Fd()) }
