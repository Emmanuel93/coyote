---
status: proposed
date: 2026-09-29
deciders: "@eramirezhdez"
---
# ADR-0027: Cadena de suministro del release (D42)

## Contexto y problema
Hoy un release de coyote son binarios con un `SHA256SUMS`. Quien los baja puede verificar que no se corrompieron, pero no qué contienen ni cómo se construyeron. `make dist` graba la hora del build, así que reconstruir desde la etiqueta da otros hashes. La propuesta pide goreleaser, un SBOM CycloneDX y procedencia SLSA nivel 1 (C6.8).

Referencias vigentes al 2026-09-29:
- SLSA v1.2. Build L1 pide que la procedencia exista y se distribuya. L2 pide además que esté firmada y que el build corra en una plataforma hospedada.
- CycloneDX 1.7.

## Opciones consideradas
- **goreleaser, syft y el generador de SLSA en GitHub Actions.** Llegan a L2 o L3, pero necesitan red, publicar y una acción de terceros.
- **Build reproducible, con SBOM y procedencia generados con Go estándar en `make dist`.** Es L1 y funciona sin red.
- **Solo `SHA256SUMS`.** Es lo que hay hoy.

## Decisión
- **Reproducible.**
  - `make dist` toma la fecha y el commit de git, no del reloj.
  - Compila con `-trimpath`, `CGO_ENABLED=0` y las mismas banderas en cada plataforma.
  - Dos builds del mismo commit con la misma versión de Go dan los mismos hashes. Una prueba lo verifica.
- **SBOM.** Un programa del repo (`tools/distmeta`) lee el buildinfo de cada binario y escribe un BOM CycloneDX 1.7 en JSON. El BOM trae:
  - coyote, con su versión y hash;
  - go-yaml, con su versión, su purl y su licencia;
  - la versión de Go.

  El número de serie se deriva del hash del binario, así que el BOM también es reproducible.
- **Procedencia.** El mismo programa escribe un statement in-toto v1 con `predicateType` `https://slsa.dev/provenance/v1`. Lleva:
  - como sujetos, cada archivo del release con su sha256;
  - `buildDefinition`: el tipo de build documentado, la etiqueta, el commit y el comando;
  - `runDetails`: el builder, que es la máquina de la persona, y las horas del build.

  No va firmada, así que cumple Build L1.
- **Verificación.** `docs/` explica cómo verificar un release:
  - `shasum -c`;
  - comparar los sujetos de la procedencia;
  - reconstruir desde la etiqueta y comparar los hashes.
- **L2.** Un workflow de release en GitHub Actions, con la procedencia firmada, queda para cuando G8 autorice publicar.

## Consecuencias
- Cualquiera puede comprobar que un binario sale de su etiqueta, reconstruyéndolo.
- L1 no prueba quién construyó el binario, solo cómo. Para eso hace falta L2.
- Subir de versión de Go cambia los hashes. La procedencia la registra.
