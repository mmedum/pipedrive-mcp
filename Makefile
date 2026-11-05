# Common dev tasks. `make check` runs every gate .github/workflows/ci.yml
# runs but the pull-request ones; the parity gate holds the two lists equal.

GO       ?= go
BIN      ?= ./pipedrive-mcp
VERSION  ?= dev
PKG       = github.com/mmedum/pipedrive-mcp
LDFLAGS   = -s -w -X $(PKG)/internal/version.Version=$(VERSION)
# Each tool runs at an exact version, built by the local toolchain: an
# installed binary can be another version, and a go-licenses built by
# another Go installation fails on the standard library. CI runs
# govulncheck and go-licenses through make, at the versions here. It runs
# golangci-lint and gitleaks through actions, so the pins gate holds those
# two equal to ci.yml's, and gitleaks's also to the rev in
# .pre-commit-config.yaml.
GOVULNCHECK_VERSION ?= v1.8.0
GO_LICENSES_VERSION ?= v1.6.0
GOLANGCI_LINT_VERSION ?= v2.14.0
GITLEAKS_VERSION ?= v8.30.1

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
vet: ## go vet ./..., and again under each build tag so the tagged files keep compiling
	$(GO) vet ./...
	$(GO) vet -tags=integration ./...
	$(GO) vet -tags=live ./...

.PHONY: lint
lint: ## golangci-lint, at the version CI pins
	$(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run

.PHONY: test
test: ## go test -race -coverprofile cov.out ./...
	$(GO) test -race -coverprofile cov.out ./...

.PHONY: cover
cover: test ## The statement-coverage floor on the packages it holds
	@$(GO) run ./scripts/gates coverage cov.out

.PHONY: coverage
coverage: test ## Open coverage report in browser
	$(GO) tool cover -html=cov.out

.PHONY: integration
integration: ## Drive the live workspace: reads, resources, guards, dry runs
	$(GO) test -tags=integration -race -count=1 ./internal/integration/

.PHONY: integration-writes
integration-writes: ## The above plus the reversible write probes — this MUTATES the workspace
	PIPEDRIVE_INTEGRATION_WRITES=1 $(MAKE) integration

.PHONY: vuln
vuln: ## govulncheck, which CI runs through this target
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

.PHONY: licenses
licenses: ## go-licenses against the allow-list, which CI runs through this target
	$(GO) run github.com/google/go-licenses@$(GO_LICENSES_VERSION) check ./... \
	  --allowed_licenses=Apache-2.0,MIT,BSD-2-Clause,BSD-3-Clause,ISC,MPL-2.0

.PHONY: staleness
staleness: ## gates deps
	go run ./scripts/gates deps

.PHONY: check
check: fmt vet lint test cover vuln licenses gitleaks staleness docs leaks pins mcpb changelog-links descriptions smoke parity ## Run every per-PR CI gate locally

# The branch's commits not on the default branch, as CI scans a pull
# request's, or every commit on HEAD in a clone without origin/main.
GITLEAKS_BASE  ?= origin/main
GITLEAKS_RANGE  = $(if $(shell git rev-parse --verify --quiet $(GITLEAKS_BASE)),$(GITLEAKS_BASE)..HEAD,HEAD)

.PHONY: gitleaks
gitleaks: ## No secret in the branch's commits or the working tree, at the gitleaks version CI runs
	$(GO) run github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION) git --log-opts="$(GITLEAKS_RANGE)" --redact --no-banner .
	$(GO) run github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION) dir . --redact --no-banner

# Not in check: it reads every commit, where the target above reads the
# branch's own.
.PHONY: gitleaks-history
gitleaks-history: ## No secret anywhere in the history (manual)
	$(GO) run github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION) git --redact --no-banner .

.PHONY: docs
docs: ## Decision IDs, citations, links, paths, the codemap and cited integration tests resolve
	@$(GO) run ./scripts/gates docs

.PHONY: leaks
leaks: ## Nothing from a real Pipedrive account is in the tree
	go run ./scripts/gates leaks

# Not in check: it reads every object the repository has published.
.PHONY: leaks-history
leaks-history: ## Nothing from a real Pipedrive account is anywhere in the history (manual)
	go run ./scripts/gates leaks history

.PHONY: pins
pins: ## Every action is a commit and every tool version is exact
	go run ./scripts/gates pins

.PHONY: changelog-links
changelog-links: ## Every CHANGELOG version heading has its link definition
	@$(GO) run ./scripts/gates changelog-links

.PHONY: mcpb
mcpb: ## The bundle manifest describes the bundle the packer stages, and the release its six archives
	@$(GO) run ./scripts/gates mcpb

# The other half of the bundle: the gate above needs only the staged
# NAMES, which are static, so it runs on every commit. This needs the
# binaries, so it runs at release time from the universal binary's post
# hook in .goreleaser.yaml. Deliberately not in `check`.
.PHONY: mcpb-pack
mcpb-pack: ## Pack the .mcpb from a built dist tree (release; manual)
	@$(GO) run ./scripts/gates mcpb-pack $(DIST) $(VERSION) $(MCPB_OUT)

.PHONY: smoke
smoke: build ## Drive the binary over stdio and read the reply
	go run ./scripts/gates smoke binary $(BIN)

.PHONY: parity
parity: ## `make check` and CI run the same gates
	@$(GO) run ./scripts/gates parity

.PHONY: evals
evals: build ## Drive a model through the tools and score it (needs credentials and the claude CLI)
	$(GO) run -tags=live ./scripts/evals -bin $(BIN)

.PHONY: descriptions
descriptions: build ## Tool descriptions against the mechanical house-style rules
	$(BIN) --dump-schemas | go run ./scripts/gates descriptions -

.PHONY: dump-schemas
dump-schemas: build ## Print the tool surface a client is served, as JSON
	$(BIN) --dump-schemas | jq .

# Not in check: it measures a pull request from the merge-base of its
# head and the base branch, which CI's schema-ack job passes.
PR_BASE_REF ?= origin/main
PR_HEAD     ?= HEAD

.PHONY: schema-ack
schema-ack: ## A commit footer names each item of the tool surface a pull request changes (pull requests)
	go run ./scripts/gates schema-ack $(PR_BASE_REF) $(PR_HEAD)

.PHONY: clean
clean: ## Remove build artifacts
	rm -f $(BIN) cov.out

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'
