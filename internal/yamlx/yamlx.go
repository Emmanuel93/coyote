// Package yamlx lee YAML de archivos que puede escribir alguien más (un PR,
// el hub de la organización): un solo documento, sin anclas ni alias. Un
// alias multiplica lo que se decodifica sin que el archivo crezca, y un
// segundo documento se ignoraría sin aviso.
package yamlx

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"
)

// maxNodes acota los nodos de un archivo de configuración.
const maxNodes = 200_000

// Check revisa la forma: un documento (o ninguno), sin anclas, alias ni
// etiquetas de tipo, y con un tope de nodos.
func Check(data []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return errors.New("el archivo tiene más de un documento YAML (---); va uno solo")
	}
	n := 0
	var walk func(*yaml.Node) error
	walk = func(x *yaml.Node) error {
		n++
		if n > maxNodes {
			return fmt.Errorf("el archivo tiene más de %d nodos YAML", maxNodes)
		}
		switch {
		case x.Kind == yaml.AliasNode:
			return fmt.Errorf("línea %d: alias YAML (*%s): no se aceptan anclas ni alias", x.Line, x.Value)
		case x.Anchor != "":
			return fmt.Errorf("línea %d: ancla YAML (&%s): no se aceptan anclas ni alias", x.Line, x.Anchor)
		case x.Tag == "!!binary":
			return fmt.Errorf("línea %d: la etiqueta !!binary no se acepta", x.Line)
		}
		for _, c := range x.Content {
			if err := walk(c); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(&doc)
}

// Strict revisa la forma con Check y decodifica en v con claves conocidas:
// una clave desconocida es un error.
func Strict(data []byte, v any) error {
	if err := Check(data); err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
