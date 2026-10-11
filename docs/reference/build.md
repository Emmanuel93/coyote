# Build y verificación de un release

Estado: desde v1.0.0 · Decisión: ADR-0027

Este documento es el `buildType` y el `builder.id` de la procedencia de cada release. Dice cómo se construye coyote y cómo verificar que un binario sale de su etiqueta.

## Tipo de build: `make dist`

Desde el commit de la etiqueta, sin red:

- Go con la versión que registra la procedencia, `-trimpath` y `CGO_ENABLED=0`.
- La versión, el commit y la fecha van por `-ldflags`. La fecha es la del commit, no la del reloj.
- Cuatro plataformas: darwin/arm64, darwin/amd64, linux/amd64 y linux/arm64.
- `tools/distmeta` lee el buildinfo de cada binario y escribe:
  - `coyote.cdx.json` junto a cada binario: un SBOM CycloneDX 1.7 con coyote, sus dependencias (go-yaml, copiada en `third_party`) y la versión de Go;
  - `coyote_<versión>.provenance.json`: un statement in-toto v1 con la procedencia SLSA v1.2. Sus sujetos son cada binario y cada SBOM con su sha256.
- `SHA256SUMS` cubre todos esos archivos.

`make verify-dist` construye el release dos veces, cada vez con la caché de Go vacía, y compara los hashes.

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
