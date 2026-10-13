//go:build !race

package cli

import "time"

// holgura es el plazo tal cual; con -race lo estira (race_on_test.go).
func holgura(d time.Duration) time.Duration { return d }

// escala es el tamaño tal cual; con -race lo reduce (race_on_test.go).
func escala(n int) int { return n }
