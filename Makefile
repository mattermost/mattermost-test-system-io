# Test System IO - Development Makefile
# ================================================
#
# Usage: make <target>
#
# Run `make help` to see all available targets.

.PHONY: help install dev dev-server dev-web build build-server build-web \
        test test-server test-server-oidc test-web lint lint-server lint-web fmt fmt-server fmt-web \
        clean clean-server clean-web clean-all check typecheck \
        db-reset run run-server run-web \
        docker-build docker-build-no-cache docker-up docker-down docker-down-volumes docker-logs \
        outdated outdated-server outdated-web update update-server update-web \
        check-target-size clean-debug-if-large clean-release-if-large \
        kill-ports kill-server-port kill-web-port kill-port

# Default target
.DEFAULT_GOAL := help

# Colors for terminal output
CYAN := \033[36m
GREEN := \033[32m
YELLOW := \033[33m
RED := \033[31m
RESET := \033[0m

# Directories
ROOT_DIR := $(shell pwd)
SERVER_DIR := $(ROOT_DIR)/apps/server
WEB_DIR := $(ROOT_DIR)/apps/web

# Size threshold for auto-cleanup (5GB in KB for macOS compatibility)
SIZE_THRESHOLD_GB := 5
SIZE_THRESHOLD_KB := $(shell echo $$(($(SIZE_THRESHOLD_GB) * 1024 * 1024)))

# Development ports
SERVER_PORT := 8080
WEB_PORT := 3000

# ============================================================================
# Help
# ============================================================================

help: ## Show this help message
	@echo ""
	@echo "$(CYAN)Test System IO - Development Commands$(RESET)"
	@echo "================================================"
	@echo ""
	@echo "$(GREEN)Usage:$(RESET) make $(YELLOW)<target>$(RESET)"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  $(CYAN)%-20s$(RESET) %s\n", $$1, $$2}'
	@echo ""

# ============================================================================
# Installation
# ============================================================================

install: install-server install-web ## Install all dependencies

install-server: ## Install Rust dependencies (cargo fetch)
	@echo "$(CYAN)Installing Rust dependencies...$(RESET)"
	cd $(SERVER_DIR) && cargo fetch

install-web: ## Install Node.js dependencies
	@echo "$(CYAN)Installing Node.js dependencies...$(RESET)"
	cd $(WEB_DIR) && npm install

# ============================================================================
# Target Size Management
# ============================================================================

# Check if debug directory exceeds threshold and clean if needed
clean-debug-if-large:
	@if [ -d "$(SERVER_DIR)/target/debug" ]; then \
		SIZE_KB=$$(du -sk $(SERVER_DIR)/target/debug 2>/dev/null | cut -f1); \
		if [ -n "$$SIZE_KB" ] && [ "$$SIZE_KB" -gt "$(SIZE_THRESHOLD_KB)" ]; then \
			echo "$(YELLOW)Debug folder is $$(du -sh $(SERVER_DIR)/target/debug | cut -f1) (>$(SIZE_THRESHOLD_GB)GB), cleaning...$(RESET)"; \
			rm -rf $(SERVER_DIR)/target/debug; \
			echo "$(GREEN)Debug folder cleaned$(RESET)"; \
		fi \
	fi

# Check if release directory exceeds threshold and clean if needed
clean-release-if-large:
	@if [ -d "$(SERVER_DIR)/target/release" ]; then \
		SIZE_KB=$$(du -sk $(SERVER_DIR)/target/release 2>/dev/null | cut -f1); \
		if [ -n "$$SIZE_KB" ] && [ "$$SIZE_KB" -gt "$(SIZE_THRESHOLD_KB)" ]; then \
			echo "$(YELLOW)Release folder is $$(du -sh $(SERVER_DIR)/target/release | cut -f1) (>$(SIZE_THRESHOLD_GB)GB), cleaning...$(RESET)"; \
			rm -rf $(SERVER_DIR)/target/release; \
			echo "$(GREEN)Release folder cleaned$(RESET)"; \
		fi \
	fi

check-target-size: ## Check and report target directory sizes
	@echo "$(CYAN)Checking target directory sizes...$(RESET)"
	@if [ -d "$(SERVER_DIR)/target/debug" ]; then \
		SIZE=$$(du -sh $(SERVER_DIR)/target/debug | cut -f1); \
		SIZE_KB=$$(du -sk $(SERVER_DIR)/target/debug | cut -f1); \
		if [ -n "$$SIZE_KB" ] && [ "$$SIZE_KB" -gt "$(SIZE_THRESHOLD_KB)" ]; then \
			echo "$(RED)Debug: $$SIZE (exceeds $(SIZE_THRESHOLD_GB)GB threshold)$(RESET)"; \
		else \
			echo "$(GREEN)Debug: $$SIZE$(RESET)"; \
		fi \
	else \
		echo "Debug: not present"; \
	fi
	@if [ -d "$(SERVER_DIR)/target/release" ]; then \
		SIZE=$$(du -sh $(SERVER_DIR)/target/release | cut -f1); \
		SIZE_KB=$$(du -sk $(SERVER_DIR)/target/release | cut -f1); \
		if [ -n "$$SIZE_KB" ] && [ "$$SIZE_KB" -gt "$(SIZE_THRESHOLD_KB)" ]; then \
			echo "$(RED)Release: $$SIZE (exceeds $(SIZE_THRESHOLD_GB)GB threshold)$(RESET)"; \
		else \
			echo "$(GREEN)Release: $$SIZE$(RESET)"; \
		fi \
	else \
		echo "Release: not present"; \
	fi

# ============================================================================
# Development
# ============================================================================

dev: ## Run both server and web in development mode concurrently
	@echo "$(CYAN)Starting server and web concurrently...$(RESET)"
	@$(MAKE) dev-server & $(MAKE) dev-web & wait

dev-server: clean-debug-if-large ## Run Rust server in development mode with auto-reload
	@echo "$(CYAN)Starting Rust server (with cargo-watch if available)...$(RESET)"
	@if command -v cargo-watch >/dev/null 2>&1; then \
		cd $(SERVER_DIR) && cargo watch -x 'run --bin mattermost-tsio'; \
	else \
		echo "$(YELLOW)cargo-watch not installed. Running without auto-reload.$(RESET)"; \
		echo "$(YELLOW)Install with: cargo install cargo-watch$(RESET)"; \
		cd $(SERVER_DIR) && cargo run --bin mattermost-tsio; \
	fi

dev-web: ## Run Vite dev server with HMR
	@echo "$(CYAN)Starting Vite dev server...$(RESET)"
	cd $(WEB_DIR) && npm run dev

run: ## Run both server and web concurrently (no auto-reload)
	@$(MAKE) run-server & $(MAKE) run-web & wait

run-server: clean-debug-if-large ## Run Rust server (no auto-reload)
	@echo "$(CYAN)Starting Rust server...$(RESET)"
	cd $(SERVER_DIR) && cargo run --bin mattermost-tsio

run-web: ## Run Vite preview server (serves built assets)
	@echo "$(CYAN)Starting Vite preview server...$(RESET)"
	cd $(WEB_DIR) && npm run preview

# ============================================================================
# Building
# ============================================================================

build: build-server build-web ## Build both server and web for production

build-server: clean-release-if-large ## Build Rust server (release mode)
	@echo "$(CYAN)Building Rust server (release)...$(RESET)"
	cd $(SERVER_DIR) && cargo build --release
	@echo "$(GREEN)Server binary: $(SERVER_DIR)/target/release/server$(RESET)"

build-web: ## Build React frontend for production
	@echo "$(CYAN)Building React frontend...$(RESET)"
	cd $(WEB_DIR) && npm run build
	@echo "$(GREEN)Frontend assets: $(WEB_DIR)/dist/$(RESET)"

build-server-dev: clean-debug-if-large ## Build Rust server (debug mode)
	@echo "$(CYAN)Building Rust server (debug)...$(RESET)"
	cd $(SERVER_DIR) && cargo build

# ============================================================================
# Testing
# ============================================================================

test: test-server test-web ## Run all tests

test-server: ## Run Rust tests (unit + E2E)
	@echo "$(CYAN)Running Rust unit tests...$(RESET)"
	cd $(SERVER_DIR) && RUST_ENV=development cargo test --lib --bins
	@echo "$(CYAN)Running OIDC E2E tests...$(RESET)"
	cd $(SERVER_DIR) && ulimit -n 4096 2>/dev/null; RUST_ENV=development cargo test --test oidc_e2e -- --test-threads=1

test-server-oidc: ## Run OIDC E2E tests only (requires running PostgreSQL)
	@echo "$(CYAN)Running OIDC E2E tests...$(RESET)"
	cd $(SERVER_DIR) && ulimit -n 4096 2>/dev/null; RUST_ENV=development cargo test --test oidc_e2e -- --test-threads=1

test-web: ## Run frontend tests with Vitest
	@echo "$(CYAN)Running frontend tests...$(RESET)"
	cd $(WEB_DIR) && npm run test

test-web-watch: ## Run frontend tests in watch mode
	@echo "$(CYAN)Running frontend tests (watch mode)...$(RESET)"
	cd $(WEB_DIR) && npm run test:watch

test-web-coverage: ## Run frontend tests with coverage report
	@echo "$(CYAN)Running frontend tests with coverage...$(RESET)"
	cd $(WEB_DIR) && npm run test:coverage

# ============================================================================
# Linting & Formatting
# ============================================================================

lint: lint-server lint-web ## Run all linters

lint-server: ## Run Clippy (Rust linter)
	@echo "$(CYAN)Running Clippy...$(RESET)"
	cd $(SERVER_DIR) && cargo clippy -- -D warnings

lint-web: ## Run oxlint
	@echo "$(CYAN)Running oxlint...$(RESET)"
	cd $(WEB_DIR) && npm run lint

fmt: fmt-server fmt-web ## Format all code

fmt-server: ## Format Rust code
	@echo "$(CYAN)Formatting Rust code...$(RESET)"
	cd $(SERVER_DIR) && cargo fmt

fmt-web: ## Format frontend code with oxfmt
	@echo "$(CYAN)Formatting frontend code...$(RESET)"
	cd $(WEB_DIR) && npm run format

fmt-check: fmt-check-server fmt-check-web ## Check formatting without changes

fmt-check-server: ## Check Rust formatting
	@echo "$(CYAN)Checking Rust formatting...$(RESET)"
	cd $(SERVER_DIR) && cargo fmt --check

fmt-check-web: ## Check frontend formatting (oxfmt)
	@echo "$(CYAN)Checking frontend formatting...$(RESET)"
	cd $(WEB_DIR) && npx oxfmt ./src --check

check: check-server typecheck ## Run all checks (compile check + typecheck)

check-server: ## Check Rust compilation without building
	@echo "$(CYAN)Checking Rust compilation...$(RESET)"
	cd $(SERVER_DIR) && cargo check

typecheck: ## Run TypeScript type checking
	@echo "$(CYAN)Running TypeScript type check...$(RESET)"
	cd $(WEB_DIR) && npm run typecheck

# ============================================================================
# Cleaning
# ============================================================================

clean: clean-server clean-web ## Clean build artifacts (keeps dependencies cached)

clean-server: ## Clean Rust build artifacts (keeps dependencies)
	@echo "$(CYAN)Cleaning Rust build artifacts...$(RESET)"
	cd $(SERVER_DIR) && cargo clean --release 2>/dev/null || true
	@# Remove incremental compilation cache (often the biggest bloat)
	rm -rf $(SERVER_DIR)/target/debug/incremental
	rm -rf $(SERVER_DIR)/target/release/incremental
	rm -rf $(SERVER_DIR)/target/debug/deps
	rm -rf $(SERVER_DIR)/target/debug/build
	rm -rf $(SERVER_DIR)/target/debug/.fingerprint
	@echo "$(GREEN)Cleaned Rust incremental build cache$(RESET)"

clean-web: ## Clean frontend build artifacts
	@echo "$(CYAN)Cleaning frontend build artifacts...$(RESET)"
	rm -rf $(WEB_DIR)/dist
	rm -rf $(WEB_DIR)/coverage
	@echo "$(GREEN)Cleaned frontend build artifacts$(RESET)"

clean-all: clean-server-all clean-web-all ## Deep clean everything (WARNING: removes all caches)
	@echo "$(GREEN)All build artifacts and caches removed$(RESET)"

clean-server-all: ## Deep clean Rust (removes entire target directory)
	@echo "$(YELLOW)Deep cleaning Rust target directory...$(RESET)"
	@echo "$(YELLOW)This will remove all cached dependencies and require full rebuild$(RESET)"
	rm -rf $(SERVER_DIR)/target
	@echo "$(GREEN)Removed $(SERVER_DIR)/target$(RESET)"

clean-web-all: ## Deep clean frontend (removes node_modules)
	@echo "$(YELLOW)Deep cleaning frontend...$(RESET)"
	rm -rf $(WEB_DIR)/node_modules
	rm -rf $(WEB_DIR)/dist
	rm -rf $(WEB_DIR)/coverage
	@echo "$(GREEN)Removed node_modules and build artifacts$(RESET)"

# ============================================================================
# Size Analysis & Maintenance
# ============================================================================

size: size-server size-web ## Show size of build artifacts

size-server: ## Show Rust target directory size
	@echo "$(CYAN)Rust target directory size:$(RESET)"
	@if [ -d "$(SERVER_DIR)/target" ]; then \
		du -sh $(SERVER_DIR)/target; \
		echo ""; \
		echo "$(CYAN)Breakdown:$(RESET)"; \
		du -sh $(SERVER_DIR)/target/*/ 2>/dev/null | sort -hr | head -10; \
	else \
		echo "No target directory found"; \
	fi

size-web: ## Show frontend build/node_modules size
	@echo "$(CYAN)Frontend directory sizes:$(RESET)"
	@if [ -d "$(WEB_DIR)/node_modules" ]; then \
		printf "node_modules: "; du -sh $(WEB_DIR)/node_modules; \
	fi
	@if [ -d "$(WEB_DIR)/dist" ]; then \
		printf "dist: "; du -sh $(WEB_DIR)/dist; \
	fi

prune: prune-server ## Prune unused dependencies and caches

prune-server: ## Remove unused Rust dependencies from cache
	@echo "$(CYAN)Pruning unused Rust dependencies...$(RESET)"
	@if command -v cargo-cache >/dev/null 2>&1; then \
		cargo cache --autoclean; \
	else \
		echo "$(YELLOW)cargo-cache not installed.$(RESET)"; \
		echo "$(YELLOW)Install with: cargo install cargo-cache$(RESET)"; \
		echo ""; \
		echo "Manual cleanup options:"; \
		echo "  - Remove ~/.cargo/registry/cache (downloaded crates)"; \
		echo "  - Remove ~/.cargo/registry/src (extracted crate sources)"; \
	fi

# ============================================================================
# Dependency Management
# ============================================================================

outdated: outdated-server outdated-web ## Check for outdated dependencies

outdated-server: ## Check for outdated Rust dependencies
	@echo "$(CYAN)Checking outdated Rust dependencies...$(RESET)"
	@if command -v cargo-outdated >/dev/null 2>&1; then \
		cd $(SERVER_DIR) && cargo outdated; \
	else \
		echo "$(YELLOW)cargo-outdated not installed.$(RESET)"; \
		echo "$(YELLOW)Install with: cargo install cargo-outdated$(RESET)"; \
		echo ""; \
		echo "Alternative: Run 'cargo update --dry-run' to see available updates"; \
		cd $(SERVER_DIR) && cargo update --dry-run 2>&1 | grep -E "Updating|Adding" || echo "All dependencies up to date"; \
	fi

outdated-web: ## Check for outdated npm dependencies
	@echo "$(CYAN)Checking outdated npm dependencies...$(RESET)"
	cd $(WEB_DIR) && npm outdated || true

update: update-server update-web ## Update all dependencies

update-server: ## Update Rust dependencies (respects version constraints)
	@echo "$(CYAN)Updating Rust dependencies...$(RESET)"
	cd $(SERVER_DIR) && cargo update
	@echo "$(GREEN)Cargo.lock updated$(RESET)"

update-web: ## Update npm dependencies (respects version constraints)
	@echo "$(CYAN)Updating npm dependencies...$(RESET)"
	cd $(WEB_DIR) && npm update
	@echo "$(GREEN)package-lock.json updated$(RESET)"

update-server-latest: ## Update Rust dependencies to latest (may break semver)
	@echo "$(YELLOW)Updating Rust dependencies to latest versions...$(RESET)"
	@echo "$(YELLOW)WARNING: This may update to incompatible versions$(RESET)"
	@if command -v cargo-upgrade >/dev/null 2>&1; then \
		cd $(SERVER_DIR) && cargo upgrade; \
	else \
		echo "$(RED)cargo-edit not installed.$(RESET)"; \
		echo "$(YELLOW)Install with: cargo install cargo-edit$(RESET)"; \
	fi

update-web-latest: ## Update npm dependencies to latest (may break semver)
	@echo "$(YELLOW)Updating npm dependencies to latest versions...$(RESET)"
	@echo "$(YELLOW)WARNING: This may update to incompatible versions$(RESET)"
	cd $(WEB_DIR) && npx npm-check-updates -u && npm install

# ============================================================================
# Database
# ============================================================================

db-reset: ## Reset database (removes PostgreSQL Docker volumes)
	@echo "$(YELLOW)Resetting database...$(RESET)"
	@echo "$(YELLOW)Stopping services and removing database volumes...$(RESET)"
	docker compose -f $(ROOT_DIR)/docker/docker-compose.dev.yml down -v
	@echo "$(GREEN)Database volumes removed. Run 'make docker-up' to recreate.$(RESET)"

# ============================================================================
# Documentation
# ============================================================================

docs: docs-server ## Generate documentation

docs-server: ## Generate Rust documentation
	@echo "$(CYAN)Generating Rust documentation...$(RESET)"
	cd $(SERVER_DIR) && cargo doc --no-deps --open

# ============================================================================
# CI/Quality Checks
# ============================================================================

ci: fmt-check lint test build ## Run all CI checks (format, lint, test, build)
	@echo "$(GREEN)All CI checks passed!$(RESET)"

pre-commit: fmt lint check typecheck ## Run pre-commit checks
	@echo "$(GREEN)Pre-commit checks passed!$(RESET)"

# ============================================================================
# Docker
# ============================================================================

docker-build: ## Build Docker image
	@echo "$(CYAN)Building Docker image...$(RESET)"
	docker build -t mattermost-test-system-io:latest .

docker-build-no-cache: ## Build Docker image without cache
	@echo "$(CYAN)Building Docker image (no cache)...$(RESET)"
	docker build --no-cache -t mattermost-test-system-io:latest .

docker-up: ## Start dev services (PostgreSQL + MinIO + Adminer)
	@echo "$(CYAN)Starting docker (PostgreSQL + MinIO + Adminer)...$(RESET)"
	docker compose -f $(ROOT_DIR)/docker/docker-compose.dev.yml up -d
	@echo ""
	@echo "$(GREEN)Development infrastructure started!$(RESET)"
	@echo "  PostgreSQL: localhost:6432"
	@echo "  MinIO:      localhost:9100 (UI: http://localhost:9101)"
	@echo "  Adminer:    http://localhost:8081"

docker-down: ## Stop dev services
	@echo "$(CYAN)Stopping docker-compose services...$(RESET)"
	docker compose -f $(ROOT_DIR)/docker/docker-compose.dev.yml down

docker-down-volumes: ## Stop dev services and remove volumes
	@echo "$(YELLOW)Stopping services and removing volumes...$(RESET)"
	docker compose -f $(ROOT_DIR)/docker/docker-compose.dev.yml down -v

docker-logs: ## Show dev services logs
	docker compose -f $(ROOT_DIR)/docker/docker-compose.dev.yml logs -f

# ============================================================================
# Utilities
# ============================================================================

setup: install setup-env ## Initial project setup
	@echo "$(GREEN)Project setup complete!$(RESET)"
	@echo ""
	@echo "Next steps:"
	@echo "  1. Run 'make docker-up' to start PostgreSQL and MinIO"
	@echo "  2. Run 'make dev-server' in one terminal"
	@echo "  3. Run 'make dev-web' in another terminal"

setup-env: ## Create .env files from examples
	@if [ ! -f "$(ROOT_DIR)/.env" ]; then \
		cp $(ROOT_DIR)/.env.example $(ROOT_DIR)/.env; \
		echo "$(GREEN)Created .env from .env.example$(RESET)"; \
	else \
		echo "$(YELLOW).env already exists, skipping$(RESET)"; \
	fi
	@if [ ! -f "$(WEB_DIR)/.env" ]; then \
		cp $(WEB_DIR)/.env.example $(WEB_DIR)/.env; \
		echo "$(GREEN)Created apps/web/.env from .env.example$(RESET)"; \
	else \
		echo "$(YELLOW)apps/web/.env already exists, skipping$(RESET)"; \
	fi

info: ## Show project information
	@echo ""
	@echo "$(CYAN)Project Information$(RESET)"
	@echo "==================="
	@echo ""
	@echo "$(GREEN)Rust:$(RESET)"
	@rustc --version 2>/dev/null || echo "  Not installed"
	@cargo --version 2>/dev/null || echo "  Not installed"
	@echo ""
	@echo "$(GREEN)Node.js:$(RESET)"
	@node --version 2>/dev/null || echo "  Not installed"
	@npm --version 2>/dev/null | xargs -I {} echo "  npm {}" || echo "  Not installed"
	@echo ""
	@echo "$(GREEN)Directories:$(RESET)"
	@echo "  Server: $(SERVER_DIR)"
	@echo "  Web: $(WEB_DIR)"
	@echo ""
	@if [ -d "$(SERVER_DIR)/target" ]; then \
		echo "$(GREEN)Rust target size:$(RESET) $$(du -sh $(SERVER_DIR)/target | cut -f1)"; \
	fi
	@if [ -d "$(WEB_DIR)/node_modules" ]; then \
		echo "$(GREEN)node_modules size:$(RESET) $$(du -sh $(WEB_DIR)/node_modules | cut -f1)"; \
	fi

# ============================================================================
# Port Management
# ============================================================================

kill-ports: kill-server-port kill-web-port ## Kill processes on all dev ports (8080, 3000)

kill-server-port: ## Kill process on server port (8080)
	@echo "$(CYAN)Killing processes on port $(SERVER_PORT)...$(RESET)"
	@PID=$$(lsof -ti :$(SERVER_PORT) 2>/dev/null); \
	if [ -n "$$PID" ]; then \
		echo "$(YELLOW)Found process $$PID on port $(SERVER_PORT)$(RESET)"; \
		kill -9 $$PID 2>/dev/null && echo "$(GREEN)Killed process $$PID$(RESET)" || echo "$(RED)Failed to kill process$(RESET)"; \
	else \
		echo "$(GREEN)No process running on port $(SERVER_PORT)$(RESET)"; \
	fi

kill-web-port: ## Kill process on web port (3000)
	@echo "$(CYAN)Killing processes on port $(WEB_PORT)...$(RESET)"
	@PID=$$(lsof -ti :$(WEB_PORT) 2>/dev/null); \
	if [ -n "$$PID" ]; then \
		echo "$(YELLOW)Found process $$PID on port $(WEB_PORT)$(RESET)"; \
		kill -9 $$PID 2>/dev/null && echo "$(GREEN)Killed process $$PID$(RESET)" || echo "$(RED)Failed to kill process$(RESET)"; \
	else \
		echo "$(GREEN)No process running on port $(WEB_PORT)$(RESET)"; \
	fi

# ============================================================================
# Utilities
# ============================================================================

kill-port: ## Kill process on specific port (usage: make kill-port PORT=8080)
	@if [ -z "$(PORT)" ]; then \
		echo "$(RED)Error: PORT is required$(RESET)"; \
		echo "Usage: make kill-port PORT=8080"; \
		exit 1; \
	fi
	@echo "$(CYAN)Killing processes on port $(PORT)...$(RESET)"
	@PID=$$(lsof -ti :$(PORT) 2>/dev/null); \
	if [ -n "$$PID" ]; then \
		echo "$(YELLOW)Found process $$PID on port $(PORT)$(RESET)"; \
		kill -9 $$PID 2>/dev/null && echo "$(GREEN)Killed process $$PID$(RESET)" || echo "$(RED)Failed to kill process$(RESET)"; \
	else \
		echo "$(GREEN)No process running on port $(PORT)$(RESET)"; \
	fi
