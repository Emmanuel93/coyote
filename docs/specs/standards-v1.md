# Estándar por capas v1

Estado: estable desde v0.1.0 · Implementación: `internal/standards` · Default: `standards/default/`

## Capas

Un proyecto resuelve su estándar encadenando capas con `extends`, de la más general a la más específica:

```
coyote:default  →  hub de la organización  →  proyecto
(la herramienta)    (coyote/standards/        (coyote/standards/
                     rules.yaml del hub)       rules.yaml del repo)
```

- **coyote:default** viene embebido en el binario: reglas R1–R17 (repo, commits, documentos, atribución) y A1–A4 (agentes). Se lee con `coyote standards show`.
- **hub**: el estándar de la organización. En v0.1 se usa si `coyote/project.yaml` apunta a un hub local (`hub: ../acme-hub` o `~/Documents/acme-hub`); el hub remoto llega en v0.2.
- **proyecto**: `coyote/standards/rules.yaml` del repo. Si no existe, rige el default.

Valores de `extends`: `coyote:default` (o vacío), `hub`, `none` (sin capas previas) o una ruta a otro `rules.yaml`, relativa al archivo que la declara. La cadena admite hasta seis saltos y rechaza ciclos.

`extends: none` deja fuera todo el default. Solo vale con `reason:` en el mismo archivo; sin motivo, el lint falla con el hallazgo **S0** y `coyote standards diff` lista cada regla que quedó fuera.

## Archivo rules.yaml

```yaml
version: 1
name: acme:backend            # opcional
extends: hub                  # coyote:default | hub | none | ruta
language: { docs: es, code: en }
rules:
  - id: P1                    # nueva regla
    title: Toda llamada de pago usa la pasarela interna
    level: MUST               # MUST | SHOULD | MAY (RFC 2119); SHOULD si falta
    scope: pr                 # repo | commit | pr | workflow | agent | org
    profiles: [backend]       # vacío = aplica a todos los perfiles
    why: Un solo punto de conciliación
    fix: Usa PaymentsGateway.charge
    check: { type: regex_absent, paths: ["src/**"], pattern: 'http\.Post\(' }
  - id: R10                   # ajuste de una regla de una capa anterior
    override: { level: MAY, until: 2026-12-31, reason: "README heredado", adr: ADR-0003 }
```

Una regla sin `check` es de proceso: no la verifica el lint, la aplican los gates, los agentes y la revisión humana.

## Ajustes, redefiniciones y dispensas

| Mecanismo | Dónde | Efecto | Salvaguarda |
|-----------|-------|--------|-------------|
| `override` | rules.yaml de una capa posterior | cambia el nivel, desactiva (`disabled: true`) o excluye rutas de sus checks (`except: [...]`) | relajar exige `reason`; `until` lo hace vencer; queda anotado |
| redefinición | misma `id` sin `override` | reemplaza lo que declara y hereda el resto, incluidos los checks | no puede bajar de nivel; queda anotada; si cambia los checks o los perfiles de una MUST exige `reason:` (si no, rige la definición anterior y el lint falla con **S1**) |
| dispensa (`waive`) | frontmatter de README.coyote.md | el hallazgo se muestra como dispensado | en reglas MUST exige un motivo real; sin eso no se aplica |

Un motivo real tiene al menos tres letras o dígitos y no es un relleno como `TODO`, `xxx`, `n/a`, `-` o `pendiente`. Vale para `override`, redefiniciones, dispensas y `extends: none`. La exención de avisos del hub solo aplica a un hub que vive fuera del repo; un "hub" dentro del proyecto cuenta como el proyecto.

Las claves desconocidas en rules.yaml son un error: `Override:` o `disabled:` fuera de `override` no pueden desactivar una regla sin que se note.

Todo ajuste aparece en `coyote standards diff` y `coyote standards explain <id>`, con la capa que lo hizo, el motivo y el ADR.

## Perfiles

El perfil del proyecto es `standards.profile` de README.coyote.md o, si falta, su `type`. Una regla con `profiles` solo aplica a esos perfiles; por ejemplo, R13 (inventario `coyote/infra.yaml`) solo aplica a `infra`. Si el perfil no coincide con el tipo del proyecto, `lint`, `show` y `diff` lo avisan.

## Tipos de check

| Tipo | Parámetros | Falla cuando |
|------|-----------|--------------|
| file_exists | `files`; cada entrada admite alternativas `"a \| b"` y globs | ninguna alternativa existe |
| file_max_tokens | `files`, `max` | el archivo supera `max` tokens estimados |
| file_max_lines | `files`, `max` | el archivo supera `max` líneas |
| regex_present | `files`, `pattern` | un archivo no contiene el patrón, o no hay archivos |
| regex_absent | `paths`, `except`, `pattern` | alguna línea coincide (máx. 5 por archivo, 50 en total) |
| path_forbidden | `paths`, `except` | existe un archivo en esas rutas |
| commit_format | `pattern`, `commits` | un commit desde que se adoptó coyote (sin merges) no sigue el formato, o el clon es superficial |
| ccfdoc_valid | `files` | README.coyote.md o CONTEXT.coyote.md no cumplen CCF-doc v1 (las advertencias cuentan como SHOULD) |
| attribution | `paths`, `except`, `commits` | un archivo, o el mensaje, autor o committer de un commit (merges incluidos), lleva atribución a IA (spec attribution-v1); o el clon es superficial |
| agents_md_current | — | AGENTS.md falta, es ajeno o está desactualizado |
| script | `script` | el comando sale con código distinto de 0 (tope de 60 s) |

Los globs admiten `**`, `*`, `?` y `{a,b}`, también anidadas; un patrón sin `/` también coincide con el nombre del archivo en cualquier carpeta, y uno que termina en `/` es un directorio (`respaldos/` es cualquier archivo bajo una carpeta `respaldos`). Los archivos son los que git conoce (versionados y nuevos no ignorados); fuera de git se recorre el directorio saltando dependencias y artefactos de build. Los archivos binarios o de más de 2 MB no se leen.

Los checks `script` ejecutan comandos escritos en rules.yaml, que puede venir de un hub o de un repo ajeno. Por eso solo corren con `coyote standards lint --scripts`; `status` y `doctor` nunca los corren.

"Desde que se adoptó coyote" significa desde el primer commit que agregó `coyote/project.yaml`: el historial previo no se juzga, y borrar y volver a agregar el archivo no cambia ese punto.

## Comandos

| Comando | Qué hace | Código de salida |
|---------|----------|------------------|
| `coyote standards lint [--strict] [--json] [--scripts]` | aplica el estándar | 1 si hay hallazgos MUST (o SHOULD con `--strict`); 0 si no |
| `coyote standards show` | reglas vigentes con su capa | 0 |
| `coyote standards diff` | solo lo que difiere del default | 0 |
| `coyote standards explain <id>` | nivel, capa, ajustes, por qué, arreglo y verificación | 1 si no existe |

## El default y su origen

El default generaliza las prácticas de repos reales de backend, móvil e infraestructura: empezar por el dominio y sus contratos (R11, R16), un README corto con el detalle citado (R10), sin respaldos ni volcados en el código (R5), IaC con inventario (R13), decisiones de riesgo con ADR (R8), commits con formato (R2) y CI con pruebas y lint (R3.ci, R9). No contiene nombres de organizaciones ni de repos: cada organización pone lo suyo en su hub.
