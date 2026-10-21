# The straum core checkout, for the e2e test.
CORE ?= ../straum

.PHONY: build test lint check e2e golden web web-test web-size clean

build:
	CGO_ENABLED=0 go build -ldflags '-s -w' -o bin/straum-console .

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

clean:
	rm -rf bin web/node_modules web/test-results
