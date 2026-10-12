# Lo carga con "." make dist antes de nada (ADR-0027), y tools/distpre.sh.
#
# Quita del entorno las variables de git que eligen el repo, el árbol o el
# índice (GIT_DIR, GIT_WORK_TREE, GIT_INDEX_FILE…, que git exporta en sus
# hooks): el release sale del HEAD de esta carpeta, y ni las comprobaciones
# previas, ni la clonación, ni el go build que sella vcs.revision leen otro
# repo. Los objetos de reemplazo no cuentan, como en distsrc.sh.
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_COMMON_DIR GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES \
	GIT_NAMESPACE GIT_REPLACE_REF_BASE GIT_SHALLOW_FILE GIT_GRAFT_FILE GIT_PREFIX
GIT_NO_REPLACE_OBJECTS=1
export GIT_NO_REPLACE_OBJECTS
