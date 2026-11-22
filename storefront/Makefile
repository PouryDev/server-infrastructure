# Generated with make new-go-project; every Go/Node command runs inside Docker.
SHELL := bash
.SHELLFLAGS := -e -o pipefail -c
.DEFAULT_GOAL := help
APP := storefront
GO_IMAGE ?= docker.arvancloud.ir/library/golang:1.26-alpine
LINT_IMAGE ?= docker.arvancloud.ir/golangci/golangci-lint:latest-alpine
GO_CACHE_VOLUME ?= gokit-cache
TAG ?= $(shell git rev-parse --short=10 HEAD)-$(shell date +%s)
export GO_IMAGE GO_CACHE_VOLUME
export HOST_UID := $(shell id -u)
export HOST_GID := $(shell id -g)
DC := docker compose -f compose.dev.yaml --profile tools
GO := $(DC) run --rm --no-deps -T tools
.PHONY: help prepare fmt fmt-check vet lint test build check tidy vendor image integration grafana-check
help: ## List commands
	@awk 'BEGIN{FS=":.*## "} /^[a-z-]+:.*## /{printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
prepare:
	@test -f .env || cp .env.example .env
	@docker volume inspect $(GO_CACHE_VOLUME) >/dev/null 2>&1 || docker volume create $(GO_CACHE_VOLUME) >/dev/null
fmt: prepare ## Format application Go files
	$(GO) gofmt -w cmd internal migrations
fmt-check: prepare ## Check application Go formatting
	$(GO) sh -c 'test -z "$$(gofmt -l cmd internal migrations)"'
vet: prepare ## Run go vet
	$(GO) go vet ./...
lint: prepare ## Run focused Go static analysis
	docker run --rm -u $(HOST_UID):$(HOST_GID) -v "$(CURDIR)":/src -v $(GO_CACHE_VOLUME):/go -w /src -e HOME=/tmp -e GOCACHE=/go/.buildcache -e GOLANGCI_LINT_CACHE=/go/.lintcache -e CGO_ENABLED=0 $(LINT_IMAGE) golangci-lint run ./...
test: prepare ## Run domain, architecture, HTTP and provider-contract tests
	$(GO) go test -count=1 ./...
build: prepare ## Build all application binaries
	$(GO) go build ./...
js-test: ## Test cart rendering and offline worker privacy boundaries
	docker run --rm -v "$(CURDIR)":/src:ro -w /src docker.arvancloud.ir/library/node:22-alpine node --test internal/adapter/web/tests/*.test.cjs
check: fmt-check vet lint test js-test ## Run formatting, vet, static analysis and tests
	docker run --rm -v "$(CURDIR)":/src:ro -w /src docker.arvancloud.ir/library/node:22-alpine node --check internal/adapter/web/assets/app.js
	docker run --rm -v "$(CURDIR)":/src:ro -w /src docker.arvancloud.ir/library/node:22-alpine node --check internal/adapter/web/assets/sw.js
	docker run --rm -v "$(CURDIR)":/src:ro -w /src docker.arvancloud.ir/library/node:22-alpine node --check internal/adapter/web/assets/navigation.js
	docker run --rm -v "$(CURDIR)":/src:ro -w /src docker.arvancloud.ir/library/node:22-alpine node --check internal/adapter/web/assets/theme-preview.js
	docker run --rm -v "$(CURDIR)":/src:ro -w /src docker.arvancloud.ir/library/node:22-alpine node --check internal/adapter/web/assets/customer.js
	docker run --rm -v "$(CURDIR)":/src:ro -w /src docker.arvancloud.ir/library/node:22-alpine node --check internal/adapter/web/assets/merchandising.js
	docker run --rm -v "$(CURDIR)":/src:ro -w /src docker.arvancloud.ir/library/node:22-alpine node --check internal/adapter/web/assets/business.js
tidy: prepare ## Resolve Go modules
	$(GO) go mod tidy
vendor: prepare ## Vendor dependencies for an offline image build
	$(GO) go mod vendor
image: vendor ## Build an immutable storefront image; TAG=...
	@if docker image inspect $(APP)-api:$(TAG) >/dev/null 2>&1; then echo 'Image tag already exists; choose a new immutable TAG' >&2; exit 1; fi
	docker build --pull=false --build-arg GO_IMAGE=$(GO_IMAGE) -t $(APP)-api:$(TAG) .
integration: ## Run real PostgreSQL/PgBouncer integration tests through the infrastructure
	$(MAKE) -C .. storefront-integration
grafana-check: ## Validate application dashboard definitions
	python3 scripts/grafana-lint.py deploy/grafana
