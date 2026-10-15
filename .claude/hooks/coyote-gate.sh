#!/bin/sh
# Generado por coyote install: gate humano de coyote (ADR-0009). No lo edites; corre coyote install.
# Sin coyote instalado, el IDE no ejecuta herramientas en este proyecto: el gate falla cerrado.
for c in "$(command -v coyote 2>/dev/null)" "$HOME/go/bin/coyote" /opt/homebrew/bin/coyote /usr/local/bin/coyote; do
  if [ -n "$c" ] && [ -x "$c" ]; then
    "$c" gate check --ide claude-code
    [ $? -eq 0 ] && exit 0
    exit 2
  fi
done
echo "coyote no está instalado o no está en el PATH: el gate humano bloquea esta acción. Instálalo (make install en el repo de coyote) y vuelve a intentar." >&2
exit 2
