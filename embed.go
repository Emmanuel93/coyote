// Package coyote expone los recursos embebidos de la herramienta: el estándar
// default, las plantillas con las que coyote init crea o adopta proyectos y
// los agentes y skills que coyote install traduce a cada IDE.
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

// Agents contiene las definiciones de los agentes coyote-* (ADR-0010).
//
//go:embed agents
var Agents embed.FS

// Skills contiene las skills coyote-*, una carpeta con su SKILL.md cada una.
//
//go:embed skills
var Skills embed.FS
