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
COMMIT_FULL := $(shell git rev-parse HEAD 2>/dev/null || echo none)
DATE_RFC    := $(shell TZ=UTC git log -1 --format=%cd --date=format-local:%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)
TAG         := $(shell git describe --tags --exact-match 2>/dev/null)
DIST    ?= dist
PKG     := github.com/Emmanuel93/coyote/internal/version
LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)
PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64

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
dist:
	@mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; out=$(DIST)/coyote_$(VERSION)_$${os}_$${arch}; \
	  echo "$$out"; \
	  GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $$out/coyote ./cmd/coyote || exit 1; \
	done
	go run ./tools/distmeta -dist $(DIST) -version $(VERSION) -commit $(COMMIT_FULL) -tag "$(TAG)" -date $(DATE_RFC)
	cd $(DIST) && { command -v sha256sum >/dev/null && sha256sum */coyote */coyote.cdx.json *.provenance.json || shasum -a 256 */coyote */coyote.cdx.json *.provenance.json; } > SHA256SUMS

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
