# Remoto v1 — ritmo humano, credenciales, push, pull y web

Estado: nuevo en v0.2.0 · Implementación: `internal/pace`, `internal/auth`, `internal/github`, `internal/usage`, `internal/web` · Decisiones: ADR-0002, ADR-0008

## Principios

- Las escrituras remotas van con la identidad y las credenciales de la persona. Git usa su SSH o su credential helper; coyote no toca esas credenciales.
- La cuenta se comporta como una persona: pocas escrituras, con pausas, y nunca insiste cuando el proveedor pide esperar.
- Ningún secreto en archivos, ningún servicio fuera de loopback.

## Ritmo humano

El estado es por cuenta (host), no por proyecto. Vive en el directorio de configuración del usuario (`~/Library/Application Support/coyote` en macOS, `~/.config/coyote` en Linux) con permisos 0600, y un lock serializa a varios procesos.

| Perfil | Escrituras/min | Escrituras/h | Pausa mínima | Reserva de cuota | Espera máxima |
|--------|----------------|--------------|--------------|------------------|---------------|
| human (por defecto) | 6 | 120 | 3 s | 20 % | 2 min |
| batch | 20 | 300 | 1 s | 20 % | 5 min |

GitHub admite hasta 80 escrituras por minuto y 500 por hora; los perfiles quedan muy por debajo. Las lecturas no consumen el cupo de escrituras.

Tanto las escrituras como las lecturas respetan:

- `Retry-After`;
- la cuota restante (`X-RateLimit-*`), dejando la reserva para el uso interactivo de la persona;
- los límites secundarios: un 403 o 429 sin `Retry-After` espera un minuto.

Si la espera superara el máximo, la operación no se hace y se informa desde qué hora reintentar.

## Credenciales

El token de la API se busca en `COYOTE_GITHUB_TOKEN`, `GH_TOKEN` o `GITHUB_TOKEN`, luego en `gh auth token` y luego en el llavero del sistema. `coyote auth login --with-token` y `--device` lo guardan en el llavero:

- **macOS:** `security`. El token viaja por la entrada estándar de `security -i`, no como argumento, así no aparece en la lista de procesos.
- **Linux:** `secret-tool`, también por la entrada estándar.

El token nunca se escribe en archivos, en el ledger ni en la salida; se muestra enmascarado (`gho_…abcd`). El device flow usa la OAuth App de la organización (client ID público) y respeta `authorization_pending`, `slow_down` y la expiración.

## push

`coyote push [--remote origin] [--agent A] [--dry-run]` publica la rama actual después de revisar:

1. que HEAD esté en una rama y que el remoto exista;
2. que un agente, o el modo `autonomous`, no publique en `main`, `master`, `trunk` ni `release/*` (A4, R17);
3. que autor y committer configurados sean una persona (R15);
4. que ningún commit que sale, merges incluidos, lleve atribución a IA ni autoría de una herramienta (R15);
5. que los commits que salen sigan el formato (R2);
6. que el estándar no tenga hallazgos MUST.

Luego espera a ritmo humano y corre `git push` sin `--force` (coyote nunca fuerza). El evento `sync` queda en el ledger y entra en el siguiente `coyote commit`. `--no-verify` omite el lint solo para una persona y queda registrado con estado `skip`.

## pull

`coyote pull [--remote origin]` corre `git pull --ff-only` y resume:

- cuántos commits llegaron y de quién;
- cuántos eventos nuevos hay en el ledger;
- qué cambió en `README.coyote.md`, `CONTEXT.coyote.md`, `coyote/decisions` y `coyote/standards`.

Si `AGENTS.md` lo generó coyote, lo actualiza. Una rama divergente no se integra sola: la persona decide cómo con git.

## Web FinOps

`coyote web [--addr 127.0.0.1:7410]` sirve el consumo del ledger del proyecto y de los repos registrados cuyos documentos estén disponibles. Ofrece estas vistas:

- proyecto › persona;
- persona › proyecto;
- modelos › persona;
- agentes › proyecto.

Muestra tokens de entrada, caché y salida, y el costo de entrada, de salida y total, en los periodos 7, 30, 90 días o todo. El modelo sale de la referencia `model:` de cada evento; los cierres (`close`) no se suman. `/api/usage` entrega lo mismo en JSON.

Protecciones:

- escucha solo en loopback;
- rechaza cualquier `Host` que no sea loopback (DNS rebinding);
- solo acepta GET y HEAD;
- no usa JavaScript;
- envía CSP `default-src 'none'`, `nosniff`, `no-referrer` y `no-store`;
- escapa todo el texto que viene del ledger.
