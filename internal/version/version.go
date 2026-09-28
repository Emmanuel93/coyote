// Package version expone la versión de la herramienta; make la fija con -ldflags.
package version

// Valores inyectados en el build.
var (
	Version = "0.1.0-dev"
	Commit  = "none"
	Date    = "unknown"
)

// String devuelve la versión completa.
func String() string {
	return Version + " (" + Commit + ", " + Date + ")"
}
