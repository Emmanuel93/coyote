# El gate en tu IDE

El gate decide sobre cada herramienta que pide un agente. Leer es libre. Lo que tiene efectos espera a que una persona lo apruebe. Tocar credenciales, la configuración del gate, la carpeta de git o el clon del hub se bloquea siempre (ADR-0009).

## 1. Instalarlo

En la raíz del proyecto, en tu terminal y no en la del agente:

```sh
coyote install --ide claude-code     # o cursor, codex, gemini, copilot, windsurf, all
```

`install` escribe el hook de cada IDE, sus agentes y skills, y el hook `commit-msg` que quita la atribución a IA. No pisa la configuración que ya tengas. Haz commit de lo que agregó.

## 2. Medir su nivel

Que el hook exista no basta: el IDE puede ignorarlo, por ejemplo con una carpeta sin confianza o en modo restringido. El nivel se mide con un canario:

```sh
coyote doctor --ide claude-code --canary    # te da un código
```

En el IDE, pídele al agente que corra `coyote doctor canary <código>`. Después corre `coyote doctor --ide claude-code`:

| Nivel | Qué significa |
| --- | --- |
| 1 | El IDE pasa por el gate y lo respeta |
| 2 | Llama al gate, pero ignora la negación |
| 3 | Nunca llama al gate |

Un IDE de nivel 2 o 3 no hace tareas R2 o R3 fuera de ramas `coyote/` con `gate pr` (R17). La matriz de IDEs está en [install-v1](../specs/install-v1.md).

## 3. Decidir lo que piden los agentes

Cuando el gate frena una acción, la deja en una cola:

```sh
coyote approvals                    # la cola y las aprobaciones vigentes
coyote review <id>                  # el comando o el diff exacto
coyote approve <id>                 # un uso, vigente una hora
coyote approve --all                # todo lo que está en la cola
coyote reject <id> --reason "usa la otra tabla"
coyote revoke <id> --reason "ya no hace falta"
```

- La aprobación va por el hash de la acción exacta: si el agente cambia una letra, vuelve a la cola.
- Un agente no puede aprobar, ni desde su sesión ni sin una terminal.
- `--uses N` (hasta 100) y `--for 8h` (hasta 24 h) dan una concesión: varios usos durante un tiempo. Una aprobación vale solo en la máquina que la firmó.
- `approve --bash "make test" --uses 5` aprueba un comando antes de que el agente lo pida.

Cada decisión queda en el ledger, y [`coyote audit`](auditoria.md) la cuenta.
