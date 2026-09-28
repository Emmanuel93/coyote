# Copia local de go-yaml

- Origen: https://github.com/yaml/go-yaml, etiqueta v3.0.5, commit e16c7af9361b241fa02d91582fb59ce4954d8afc (2026-07-26).
- Módulo: go.yaml.in/yaml/v3 (fork mantenido de gopkg.in/yaml.v3), licencias MIT y Apache-2.0 (ver LICENSE y NOTICE).
- Cambios: se quitaron los archivos *_test.go y el go.mod quedó sin requisitos; el código de la librería no se modificó.
- Motivo: compilar Coyote sin red ni proxy de módulos (ADR-0004). Para volver al módulo remoto basta con quitar el replace de go.mod.
