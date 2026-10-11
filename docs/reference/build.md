# Build y verificación de un release

Estado: desde v1.0.0 · Decisión: ADR-0027

Este documento es el `buildType` y el `builder.id` de la procedencia de cada release. Dice cómo se construye coyote y cómo verificar que un binario sale de su etiqueta.

## Tipo de build: `make dist`

Desde el commit de la etiqueta, sin red y con el árbol limpio (sin cambios ni archivos nuevos): si no, `make dist` no corre. Compila en un clon limpio de HEAD en una carpeta temporal que prepara `tools/distsrc.sh`, así que ni un archivo con skip-worktree ni uno ignorado entran al binario. La carpeta `dist/` empieza vacía en cada build, y `DIST` solo acepta una carpeta relativa del repo que git ignora y sin archivos versionados.

`tools/distsrc.sh` corre git sin nada del entorno de quien compila: ni las variables `GIT_*` (`GIT_CONFIG_COUNT`, `GIT_CONFIG_PARAMETERS`…), ni la configuración global o del sistema, ni los atributos globales (`core.attributesFile`, `$XDG_CONFIG_HOME/git/attributes`: un `*.md eol=crlf` cambiaría los bytes que embebe el binario), ni plantillas, hooks, `fsmonitor` o reemplazos de objetos. El checkout fuerza `core.autocrlf=false` y `core.eol=lf`. Las abreviaturas de commit llevan 12 dígitos fijos: la versión no cambia con el tamaño del repo.

- Go con la versión que registra la procedencia, `-trimpath`, `CGO_ENABLED=0` y el entorno fijo: `GOAMD64=v1`, `GOARM64=v8.0`, sin `GOEXPERIMENT`, `GOWORK=off`, `GOTOOLCHAIN=local`, `GOENV=off` y `GOFLAGS=-mod=mod`. Ni el entorno ni la línea de comandos de `make` los cambian.
- La versión, el commit y la fecha salen de git dentro de la receta y van por `-ldflags`; el entorno no los cambia. La fecha es la del commit, no la del reloj.
- Cuatro plataformas: darwin/arm64, darwin/amd64, linux/amd64 y linux/arm64.
- `tools/distmeta` revisa con el buildinfo de cada binario que sea de su plataforma, que salga del commit, que no tenga cambios sin commit y que se compiló con las opciones de `make dist`: `-trimpath`, `CGO_ENABLED=0`, sin `GOEXPERIMENT` ni nada que cambie el código (`-gcflags`, `-asmflags`, `-tags`, `-race`, `-cover`, `-pgo`…). Escribe:
  - `coyote.cdx.json` junto a cada binario: un SBOM CycloneDX 1.7 con coyote, sus dependencias (go-yaml, copiada en `third_party`) y la versión de Go;
  - `coyote_<versión>.provenance.json`: un statement in-toto v1 con la procedencia SLSA v1.2. Sus sujetos son cada binario y cada SBOM con su sha256. Las opciones del build que valen igual en las cuatro plataformas van juntas (`settings`); las que son de una arquitectura (`GOAMD64` solo existe en amd64, `GOARM64` en arm64) van por plataforma (`platformSettings`).
- `SHA256SUMS` cubre todos esos archivos.

Lo que `distmeta` no puede verificar, y lo que sí lo prueba:

- Con `-trimpath` Go no registra las `-ldflags` en el buildinfo (golang/go#52372). Unas `-ldflags` distintas, que cambiarían la versión que imprime el binario, no se ven en `distmeta`. Lo que las prueba es `make verify-dist`: reconstruir el release en dos cachés vacías y obtener los mismos bytes.
- `vcs.modified` lo mide `go build` en la carpeta donde compiló. Dentro del clon recién hecho de `make dist` no hay nada que esconder; en cualquier otra carpeta, git puede ocultar un cambio (`assume-unchanged`, `skip-worktree`). Por eso la revisión de `distmeta` vale para lo que sale de `make dist`, no para un binario que alguien compiló por su cuenta y le puso la misma carpeta.

`make verify-dist` construye el release dos veces, cada vez con la caché de Go vacía, y compara los hashes. La CI lo corre en cada cambio.

## Constructor local

El release se construye en la máquina de la persona, no en una plataforma hospedada, y la procedencia no va firmada. Eso es SLSA Build L1: la procedencia existe y dice cómo se construyó, pero no prueba quién lo hizo. L2 pide un builder hospedado y la procedencia firmada (por ejemplo, GitHub Actions); queda para cuando se autorice publicar el release ahí.

## Verificar un release

1. Los hashes:

   ```sh
   shasum -a 256 -c SHA256SUMS
   ```

2. La procedencia. `gitCommit` tiene que ser el commit de la etiqueta, y cada sujeto, un archivo del release con ese sha256:

   ```sh
   git rev-parse v1.0.0^{commit}
   ```

3. Reconstruir. Con la misma versión de Go que dice la procedencia:

   ```sh
   git checkout v1.0.0 && make dist && diff dist/SHA256SUMS <SHA256SUMS del release>
   ```

   Si los hashes coinciden, el binario sale de ese commit, sin importar quién lo construyó.
4. Las dependencias están en el SBOM de cada binario (`coyote.cdx.json`).
