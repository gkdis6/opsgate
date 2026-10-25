GO ?= go
VERSION ?= dev
BIN := bin/opsgate

.PHONY: build test vet lint vuln clean
build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-X github.com/gkdis6/opsgate/internal/server.Version=$(VERSION)" -o $(BIN) ./cmd/opsgate

test:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; else echo "golangci-lint not installed; skipping lint"; fi

vuln:
	govulncheck ./...

clean:
	rm -rf bin .gocache

.PHONY: snapshot docker
snapshot:
	goreleaser release --snapshot --clean

docker:
	docker build --build-arg VERSION=$(VERSION) -t opsgate:$(VERSION) .
