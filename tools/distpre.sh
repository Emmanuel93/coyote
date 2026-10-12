#!/bin/sh
# Comprobaciones previas de make dist (ADR-0027): la carpeta de salida es una
# ruta sencilla dentro del repo que git ignora y sin archivos versionados, y el
# árbol está limpio. Escribe en la salida el commit HEAD de este repo, el que
# se va a compilar; los avisos van a la salida de error.
#
# git corre sin las variables que eligen el repo, el árbol o el índice
# (tools/distenv.sh): lo que dice el árbol limpio y el HEAD son de este repo.
#
# uso: tools/distpre.sh <carpeta de salida>
set -eu
[ $# -eq 1 ] || { echo "uso: distpre.sh <carpeta de salida>" >&2; exit 2; }
dist=$1
. "$(dirname "$0")/distenv.sh"

case "$dist" in
"" | /* | *..* | . | ./ | .git | .git/* | *[!A-Za-z0-9._/-]*)
	echo "DIST tiene que ser una carpeta relativa dentro del repo, con letras, números, . _ - y /" >&2
	exit 1
	;;
esac
if ! git check-ignore -q "$dist/coyote" || [ -n "$(git ls-files -- "$dist")" ]; then
	echo "DIST tiene que ser una carpeta que git ignora y sin archivos versionados (make dist la borra entera)" >&2
	exit 1
fi
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
	echo "make dist necesita el árbol limpio: haz commit o guarda los cambios (un release sale de un commit)" >&2
	exit 1
fi
git rev-parse HEAD
