//go:build race

package glob

import "time"

// holgura estira el plazo de una prueba de tiempo: con el detector de carreras
// (go test -race, como corre la CI) el código es de cinco a veinte veces más lento,
// y lo que se mide es que el trabajo no crezca sin tope, no cuántos segundos tarda.
func holgura(d time.Duration) time.Duration { return 12 * d }
