# Auditoría

`coyote audit` arma el reporte de un mes con lo que coyote ya registra: el ledger, las decisiones de `coyote/approvals/` y la historia de git. No usa red ni modelos (ADR-0024). Es evidencia para una auditoría, no una certificación.

## 1. Generarlo

```sh
coyote audit                          # el mes actual, en UTC
coyote audit --month 2026-09          # otro mes
coyote audit --month 2026-09 --write  # lo deja en coyote/audit/2026-09.md
```

Revisa el archivo y haz commit:

```sh
git add coyote/audit/2026-09.md
coyote commit -m "docs(audit): reporte de septiembre de 2026"
```

`audit` sale con 1 si alguna verificación falla, así que sirve en la CI, y `--json` da el mismo reporte para otra herramienta. Para un agente, `audit` es lectura libre y `audit --write` pide aprobación.

## 2. Qué trae

- **Resumen:** eventos, personas, agentes y el costo contra el tope del mes.
- **Eventos:** por tipo y por actor.
- **Aprobaciones y gate:** quién aprobó qué, las concesiones, lo que bloqueó el gate, los planes `autonomous` y cada gate de release.
- **Costo:** por persona, agente, modelo y workstream.
- **Integridad:** si el registro es confiable (abajo).
- **Controles:** qué control de ISO/IEC 42001 y del NIST AI RMF apoya cada parte.
- **Límites:** lo que el reporte no prueba. La identidad es la que declara cada máquina, lo que un agente hizo por fuera de coyote no está y el costo es una estimación. Una historia reescrita no se ve desde HEAD: el reporte anota el commit y la huella del ledger para que compares con el del mes anterior.

## 3. Qué hacer con una falla o un aviso

| Verificación | Qué hacer |
| --- | --- |
| El ledger solo agrega líneas | Un commit cambió, borró o cambió de tipo un archivo del ledger: `git log -p -- coyote/ledger` dice cuál y quién. El original sigue en la historia de git. No lo restaures editando, que sería otra reescritura: agrega un evento que explique qué pasó, y la falla queda como evidencia del mes. Un aviso de clon superficial se quita con `git fetch --unshallow` |
| Actor, archivo y autor del commit | Una persona escribió en el archivo de otra, o un mismo autor de git registra a nombre de varias. Revisa la identidad de git de esa máquina |
| Aprobaciones registradas por un agente | Un agente registró un `apr` o un `rej` que ningún registro de `coyote/approvals/` respalda: el evento tiene que nombrar al empezar el gate del registro, decir su decisión y no ser anterior a ella, y cada registro respalda un solo evento. Confirma la decisión con la persona y deja su registro. Con el registro, la falla baja a aviso |
| Gates de release con su evento en el ledger | Una decisión de `coyote/approvals/` no tiene su evento. Regístralo tarde, y dilo: `coyote record apr "G2 aprobado: v0.2.0; registro tardío" --refs apr:P-0002`. Cuenta para el mes de la decisión |
| Eventos con fecha anterior a su commit | Un evento llegó en un commit del mes con una fecha más de un día anterior, así que puede caer en un mes ya auditado. Si es una importación o un squash, déjalo anotado; si es una aprobación de un agente que cae en un mes anterior, es falla: confírmala con la persona. El mismo aviso trae un commit con fecha futura (un reloj adelantado): cuenta como llegado con el que le sigue, y si esa fecha pasa puede contarse otra vez al mes siguiente, así que pasada la fecha conviene volver a auditar el periodo y comparar la huella ([audit-v1](../specs/audit-v1.md)) |
| Eventos con fecha futura | Una máquina tiene el reloj adelantado. Corrígelo: un evento en el futuro afloja los topes y las ventanas |
| Líneas inválidas del ledger | Alguien editó el ledger a mano. El reporte dice el archivo y la línea |
| Commits sin atribución a IA (R15) | Un commit del mes la lleva. Si no se ha publicado, corrige el mensaje; si ya se publicó, déjalo anotado. `coyote hooks install` evita que se repita |

El detalle de cada verificación está en [audit-v1](../specs/audit-v1.md).
