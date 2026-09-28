# Plan de ejecución — Coyote

Estado: v0.1.0 aprobada en G1 (P-0001) · v0.2 en planeación · actualizado 2026-09-28

Este plan ejecuta la propuesta aprobada ("Plan de construcción — Framework Coyote"). Cada release se razona con la plantilla de cinco partes que usarán los agentes de Coyote (problema, restricciones, opciones, decisión, riesgos) y cierra con un gate humano: nada se etiqueta, se publica ni toca otros proyectos sin autorización explícita.

## Reglas de ejecución

1. Aislamiento. Todo vive en `~/Documents/coyote`. Los repos de `~/Documents/projects` no se modifican; de ellos solo se leyó lo necesario para derivar el estándar default y la identidad de git.
2. Gates. Cada release cierra con un reporte en `docs/releases/` (qué se hizo, evidencia, riesgos, qué se autoriza). La decisión se registra en `coyote/approvals/` y en el ledger.
3. Autoría humana. Los commits llevan la identidad de git de la persona dueña del repo; ningún commit, documento o release lleva firmas de herramientas de IA (R15).
4. Sin efectos externos sin gate: nada de push, releases públicos, repos remotos ni llamadas pagadas a APIs.
5. Registro. Cada evento relevante queda en `coyote/ledger/` en CCF.

## Releases y gates

| Release | Alcance | Gate | Qué se autoriza en el gate |
| --- | --- | --- | --- |
| v0.1 init (local) | Formatos CCF, ledger, estándar por capas, filtro de atribución, CLI local, CI | G1 | Etiquetar v0.1.0, publicar el repo en GitHub, arrancar v0.2 |
| v0.2 remote + context | Remotos git con autoría humana y cuotas; índice local; `get context`; `ask`; web v0 | G2 | Uso del token de GitHub; costo del primer job de indexado |
| v0.3 gate | Aprobaciones por hash, hooks de IDE, agentes y skills, `install` para Claude Code y Cursor | G3 | Piloto en seco sobre un dominio real, en solo lectura |
| v0.4 run | Router de modelos, workflows desde el dominio, autonomía, cierres con costo, web v1 | G4 | Presupuesto de API y features piloto |
| v0.5 team | Colaboración, IaC, DevSecOps, SRE, resto de IDEs | G5 | Aplicar Coyote a los repos de la organización |
| v1.0 | Endurecimiento, auditoría, documentación | G6 | Release 1.0 |

## v0.1 init — razonamiento

**Problema.** Hace falta una base local que ya cumpla las reglas que Coyote va a exigir a otros: formatos compactos para registrar y contextualizar, un estándar por capas que se pueda validar, y la garantía de que nada sale con firmas de herramientas de IA. Sin eso, cada release posterior se construiría sobre acuerdos verbales.

**Restricciones.** Sin tocar otros proyectos; sin credenciales de GitHub ni API key en esta etapa; el entorno de construcción no llega al proxy de módulos de Go; la herramienta no puede contener datos de ninguna organización; cada entregable debe poder revisarse en minutos.

**Opciones.**
- A. Seguir el orden de la propuesta: v0.1 con `push` y `pull` contra GitHub y los seis repos inicializados. Choca con el aislamiento y con la falta de credenciales.
- B. Construir capas completas (todos los formatos, luego toda la CLI, luego remotos). Retrasa lo que se puede usar y probar.
- C. Corte vertical local: formatos, ledger, estándar, atribución y una CLI que los usa de punta a punta sobre un proyecto propio y uno sintético; remotos y repos reales pasan a gates posteriores.

**Decisión.** C. Es lo único que se puede verificar completo sin credenciales ni efectos externos, y deja listo lo que más cuesta rehacer: el formato del registro, el estándar y el filtro de atribución. `push` y `pull` pasan a v0.2 (requieren tu token); aplicar Coyote a los repos de la organización pasa a G5.

**Riesgos.** Que el estimador local de tokens se desvíe del conteo real (mitigación: es conservador y v0.2 lo contrasta con `count_tokens`); falsos positivos del filtro de atribución en documentación (mitigación: patrones de frase, lista de rutas permitidas y pruebas); que la CLI sin framework crezca desordenada (mitigación: un archivo por comando y pruebas de punta a punta).

### Tareas de v0.1

| ID | Tarea | Razonamiento | Aceptación | Estado |
| --- | --- | --- | --- | --- |
| T1 | Espacio aislado y entorno | El proyecto debe vivir fuera de `projects/` y compilar sin red | Carpeta propia, Go 1.24 en el entorno de construcción, go-yaml copiado con procedencia | Hecho |
| T2 | Plan y ADRs | Las decisiones que no tomaste explícitamente deben quedar razonadas y revisables | Este plan y ADR-0001 a ADR-0006 | Hecho |
| T3 | CCF v1 y CCF-doc v1 | Son la memoria del sistema; cambiar el formato después es caro | Specs, parsers con validación y pruebas de ida y vuelta | Hecho |
| T4 | Ledger | Append-only, un archivo por día y persona para no generar conflictos en git | `record` y `log` con filtros; pruebas | Hecho |
| T5 | Estándar por capas | El default sale de tus repos; cada organización y proyecto lo ajusta | `rules.yaml` default R1–R17 y A1–A4, `extends`, overrides con motivo y caducidad, lint con checks | Hecho |
| T6 | Atribución | Requisito explícito: ningún entregable firmado por herramientas de IA | Scrub en commit, hook `commit-msg` autónomo, `gate attribution` para PreToolUse, check en lint y CI | Hecho |
| T7 | CLI local | Los comandos son la interfaz de humanos, agentes e IDEs | `init`, `status`, `note`, `record`, `log`, `commit`, `standards`, `attribution`, `gate`, `generate agents`, `hooks`, `doctor` | Hecho |
| T8 | Dogfooding | La herramienta debe cumplir su propio estándar | `coyote/` propio, AGENTS.md generado, lint en verde, commits con `coyote commit` | Hecho |
| T9 | Ejemplo y CI | Probar sin datos reales y dejar la verificación automática lista para GitHub | `examples/acme-shop` y `.github/workflows/ci.yml` | Hecho |
| T10 | Verificación | Evidencia antes de pedir autorización | vet, pruebas, demo de punta a punta y revisión independiente | Hecho: dos revisiones adversariales, 13 + 9 hallazgos corregidos (ver docs/releases/v0.1.0.md) |
| T11 | Entrega y gate G1 | Pedir autorización con evidencia, no con promesas | Repo en `~/Documents/coyote` con historia y autoría tuya; reporte en `docs/releases/v0.1.0.md` | Hecho: G1 aprobado (P-0001) |

## v0.2 remote + context — razonamiento (borrador para G2)

**Problema.** Coyote todavía no colabora: el contexto no viaja entre personas y los agentes no pueden pedir un paquete de contexto acotado.

**Restricciones.** Toda escritura remota con la identidad de quien pone sus credenciales; cuotas de GitHub administradas al ritmo de una persona; nada de código fuera de GitHub si no se necesita; costo de indexado bajo aprobación.

**Opciones.** Servidor propio desde el inicio; o GitHub como backend, índice local en SQLite y publicación de shards por CI.

**Decisión propuesta.** GitHub como backend y SQLite local (propuesta aprobada, D2 y D18 diferida); OAuth device flow con el token en el llavero; módulo de cuotas con perfil `human`.

**Riesgos.** Límites secundarios de GitHub en escrituras; tamaño del índice en laptops; filtración de datos al embeddear (embeddings locales por defecto).

## v0.3 a v1.0

Siguen el orden de la propuesta. Cada una recibirá su razonamiento completo al cerrar la anterior, con lo aprendido en su gate; así el plan no fija hoy lo que conviene decidir con evidencia.

## Supuestos sobre decisiones abiertas

| Decisión | Supuesto usado en la ejecución | Por qué | Se confirma en |
| --- | --- | --- | --- |
| D2 nube | Sin servidores; GitHub como backend | Menor costo y nada que operar para el MVP | G2 |
| D4 embeddings | Locales por defecto | No sacar código a terceros sin autorización | G2 |
| D6 aprobación R2–R3 | Pull request de GitHub | Deja rastro con identidad y revisión | G3 |
| D10 visibilidad de costos | Cada persona ve lo suyo, admins todo | Menor exposición por defecto | G4 |
| D12 hub | `kredius`, sin crearlo hasta G5 | Aislamiento | G5 |
| D13 nombres de archivo | `README.coyote.md` y `CONTEXT.coyote.md` | Coherente con `README.md` y `AGENTS.md` | Confirmado en G1 |
| D14 alcance de R15 | Commits, PRs, comentarios, docs, releases y autoría del commit | Es lo que pediste | Confirmado en G1 |
| D15 web | Plantillas Go con HTMX embebidas | Un solo binario | G2 |
| D16 tokens | Llavero del sistema | No dejar secretos en disco | G2 |
| D19 autonomía por defecto | `manual` | Nada con efectos sin aprobación mientras no haya evidencia | G3 |
