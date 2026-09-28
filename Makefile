# Compila sin red: la única dependencia (go-yaml) está copiada en third_party
# y go.mod la reemplaza por esa ruta.
export GOFLAGS := -mod=mod
export GOPROXY := off
export GOSUMDB := off

VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo 0.1.0-dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%MZ)
PKG     := github.com/Emmanuel93/coyote/internal/version
LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)
PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64

.PHONY: build test vet lint check install dist clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/coyote ./cmd/coyote

test:
	go test ./...

vet:
	go vet ./...

# El repo cumple el estándar que distribuye.
lint: build
	./bin/coyote standards lint
	./bin/coyote generate agents --check
	./bin/coyote attribution check
	./bin/coyote -C examples/acme-shop standards lint

check: vet test lint

install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/coyote

dist:
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; out=dist/coyote_$(VERSION)_$${os}_$${arch}; \
	  echo "$$out"; \
	  GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $$out/coyote ./cmd/coyote || exit 1; \
	done
	cd dist && { command -v sha256sum >/dev/null && sha256sum */coyote || shasum -a 256 */coyote; } > SHA256SUMS

clean:
	rm -rf bin dist
