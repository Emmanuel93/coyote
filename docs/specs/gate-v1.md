# Gate v1 — aprobación humana de acciones exactas

Estado: nuevo en v0.3.0 · Implementación: `internal/gate`, `internal/approval`, `coyote gate check`, `coyote approvals|review|approve|reject|revoke|propose` · Decisión: ADR-0009

## Para qué

La regla A1 dice que ningún agente ejecuta acciones con efectos sin aprobación humana. El gate la hace cumplir donde el agente actúa: en el hook previo del IDE, que corre antes de los permisos, dentro de los subagentes y aun en `bypassPermissions`. Nada depende del prompt.

## Cómo decide

`coyote gate check` recibe por la entrada estándar la llamada de herramienta tal como la manda el IDE y decide en este orden:

1. **R15.** Un commit, tag, PR o release con atribución a IA, o con la autoría de una herramienta, se bloquea.
2. **Bloqueos que ninguna aprobación levanta:**
   - que un agente corra `coyote approve`, `reject`, `revoke`, `auth`, `hooks` o `install`;
   - que escriba en el gate: `.claude/settings*.json`, `.claude/hooks/`, `.cursor/hooks*`, `.git/`, `.coyote/`, `coyote/approvals/`, `coyote/ledger/`, la configuración global del IDE, de git o del shell;
   - que lea o escriba credenciales: `~/.ssh`, `~/.aws`, `~/.config/gh`, `~/.netrc`, la clave local de coyote y parecidas; también recorrer una carpeta que las contiene (`grep -r ~`);
   - que un subagente declare en un comando un agente distinto del que reporta el IDE.
3. **Lectura libre.** Las herramientas que solo leen (Read, Grep, Glob, WebFetch, Task, TodoWrite y las MCP cuyo nombre empieza con get, list, search, read, fetch, view, show o describe) y una lista cerrada de comandos de solo lectura pasan sin registro.
4. **Todo lo demás necesita una aprobación de la acción exacta.**

### Comandos de solo lectura

Un comando pasa sin aprobación solo si cada segmento (separados por `;`, `&&`, `||` o `|`) es un programa de la lista, llamado por su nombre, con argumentos que no escriben ni ejecutan:

| Programas | Restricciones |
|-----------|---------------|
| `ls`, `cat`, `head`, `tail`, `wc`, `stat`, `du`, `df`, `grep`, `diff`, `cmp`, `cut`, `tr`, `nl`, `column`, `comm`, `jq`, `strings`, `od`, `sha256sum` y similares, `echo`, `printf`, `pwd`, `which`, `cd`, `sleep` | ninguna |
| `find` | sin `-exec`, `-execdir`, `-ok`, `-delete`, `-fprint*`, `-fls` |
| `sort`, `uniq`, `tree`, `xxd`, `file`, `date`, `rg` | sin las banderas que escriben archivos o corren programas (`-o`, `--pre`, `-C`…) |
| `git` | subcomandos de lectura (`status`, `log`, `diff`, `show`, `blame`, `grep`, `rev-parse`, `ls-files`…); `branch`, `tag`, `remote`, `config`, `stash`, `reflog` solo para listar; sin `-c`, `--git-dir`, `--output`, `--ext-diff` ni `--textconv` |
| `go` | `version`, `env` sin `-w`, `vet` sin `-vettool` |
| `coyote` | `status`, `log`, `get`, `ask` sin `--record`, `standards`, `attribution check`, `doctor`, `approvals`, `review`, `index`, `propose`, `install --check` |

Se rechaza cualquier construcción que el análisis no pueda garantizar: sustituciones (`$(...)`, comillas invertidas), variables, asignaciones de entorno, redirecciones a archivos (solo se admiten `/dev/null` y `2>&1`), subshells, segundo plano, heredocs, comentarios, rutas explícitas al programa y comodines sin comillas en programas cuyas banderas importan. Correr pruebas o compilar necesita aprobación: ejecuta código que el agente pudo escribir.

## Hash de la acción

| Herramienta | Qué entra en el hash |
|-------------|----------------------|
| Bash, Shell (Cursor), shell (Codex) | el comando sin espacios al borde y la carpeta relativa al proyecto; `dangerouslyDisableSandbox` si está activo |
| Write | la ruta relativa y el SHA-256 del contenido |
| Edit | la ruta, los SHA-256 del texto viejo y el nuevo, y `replace_all` |
| cualquier otra | el nombre y la entrada completa en JSON canónico (claves ordenadas, números como llegaron) |

La descripción o el tiempo de espera no cambian el hash; un carácter del comando o del contenido, sí. Un campo desconocido en Write o Edit hace que se hashee la entrada completa. El mismo comando pide la misma aprobación desde Claude Code o desde Cursor.

## Aprobaciones

Si falta la aprobación, el gate encola una propuesta en `.coyote/proposals/` (local, 0600) y bloquea con un mensaje que dice cómo aprobarla. El mismo hash reusa la propuesta y cuenta intentos.

| Comando | Qué hace |
|---------|----------|
| `coyote approvals [--all]` | la cola; con `--all`, también las aprobaciones vigentes, agotadas, vencidas y revocadas, y los registros que no validan |
| `coyote review [id]` | el comando, el diff (con el git del sistema) o la entrada completa, quién lo pidió y desde qué agente |
| `coyote approve <id>... [--uses N] [--for 1h]` | aprueba; `--all` aprueba la cola; `--bash CMD` aprueba un comando antes de que se pida |
| `coyote reject <id> --reason TEXTO` | rechaza; el agente recibe el motivo si repite la llamada (24 h) |
| `coyote revoke <id> --reason TEXTO` | revoca una aprobación vigente |
| `coyote propose --bash CMD` | encola un comando sin intentarlo |

`approve`, `reject` y `revoke` exigen una persona: se niegan si corren dentro de una sesión de agente (`COYOTE_IDE`, que pone `coyote install`, o `CLAUDECODE`, que pone Claude Code) o sin una terminal interactiva.

Cada aprobación es `coyote/approvals/<id>.json`:

```json
{ "id": "P-7q3k9d", "type": "action", "hash": "sha256:…", "object": "Bash: go test ./...", "tool": "Bash",
  "project": "tienda", "ws": "W-0003", "requested_by": "@ana/coyote-dev", "approver": "@ana", "via": "cli",
  "ts": "2026-09-28T07:10:00Z", "expires": "2026-09-28T08:10:00Z", "uses": 3, "mac": "hmac-sha256:…" }
```

- Vale de 1 a 100 usos y 24 horas como máximo; por defecto, un uso durante una hora.
- `mac` es un HMAC-SHA256 con una clave local de la persona (0600, en su directorio de configuración, fuera del repo). Un registro modificado, copiado de otra máquina o de otro proyecto no valida.
- El registro nunca se reescribe. Los usos son los eventos `gate` con estado `ok` y `apr:<id>` en el ledger; una revocación es un evento `rej` con `apr:<id>`. Un lock en `.coyote/gate.lock` impide gastar el mismo uso dos veces.
- Las aprobaciones de gate de release (`P-0001`, `P-0002`) no son de acción y el gate las ignora.

## Ledger

| Decisión | Evento |
|----------|--------|
| lectura | ninguno |
| aprobada | `gate` · `ok` · `aprobado: <acción>` · `apr:<id> hash:<16 hex>` |
| en cola | `gate` · `pend` · `en cola: <acción>` · `prop:<id>`; una vez cada 10 minutos por propuesta |
| bloqueada | `gate` · `fail` · `bloqueado: <motivo>`; una vez cada 10 minutos por acción |
| aprobar, rechazar, revocar | `apr` o `rej` a nombre de la persona |

El actor es `@persona/<agente>`, con el agente que reporta el IDE (`agent_type` de Claude Code) o el nombre del IDE. La descripción se escribe sin secretos (tokens, contraseñas, credenciales en URLs) y sin texto de atribución a IA.

## Entradas y salidas por IDE

| IDE | Hook | Entrada | Bloqueo |
|-----|------|---------|---------|
| Claude Code | `PreToolUse`, matcher vacío, en `.claude/settings.json` | `tool_name`, `tool_input`, `cwd`, `session_id`, `agent_type` | salida 2 con el motivo en stderr |
| Cursor | `preToolUse` con `failClosed` en `.cursor/hooks.json` | `tool_name`, `tool_input`, `conversation_id`, `workspace_roots` | JSON `{"permission":"deny",…}` y salida 2; `{"permission":"allow"}` al permitir |
| Codex | `PreToolUse` | como Claude Code; el comando puede venir como lista (`bash -lc "…"`) | salida 2 |
| Copilot | `preToolUse` | `toolName`, `toolArgs` (JSON en texto) | salida 2 |

El gate falla cerrado: una entrada ilegible, un proyecto que no se encuentra, un error o un pánico bloquean las acciones con efectos. El hook que instala `coyote install` también bloquea si no encuentra el binario de coyote.

## Límites conocidos

- Lo que hace un comando aprobado es responsabilidad de quien lo aprueba: si apruebas `make x`, corre lo que diga el Makefile.
- El análisis de solo lectura es conservador y puede pedir aprobación para comandos inocuos (`git config user.name` sin `--get`).
- Los bloqueos por texto (rutas del gate o credenciales en un comando) pueden bloquear un mensaje de commit que solo las menciona.
- Las herramientas MCP de lectura se reconocen por su nombre; un servidor MCP mal nombrado queda del lado de la lectura.
- IDEs sin hook previo (Devin, Zed) no están cubiertos: llegan con el gate de CI en v0.5.
- `supervised` y `autonomous` se aceptan en `project.yaml`, pero hasta v0.4 el gate aplica `manual`.
