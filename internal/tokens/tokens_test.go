package tokens

import (
	"strings"
	"testing"
)

func TestEstimate(t *testing.T) {
	if Estimate("") != 0 || Estimate("   \n") != 0 {
		t.Fatal("un texto vacío debe estimar 0")
	}
	if got := Estimate("hola"); got < 1 {
		t.Fatalf("estimación mínima: %d", got)
	}
	line := "inv|payments|un pago nunca se dispersa dos veces|ref"
	short := Estimate(line)
	long := Estimate(strings.Repeat(line+"\n", 10))
	if long < short*9 {
		t.Fatalf("la estimación no crece con el texto: %d contra %d", long, short)
	}
	text := "Plataforma de comercio: catálogo, carrito, pagos, envíos, devoluciones, facturación y atención"
	if got := Estimate(text); got < 20 || got > 60 {
		t.Fatalf("estimación fuera de rango para prosa: %d", got)
	}
}
