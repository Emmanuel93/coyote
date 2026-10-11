# Primeros 15 minutos con coyote

Al terminar vas a tener un proyecto con contexto versionado, un commit con tu autoría, el gate puesto en tu IDE, una acción de agente aprobada por ti y el reporte de lo que pasó. Cada comando de este tutorial lo corre una prueba de coyote (`TestTutorial`): si algo cambia, la prueba falla antes que tú.

Necesitas git con tu nombre y tu correo configurados.

## 1. Instala coyote

Copia el binario de tu plataforma del release o compílalo con Go:

```sh
make install                                  # desde el repo de coyote (no se prueba)
coyote version
```

## 2. Crea un proyecto

```sh
coyote init tienda --type backend --purpose "API de pedidos de la tienda"
cd tienda
coyote doctor
```

`init` crea el repo y los documentos que leen las personas y los agentes: `README.coyote.md`, `CONTEXT.coyote.md`, `AGENTS.md` y `coyote/project.yaml`. `doctor` revisa que todo esté en orden. Los avisos dicen qué falta, sin detenerte.

## 3. Deja contexto y pídelo

Lo que el equipo aprende se escribe una vez y llega a cada agente:

```sh
coyote note "los pedidos se confirman al capturar el pago" --type inv --scope pedidos
coyote get context --scope pedidos
coyote ask "cuándo se confirma un pedido"
```

`get context` arma un paquete acotado en tokens y `ask` busca con referencias. Ninguno de los dos llama a un modelo. Lo que citan de los repos sale marcado como dato, no como instrucción.

## 4. Haz commit con tu autoría

```sh
git add -A
coyote commit -m "chore: adopta coyote"
```

El commit lleva tu identidad de git, el formato `tipo(ámbito)` y ninguna atribución a herramientas de IA. Además queda en el ledger.

## 5. Pon el gate en tu IDE

```sh
coyote install --ide claude-code
```

Desde ahora, cada herramienta del agente pasa por `coyote gate check`. Leer es libre; lo que tiene efectos espera a que lo apruebes. Para ver cómo se siente sin abrir el IDE, pide tú lo que pediría un agente:

```sh
coyote propose --bash "go test ./..."
coyote approvals
coyote review <id>
coyote approve <id>
```

`review` muestra el comando exacto y `approve` lo aprueba por su hash: si el agente cambia una letra, la aprobación ya no sirve. En el IDE, mide que el gate de verdad se respete con `coyote doctor --ide claude-code --canary`.

## 6. Mira lo que pasó

```sh
coyote log
coyote audit
coyote web                                    # se queda corriendo; Ctrl+C para salir (no se prueba)
```

`log` muestra el ledger con sus totales. `audit` arma el reporte del mes: eventos, aprobaciones, costo e integridad. `web` muestra lo mismo en el navegador, con una URL que lleva la clave de la sesión.

## Siguiente

- [Guías](../guides/README.md): el gate en cada IDE, workstreams, el pipeline de PRs, el hub y los SLOs.
- [Referencia de comandos](../reference/cli.md).
- [Por qué coyote es así](../explanation/diseno.md).
