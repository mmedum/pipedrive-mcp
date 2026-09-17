# Common dev tasks. CI runs the same gates from .github/workflows/ci.yml;
# this Makefile is for the same checks locally.

GO       ?= go
GOFLAGS  ?=
BIN      ?= ./pipedrive-mcp
VERSION  ?= dev
PKG       = github.com/mmedum/pipedrive-mcp
LDFLAGS   = -s -w -X $(PKG)/internal/version.Version=$(VERSION)
GOBIN    := $(shell $(GO) env GOPATH)/bin
GOVULNCHECK_VERSION ?= v1.1.4
GO_LICENSES_VERSION ?= v1.6.0
GOLANGCI_LINT_VERSION ?= v2.13.2

.PHONY: all
all: check

.PHONY: build
build: ## Build the binary into ./pipedrive-mcp
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) ./cmd/pipedrive-mcp

.PHONY: install
install: ## go install the binary
	CGO_ENABLED=0 $(GO) install -trimpath -ldflags="$(LDFLAGS)" ./cmd/pipedrive-mcp

.PHONY: fmt
fmt: ## Verify gofmt cleanliness (no stdout = pass)
	@out=$$($(GO) fmt ./...); if [ -n "$$out" ]; then echo "gofmt rewrote files:"; echo "$$out"; exit 1; fi
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt issues:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## go vet ./... (twice: the integration suite is behind a build tag)
	$(GO) vet ./...
	$(GO) vet -tags=integration ./...

.PHONY: lint
lint: ## golangci-lint run
	golangci-lint run

.PHONY: test
test: ## go test -race -coverprofile cov.out ./...
	$(GO) test -race -coverprofile cov.out ./...

.PHONY: coverage
coverage: test ## Open coverage report in browser
	$(GO) tool cover -html=cov.out

.PHONY: integration
integration: ## Drive the live workspace: reads, resources, guards, dry runs
	$(GO) test -tags=integration -race -count=1 ./internal/integration/

.PHONY: integration-writes
integration-writes: ## The above plus the reversible write probes — this MUTATES the workspace
	PIPEDRIVE_INTEGRATION_WRITES=1 $(MAKE) integration

.PHONY: install-tools
install-tools: ## Install go-installed tools (govulncheck, go-licenses, golangci-lint) at pinned versions
	$(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	$(GO) install github.com/google/go-licenses@$(GO_LICENSES_VERSION)
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: verify-tool-versions
verify-tool-versions: ## Assert locally-installed tool versions match the CI pins
	@want="$(GOLANGCI_LINT_VERSION)"; \
	got=$$(golangci-lint --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1); \
	if [ "v$$got" != "$$want" ]; then \
		echo "::error::golangci-lint version mismatch: have v$$got, pin is $$want"; \
		echo "Run: make install-tools"; \
		exit 1; \
	fi
	@echo "tool versions match CI pins"

.PHONY: vuln
vuln: ## govulncheck ./...
	@command -v govulncheck >/dev/null 2>&1 || [ -x "$(GOBIN)/govulncheck" ] || $(MAKE) install-tools
	@PATH="$(GOBIN):$$PATH" govulncheck ./...

.PHONY: licenses
licenses: ## go-licenses against the allow-list
	@command -v go-licenses >/dev/null 2>&1 || [ -x "$(GOBIN)/go-licenses" ] || $(MAKE) install-tools
	@PATH="$(GOBIN):$$PATH" go-licenses check ./... \
	  --allowed_licenses=Apache-2.0,MIT,BSD-2-Clause,BSD-3-Clause,ISC,MPL-2.0

.PHONY: staleness
staleness: ## gates deps
	go run ./scripts/gates deps

.PHONY: check
check: verify-tool-versions fmt vet lint test vuln licenses staleness leaks pins smoke ## Run every per-PR CI gate locally

.PHONY: leaks
leaks: ## Nothing from a real Pipedrive account is in the tree
	go run ./scripts/gates leaks

.PHONY: pins
pins: ## Every action is a commit and every tool version is exact
	go run ./scripts/gates pins

.PHONY: smoke
smoke: build ## Drive the binary over stdio and read the reply
	go run ./scripts/gates smoke binary $(BIN)

.PHONY: dump-schemas
dump-schemas: build ## Print the registered tool schemas as JSON
	$(BIN) --dump-schemas | jq .

.PHONY: clean
clean: ## Remove build artifacts
	rm -f $(BIN) cov.out

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'
