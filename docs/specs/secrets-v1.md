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

- **Leer un archivo de secretos.** Con cualquier herramienta de lectura, con un comando (`cat`, `source`, `cp`, `--env-file=…`, `grep -f.env`, un script de `python -c`) o con una búsqueda cuyo patrón de nombres lo alcanza (`*.pem`, `**/.env*`, `rg -g '*.env'`, `grep --include=*.env`). Un comodín del shell se juzga por todos los archivos que alcanzaría en ese momento, leído como lo lee bash (clases de POSIX como `[[:alpha:]]`, `[!x]`, `[]x]`, equivalencias y símbolos de colación, y los corchetes citados) y, cuando no se sabe, de más; si la expansión pasa los topes de trabajo del gate (docs/specs/gate-v1.md), el comando pide aprobación en lugar de pasar por lo que no se alcanzó a ver; el patrón de una búsqueda (`rg -g '**/.e[n]v'`) se juzga igual, con cada grupo entre corchetes como un caracter cualquiera. El comando se lee como lo correría el shell (docs/specs/gate-v1.md): las comillas no esconden un subcomando. Para una herramienta de lectura, una ruta con `[`, `?` o `*` es el nombre literal de un archivo (`[x].env`, o la carpeta `Cliente [ACME]/`): se juzga tal cual y, si además puede ser un patrón, por lo que alcanza.
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

- `list` muestra rutas, tipos y nombres: las variables de un `.env` o de un `.properties`, las claves de primer nivel de un JSON o un YAML y las salidas de un tfstate, nunca sus valores. Un nombre con forma de secreto o de valor (un tramo de base64) se omite, y las líneas de una llave o de un valor de varias líneas no se leen como nombres. Así un agente sabe qué variables existen sin leerlas. Con `--no-names` no abre los archivos. Recorre el árbol de trabajo, salvo dependencias y compilación (`node_modules`, `vendor`, `build`, `dist`, `target`…), y suma lo que git sigue en esas carpetas, para listar lo mismo que `scan` reporta y que el gate bloquea. Si no puede leer el `project.yaml` de un proyecto, la lista sale incompleta, lo dice y termina con 1.
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
- los valores de ejemplo: los de la documentación de AWS (`AKIA…EXAMPLE`), un valor con `example`, `your`, `here`, `dummy` o `xxxx` como palabra entera (`xoxb-your-bot-token`, `ghp_YOUR_TOKEN_HERE`) o con un mismo caracter seis veces seguidas. Dentro de un token al azar esas letras no cuentan.

El escáner revisa todas las líneas, de cualquier largo: una línea de más de 64 KiB (un archivo minificado) se revisa por tramos. En un cambio cuenta las líneas de cada hunk, así que una línea agregada que empieza con `++` no se confunde con el encabezado de otro archivo.

Dónde corre:

- **`coyote commit`.** No deja pasar un secreto, ni con `--no-verify`.
- **Hook `commit-msg`.** Detiene cualquier `git commit` con secretos, también el de un agente.
- **Lint.** La regla R18 (MUST) revisa los archivos del proyecto: ninguno es un archivo de secretos ni lleva un secreto escrito.
- **`coyote gate pr`.** Un PR que agrega secretos queda en R3 y no pasa con ninguna aprobación: el secreto ya está en GitHub y hay que rotarlo.
- **Gate.** Cubre lo que escribe un agente. Un encabezado PEM sin cuerpo pide la aprobación normal: si un agente escribe una llave en dos ediciones, la persona ve el encabezado al aprobar la primera, y el escáner detiene la llave completa en el commit.

Cada ruta de un cambio lleva las reglas de los proyectos coyote que la contienen. Las del más cercano (su `coyote/project.yaml`) valen para lo suyo, con la ruta relativa a su carpeta. De cada proyecto de afuera se suman los archivos que declara en `secrets.files`, porque un proyecto anidado no tapa lo que el de afuera declara. La dispensa (`secrets.allow`) del más cercano vale para lo suyo, salvo para un archivo que el de afuera declara y no dispensa: ahí hacen falta las dos. La dispensa de un proyecto de afuera no alcanza a lo que declara o dispensa uno anidado. Fuera de un proyecto valen los nombres de siempre. Así el hook `commit-msg`, que git corre en la raíz del repo, aplica las reglas de un proyecto que vive en una subcarpeta, y un monorepo aplica las de cada proyecto y las de los que lo contienen. `gate pr` toma las reglas de la rama base.

El gate aplica lo mismo con el IDE abierto en la raíz del monorepo o en un proyecto anidado: juzga cada ruta del repo con las reglas de los proyectos que la contienen y una ruta fuera del repo, solo con los nombres de siempre. Lo mismo vale para los otros checkouts del repo: con el IDE en un worktree enlazado (Claude Code los pone en `.claude/worktrees/`), los archivos que declara el proyecto y que solo existen en el checkout principal o en un worktree hermano (los ignorados por git, justo los secretos) se protegen aunque se lean por su ruta absoluta o con `../..`. `coyote secrets list` y `coyote secrets scan` corridos desde un proyecto anidado aplican también las reglas de los de afuera, con las rutas relativas al proyecto donde se corren. Un agente tampoco escribe el gobierno de un proyecto anidado (su `coyote/project.yaml`, siempre; `infra.yaml`, `hub.yaml`, `approvals/` y `ledger/` donde hay un proyecto, es decir, un `project.yaml`), igual que el de la raíz: quien cambia ese `project.yaml` cambia lo que se bloquea en todo su subárbol. Una carpeta `pkg/coyote/ledger/` con código no es el ledger de nadie. Con el IDE en un proyecto anidado tampoco escribe el gobierno de los de afuera ni el de los otros checkouts. `coyote propose` y `coyote approve` juzgan un comando con las mismas reglas que el gate.

Los proyectos salen de lo versionado y de lo que hay en el árbol de trabajo. Un `coyote/project.yaml` que git sigue cuenta aunque falte en el disco (un `sparse-checkout`) o no se pueda leer (un enlace roto), y uno que está en el disco y todavía no se versiona protege lo que se prepara para el commit. Si un proyecto que git sigue no se puede leer, el escaneo, el commit y `gate pr` terminan con error nombrando ese archivo, aunque no encuentren nada: sin sus reglas no pueden dar el visto bueno, y no siguen como si ese proyecto no existiera. El gate bloquea entonces todo lo que hay bajo él y dice que la persona corrige el archivo. Un proyecto dentro de dependencias no dispensa nada, pero los archivos que declara en `secrets.files` siguen siendo archivos de secretos. Cuenta como tal el que tiene una carpeta `vendor`, `node_modules` o `third_party` en su ruta desde la raíz del repo, también la suya (`apps/vendor`); un `project.yaml` roto ahí no se reporta, porque no es de este repo. Para dispensar algo en uno de esos proyectos, la dispensa va en el proyecto de afuera, con la ruta desde su carpeta.

Un cuerpo de llave que se pega bajo un encabezado que ya estaba en el archivo también se detiene: en el commit se busca el encabezado en la versión nueva del archivo, y en el gate, en el archivo actual.

Un archivo en UTF-16 con BOM (lo que escribe PowerShell 5) se revisa como texto, también si `.gitattributes` le pone `diff` y git lo muestra con bytes 0, y también si trae secuencias ANSI u otros controles. Lo que lo vuelve un binario es que, decodificado, al menos cuatro unidades y más del 1 % sean pares rotos o NUL, que es lo que traen unos bytes cualesquiera que empiezan con FF FE; un binario así no se revisa como texto. La plantilla `templates/secret.yaml` de un chart de Helm no es un archivo de secretos: arma el secreto con valores que vienen de afuera.

## Límites

- El escáner reconoce formas conocidas. Un secreto de forma libre, como una contraseña corta en un `application.yml`, no se reconoce por patrón. La defensa principal es no dejar leer los archivos de secretos y declararlos en `secrets.files`.
- Para más patrones en el PR, el pipeline puede correr gitleaks (`features.gitleaks`, ADR-0021): fijado por hash, con la configuración de la rama base y su reporte en `gate pr` (docs/specs/ci-v1.md).
- Una búsqueda recursiva de una herramienta del IDE (Grep) depende de que respete `.gitignore`: los archivos de secretos deben estar ignorados, y R18 lo revisa.
- `docker compose up`, `npm run dev` o un programa que carga `.env` por dentro no nombran el archivo: piden la aprobación normal y la persona ve el comando. Lo mismo `find … -exec cat {} +` sobre una carpeta con secretos: arma sus palabras al correr.
- Los patrones de `secrets.files` distinguen mayúsculas, como los globs del resto de coyote; los nombres de siempre (`.env`, `*.pem`…) no. En un disco que no las distingue (macOS), leer un archivo declarado con otras mayúsculas podría esquivar el patrón. No se reprodujo, porque la prueba exige ese disco.
- `gate pr` toma `secrets.files` y `secrets.allow` del `coyote/project.yaml` de la rama base, el del proyecto que contiene cada ruta: un PR no se dispensa a sí mismo.
