package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Configuración JSON con el orden de sus claves: coyote cambia solo sus
// entradas y deja el archivo de la persona como estaba.

type member struct {
	Key   string
	Value any
}

// object es un objeto JSON ordenado.
type object struct{ members []member }

func (o *object) get(k string) (any, bool) {
	for _, m := range o.members {
		if m.Key == k {
			return m.Value, true
		}
	}
	return nil, false
}

func (o *object) set(k string, v any) {
	for i, m := range o.members {
		if m.Key == k {
			o.members[i].Value = v
			return
		}
	}
	o.members = append(o.members, member{k, v})
}

// child devuelve el objeto en k, creándolo si falta. Si k existe y no es un
// objeto, es un error: no se pisa lo que la persona escribió.
func (o *object) child(k string) (*object, error) {
	v, ok := o.get(k)
	if !ok {
		c := &object{}
		o.set(k, c)
		return c, nil
	}
	c, ok := v.(*object)
	if !ok {
		return nil, fmt.Errorf("%q no es un objeto", k)
	}
	return c, nil
}

func parseJSON(data []byte) (*object, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return &object{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("hay contenido después del objeto JSON")
	}
	o, ok := v.(*object)
	if !ok {
		return nil, errors.New("la raíz no es un objeto JSON")
	}
	return o, nil
}

func parseValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := &object{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, ok := kt.(string)
				if !ok {
					return nil, errors.New("clave inválida")
				}
				v, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				o.set(k, v)
			}
			_, err := dec.Token() // }
			return o, err
		case '[':
			var arr []any
			for dec.More() {
				v, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			_, err := dec.Token() // ]
			if arr == nil {
				arr = []any{}
			}
			return arr, err
		}
		return nil, fmt.Errorf("delimitador inesperado %v", t)
	default:
		return tok, nil
	}
}

// encodeJSON escribe con sangría de dos espacios, como MarshalIndent.
func encodeJSON(v any) []byte {
	var b bytes.Buffer
	writeValue(&b, v, "")
	b.WriteString("\n")
	return b.Bytes()
}

func writeValue(b *bytes.Buffer, v any, indent string) {
	switch t := v.(type) {
	case *object:
		if len(t.members) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, m := range t.members {
			k, _ := json.Marshal(m.Key)
			b.WriteString(indent + "  " + string(k) + ": ")
			writeValue(b, m.Value, indent+"  ")
			if i < len(t.members)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(indent + "}")
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, x := range t {
			b.WriteString(indent + "  ")
			writeValue(b, x, indent+"  ")
			if i < len(t)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(indent + "]")
	case json.Number:
		b.WriteString(t.String())
	default:
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(t)
		b.WriteString(strings.TrimSuffix(buf.String(), "\n"))
	}
}
