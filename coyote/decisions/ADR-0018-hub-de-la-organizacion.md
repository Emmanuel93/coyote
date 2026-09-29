---
status: proposed
date: 2026-09-29
deciders: "@eramirezhdez"
---
# ADR-0018: Hub de la organización leído en un commit

## Contexto y problema
El estándar se arma por capas (`coyote:default`, hub y proyecto), pero la capa del hub solo acepta una ruta local y se lee del árbol de trabajo: lo que no tiene commit en esa carpeta se aplica a todos los proyectos que la usan. Tampoco hay dónde declarar lo que es de la organización y no de un proyecto: quién administra (D10), el tope de gasto total y qué proyectos existen. D12 fija el nombre del hub; falta su forma.

## Opciones consideradas
- Ruta local leída del árbol de trabajo, como hoy.
- Un repo coyote de tipo `hub`, leído en un commit con git de plomería.
- Un hub como servicio con API propia: choca con D2 (sin servidores).

## Decisión
- **Repo.** El hub es un proyecto coyote de tipo `hub`. `coyote hub init` lo crea con `coyote/hub.yaml`, la capa del estándar (`coyote/standards/rules.yaml`) y la carpeta de dominios (R4).
- **Referencia.** `hub:` en `project.yaml` acepta `{path, ref}`. `path` es el clon local (relativo al proyecto o con `~`); `ref` es una rama, etiqueta o commit, `main` si falta. Una cadena sigue valiendo como `path`.
- **Lectura.** Todo lo del hub se lee del commit de `ref` con git de plomería: sin filtros, sin fsmonitor y sin tocar el árbol de trabajo del hub. Un `extends` relativo dentro del hub se resuelve en el mismo commit y no sale del repo.
- **`hub.yaml` v1.** Declara:
  - la organización;
  - los admins, que ven los costos de todas las personas (D10);
  - el tope mensual de la organización;
  - sus proyectos, con la ruta de su clon, para la vista de admins y `hub status`.
- **Visibilidad.** `standards status`, `doctor` y `hub status` dicen qué commit del hub rige. Un hub que no se puede leer es un error, no un regreso silencioso a `coyote:default`.

## Consecuencias
- Un cambio al estándar de la organización rige cuando tiene commit en `ref`; con el hub protegido, eso pasa por PR y por sus dueños.
- Cada persona necesita un clon del hub. `hub status` avisa cuando el clon no está o cuando `ref` no existe en él.
- Las rutas de los proyectos en `hub.yaml` son de la máquina de cada quien. Un proyecto sin clon se muestra como tal, sin fallar.
