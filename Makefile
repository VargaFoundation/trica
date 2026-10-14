# trica: the commands CI runs (see AGENTS.md). Nothing here that CI does not run,
# except the local conveniences under "dev".
SHELL := /bin/bash
.DEFAULT_GOAL := help

GO ?= go
GOLANGCI_LINT_VERSION ?= v2.14.0
GOLANGCI_LINT ?= golangci-lint
COVERAGE_MIN ?= 80
# Packages whose coverage counts: everything but command entry points and test helpers.
COVER_PKGS = $(shell $(GO) list ./... | grep -vE '/cmd/|/internal/testdb|/node_modules/' | paste -sd, -)

# Local PostgreSQL for the integration tests (`make db`). The port is not 5432 on
# purpose: a developer machine usually has one already.
DB_CONTAINER ?= trica-pg
DB_PORT ?= 55499
TEST_DATABASE_URL ?= postgres://trica:trica@127.0.0.1:$(DB_PORT)/trica?sslmode=disable

.PHONY: help
help:  ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

# ─────────────────────────── gate ───────────────────────────
.PHONY: ci
ci: lint vet test-cov  ## Everything that blocks a PR

.PHONY: lint
lint:  ## golangci-lint (formatting included)
	$(GOLANGCI_LINT) run ./...

.PHONY: fmt
fmt:  ## Format the Go code
	$(GOLANGCI_LINT) fmt ./...

.PHONY: vet
vet:  ## go vet
	$(GO) vet ./...

.PHONY: test
test:  ## Unit and integration tests (integration tests need TRICA_TEST_DATABASE_URL)
	$(GO) test -race ./...

.PHONY: test-cov
test-cov:  ## Tests with coverage across packages; fails under COVERAGE_MIN %
	$(GO) test -race -covermode=atomic -coverpkg=$(COVER_PKGS) -coverprofile=coverage.out ./...
	@total=$$($(GO) tool cover -func=coverage.out | awk '/^total:/ {sub("%","",$$3); print $$3}'); \
	  echo "coverage: $$total% (minimum $(COVERAGE_MIN)%)"; \
	  awk -v t="$$total" -v m="$(COVERAGE_MIN)" 'BEGIN { exit !(t+0 >= m+0) }' || \
	  { echo "coverage under $(COVERAGE_MIN)%"; exit 1; }

.PHONY: tools
tools:  ## Install the pinned golangci-lint
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

# ─────────────────────────── web ───────────────────────────
# The UI lives in web/ (React + TypeScript + Vite, see docs/reference/ui.md). It
# is optional for the Go gate above: without the `webui` build tag the binary
# embeds a placeholder page, so `make ci` needs no Node.
WEB_DIR := web
NPM ?= npm

.PHONY: web
web: web-install web-lint web-typecheck web-api-check web-test web-build web-embed-test  ## Everything the web CI job runs

.PHONY: web-install
web-install:  ## Install the UI dependencies from the lockfile
	$(NPM) --prefix $(WEB_DIR) ci

.PHONY: web-lint
web-lint:  ## ESLint on the UI
	$(NPM) --prefix $(WEB_DIR) run lint

.PHONY: web-typecheck
web-typecheck:  ## TypeScript, strict
	$(NPM) --prefix $(WEB_DIR) run typecheck

.PHONY: web-api-check
web-api-check:  ## The generated API types match web/openapi.json
	$(NPM) --prefix $(WEB_DIR) run gen:api:check

.PHONY: web-test
web-test:  ## Vitest (components, API contract, i18n, accessibility)
	$(NPM) --prefix $(WEB_DIR) test

.PHONY: web-audit
web-audit:  ## Known vulnerabilities in what ships to the browser (dev tools excluded)
	$(NPM) --prefix $(WEB_DIR) audit --omit=dev --audit-level=high

.PHONY: web-build
web-build:  ## Build the UI into internal/web/dist (what -tags webui embeds)
	$(NPM) --prefix $(WEB_DIR) run build

.PHONY: web-embed-test
web-embed-test:  ## Go tests of the handler against the real build (needs web-build first)
	$(GO) test -tags webui -count=1 ./internal/web

.PHONY: web-e2e
web-e2e:  ## Playwright (Chromium) against cmd/trica-e2e-server; needs web-build and `npm --prefix web run e2e:install` once
	cd $(WEB_DIR) && npx playwright test

.PHONY: web-api-update
web-api-update:  ## Re-export web/openapi.json from the Go API, then regenerate the types
	TRICA_UPDATE_OPENAPI=1 $(GO) test ./internal/api -run TestWebOpenAPISnapshotIsCurrent
	$(NPM) --prefix $(WEB_DIR) run gen:api

# ─────────────────────────── dev ───────────────────────────
.PHONY: db
db:  ## Start a throwaway PostgreSQL for the integration tests, print the URL to export
	@docker inspect $(DB_CONTAINER) >/dev/null 2>&1 || \
	  docker run -d --name $(DB_CONTAINER) -e POSTGRES_USER=trica -e POSTGRES_PASSWORD=trica \
	    -e POSTGRES_DB=trica -p 127.0.0.1:$(DB_PORT):5432 postgres:17
	@docker start $(DB_CONTAINER) >/dev/null
	@until docker exec $(DB_CONTAINER) pg_isready -U trica >/dev/null 2>&1; do sleep 1; done
	@echo "export TRICA_TEST_DATABASE_URL='$(TEST_DATABASE_URL)'"

.PHONY: db-down
db-down:  ## Remove the throwaway PostgreSQL
	docker rm -f $(DB_CONTAINER)

# ─────────────────────── agent: binary, packages, footprint ───────────────────────
# Packages are NOT signed here: signing (cosign, the repository keys) belongs to the
# release pipeline. Locally, VERSION defaults to 0.0.0; the pipeline passes the tag.
VERSION ?= 0.0.0
ARCHES ?= amd64 arm64
DIST ?= dist
NFPM_VERSION ?= v2.48.0
NFPM ?= $(CURDIR)/bin/nfpm
SUDO ?= sudo

.PHONY: build-agent
build-agent:  ## Static trica-agent (CGO_ENABLED=0) for each of ARCHES, into dist/
	@mkdir -p $(DIST)
	@for a in $(ARCHES); do \
	  echo "building $(DIST)/trica-agent_linux_$$a"; \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$a $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" \
	    -o $(DIST)/trica-agent_linux_$$a ./cmd/trica-agent || exit 1; \
	done

$(NFPM):
	GOBIN=$(dir $(NFPM)) $(GO) install github.com/goreleaser/nfpm/v2/cmd/nfpm@$(NFPM_VERSION)

.PHONY: package-agent
package-agent: build-agent $(NFPM)  ## deb and rpm of trica-agent for each of ARCHES, into dist/ (unsigned)
	@for a in $(ARCHES); do for p in deb rpm; do \
	  VERSION=$(VERSION) ARCH=$$a BINARY=$(DIST)/trica-agent_linux_$$a \
	    $(NFPM) package --config packaging/nfpm.yaml --packager $$p --target $(DIST)/ || exit 1; \
	done; done

.PHONY: agent-footprint
agent-footprint:  ## Measure the real agent's RSS and CPU over a full cycle (30 s); fails above 50 MB or 1 %
	@mkdir -p $(DIST)
	CGO_ENABLED=0 $(GO) build -trimpath -o $(DIST)/trica-agent ./cmd/trica-agent
	$(GO) test -c -o $(DIST)/agent-footprint.test ./cmd/trica-agent
	$(SUDO) env TRICA_REQUIRE_FOOTPRINT=1 TRICA_FOOTPRINT_BIN=$(abspath $(DIST)/trica-agent) \
	  $(abspath $(DIST)/agent-footprint.test) -test.run 'Footprint|Proc' -test.v

.PHONY: clean
clean:  ## Remove build and coverage outputs
	rm -rf bin dist coverage.out coverage.html internal/web/dist
