# The straum core checkout, for the e2e test.
CORE ?= ../straum

.PHONY: build test lint check e2e golden clean

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

# Rewrite testdata/*.golden.* after an intended change to the state JSON.
golden:
	go test . -run TestGolden -update

clean:
	rm -rf bin
