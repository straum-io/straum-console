# The straum core checkout, for the e2e test.
CORE ?= ../straum

.PHONY: build test lint check e2e soak bench golden web web-test web-size release-snapshot image install-test clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

build:
	CGO_ENABLED=0 go build -ldflags '-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)' -o bin/straum-console .

test:
	go test -race ./...

lint:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo 'gofmt: files above need formatting' >&2; exit 1; }
	go vet ./...
	go vet -tags e2e ./...

# Everything that needs no core checkout.
check: lint test build

# A real three-node cluster on loopback, built from $(CORE).
e2e:
	cd $(CORE) && cargo build -p server --bin straum-server
	STRAUM_SERVER_BIN=$(abspath $(CORE))/target/debug/straum-server go test -tags e2e -race -count=1 -run E2E ./...

# Plan §14 under load: 5 fixture nodes, 64 streams, SOAK_DURATION (1h).
soak: build
	SOAK_OUT=$${SOAK_OUT:-soak.csv} go test -tags soak -run Soak -count=1 -timeout 3h -v .

bench:
	go test -run '^$$' -bench . -benchmem -count=10 .

# The frontend (needs Node): install from the lockfile, type-check, build
# web/dist, which the binary embeds and git keeps.
web:
	cd web && npm ci && npm run build

# Playwright against the console binary and fixture nodes.
web-test: build
	cd web && npx playwright install chromium && npm test

# JS + CSS at most 60 KB gzip (plan §14).
web-size:
	@n=$$(cat web/dist/app.js web/dist/app.css | gzip -9 | wc -c); echo "web/dist JS + CSS: $$n bytes gzip"; test $$n -le 61440

# Rewrite testdata/*.golden.* after an intended change to the state JSON.
golden:
	go test . -run TestGolden -update

GORELEASER := go run github.com/goreleaser/goreleaser/v2@v2.18.3

# Check .goreleaser.yaml and build every release archive locally, without
# publishing anything.
release-snapshot:
	$(GORELEASER) check
	$(GORELEASER) release --snapshot --clean

# A local image from source, for this machine's CPU (Dockerfile; CON-D-6 A).
CONTAINER ?= docker
ARCH ?= $(shell go env GOARCH)
image:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -trimpath -ldflags '-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)' -o image/linux-$(ARCH)/straum-console .
	$(CONTAINER) build --platform linux/$(ARCH) --build-arg TARGETARCH=$(ARCH) -t straum-console:dev .

# install.sh and uninstall.sh against the archives of `make release-snapshot`.
install-test:
	scripts/test-install.sh

clean:
	rm -rf bin dist image web/node_modules web/test-results
