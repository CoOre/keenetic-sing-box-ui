PKG          := github.com/CoOre/keenetic-sing-box-ui
CMD          := ./cmd/keenetic-sing-box-ui
BIN_NAME     := keenetic-sing-box-ui
DIST_DIR     := dist
VERSION      ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT       ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE         ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS      := -s -w \
                -X main.version=$(VERSION) \
                -X main.commit=$(COMMIT) \
                -X main.date=$(DATE)
GOFLAGS      := -trimpath -ldflags '$(LDFLAGS)'
NPM_CACHE    ?= /tmp/ksbui-npmcache

.PHONY: all build build-arm64 build-web web-install run test lint tidy clean package install-router

all: build

# Frontend: install deps (isolated cache to dodge root-owned ~/.npm) and build
# into web/dist, which the Go binary embeds.
web-install:
	cd web && npm install --legacy-peer-deps --cache $(NPM_CACHE)

build-web:
	cd web && [ -d node_modules ] || npm install --legacy-peer-deps --cache $(NPM_CACHE)
	cd web && npm run build

build: build-web
	go build $(GOFLAGS) -o $(DIST_DIR)/$(BIN_NAME) $(CMD)

build-arm64: build-web
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
		go build $(GOFLAGS) -o $(DIST_DIR)/$(BIN_NAME)-linux-arm64 $(CMD)

# Go-only build, assuming web/dist is already built/committed.
build-go:
	go build $(GOFLAGS) -o $(DIST_DIR)/$(BIN_NAME) $(CMD)

run: build
	$(DIST_DIR)/$(BIN_NAME) --listen 127.0.0.1:9091

test:
	go test ./...

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

clean:
	rm -rf $(DIST_DIR)

package: build-arm64
	mkdir -p $(DIST_DIR)/pkg/opt/bin
	mkdir -p $(DIST_DIR)/pkg/opt/etc/init.d
	cp $(DIST_DIR)/$(BIN_NAME)-linux-arm64 $(DIST_DIR)/pkg/opt/bin/$(BIN_NAME)
	cp packaging/entware/S99keenetic-sing-box-ui $(DIST_DIR)/pkg/opt/etc/init.d/S99keenetic-sing-box-ui
	chmod +x $(DIST_DIR)/pkg/opt/bin/$(BIN_NAME) $(DIST_DIR)/pkg/opt/etc/init.d/S99keenetic-sing-box-ui
	tar -czf $(DIST_DIR)/$(BIN_NAME)_$(VERSION)_aarch64.tar.gz -C $(DIST_DIR)/pkg .
	cd $(DIST_DIR) && shasum -a 256 $(BIN_NAME)_$(VERSION)_aarch64.tar.gz > sha256sums.txt

install-router:
	scripts/install-router.sh
