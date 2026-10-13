//go:build !race

package glob

import "time"

// holgura es el plazo tal cual; con -race lo estira (race_on_test.go).
func holgura(d time.Duration) time.Duration { return d }
