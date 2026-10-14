# trica: the commands CI runs (see AGENTS.md). Nothing here that CI does not run,
# except the local conveniences under "dev".
SHELL := /bin/bash
.DEFAULT_GOAL := help

GO ?= go
GOLANGCI_LINT_VERSION ?= v2.14.0
GOLANGCI_LINT ?= golangci-lint
COVERAGE_MIN ?= 80
# Packages whose coverage counts: everything but command entry points and test helpers.
COVER_PKGS = $(shell $(GO) list ./... | grep -vE '/cmd/|/internal/testdb' | paste -sd, -)

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

.PHONY: clean
clean:  ## Remove build and coverage outputs
	rm -rf bin dist coverage.out coverage.html
