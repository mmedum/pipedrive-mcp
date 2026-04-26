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
vet: ## go vet ./...
	$(GO) vet ./...

.PHONY: lint
lint: ## golangci-lint run
	golangci-lint run

.PHONY: test
test: ## go test -race -coverprofile cov.out ./...
	$(GO) test -race -coverprofile cov.out ./...

.PHONY: coverage
coverage: test ## Open coverage report in browser
	$(GO) tool cover -html=cov.out

.PHONY: install-tools
install-tools: ## Install go-installed tools (govulncheck, go-licenses) at pinned versions
	$(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	$(GO) install github.com/google/go-licenses@$(GO_LICENSES_VERSION)

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
staleness: ## scripts/staleness-check.sh
	bash scripts/staleness-check.sh

.PHONY: check
check: fmt vet lint test vuln licenses staleness ## Run every per-PR CI gate locally

.PHONY: docker
docker: ## Build the Docker image as pipedrive-mcp:dev
	docker build -t pipedrive-mcp:dev --build-arg VERSION=$(VERSION) .

.PHONY: smoke
smoke: build docker ## Run the binary and Docker stdio smoke tests
	bash scripts/stdio-smoke.sh binary $(BIN)
	bash scripts/stdio-smoke.sh docker pipedrive-mcp:dev

.PHONY: dump-schemas
dump-schemas: build ## Print the registered tool schemas as JSON
	$(BIN) --dump-schemas | jq .

.PHONY: clean
clean: ## Remove build artifacts
	rm -f $(BIN) cov.out

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'
