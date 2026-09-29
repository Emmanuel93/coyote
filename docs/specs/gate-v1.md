# Gate v1 — aprobación humana de acciones exactas

Estado: nuevo en v0.3.0; en v0.6.0 reconoce Codex, Gemini CLI, Copilot, Windsurf, Devin CLI y Junie, y mide el nivel de cada IDE · Implementación: `internal/gate`, `internal/approval`, `coyote gate check`, `coyote approvals|review|approve|reject|revoke|propose` · Decisiones: ADR-0009, ADR-0015

## Para qué

La regla A1 dice que ningún agente ejecuta acciones con efectos sin aprobación humana. El gate la hace cumplir donde el agente actúa: en el hook previo del IDE, que corre antes de los permisos, dentro de los subagentes y aun en `bypassPermissions`. Nada depende del prompt.

## Cómo decide

`coyote gate check` recibe por la entrada estándar la llamada de herramienta tal como la manda el IDE y decide en este orden:

1. **R15.** Un commit, tag, PR o release con atribución a IA, o con la autoría de una herramienta, se bloquea.
2. **Bloqueos que ninguna aprobación levanta:**
   - que un agente corra `coyote approve`, `reject`, `revoke`, `auth`, `hooks` o `install`, o que lance otros agentes con `coyote run`, `coyote ws run` o `coyote ws continue`. No importa qué lo invoque: el gate busca la palabra `coyote` seguida del subcomando en cada segmento (`watch`, `xargs`, `find -exec`…). También sigue las variables que guardan `coyote` y lee como comando el texto que el shell ejecutaría (`bash -c`, `eval`, `script -c`, `$(…)`, un `echo … | bash`). Un mensaje de commit, un título o un patrón de `grep` que solo lo mencionan no cuentan;
   - que escriba en el gate: `.git/` y toda carpeta de git del proyecto (la común de un worktree enlazado), `.coyote/`, `coyote/approvals/`, `coyote/ledger/`, `coyote/project.yaml` (ahí viven la autonomía y el hub que rige), `coyote/infra.yaml`, `coyote/hub.yaml` (admins y presupuesto de la organización) y, desde un proyecto, cualquier archivo del clon del hub que declara, su carpeta de git incluida, la configuración global del IDE, de git o del shell, y los hooks o la configuración que los apaga en cada IDE:
     - `.claude/settings*.json` y `.claude/hooks/`; `.cursor/hooks*`;
     - `.codex/hooks*` y `.codex/config.toml`; `.gemini/settings.json` y `.gemini/hooks/`; `.github/hooks/`;
     - `.windsurf/hooks*`, `.devin/hooks*` y `.devin/config*.json`; `.junie/config.json`, `.kiro/hooks/` y `.clinerules/hooks/`;
     - en `.vscode/settings.json`, las opciones que apagan los hooks de VS Code o aprueban herramientas solas (`chat.useHooks`, `chat.hookFilesLocations`, `chat.tools.*autoApprove`);
   - el canario de `coyote doctor` (`coyote doctor canary <código>`): se niega a propósito para medir el nivel del IDE (ADR-0015);
   - que lea o escriba credenciales: `~/.ssh`, `~/.aws`, `~/.config/gh`, `~/.netrc`, la clave local de coyote y parecidas; también recorrer una carpeta que las contiene (`grep -r ~`), o leer o buscar dentro de una carpeta de git (la del proyecto, la de un worktree, la del clon del hub o cualquier ruta con un `.git`), cuya `config` puede llevar un token en la URL de un remoto. Listar sus nombres (`ls .git`) sí se puede;
   - un comando con efectos que nombra el clon del hub declarado, por su ruta como la escribe `coyote/project.yaml` o absoluta (`git -C ../acme-hub commit`);
   - que aplique infraestructura: `terraform apply` o `destroy` y los demás cambios de estado, los comandos de apply de `coyote/infra.yaml` y cualquier cambio a un ambiente `reviewed`; el inventario tampoco lo escribe un agente (docs/specs/infra-v1.md, ADR-0017). Una herramienta de apply cuyo subcomando llega al correr (`… | xargs terraform`, `terraform "$@"`, un alias, `terraform $VERBO`) se bloquea: podría ser apply;
   - que lea o escriba un archivo de secretos del proyecto (`.env`, tfstate, llaves, keystores, kubeconfig, cuentas de servicio), que escriba un secreto literal (también en la entrada de una herramienta MCP) o que corra un comando que imprime o crea credenciales (`gcloud auth print-access-token`, `kubectl get secret`, `terraform output`, `env` a secas…); el detalle está en docs/specs/secrets-v1.md (ADR-0016);
   - que un subagente declare en un comando un agente distinto del que reporta el IDE.
3. **Lectura libre.** Las herramientas que solo leen (Read, Grep, Glob, WebFetch, Task, TodoWrite y las MCP cuyo nombre empieza con get, list, search, read, fetch, view, show o describe) y una lista cerrada de comandos de solo lectura pasan sin registro.
4. **Todo lo demás necesita una aprobación de la acción exacta.**

### Cómo lee un comando

Los bloqueos revisan el comando como lo correría el shell, segmento por segmento:

- **Comillas y escapes.** No esconden una palabra: `terraform "apply"`, `t"erraform" apply`, `terraform ap\ply` y `$'\x61pply'` son `terraform apply`.
- **Llaves y variables.** Se expanden las llaves (`terraform {apply,}`) y las variables asignadas antes en el mismo comando (`a=apply; terraform $a`).
- **Comandos dentro de otros.** El texto entre comillas que recibe un programa que lo ejecuta (`bash -c`, `ssh`, `sudo`, `docker run … sh -c`) se lee como otro comando; también los argumentos de `eval`, el de `$(…)` y el de las comillas invertidas. Un listado (`ls`, `find`) que alimenta a otro programa (`ls .env | xargs cat`, `cat $(ls .env)`) cuenta como lectura de lo que lista.
- **Datos.** No cuentan: los mensajes y títulos (`-m`, `--body`), las notas de coyote, lo que imprime `echo` o `printf` (salvo sus redirecciones; `echo "**"` no lee nada), el patrón de `grep`, `rg` o `git grep` y los valores de `git log --grep`, `--author` o `-S`. Si el comando tiene un programa que ejecuta lo que recibe (`sh`, `eval`, `xargs`, `python`…), los datos sí cuentan: `echo "…" | sh` corre el texto.
- **Sustituciones.** Si el comando arma palabras al correr (`$(…)`), también se revisa el texto completo.
- **Búsquedas.** `grep -r` y `diff -r` leen todo, también `.git`: sobre una carpeta que contiene una carpeta de git piden aprobación, salvo con `--exclude-dir=.git`. `rg` y `git grep` no entran a `.git` y siguen libres.
- **Lectura.** Un comando de solo lectura puede mirar la configuración del gate (los hooks, `coyote/project.yaml`). No puede leer credenciales ni la configuración de un IDE que puede llevar tokens en el `env` de sus servidores MCP (`.claude/settings*.json`, `.codex/config.toml`, `.gemini/settings.json`, `~/.copilot/config.json`, `.devin/config*.json`, `.junie/config.json`, `managed-settings`, `~/.claude.json`, los archivos del shell); de esa configuración, `ls`, `stat` o `find` sí ven el nombre.
- **Claves.** Las que apagan el gate (`disableAllHooks`, `core.hooksPath`, `chat.useHooks`) bloquean aunque vayan en un dato: `echo '{"disableAllHooks": true}' > x.json` se bloquea.

### Comandos de solo lectura

Un comando pasa sin aprobación solo si cada segmento (separados por `;`, `&&`, `||` o `|`) es un programa de la lista, llamado por su nombre, con argumentos que no escriben ni ejecutan:

| Programas | Restricciones |
|-----------|---------------|
| `ls`, `cat`, `head`, `tail`, `wc`, `stat`, `du`, `df`, `grep`, `diff`, `cmp`, `cut`, `tr`, `nl`, `column`, `comm`, `jq`, `strings`, `od`, `sha256sum` y similares, `echo`, `printf`, `pwd`, `which`, `cd`, `sleep` | ninguna |
| `find` | sin `-exec`, `-execdir`, `-ok`, `-delete`, `-fprint*`, `-fls` |
| `sort`, `uniq`, `tree`, `xxd`, `file`, `date`, `rg` | sin las banderas que escriben archivos o corren programas (`-o`, `--pre`, `-C`…) |
| `git` | subcomandos de lectura (`status`, `log`, `diff`, `show`, `blame`, `grep`, `rev-parse`, `ls-files`…); `branch`, `tag`, `stash` y `reflog` solo para listar; `remote` solo los nombres (las URLs pueden llevar tokens); `config` solo `--get` de claves sin secretos (`user.name`, `user.email`, `init.defaultBranch`…), nunca `--list` ni `--get-regexp`; sin `-c`, `--git-dir`, `--output`, `--ext-diff` ni `--textconv` |
| `go` | `version` y `env` con variables nombradas sin secretos (`GOPATH`, `GOOS`…); `vet`, `build` y `test` necesitan aprobación porque compilan (cgo corre el compilador de C) |
| `coyote` | `status`, `log`, `get`, `ask` sin `--record`, `standards`, `attribution check`, `doctor` sin el canario, `approvals`, `review`, `index`, `propose`, `install --check`, `ws status`, `ws check`, `secrets list` y `secrets scan` |

Se rechaza cualquier construcción que el análisis no pueda garantizar: sustituciones (`$(...)`, comillas invertidas), variables, asignaciones de entorno, redirecciones a archivos (solo se admiten `/dev/null` y `2>&1`), subshells, segundo plano, heredocs, comentarios, rutas explícitas al programa y comodines sin comillas en programas cuyas banderas importan. Correr pruebas o compilar necesita aprobación: ejecuta código que el agente pudo escribir.

## Hash de la acción

| Herramienta | Qué entra en el hash |
|-------------|----------------------|
| Bash, Shell (Cursor), shell (Codex), `run_shell_command` (Gemini CLI), `bash` (Copilot), `run_command` (Windsurf), `exec` (Devin CLI) | el comando sin espacios al borde, la carpeta relativa al proyecto y cualquier otro campo con valor (`run_in_background`, `is_background`, `dangerouslyDisableSandbox`…); la descripción, la justificación, el tiempo de espera y los identificadores de sesión no cuentan, y un campo falso o vacío equivale a no traerlo |
| Write | la ruta relativa y el SHA-256 del contenido |
| Edit | la ruta, los SHA-256 del texto viejo y el nuevo, y `replace_all` |
| cualquier otra | el nombre y la entrada completa en JSON canónico (claves ordenadas, números como llegaron) |

La descripción o el tiempo de espera no cambian el hash; un carácter del comando o del contenido, sí. Un campo desconocido en Write o Edit hace que se hashee la entrada completa. El mismo comando pide la misma aprobación desde cualquier IDE.

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

`approve`, `reject` y `revoke` exigen una persona: se niegan si corren dentro de una sesión de agente (`COYOTE_IDE`, que pone `coyote install`; `CLAUDECODE`, de Claude Code; `CURSOR_AGENT`; `GEMINI_CLI`; `CODEX_SANDBOX`) o sin una terminal interactiva.

Cada aprobación es `coyote/approvals/<id>.json`:

```json
{ "id": "P-7q3k9d", "type": "action", "hash": "sha256:…", "object": "Bash: go test ./...", "tool": "Bash",
  "project": "tienda", "ws": "W-0003", "requested_by": "@ana/coyote-dev", "approver": "@ana", "via": "cli",
  "ts": "2026-09-28T07:10:00Z", "expires": "2026-09-28T08:10:00Z", "uses": 3, "mac": "hmac-sha256:…" }
```

- Vale de 1 a 100 usos y 24 horas como máximo; por defecto, un uso durante una hora. Un comando que cambia infraestructura se aprueba siempre de a un uso.
- `mac` es un HMAC-SHA256 con una clave local de la persona (0600, en su directorio de configuración, fuera del repo). `root` es una huella de la carpeta del proyecto. Un registro modificado, copiado de otra máquina, de otro proyecto o de otra copia del mismo proyecto no valida.
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

El actor es `@persona/<agente>`, con el agente que reporta el IDE (`agent_type` de Claude Code) o el nombre del IDE. La descripción, y el campo `object` de un registro de aprobación, se escriben sin secretos: los que reconoce el escáner de secretos (docs/specs/secrets-v1.md), variables con nombre de secreto (`DB_PASSWORD=…`, `GITHUB_TOKEN=…`), banderas de contraseña (`--password …`, `-u usuario:clave`, `mysql -p…`) y credenciales en URLs; y sin texto de atribución a IA.

## Entradas y salidas por IDE

| IDE | Hook | Entrada | Bloqueo |
|-----|------|---------|---------|
| Claude Code | `PreToolUse`, matcher vacío, en `.claude/settings.json` | `tool_name`, `tool_input`, `cwd`, `session_id`, `agent_type` | salida 2 con el motivo en stderr |
| Cursor | `preToolUse` con `failClosed` en `.cursor/hooks.json` | `tool_name`, `tool_input`, `conversation_id`, `workspace_roots` | JSON `{"permission":"deny",…}` y salida 2; `{"permission":"allow"}` al permitir |
| Codex | `PreToolUse` en `.codex/hooks.json` | como Claude Code, más `turn_id`; el comando puede venir como lista (`bash -lc "…"`) y `apply_patch` trae el parche en `command` | salida 2 |
| Copilot (CLI, VS Code y agente de GitHub) | `preToolUse` en `.github/hooks/coyote.json` | `toolName`, `toolArgs` (objeto o JSON en texto), `cwd`, `sessionId`, `timestamp` | JSON `{"permissionDecision":"deny",…}` y salida 2 |
| Gemini CLI | `BeforeTool` en `.gemini/settings.json` | `tool_name`, `tool_input`, `cwd`, `mcp_context`; `run_shell_command` trae su carpeta en `dir_path`, relativa al proyecto | salida 2 |
| Windsurf (Devin Desktop) | `pre_run_command`, `pre_write_code`, `pre_read_code` y `pre_mcp_tool_use` en `.windsurf/hooks.json` | `agent_action_name`, `trajectory_id` y `tool_info` (`command_line` y `cwd`; `file_path` y `edits`; `mcp_server_name`, `mcp_tool_name` y `mcp_tool_arguments`) | salida 2 |
| Devin CLI | el `PreToolUse` de Claude Code | como Claude Code, más `prompt_id`; la shell es `exec` | salida 2 |
| Junie CLI | `PreToolUse` en la configuración de usuario | como Claude Code | salida 2 |

El gate reconoce el IDE por el formato de la entrada, aunque el hook sea de otro IDE: Devin CLI, y VS Code con `chat.useClaudeHooks`, corren el hook de Claude Code. Cada llamada queda en el latido del IDE (`.coyote/gate/ides.json`), con el que `coyote doctor --ide` mide su nivel (docs/specs/install-v1.md).

El gate falla cerrado: una entrada ilegible, un error o un pánico bloquean las acciones con efectos; un `coyote/project.yaml` que no se puede leer bloquea todas las herramientas, lecturas incluidas, hasta que la persona lo corrija. Fuera de un proyecto coyote solo pasan las lecturas que no tocan credenciales. El hook que instala `coyote install` también bloquea si no encuentra el binario de coyote.

`review` y `approvals` muestran escapados los caracteres de control e invisibles (escapes de terminal, retornos de carro, controles de dirección Unicode): lo que la persona ve es lo que aprueba. El ledger tampoco los guarda.

## Límites conocidos

- Lo que hace un comando aprobado es responsabilidad de quien lo aprueba: si apruebas `make x`, corre lo que diga el Makefile.
- El análisis de solo lectura es conservador y puede pedir aprobación para comandos inocuos (`git config user.name` sin `--get`).
- Un comando que arma una palabra al correr con algo que el gate no ve (`$(which terraform) apply`, una variable del entorno) no se reconoce como bloqueo: pide la aprobación normal y la persona ve el comando completo. Si la palabra que falta es el subcomando de una herramienta de apply conocida, se bloquea.
- Un comando que llega al clon del hub sin nombrarlo (`cd ..` y después `cd acme-hub`) no se reconoce: pide aprobación.
- La salida de coyote en la terminal y en la web no lleva controles ni caracteres de formato invisibles: se escriben como su código (`\u202e`), también dentro de un JSON, donde siguen siendo un escape válido.
- El texto que el gate no sabe si es dato (el script de `sed` o de `awk`, el comando completo cuando hay sustituciones) todavía puede bloquear un comando que solo menciona una ruta del gate o una credencial.
- La cola (`.coyote/proposals/`) guarda cada comando tal cual para que la persona lo revise: vive en `.coyote/`, nunca se versiona y se escribe con permisos 0600.
- El gate lee el texto del comando, no lo que corre por dentro: un script que el agente escribió y la persona aprobó puede llamar a `coyote ws continue`. Por eso `coyote review` muestra el contenido de cada archivo que se aprueba escribir.
- Las herramientas MCP de lectura se reconocen por su nombre; un servidor MCP mal nombrado queda del lado de la lectura.
- WebFetch y WebSearch se tratan como lectura. La salida de datos del proyecto por red la controlan los permisos de dominio del IDE.
- Solo Claude Code reporta el subagente: en los demás IDEs un comando puede declarar otro `--agent`, y la persona lo ve al aprobarlo.
- IDEs sin hook previo o con hooks apagados (Zed, el plugin de Junie, un IDE de nivel 3 medido) no están cubiertos en la máquina: lo que llegue a un PR lo revisa `coyote gate pr` (docs/specs/ci-v1.md), y R17 los limita.
- Si un IDE lee dos hooks de coyote (VS Code con el de Copilot y, con `chat.useClaudeHooks`, el de Claude Code), el gate corre dos veces por acción y una aprobación de un solo uso se gasta en la primera: instala uno solo por IDE.
- `supervised` y `autonomous` cambian cuándo se detiene el motor de workstreams (ADR-0014), no lo que el gate deja pasar: en los tres modos el gate aplica `manual`.
