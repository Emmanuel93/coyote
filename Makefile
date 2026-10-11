# Compila sin red: la única dependencia (go-yaml) está copiada en third_party
# y go.mod la reemplaza por esa ruta.
export GOFLAGS := -mod=mod
export GOPROXY := off
export GOSUMDB := off

VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo 0.1.0-dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
# La fecha es la del commit, no la del reloj: dos builds del mismo commit dan
# los mismos binarios (ADR-0027).
DATE    ?= $(shell TZ=UTC git log -1 --format=%cd --date=format-local:%Y-%m-%dT%H:%MZ 2>/dev/null || date -u +%Y-%m-%dT%H:%MZ)
DIST    ?= dist
LDFLAGS = -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)

.PHONY: build test vet lint check install dist verify-dist clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/coyote ./cmd/coyote

test:
	go test ./...

vet:
	go vet ./...
	@out=$$(gofmt -l cmd internal tools); if [ -n "$$out" ]; then echo "sin gofmt:"; echo "$$out"; exit 1; fi

# El repo cumple el estándar que distribuye.
lint: build
	./bin/coyote standards lint
	./bin/coyote generate agents --check
	./bin/coyote attribution check
	./bin/coyote eval
	./bin/coyote -C examples/acme-shop standards lint

check: vet test lint

install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/coyote

# Binarios, SBOM CycloneDX de cada uno y procedencia SLSA Build L1 (ADR-0027).
# Todo sale del commit: se compila en un clon limpio de HEAD, sin la
# configuración de git de quien compila (core.autocrlf cambiaría los bytes), así que ni
# los cambios sin commit, ni skip-worktree, ni un archivo ignorado entran al
# binario. La versión, el commit y la fecha se leen de git en la receta, y el
# entorno de Go queda fijo: ni el entorno ni la línea de comandos los cambian.
# La carpeta empieza vacía: SHA256SUMS y la procedencia cubren solo este build.
override DIST_GOENV := CGO_ENABLED=0 GOAMD64=v1 GOARM64=v8.0 GOEXPERIMENT= GOWORK=off GOTOOLCHAIN=local GOFIPS140=off GOENV=off GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off
override PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64
override PKG := github.com/Emmanuel93/coyote/internal/version
dist:
	@case "$(DIST)" in ""|/*|*..*|.|./|.git|.git/*|*[!A-Za-z0-9._/-]*) echo "DIST tiene que ser una carpeta relativa dentro del repo, con letras, números, . _ - y /"; exit 1;; esac
	@git check-ignore -q "$(DIST)/coyote" && test -z "$$(git ls-files -- "$(DIST)")" || { echo "DIST tiene que ser una carpeta que git ignora y sin archivos versionados (make dist la borra entera)"; exit 1; }
	@test -z "$$(git status --porcelain --untracked-files=normal)" || { echo "make dist necesita el árbol limpio: haz commit o guarda los cambios (un release sale de un commit)"; exit 1; }
	@rm -rf "./$(DIST)" && mkdir -p "./$(DIST)"
	@out=$$(cd "./$(DIST)" && pwd) && head=$$(git rev-parse HEAD) && wt=$$(mktemp -d "$${TMPDIR:-/tmp}/coyote-dist.XXXXXX") && \
	trap 'rm -rf "$$wt"' EXIT && \
	export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 && \
	git clone -q --shared --no-checkout . "$$wt/src" && cd "$$wt/src" && \
	git -c core.autocrlf=false -c core.eol=lf checkout -q --detach "$$head" && \
	ver=$$(git describe --tags --always) && commit=$$(git rev-parse --short HEAD) && full=$$(git rev-parse HEAD) && \
	date=$$(TZ=UTC git log -1 --format=%cd --date=format-local:%Y-%m-%dT%H:%MZ) && \
	rfc=$$(TZ=UTC git log -1 --format=%cd --date=format-local:%Y-%m-%dT%H:%M:%SZ) && \
	tag=$$(git describe --tags --exact-match 2>/dev/null || true) && \
	for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; dst="$$out/coyote_$${ver}_$${os}_$${arch}"; \
	  echo "$(DIST)/coyote_$${ver}_$${os}_$${arch}"; \
	  env $(DIST_GOENV) GOOS=$$os GOARCH=$$arch go build -trimpath \
	    -ldflags "-s -w -X $(PKG).Version=$$ver -X $(PKG).Commit=$$commit -X $(PKG).Date=$$date" -o "$$dst/coyote" ./cmd/coyote || exit 1; \
	done && \
	env $(DIST_GOENV) go run ./tools/distmeta -dist "$$out" -version $$ver -commit $$full -tag "$$tag" -date $$rfc -platforms "$(PLATFORMS)"
	cd "./$(DIST)" && { command -v sha256sum >/dev/null && sha256sum */coyote */coyote.cdx.json *.provenance.json || shasum -a 256 */coyote */coyote.cdx.json *.provenance.json; } > SHA256SUMS

# Construye el release dos veces, cada una con su caché de Go vacía, y compara:
# los hashes tienen que ser iguales.
verify-dist:
	rm -rf .dist-a .dist-b .gocache-a .gocache-b
	GOCACHE=$(CURDIR)/.gocache-a $(MAKE) --no-print-directory dist DIST=.dist-a >/dev/null
	GOCACHE=$(CURDIR)/.gocache-b $(MAKE) --no-print-directory dist DIST=.dist-b >/dev/null
	cmp .dist-a/SHA256SUMS .dist-b/SHA256SUMS && echo "reproducible: $$(wc -l < .dist-a/SHA256SUMS) archivos con los mismos hashes"
	rm -rf .dist-a .dist-b .gocache-a .gocache-b

clean:
	rm -rf bin dist .dist-a .dist-b .gocache-a .gocache-b
