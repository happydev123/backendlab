GO        ?= go
BIN       ?= bin
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
LDFLAGS   := -s -w -X github.com/happydev123/backendlab/internal/version.Version=$(VERSION)
PKGS      := ./...

.PHONY: all build fmt fmt-check vet test race integration check clean

all: check build

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/ ./cmd/backendlab ./cmd/mockservice

fmt:
	gofmt -w cmd internal migrations

fmt-check:
	@out="$$(gofmt -l cmd internal migrations)"; \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	$(GO) vet $(PKGS)

test:
	$(GO) test -count=1 $(PKGS)

race:
	CGO_ENABLED=1 $(GO) test -race -count=1 $(PKGS)

# Requires BACKENDLAB_TEST_DATABASE_URL pointing at a disposable database.
integration:
	@test -n "$$BACKENDLAB_TEST_DATABASE_URL" || (echo "BACKENDLAB_TEST_DATABASE_URL is not set"; exit 1)
	$(GO) test -count=1 ./internal/store/... ./internal/jobs/... ./internal/auth/...

check: fmt-check vet test

clean:
	rm -rf $(BIN)
