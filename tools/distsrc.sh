#!/bin/sh
# Prepara las fuentes de un release (ADR-0027): un clon de <repo> en
# <destino>/src con <commit> sin cambios, y en <destino>/meta lo que el build
# necesita saber de él, un dato por archivo: ver, commit, full, date, rfc, tag.
#
# git corre sin nada del entorno de quien compila: ni variables GIT_*, ni
# configuración global o del sistema, ni atributos globales (core.attributesFile
# o $XDG_CONFIG_HOME/git/attributes), ni plantillas ni hooks. Así el mismo
# commit da los mismos bytes en cualquier máquina; un eol=crlf global o un hook
# de plantilla no llegan al binario. Las abreviaturas llevan 12 dígitos fijos:
# la versión no cambia con el tamaño del repo.
#
# uso: tools/distsrc.sh <repo> <destino> <commit>
set -eu
[ $# -eq 3 ] || { echo "uso: distsrc.sh <repo> <destino> <commit>" >&2; exit 2; }
repo=$1
dest=$2
head=$3

g() {
	env -i PATH="$PATH" HOME=/nonexistent XDG_CONFIG_HOME=/nonexistent LC_ALL=C TZ=UTC \
		GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 GIT_ATTR_NOSYSTEM=1 GIT_TERMINAL_PROMPT=0 GIT_NO_REPLACE_OBJECTS=1 \
		git -c core.attributesFile=/dev/null -c core.hooksPath=/dev/null -c core.fsmonitor=false "$@"
}

mkdir -p "$dest/meta" "$dest/sin-plantillas"
g clone -q --shared --no-checkout --template="$dest/sin-plantillas" "$repo" "$dest/src"
cd "$dest/src"
g -c core.autocrlf=false -c core.eol=lf checkout -q --detach "$head"
g describe --tags --always --abbrev=12 >../meta/ver
g rev-parse --short=12 HEAD >../meta/commit
g rev-parse HEAD >../meta/full
g log -1 --format=%cd --date=format-local:%Y-%m-%dT%H:%MZ >../meta/date
g log -1 --format=%cd --date=format-local:%Y-%m-%dT%H:%M:%SZ >../meta/rfc
g describe --tags --exact-match >../meta/tag 2>/dev/null || : >../meta/tag
