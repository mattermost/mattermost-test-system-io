# Test System IO — Development Makefile (Go backend + React web)
# =================================================================
# `make help` for a categorized list. Every user-facing target has a `##` comment.
# Internal helpers (like `ensure-docker`) have none so they don't clutter help.

.PHONY: help \
        install install-server install-web tools \
        dev dev-server dev-web dev-web-watch \
        build build-server build-web \
        test test-server test-server-e2e test-web \
        lint lint-server lint-web \
        vet vet-server \
        fmt fmt-server fmt-check-server fmt-web fmt-check-web \
        typecheck typecheck-web \
        ci ensure-docker \
        db-migrate db-status db-reset seed \
        docker-up docker-down docker-logs docker-build \
        clean clean-server clean-web clean-all \
        outdated outdated-server outdated-web \
        update update-server update-web \
        audit audit-server \
        kill-ports kill-server-port kill-web-port kill-port

.DEFAULT_GOAL := help

# ----- Colors -----
CYAN   := \033[36m
BOLD   := \033[1m
GREEN  := \033[32m
YELLOW := \033[33m
RED    := \033[31m
RESET  := \033[0m

# ----- Paths and ports -----
ROOT_DIR    := $(shell pwd)
SERVER_DIR  := $(ROOT_DIR)/apps/server
WEB_DIR     := $(ROOT_DIR)/apps/web
SERVER_PORT := 8080
WEB_PORT    := 3000
# macOS default open-file limit (256) breaks testcontainers; bump for test targets.
ULIMIT_N    := 4096

# ----- Go tooling -----
GO    ?= go
GOFMT ?= gofmt

# Tool versions pinned; run via `go run` so no GOBIN setup is required.
# Keep GOLANGCI_LINT_VERSION in sync with .github/workflows/ci.yml's
# `golangci-lint` step (version input) so CI and local lint against the same ruleset.
GOLANGCI_LINT_VERSION := v2.11.4
GOIMPORTS_PKG         := golang.org/x/tools/cmd/goimports@latest
GOVULNCHECK_PKG       := golang.org/x/vuln/cmd/govulncheck@latest
GOLANGCI_LINT_CMD     := $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GOIMPORTS_CMD         := $(GO) run $(GOIMPORTS_PKG)
GOVULNCHECK_CMD       := $(GO) run $(GOVULNCHECK_PKG)

# ----- Build identity injected via -ldflags (dev vs staging vs prod conventions) -----
#   dev      → <base>-<short-sha>.dev[+dirty]   (set here; used by `make dev-server` / `make build-server`)
#   staging  → <base>-<short-sha>.beta          (set in .github/workflows/deploy_staging.yml)
#   prod     → <base>                           (set in .github/workflows/deploy_production.yml)
VERSION_BASE := $(shell tr -d '[:space:]' < $(SERVER_DIR)/VERSION 2>/dev/null || echo 0.0.0)
SHORT_SHA    := $(shell git -C $(ROOT_DIR) rev-parse --short HEAD 2>/dev/null || echo unknown)
ifneq ($(shell git -C $(ROOT_DIR) status --porcelain 2>/dev/null),)
  DIRTY_META := +dirty
else
  DIRTY_META :=
endif
VERSION      := $(VERSION_BASE)-$(SHORT_SHA).dev$(DIRTY_META)
COMMIT_SHA   := $(SHORT_SHA)$(if $(DIRTY_META),-dirty,)
BUILD_TIME   := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS         := -X main.version=$(VERSION) -X main.commitSHA=$(COMMIT_SHA) -X main.buildTime=$(BUILD_TIME)
RELEASE_LDFLAGS := -s -w $(LDFLAGS)

# ----- Docker -----
COMPOSE := docker compose -f docker/docker-compose.dev.yml
# Auto-detect docker socket from the active context — works for Docker Desktop,
# OrbStack, Colima, etc. E2E tests use this so testcontainers-go finds the
# same daemon the CLI sees.
DOCKER_HOST_AUTO := $(shell docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null)
E2E_ENV := DOCKER_HOST='$(DOCKER_HOST_AUTO)' TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE='$(patsubst unix://%,%,$(DOCKER_HOST_AUTO))'

# ============================================================================

##@ Help

help: ## Show this categorized help
	@awk 'BEGIN { \
		FS = ":.*##"; \
		printf "\n$(BOLD)Test System IO$(RESET) — make targets\n"; \
	} \
	/^##@ / { \
		printf "\n$(BOLD)$(CYAN)%s$(RESET)\n", substr($$0, 5); \
	} \
	/^[a-zA-Z0-9][a-zA-Z0-9_-]*:.*##/ { \
		printf "  $(CYAN)%-22s$(RESET) %s\n", $$1, $$2; \
	}' $(MAKEFILE_LIST)
	@echo ""

##@ Installation

install: install-server install-web ## Install server + web deps

install-server: ## Fetch Go module deps
	@echo "$(CYAN)Fetching Go dependencies...$(RESET)"
	cd $(SERVER_DIR) && $(GO) mod download

install-web: ## Install npm deps (apps/web)
	@echo "$(CYAN)Installing Node.js dependencies...$(RESET)"
	cd $(WEB_DIR) && npm ci

tools: ## Install pinned Go CLI tools to GOBIN (optional; other targets use `go run`)
	@echo "$(CYAN)Installing Go developer tools (optional)...$(RESET)"
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	$(GO) install $(GOIMPORTS_PKG)
	@echo "$(GREEN)Installed into $$(go env GOBIN).$(RESET)"

##@ Development

dev: ## Run server + web concurrently with auto-reload
	@echo "$(CYAN)Starting Go server + Vite web concurrently...$(RESET)"
	@$(MAKE) dev-server & $(MAKE) dev-web & wait

dev-server: ## Run Go server (ldflags inject version/sha/build time)
	@echo "$(CYAN)Starting Go server on :$(SERVER_PORT) ($(VERSION)@$(COMMIT_SHA))...$(RESET)"
	@ulimit -n $(ULIMIT_N); cd $(SERVER_DIR) && $(GO) run -ldflags '$(LDFLAGS)' ./cmd/tsio

dev-web: ## Run Vite dev server with HMR
	@echo "$(CYAN)Starting Vite dev server on :$(WEB_PORT)...$(RESET)"
	cd $(WEB_DIR) && npm run dev

dev-web-watch: ## Run Vite in watch-build mode; refreshes the embedded bundle on every save (use when iterating on the web with :8080)
	cd $(WEB_DIR) && npm run build -- --watch

##@ Build

build: build-server build-web ## Build server binaries + web production bundle

build-server: ## Build tsio + tsioctl binaries (stripped, trimpath)
	@echo "$(CYAN)Building Go binaries ($(VERSION))...$(RESET)"
	cd $(SERVER_DIR) && \
		$(GO) build -trimpath -buildvcs=true -ldflags '$(RELEASE_LDFLAGS)' -o ./bin/tsio   ./cmd/tsio && \
		$(GO) build -trimpath -buildvcs=true -ldflags '$(RELEASE_LDFLAGS)' -o ./bin/tsioctl ./cmd/tsioctl

build-web: ## Build web production bundle
	@echo "$(CYAN)Building web bundle...$(RESET)"
	cd $(WEB_DIR) && npm run build

##@ Test

test: test-server test-web ## Run unit tests for server + web

test-server: ## Run Go unit tests (race)
	@echo "$(CYAN)Running Go tests with -race...$(RESET)"
	@ulimit -n $(ULIMIT_N); cd $(SERVER_DIR) && $(GO) test -race -count=1 ./...

test-server-e2e: ensure-docker ## Run every -tags=e2e package (admin_cli, oidc, contract); Docker required
	@echo "$(CYAN)Running all -tags=e2e tests (DOCKER_HOST=$(DOCKER_HOST_AUTO))...$(RESET)"
	@ulimit -n $(ULIMIT_N); cd $(SERVER_DIR) && $(E2E_ENV) $(GO) test -race -tags=e2e -count=1 ./tests/...

test-web: ## Run web tests (vitest)
	cd $(WEB_DIR) && npm run test

##@ Lint, Vet, Format

lint: lint-server lint-web ## Run all linters

lint-server: ## golangci-lint on the Go module
	@echo "$(CYAN)Linting Go (golangci-lint $(GOLANGCI_LINT_VERSION))...$(RESET)"
	cd $(SERVER_DIR) && $(GOLANGCI_LINT_CMD) run ./...

lint-web: ## Lint the web client
	cd $(WEB_DIR) && npm run lint

vet: vet-server ## Run `go vet` (fast baseline static analyzers)

vet-server: ## go vet ./... on the Go module
	@echo "$(CYAN)Running go vet...$(RESET)"
	cd $(SERVER_DIR) && $(GO) vet ./...

fmt: fmt-server fmt-web ## Format server (gofmt+goimports) and web (prettier/oxfmt)

fmt-server: ## Format Go code (writes)
	@echo "$(CYAN)Formatting Go code...$(RESET)"
	cd $(SERVER_DIR) && $(GOFMT) -s -w . && $(GOIMPORTS_CMD) -w .

fmt-check-server: ## Verify Go is gofmt-clean (CI-friendly; exit 1 on drift)
	@cd $(SERVER_DIR) && out=$$($(GOFMT) -s -l .); \
	if [ -n "$$out" ]; then \
		echo "$(RED)gofmt -s finds formatting issues:$(RESET)"; \
		echo "$$out"; \
		exit 1; \
	fi

fmt-web: ## Format web client (writes)
	cd $(WEB_DIR) && npm run format

fmt-check-web: ## Verify web client is oxfmt-clean
	cd $(WEB_DIR) && npm run format:check

typecheck: typecheck-web ## Type-check (web only; Go type-checks in `vet`/`build`)

typecheck-web: ## Type-check TypeScript (tsc --noEmit)
	cd $(WEB_DIR) && npm run typecheck

##@ CI

ci: vet lint typecheck test build ## Full CI gate (vet, lint, typecheck, test, build). Excludes e2e (needs Docker).

# Internal: precheck that Docker is reachable before any testcontainers target.
ensure-docker:
	@if ! docker info >/dev/null 2>&1; then \
		echo ""; \
		echo "$(RED)Docker daemon is not running.$(RESET)"; \
		echo "$(YELLOW)E2E tests use testcontainers-go to spin up Postgres.$(RESET)"; \
		echo "$(YELLOW)Start Docker / OrbStack / Colima and retry.$(RESET)"; \
		echo ""; \
		exit 1; \
	fi

##@ Database

db-migrate: ## Apply pending migrations forward-only (idempotent)
	@echo "$(CYAN)Applying migrations...$(RESET)"
	@ulimit -n $(ULIMIT_N); cd $(SERVER_DIR) && $(GO) run ./cmd/tsioctl db migrate

db-status: ## Show current migration version
	@ulimit -n $(ULIMIT_N); cd $(SERVER_DIR) && $(GO) run ./cmd/tsioctl db status

db-reset: ## DESTRUCTIVE: drop schema and re-apply all migrations (dev only)
	@echo "$(YELLOW)⚠  Resetting database (all data lost)...$(RESET)"
	@ulimit -n $(ULIMIT_N); cd $(SERVER_DIR) && $(GO) run ./cmd/tsioctl db reset

seed: ## Insert dev fixtures (default group + dev API key)
	@echo "$(CYAN)Seeding dev fixtures...$(RESET)"
	@ulimit -n $(ULIMIT_N); cd $(SERVER_DIR) && $(GO) run ./cmd/tsioctl db seed

##@ Docker Compose (dev infra) & image build

docker-up: ## Start dev infrastructure (Postgres 18.3 + MinIO + adminer)
	$(COMPOSE) up -d

docker-down: ## Stop dev infrastructure
	$(COMPOSE) down

docker-logs: ## Tail logs of dev infrastructure
	$(COMPOSE) logs -f

docker-build: ## Build the tsio-server container image
	docker build -t tsio-server:dev -f apps/server/Dockerfile .

##@ Clean

clean: clean-server clean-web ## Clean build artifacts

clean-server: ## Remove Go build artifacts
	@echo "$(CYAN)Cleaning Go build artifacts...$(RESET)"
	rm -rf $(SERVER_DIR)/bin $(SERVER_DIR)/coverage.out

clean-web: ## Remove web build artifacts
	cd $(WEB_DIR) && rm -rf dist coverage

clean-all: clean ## Clean + purge caches (go mod/test cache, node_modules)
	cd $(SERVER_DIR) && $(GO) clean -cache -testcache -modcache
	cd $(WEB_DIR) && rm -rf node_modules

##@ Dependencies

outdated: outdated-server outdated-web ## List outdated deps (server + web)

outdated-server: ## List outdated Go modules
	cd $(SERVER_DIR) && $(GO) list -u -m all

outdated-web: ## List outdated npm deps
	cd $(WEB_DIR) && npm outdated || true

update: update-server update-web ## Bump to latest (bump-and-review; run tests after)

update-server: ## Update Go modules to latest (direct + test deps) then tidy
	@echo "$(CYAN)Updating Go modules to latest...$(RESET)"
	cd $(SERVER_DIR) && $(GO) get -u -t ./... && $(GO) mod tidy
	@echo "$(GREEN)Done. Review $(SERVER_DIR)/go.mod and $(SERVER_DIR)/go.sum before committing.$(RESET)"

update-web: ## Update npm packages
	cd $(WEB_DIR) && npm update

audit: audit-server ## Security audit (server)

audit-server: ## Run govulncheck against the Go module
	@echo "$(CYAN)Running govulncheck...$(RESET)"
	cd $(SERVER_DIR) && $(GOVULNCHECK_CMD) ./...

##@ Ports

kill-ports: kill-server-port kill-web-port ## Kill processes on both dev ports

kill-server-port: ## Free :$(SERVER_PORT)
	@$(MAKE) kill-port PORT=$(SERVER_PORT)

kill-web-port: ## Free :$(WEB_PORT)
	@$(MAKE) kill-port PORT=$(WEB_PORT)

kill-port: ## Kill process on a specific port (usage: make kill-port PORT=1234)
	@if [ -z "$(PORT)" ]; then echo "$(RED)PORT is required$(RESET)"; exit 1; fi
	@pid=$$(lsof -ti tcp:$(PORT) || true); \
	if [ -n "$$pid" ]; then \
	  echo "$(YELLOW)Killing process $$pid on port $(PORT)$(RESET)"; \
	  kill -9 $$pid; \
	else \
	  echo "$(GREEN)No process on port $(PORT)$(RESET)"; \
	fi
