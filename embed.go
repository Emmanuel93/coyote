// Package coyote expone los recursos embebidos de la herramienta: el estándar
// default y las plantillas con las que coyote init crea o adopta proyectos.
package coyote

import "embed"

// Standards contiene standards/default: STANDARD.md, rules.yaml y attribution.yaml.
//
//go:embed standards/default
var Standards embed.FS

// Templates contiene las plantillas de proyecto.
//
//go:embed all:templates
var Templates embed.FS
