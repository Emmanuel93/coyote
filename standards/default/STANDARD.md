# Estándar Coyote — default de la herramienta v0.1.0

Las palabras MUST, SHOULD y MAY se interpretan según RFC 2119. Este es el estándar que trae la herramienta; cada organización lo extiende en su hub y cada proyecto en `coyote/standards/rules.yaml`. La forma ejecutable está en `rules.yaml`: `coyote standards lint` la valida y `coyote standards explain <id>` explica cada regla.

El default se derivó de repos reales construidos desde el dominio: conserva lo que funcionó (dominio primero, un servicio por contexto con datos propios, contratos generados, decisiones escritas) y corrige lo que costó (conocimiento duplicado, trackers a mano, respaldos dentro del código, commits sin formato, CI ausente).

## 1. Proyectos

- R3 (MUST). Todo proyecto vive en git remoto con `.gitignore`, `AGENTS.md` generado y CI mínima (R3.ci, SHOULD).
- R5 (MUST). El código no contiene respaldos, volcados ni salidas de agentes; la evidencia pesada va en git-lfs del proyecto.
- R10 (SHOULD). El README tiene dos pantallas; el detalle vive en `coyote/` y en el hub, y se cita.
- R12 (MUST). Las exclusiones de indexado están en `.coyoteignore`; no hay datos personales en `coyote/`.
- R13 (MUST, perfil infra). La infraestructura existe como código en su repo con `coyote/infra.yaml` vigente: ambientes con presupuesto y política de apply, stacks con estado remoto y versiones fijadas (`coyote infra check`). Ningún agente aplica; un ambiente `reviewed` solo lo aplica la persona o un pipeline con revisor.
- R14 (MUST). Todo proyecto tiene `README.md`, `README.coyote.md` y `CONTEXT.coyote.md` válidos; CI los publica en cada merge.

## 2. Cambios

- R1 (SHOULD). La prosa va en el idioma de docs de la organización; el código y los identificadores de Coyote, en inglés.
- R2 (MUST). Los commits siguen `tipo(ámbito): descripción`.
- R6 (SHOULD). El avance sale del ledger (`coyote status`, `coyote log`), no de trackers manuales.
- R7 (SHOULD). `AGENTS.md` se genera desde los documentos coyote y se mantiene vigente.
- R8 (MUST). Toda decisión de riesgo R2 o R3 tiene ADR en MADR antes del parche.
- R9 (SHOULD). CI corre pruebas y `coyote standards lint`.
- R11 (SHOULD). OpenAPI y AsyncAPI son la fuente de tipos entre servicios y clientes.
- R15 (MUST). Commits, PRs, comentarios, documentos y releases no llevan atribución a herramientas o modelos de IA: ni trailers de coautoría de asistentes, ni pies promocionales, ni enlaces de sesión. La trazabilidad de qué agente hizo qué vive en el ledger. Los patrones están en `attribution.yaml`.
- R16 (SHOULD). Todo feature parte del modelo de dominio y de sus contratos antes del código.
- R17 (MUST). Un IDE sin hooks que bloqueen (nivel 2 o 3) no ejecuta tareas de riesgo R2 o R3 fuera de ramas `coyote/` protegidas por `coyote gate pr`. El nivel de cada IDE se mide con `coyote doctor --ide <ide> --canary`.
- R18 (MUST). Ningún archivo del repo es un archivo de secretos (`.env`, tfstate, llaves, keystores, kubeconfig, cuentas de servicio) ni lleva un secreto escrito (llaves privadas, tokens de nube o de GitHub, URLs con contraseña). Un secreto que llegó a git se rota. Los falsos positivos se dispensan con motivo en `project.yaml` (`secrets.allow`) o con `coyote:allow-secret` en la línea.

## 3. Organización

- R4 (SHOULD). Los dominios viven una sola vez, en el hub de la organización.

## 4. Agentes

- A1 (MUST). Un agente no ejecuta acciones con efectos sin aprobación humana por step, por plan o por concesión de autonomía; la herramienta ejecuta lo aprobado.
- A2 (MUST). Todo step declara contrato, budget y esquema de salida.
- A3 (MUST). Todo evento queda en el ledger en CCF y todo cierre de workstream deja su resumen de tokens, modelos y gasto.
- A4 (MUST). Un loop autónomo corre en una rama `ws/`, dentro de sus topes, y su resultado lo evalúa la persona que lo autorizó.

## Cómo se ajusta

Una capa superior puede agregar reglas, endurecerlas, relajarlas o desactivarlas. Relajar o desactivar exige `reason` y conviene un ADR; `until` hace que el ajuste caduque y la regla vuelva sola a su nivel original. Un proyecto también puede dispensar reglas concretas desde el frontmatter de `README.coyote.md` (`standards: { waive: [{id, reason, adr}] }`); dispensar una regla MUST exige motivo. Redefinir una regla con el mismo `id` no puede bajar su nivel y queda a la vista en `coyote standards diff`.
