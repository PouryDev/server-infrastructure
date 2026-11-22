SHELL := bash
.DEFAULT_GOAL := help
ROOT := $(abspath ..)
RUN := $(ROOT)/scripts/manager.sh
.PHONY: help install build image up stop logs test check integration shipping-integration resources-integration fmt tidy
.PHONY: vpn-integration
vpn-integration: ## Verify VPN traffic, persistence and recovery with the real Xray core
	@$(RUN) vpn-integration
help: ## List manager commands
	@awk 'BEGIN{FS=":.*## "} /^[a-z-]+:.*## /{printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
install: ## Install locked React dependencies (Docker)
	@$(RUN) install
build: ## Build the Go executable and React production assets (Docker)
	@$(RUN) build
image: ## Build the standalone application image
	@$(RUN) image
up: ## Build and start the manager with the shared infrastructure
	@$(RUN) up
stop: ## Stop only the manager; preserve state and audit history
	@$(RUN) stop
logs: ## Tail manager logs
	@$(RUN) logs
test: ## Run Go and React tests
	@$(RUN) test
check: ## Check formatting, Go vet, tests and production build
	@$(RUN) check
integration: ## Verify real lifecycle with disposable Docker containers
	@$(RUN) integration
shipping-integration: ## Ship/deploy locally and transfer over disposable loopback SSH
	@$(RUN) shipping-integration
fmt: ## Format Go source
	@$(RUN) fmt
tidy: ## Resolve Go modules
	@$(RUN) tidy

resources-integration: ## Verify real resource changes, Git commits and rollback on disposable containers
	@$(RUN) resources-integration
