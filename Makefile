BINARY        := hserver-panel
MODULE        := github.com/IamYGT/heyserver
VERSION       := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_COMMIT  := $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
BUILD_DATE    := $(shell git show -s --format=%cI HEAD 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)
PROJECT_URL   ?=
LDFLAGS       := -ldflags "-s -w \
	-X $(MODULE)/internal/config.Version=$(VERSION) \
	-X $(MODULE)/internal/config.BuildCommit=$(BUILD_COMMIT) \
	-X $(MODULE)/internal/config.BuildDate=$(BUILD_DATE) \
	-X $(MODULE)/internal/config.ProjectURL=$(PROJECT_URL)"
EMBED_DIR     := cmd/hserver/web/dist
GO_BUILD      := scripts/go-build-with-frontend.sh
BIN_PATH      := bin/$(BINARY)
CLI_BIN_PATH  := bin/hserverctl
SERVICE       := hserver
ARCH          ?= $(shell go env GOARCH)
RELEASE_PANEL := bin/$(BINARY)-linux-$(ARCH)
RELEASE_AGENT := bin/hserver-agent-linux-$(ARCH)
RELEASE_CLI   := bin/hserverctl-linux-$(ARCH)

.PHONY: all build dev dev-check dev-setup frontend sync-dist backend dev-frontend clean \
        install upgrade deploy rollback doctor uninstall \
        lint lint-go lint-frontend \
        test test-go test-frontend test-shell test-docker test-public-source test-all test-coverage test-coverage-check \
        test-security test-database-restore test-contributor-ci-contract \
        gen-routes verify-routes gen-api-docs verify-api-docs \
        ci ci-fast ci-pr ci-full \
        release release-check \
        help

# ─── Default ─────────────────────────────────────────────────────────────────

all: frontend build

# ─── Frontend ────────────────────────────────────────────────────────────────

frontend:
	cd web && npm ci && npm run build

# Explicitly refresh the tracked embed snapshot. Canonical builds use an
# overlay instead, so a frontend build cannot mark the source checkout dirty.
sync-dist: frontend
	@echo "Syncing frontend dist to embed dir..."
	@rm -rf $(EMBED_DIR)
	@mkdir -p $(EMBED_DIR)
	@cp -r web/dist/* $(EMBED_DIR)/
	@echo "Synced: $$(ls $(EMBED_DIR)/assets/index-*.js 2>/dev/null | xargs -r basename)"

# ─── Backend Build ───────────────────────────────────────────────────────────

build: frontend
	@mkdir -p bin
	CGO_ENABLED=1 $(GO_BUILD) $(LDFLAGS) -o $(BIN_PATH) ./cmd/hserver
	CGO_ENABLED=0 $(GO_BUILD) $(LDFLAGS) -o $(CLI_BIN_PATH) ./cmd/hserverctl
	@echo "Built: $(BIN_PATH) ($$(du -h $(BIN_PATH) | cut -f1))"
	@echo "Built: $(CLI_BIN_PATH) ($$(du -h $(CLI_BIN_PATH) | cut -f1))"

backend:
	go run ./cmd/hserver

dev-frontend:
	cd web && npm run dev

dev:
	@echo "Run 'make backend' and 'make dev-frontend' in separate terminals"

## dev-check: Inspect the local contributor toolchain without changing it
dev-check:
	./scripts/dev-setup.sh check

## dev-setup: Verify the toolchain and install locked Go/frontend dependencies
dev-setup:
	./scripts/dev-setup.sh setup

# ─── Lint ────────────────────────────────────────────────────────────────────

## lint: Run all linters (Go + Frontend)
lint: lint-go lint-frontend

## lint-go: Run golangci-lint when installed, otherwise fall back to go vet
lint-go:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --timeout=5m ./...; \
	else \
		echo "golangci-lint not found; running go vet fallback"; \
		go vet ./...; \
	fi

## lint-frontend: Run ESLint + TypeScript type check
lint-frontend:
	cd web && npm run lint
	cd web && npx tsc --noEmit

# ─── CI (local) ──────────────────────────────────────────────────────────────

## ci-fast: Run the fast provider-neutral contributor baseline (not GitHub parity)
ci-fast: lint test-all test-coverage-check build
	@echo ""
	@echo "═══════════════════════════════════════"
	@echo "  ✅ LOCAL FAST BASELINE PASSED"
	@echo "═══════════════════════════════════════"

## ci: Backward-compatible alias for ci-fast
ci: ci-fast

## ci-pr: Run the full locally reproducible contributor acceptance gates
ci-pr: ci-fast test-security test-database-restore test-docker test-public-source
	@echo ""
	@echo "═══════════════════════════════════════"
	@echo "  ✅ LOCAL PR GATE PASSED"
	@echo "  (GitHub-only architecture and release gates still apply)"
	@echo "═══════════════════════════════════════"

## ci-full: Alias for the full local PR gate
ci-full: ci-pr

# ─── Tests ───────────────────────────────────────────────────────────────────

## test: Alias for test-all
test: test-all

## test-go: Run all Go tests
test-go:
	CGO_ENABLED=1 go test ./... -count=1

## test-frontend: Run Vitest unit tests
test-frontend:
	cd web && npm test
	cd web && npm run test:api-routes

## test-shell: Run portable installer and release packaging checks
test-shell:
	./scripts/test-dev-setup.sh
	./scripts/test-init-env.sh
	./scripts/test-build-metadata.sh
	./scripts/test-extension-catalog.py
	./scripts/test-new-extension.sh
	./scripts/test-debian12-release-gate.sh
	./scripts/test-bootstrap-install.sh
	./scripts/test-manual-release-install.sh
	./scripts/test-release-trust.sh
	./scripts/test-public-install.sh
	./scripts/test-public-release-assets.sh
	./scripts/test-hserver-doctor.sh
	./scripts/test-hserver-install.sh
	./scripts/test-hserver-agent-install.sh
	./scripts/test-restore-db.sh
	./scripts/test-package-release.sh
	./scripts/test-release-manifest.sh
	./scripts/test-release-version.sh
	./scripts/test-release-changelog.sh
	./scripts/test-release-signing.sh
	./scripts/test-openapi-docs.sh
	./scripts/mail-service-openapi-contract-test.sh
	./scripts/managed-read-openapi-contract-test.sh
	./scripts/test-ci-release-gates.sh
	./scripts/test-provider-network-managed-agent-acceptance.sh
	./scripts/test-provider-network-receipt-verifier.sh
	./scripts/test-provider-network-receipt-signing.sh
	./scripts/test-public-source-acceptance-arch.sh
	./scripts/test-export-public-source.sh
	./scripts/test-create-public-repository.sh
	./scripts/test-provider-neutral-scripts.sh
	./scripts/test-public-docs.sh
	./scripts/test-community-docs.sh
	./scripts/test-contributor-ci-contract.sh
	$(MAKE) verify-api-docs

## test-security: Run the same govulncheck package scan used by GitHub Actions
test-security:
	@command -v govulncheck >/dev/null 2>&1 || { \
		echo "govulncheck is required for the full contributor gate; install it with: go install golang.org/x/vuln/cmd/govulncheck@latest" >&2; \
		exit 1; \
	}
	govulncheck ./...

## test-database-restore: Run both isolated PostgreSQL and MariaDB restore drills
test-database-restore:
	./scripts/test-postgresql-restore-drill.sh
	./scripts/test-mariadb-restore-drill.sh

## test-contributor-ci-contract: Verify the documented local CI command contract
test-contributor-ci-contract:
	./scripts/test-contributor-ci-contract.sh

## test-docker: Build and exercise the isolated Docker quick-evaluation path
test-docker:
	./scripts/test-docker-evaluation.sh

## test-public-source: Export, build, package, and verify the Git-free public source tree
test-public-source:
	ARCH=$(ARCH) ./scripts/test-public-source-acceptance.sh

## test-all: Go + frontend unit tests + route manifest drift + lifecycle checks
test-all: test-go test-frontend verify-routes test-shell

## gen-api-docs: Regenerate the complete API route inventory
gen-api-docs:
	@HSERVER_ROOT=$$(pwd) go run ./scripts/gen-api-routes/main.go

## verify-api-docs: Ensure the committed API route inventory is current
verify-api-docs:
	@HSERVER_ROOT=$$(pwd) go run ./scripts/gen-api-routes/main.go -check
	@cmp -s docs/openapi.json cmd/hserver/web/dist/openapi.json || (echo "embedded openapi.json is stale — run make sync-dist" && exit 1)
	@echo "Embedded OpenAPI contract is current"

## gen-routes: Regenerate routes_manifest.go from router.go (single source)
gen-routes:
	@HSERVER_ROOT=$$(pwd) go run ./scripts/gen-routes-manifest/main.go

## verify-routes: Ensure router.go and routes_manifest.go stay in sync
verify-routes: gen-routes
	@git diff --exit-code internal/api/routes_manifest.go || (echo "routes_manifest.go out of date — run make gen-routes" && exit 1)
	@./scripts/verify-routes.sh

## test-coverage: Run tests and generate HTML + text coverage report
test-coverage:
	@mkdir -p coverage
	CGO_ENABLED=1 go test \
		-coverprofile=coverage/coverage.out \
		-covermode=atomic \
		$(GO_TEST_PKGS)
	@go tool cover -func=coverage/coverage.out | tail -1
	@go tool cover -html=coverage/coverage.out -o coverage/coverage.html
	@echo "Coverage report: coverage/coverage.html"

## test-coverage-check: Fail if total Go coverage drops below MIN_COVERAGE (default 21)
MIN_COVERAGE ?= 25
GO_TEST_PKGS := $(shell go list ./... | grep -vE 'testcmd|flatted/golang|/terminal$$|/testutil$$')
test-coverage-check:
	@mkdir -p coverage
	@CGO_ENABLED=1 go test -coverprofile=coverage/coverage.out -covermode=atomic $(GO_TEST_PKGS) > /dev/null
	@TOTAL=$$(go tool cover -func=coverage/coverage.out | awk '/^total:/ {gsub(/%/,"",$$3); print $$3}'); \
	echo "Total coverage: $${TOTAL}% (min $(MIN_COVERAGE)%)"; \
	awk -v cov="$$TOTAL" -v min="$(MIN_COVERAGE)" 'BEGIN { if (cov+0 < min+0) { print "FAIL: coverage below minimum"; exit 1 } }'

API_MIN_COVERAGE ?= 70
DB_MIN_COVERAGE ?= 70

## test-coverage-packages: Enforce per-package coverage gates (api + database)
test-coverage-packages:
	@mkdir -p coverage
	@CGO_ENABLED=1 go test ./internal/api/... -coverprofile=coverage/api.out -covermode=atomic > /dev/null
	@API=$$(go tool cover -func=coverage/api.out | awk '/^total:/ {gsub(/%/,"",$$3); print $$3}'); \
	echo "internal/api coverage: $${API}% (min $(API_MIN_COVERAGE)%)"; \
	awk -v cov="$$API" -v min="$(API_MIN_COVERAGE)" 'BEGIN { if (cov+0 < min+0) { print "FAIL: api coverage below minimum"; exit 1 } }'
	@CGO_ENABLED=1 go test ./internal/services/database/... -coverprofile=coverage/database.out -covermode=atomic > /dev/null
	@DB=$$(go tool cover -func=coverage/database.out | awk '/^total:/ {gsub(/%/,"",$$3); print $$3}'); \
	echo "database coverage: $${DB}% (min $(DB_MIN_COVERAGE)%)"; \
	awk -v cov="$$DB" -v min="$(DB_MIN_COVERAGE)" 'BEGIN { if (cov+0 < min+0) { print "FAIL: database coverage below minimum"; exit 1 } }'

# ─── Release ─────────────────────────────────────────────────────────────────

## release-check VERSION=x.y.z: Require a stable release identity
release-check:
	@./scripts/validate-release-version.sh "$(VERSION)" >/dev/null
	@if test -n "$$(git status --porcelain=v1 --untracked-files=all)"; then \
		echo "release requires a clean Git worktree; commit source changes before building" >&2; \
		git status --short >&2; \
		exit 1; \
	fi

## release VERSION=x.y.z ARCH=amd64: Build, test, and package a stable release tarball
release: release-check
	@echo "Building release $(VERSION)..."
	@$(MAKE) test
	@$(MAKE) frontend
	@mkdir -p bin dist
	CGO_ENABLED=1 GOOS=linux GOARCH=$(ARCH) $(GO_BUILD) \
		$(LDFLAGS) \
		-o $(RELEASE_PANEL) \
		./cmd/hserver
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) $(GO_BUILD) \
		-ldflags "-s -w -X main.agentVersion=$(VERSION)" \
		-o $(RELEASE_AGENT) \
		./cmd/hserver-agent
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) $(GO_BUILD) \
		$(LDFLAGS) \
		-o $(RELEASE_CLI) \
		./cmd/hserverctl
	@./scripts/package-release.sh $(VERSION) $(ARCH) $(RELEASE_PANEL) $(RELEASE_AGENT) $(RELEASE_CLI) dist
	@echo "Release ready:"
	@ls -lh dist/$(BINARY)-$(VERSION)-linux-$(ARCH).tar.gz
	@cat dist/$(BINARY)-$(VERSION)-linux-$(ARCH).tar.gz.sha256

# ─── Cleanup ─────────────────────────────────────────────────────────────────

## clean: Remove all build artifacts
clean:
	rm -rf bin/ web/dist $(EMBED_DIR) web/node_modules coverage/ dist/

## clean-dist: Remove only frontend build artifacts (keep node_modules)
clean-dist:
	rm -rf bin/ web/dist $(EMBED_DIR) coverage/ dist/

# ─── Native lifecycle ────────────────────────────────────────────────────────

## install: Build and install HServer as a native systemd service
install: build
	sudo ./scripts/hserver-install.sh install \
		--binary "$(abspath $(BIN_PATH))" \
		--cli-binary "$(abspath $(CLI_BIN_PATH))"

## upgrade: Build, snapshot current state, upgrade, health-check, and auto-rollback on failure
upgrade: build
	sudo ./scripts/hserver-install.sh upgrade \
		--binary "$(abspath $(BIN_PATH))" \
		--cli-binary "$(abspath $(CLI_BIN_PATH))"

## deploy: Alias for the portable native upgrade flow
deploy: upgrade

## rollback: Restore the latest pre-upgrade binary and database snapshot
rollback:
	sudo ./scripts/hserver-install.sh rollback

## doctor: Validate the current host and native HServer installation
doctor:
	sudo ./scripts/hserver-doctor.sh installed

## uninstall: Remove the service and binary while preserving configuration and data
uninstall:
	sudo ./scripts/hserver-install.sh uninstall

# ─── Help ────────────────────────────────────────────────────────────────────

## help: Show available targets
help:
	@echo "HServer Panel — Makefile targets"
	@echo ""
	@grep -E '^## ' Makefile | sed 's/^## /  /'
	@echo ""
	@echo "Variables:"
	@echo "  VERSION     Current: $(VERSION)"
	@echo "  BINARY      $(BINARY)"
	@echo "  SERVICE     $(SERVICE)"
	@echo "  ARCH        $(ARCH)"
