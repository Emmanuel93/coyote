#!/bin/sh
# Comprobaciones previas de make dist (ADR-0027): la carpeta de salida es una
# ruta sencilla dentro del repo que git ignora y sin archivos versionados, y el
# árbol está limpio. Escribe en la salida el commit HEAD de este repo, el que
# se va a compilar; los avisos van a la salida de error.
#
# git corre sin las variables que eligen el repo, el árbol o el índice
# (GIT_DIR, GIT_WORK_TREE, GIT_INDEX_FILE…, que git exporta en sus hooks): el
# release sale del HEAD de esta carpeta, y lo que dice el árbol limpio es de
# este mismo repo. Los objetos de reemplazo no cuentan, como en distsrc.sh.
#
# uso: tools/distpre.sh <carpeta de salida>
set -eu
[ $# -eq 1 ] || { echo "uso: distpre.sh <carpeta de salida>" >&2; exit 2; }
dist=$1

g() {
	env -u GIT_DIR -u GIT_WORK_TREE -u GIT_INDEX_FILE -u GIT_COMMON_DIR -u GIT_OBJECT_DIRECTORY -u GIT_ALTERNATE_OBJECT_DIRECTORIES \
		-u GIT_NAMESPACE -u GIT_REPLACE_REF_BASE -u GIT_SHALLOW_FILE -u GIT_GRAFT_FILE -u GIT_PREFIX GIT_NO_REPLACE_OBJECTS=1 \
		git "$@"
}

case "$dist" in
"" | /* | *..* | . | ./ | .git | .git/* | *[!A-Za-z0-9._/-]*)
	echo "DIST tiene que ser una carpeta relativa dentro del repo, con letras, números, . _ - y /" >&2
	exit 1
	;;
esac
if ! g check-ignore -q "$dist/coyote" || [ -n "$(g ls-files -- "$dist")" ]; then
	echo "DIST tiene que ser una carpeta que git ignora y sin archivos versionados (make dist la borra entera)" >&2
	exit 1
fi
if [ -n "$(g status --porcelain --untracked-files=normal)" ]; then
	echo "make dist necesita el árbol limpio: haz commit o guarda los cambios (un release sale de un commit)" >&2
	exit 1
fi
g rev-parse HEAD
