GO        ?= go
BIN       ?= bin
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
LDFLAGS   := -s -w -X github.com/happydev123/backendlab/internal/version.Version=$(VERSION)
PKGS      := ./...
FUZZTIME  ?= 10s

.PHONY: all build fmt fmt-check vet test race integration fuzz-smoke demo check clean

all: check build

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/ ./cmd/backendlab ./cmd/mockservice

fmt:
	gofmt -w cmd internal migrations web demo

fmt-check:
	@out="$$(gofmt -l cmd internal migrations web demo)"; \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	$(GO) vet $(PKGS)
	$(GO) vet -tags demo ./demo/...

test:
	$(GO) test -count=1 $(PKGS)

race:
	CGO_ENABLED=1 $(GO) test -race -count=1 $(PKGS)

# Requires BACKENDLAB_TEST_DATABASE_URL pointing at a disposable database.
integration:
	@test -n "$$BACKENDLAB_TEST_DATABASE_URL" || (echo "BACKENDLAB_TEST_DATABASE_URL is not set"; exit 1)
	$(GO) test -count=1 ./internal/store/... ./internal/jobs/... ./internal/auth/... ./internal/httpapi/... ./internal/server/...

# Runs every fuzz target briefly. Seeds live in the test files.
fuzz-smoke:
	$(GO) test ./internal/jsonptr -run '^$$' -fuzz FuzzParse -fuzztime $(FUZZTIME)
	$(GO) test ./internal/suite -run '^$$' -fuzz FuzzParseTemplate -fuzztime $(FUZZTIME)
	$(GO) test ./internal/compare -run '^$$' -fuzz FuzzJSONCompare -fuzztime $(FUZZTIME)
	$(GO) test ./internal/stream -run '^$$' -fuzz FuzzParser -fuzztime $(FUZZTIME)
	$(GO) test ./internal/validate -run '^$$' -fuzz FuzzSumRule -fuzztime $(FUZZTIME)
	$(GO) test ./internal/validate -run '^$$' -fuzz FuzzDecimalParsing -fuzztime $(FUZZTIME)
	$(GO) test ./internal/plans -run '^$$' -fuzz FuzzParse -fuzztime $(FUZZTIME)

# End-to-end demonstration; requires BACKENDLAB_TEST_DATABASE_URL.
demo:
	$(GO) test -tags demo -count=1 -v ./demo/

check: fmt-check vet test

clean:
	rm -rf $(BIN)
