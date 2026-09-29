# Secretos v1 — archivos de secretos, credenciales y secretos escritos

Estado: nuevo en v0.6.0 · Implementación: `internal/secrets`, `internal/gate`, `coyote secrets`, `coyote commit`, hook `commit-msg`, check `secrets` (R18), `coyote gate pr` · Decisión: ADR-0016

Un agente que lee un secreto lo tiene en su contexto y lo puede escribir en un archivo, un comando o un PR. coyote hace tres cosas:

- el gate no deja que un agente lea ni escriba archivos de secretos, ni que corra comandos que imprimen credenciales, aun con aprobación;
- un escáner detiene los secretos escritos antes del commit y en el PR;
- ningún reporte, mensaje ni evento del ledger muestra el valor de un secreto.

## Archivos de secretos

Se reconocen por su nombre:

| Tipo | Nombres |
|------|---------|
| Variables de entorno | `.env`, `.env.*`, `*.env`, `.envrc` |
| Estado de Terraform | `*.tfstate`, `*.tfstate.backup`, `*.tfstate.*` |
| Llaves y certificados | `*.pem` con llave privada, `*.key`, `*.p12`, `*.pfx`, `*.p8`, `*.ppk`, `id_rsa`, `id_ed25519` y parecidas |
| Almacenes de llaves | `*.jks`, `*.keystore`, `key.properties`, `keystore.properties` |
| Nube y Kubernetes | `kubeconfig`, `*.kubeconfig`, `*-key.json`, `service-account*.json`, `client_secret*.json`, `credentials.json` |
| Otros | `secrets.*` y `secret.*` de datos (YAML, JSON, TOML…), `.npmrc`, `.pypirc`, `.netrc`, `.git-credentials`, `.docker/config.json`, `.htpasswd`, `.vault-token`, `*.ovpn` |

Las plantillas (`.example`, `.sample`, `.template`, `.dist`, `.tmpl`, `.tpl`, `.defaults`) no son secretos: un agente las lee y las edita.

Un `.pem` puede ser solo un certificado o una llave pública: cuenta como secreto si lleva el encabezado de una llave privada, o si el proyecto lo declara en `secrets.files`. Un `.pem` que no se puede leer (un symlink a un dispositivo, más de 1 MiB) cuenta como secreto. `*.p12` y `*.pfx` son binarios y cuentan siempre.

El proyecto suma los suyos y dispensa falsos positivos en `coyote/project.yaml`, que ningún agente edita:

```yaml
secrets:
  files: ["config/prod/*.yaml"]          # también son archivos de secretos
  allow:
    - { path: "testdata/**", reason: "llaves de prueba generadas para las pruebas de TLS" }
```

Una dispensa necesita un motivo real, igual que en el estándar.

## Gate

Se bloquea siempre, aun con aprobación:

- **Leer un archivo de secretos.** Con cualquier herramienta de lectura, con un comando (`cat`, `source`, `cp`, `--env-file=…`, `grep -f.env`, un script de `python -c`) o con una búsqueda cuyo patrón de nombres lo alcanza (`*.pem`, `**/.env*`, `rg -g '*.env'`, `grep --include=*.env`). Un comodín del shell se juzga por los archivos que alcanzaría en ese momento. El comando se lee como lo correría el shell (docs/specs/gate-v1.md): las comillas no esconden un subcomando.
- **Escribir un archivo de secretos.** Los valores los pone la persona.
- **Escribir un secreto literal** en cualquier archivo: el escáner revisa el contenido nuevo (nunca lo que se reemplaza). Una línea marcada con `coyote:allow-secret` pide la aprobación normal.
- **Correr un comando que imprime o crea credenciales:**
  - gcloud: `auth print-access-token`, `auth login`, `secrets versions access`, `iam service-accounts keys create`, `container clusters get-credentials`;
  - aws: `sts`, `configure get`, `secretsmanager get-secret-value`, `ssm get-parameter --with-decryption`, `ecr get-login-password`, `eks get-token`, `kms decrypt`;
  - az: `account get-access-token`, `keyvault secret show`, `aks get-credentials`;
  - Kubernetes: `kubectl get secret`, `describe secret` (sus anotaciones pueden llevar los datos), `create token`, `config view --raw`;
  - Terraform u OpenTofu: `output`, `show`, `console`, `state pull` y `state show`;
  - `helm get values`, `vault read` y `kv get`, `docker login`, `docker inspect`, `docker compose config`, `gh auth status --show-token`;
  - gestores de contraseñas y descifrado: `op`, `bw`, `pass`, `sops -d`, `gpg --decrypt`;
  - el entorno completo: `env` o `printenv` a secas, `export -p`, `declare -p`, `readonly`, `set` a secas, `/proc/*/environ`, y `echo`, `printenv` o `declare -p` de una variable con nombre de secreto (`$GITHUB_TOKEN`).

Los mensajes de commit, los títulos y las notas de coyote son datos: mencionar uno de estos comandos no lo corre. Usar una variable en un comando (`curl -H "Authorization: Bearer $GITHUB_TOKEN"`) no la imprime y pide la aprobación normal.

Pide aprobación, con la razón a la vista: una búsqueda que lee todos los archivos de una carpeta con archivos de secretos, sin respetar `.gitignore` (`grep -r`, `diff -r`, `rg --hidden`, `-u` o `--no-ignore*`, `git grep --no-index` o `--no-exclude-standard`). `rg` y `git grep` a secas pasan libres.

Nombrar un archivo de secretos con programas que solo ven nombres o metadatos (`ls`, `find` sin `-exec`, `stat`, `wc`, `test`) no lo lee.

## coyote secrets

```sh
coyote secrets list [--json]             # archivos de secretos del proyecto y los nombres de sus variables
coyote secrets scan [--json]             # secretos en los archivos versionados de un repo
coyote secrets scan --staged             # en lo preparado para el commit
coyote secrets scan --range base...head  # en lo que agrega un rango, como un PR
```

- `list` muestra rutas, tipos y nombres: las variables de un `.env` o de un `.properties`, las claves de primer nivel de un JSON o un YAML y las salidas de un tfstate, nunca sus valores. Un nombre con forma de secreto o de valor (un tramo de base64) se omite, y las líneas de una llave o de un valor de varias líneas no se leen como nombres. Así un agente sabe qué variables existen sin leerlas. Con `--no-names` no abre los archivos.
- `scan` sale con 1 si encuentra algo. Lee con git de plomería, sin escribir: sirve en cualquier repo, también fuera de un proyecto coyote (con `-C <ruta>`).
- Los dos subcomandos pasan el gate sin aprobación.

## Escáner

Solo patrones de alta confianza:

| Tipo | Qué reconoce |
|------|--------------|
| Llave privada | el encabezado PEM de una llave privada (RSA, EC, DSA, OpenSSH, PGP, cifrada) con su cuerpo: en las líneas siguientes, o en la misma tras un `\n` escapado, en cadenas concatenadas o aplanada con espacios |
| AWS | llaves de acceso (`AKIA…`, `ASIA…`) y la llave secreta junto a su nombre |
| GitHub, GitLab | `ghp_…`, `gho_…`, `github_pat_…`, `glpat-…` |
| Slack | tokens `xox…` y webhooks |
| Stripe, SendGrid, npm, PyPI | sus prefijos de llaves vivas |
| APIs de modelos | llaves con prefijo `sk-ant-` y `sk-proj-` |
| URL con contraseña | `esquema://usuario:contraseña@servidor`, si la contraseña es larga y mezcla caracteres; los ejemplos (`password`, `${TOKEN}`) no cuentan |

No cuentan:

- una llave de API de Google (`AIza…`): en Firebase va dentro de la app por diseño y se limita por app;
- el encabezado PEM solo, sin cuerpo: lo menciona el código que lee llaves y su documentación;
- los valores de ejemplo: la llave de ejemplo de AWS (`AKIA…EXAMPLE`), `xoxb-your-bot-token`, un valor con `example`, `your`, `dummy`, `xxxx` o un mismo caracter seis veces seguidas.

El escáner revisa todas las líneas, de cualquier largo: una línea de más de 64 KiB (un archivo minificado) se revisa por tramos. En un cambio cuenta las líneas de cada hunk, así que una línea agregada que empieza con `++` no se confunde con el encabezado de otro archivo.

Dónde corre:

- **`coyote commit`.** No deja pasar un secreto, ni con `--no-verify`.
- **Hook `commit-msg`.** Detiene cualquier `git commit` con secretos, también el de un agente.
- **Lint.** La regla R18 (MUST) revisa los archivos del proyecto: ninguno es un archivo de secretos ni lleva un secreto escrito.
- **`coyote gate pr`.** Un PR que agrega secretos queda en R3 y no pasa con ninguna aprobación: el secreto ya está en GitHub y hay que rotarlo.
- **Gate.** Cubre lo que escribe un agente. Un encabezado PEM sin cuerpo pide la aprobación normal: si un agente escribe una llave en dos ediciones, la persona ve el encabezado al aprobar la primera, y el escáner detiene la llave completa en el commit.

En un proyecto que vive en una subcarpeta de su repo, `secrets.files` y `secrets.allow` se toman relativas a la carpeta del proyecto; fuera de ella valen los nombres de siempre.

## Límites

- El escáner reconoce formas conocidas. Un secreto de forma libre, como una contraseña corta en un `application.yml`, no se reconoce por patrón. La defensa principal es no dejar leer los archivos de secretos y declararlos en `secrets.files`.
- Una búsqueda recursiva de una herramienta del IDE (Grep) depende de que respete `.gitignore`: los archivos de secretos deben estar ignorados, y R18 lo revisa.
- `docker compose up`, `npm run dev` o un programa que carga `.env` por dentro no nombran el archivo: piden la aprobación normal y la persona ve el comando.
- `gate pr` usa las reglas de siempre y las marcas `coyote:allow-secret`; las dispensas de `project.yaml` aplican en la máquina, en el commit y en el lint.
