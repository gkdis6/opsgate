GO ?= go
VERSION ?= dev
LINT_OPTIONAL ?= 0
BIN := bin/opsgate

.PHONY: build test vet lint vuln clean licenses fmt fmt-check tidy-check check help
build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-X github.com/gkdis6/opsgate/internal/buildinfo.version=$(VERSION)" -o $(BIN) ./cmd/opsgate

fmt:
	gofmt -w .

fmt-check:
	@files=$$(gofmt -l .) || exit $$?; if [ -n "$$files" ]; then printf 'Run gofmt on:\n%s\n' "$$files"; exit 1; fi

tidy-check:
	$(GO) mod tidy -diff

check: fmt-check tidy-check vet lint test

test:
	sh scripts/check-pins.sh
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

lint:
	sh scripts/check-pins.sh
	sh scripts/check-pins_test.sh
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	elif [ "$(LINT_OPTIONAL)" = "1" ]; then \
		echo "golangci-lint not installed; skipping lint (LINT_OPTIONAL=1)"; \
	else \
		echo "golangci-lint not installed; install v2.12.0 or set LINT_OPTIONAL=1" >&2; \
		exit 1; \
	fi

licenses:
	GO=$(GO) sh scripts/third-party-licenses.sh

vuln:
	govulncheck ./...

clean:
	rm -rf bin .gocache

.PHONY: snapshot docker
snapshot:
	goreleaser release --snapshot --clean

docker:
	docker build --build-arg VERSION=$(VERSION) -t opsgate:$(VERSION) .

help:
	@printf '%s\n' 'build       Build bin/opsgate (VERSION=dev by default)' \
		'test        Check pins and run tests with the race detector' \
		'vet         Run go vet' \
		'lint        Check pins and run golangci-lint (LINT_OPTIONAL=1 permits a missing linter)' \
		'fmt         Format Go files' \
		'fmt-check   Check Go formatting' \
		'tidy-check  Check module tidiness without modifying files' \
		'check       Run fmt-check, tidy-check, vet, lint and test' \
		'vuln        Run govulncheck' \
		'licenses    Collect third-party licenses' \
		'snapshot    Build a GoReleaser snapshot' \
		'docker      Build the container image' \
		'clean       Remove build output and local Go cache' \
		'help        List targets'
