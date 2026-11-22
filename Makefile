# Run `make` (or `make help`) to list targets.
# Targets that need input ask for it one value at a time, with an example in parentheses and the
# default in brackets. Any value can also be passed up front to skip its question, e.g.
#   make new-project NAME=shop CLIENT_DOMAIN=shop.example.com

SHELL := bash
.SHELLFLAGS := -e -o pipefail -c
.ONESHELL:
.DEFAULT_GOAL := help

DC := docker compose
LIB := . scripts/lib-prompt.sh
NEED_ENV = test -f .env || { echo "No .env yet - run: make init" >&2; exit 1; }

.PHONY: help init up down restart update pull ps status logs config \
        new-project ship firewall update-arvan-ips \
        nginx-test nginx-reload prometheus-reload dashboards project-dashboards project-access project-dashboards-import agents-sync \
        psql redis-cli tunnel \
        new-go-project gokit-test gokit-check gokit-integration gokit-bench gokit-bench-db gokit-sync gokit-jwt-key \
        mizan-test mizan-check mizan-integration \
        mizan-ui-up mizan-ui-down mizan-ui-reset mizan-ui-logs mizan-ui-install mizan-ui-test mizan-ui-typecheck mizan-ui-lint mizan-ui-build mizan-ui-check \
        mizan-host-up mizan-host-down mizan-host-reset mizan-host-logs mizan-host-test mizan-host-check mizan-host-integration mizan-host-user-create mizan-host-ui mizan-host-sync mizan-host-seed \
        laravelkit-test laravelkit-check laravelkit-fmt laravelkit-integration laravelkit-install laravelkit-sync laravelkit-jwt-key

help: ## Show this help
	@awk 'BEGIN{FS=":.*## "} /^##@/{printf "\n\033[1m%s\033[0m\n", substr($$0,5)} /^[a-zA-Z_-]+:.*## /{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo

##@ Stack
init: ## First-time setup: .env with random secrets + data directories (uses sudo)
	@scripts/init-host.sh

up: ## Start / update all shared services
	@$(NEED_ENV)
	@test -f manager/.env || { echo "Initialize manager credentials with python3 scripts/manager-credentials.py" >&2; exit 1; }
	@scripts/manager.sh image
	$(DC) up -d
	$(DC) ps

down: ## Stop ALL shared services (asks first)
	@$(NEED_ENV)
	$(LIB)
	confirm "This stops Postgres, Redis, RabbitMQ and nginx for EVERY project. Continue?" || { echo "aborted"; exit 0; }
	$(DC) down

restart: ## Restart one service (asks which)
	@$(NEED_ENV)
	$(LIB)
	ask SERVICE "Service to restart" "nginx" "" '^[a-z-]+$$'
	$(DC) restart "$$SERVICE"

pull: ## Pull the images pinned in compose.yaml
	@$(NEED_ENV)
	$(DC) pull --ignore-buildable

update: ## Pull images and recreate whatever changed
	@$(NEED_ENV)
	@scripts/manager.sh image
	$(DC) pull --ignore-buildable
	$(DC) up -d --remove-orphans
	$(DC) ps

ps: ## Container state and health
	@$(NEED_ENV)
	$(DC) ps --format 'table {{.Service}}\t{{.State}}\t{{.Health}}\t{{.Status}}'

status: ## State + memory/CPU of every infra container
	@$(NEED_ENV)
	$(DC) ps --format 'table {{.Service}}\t{{.State}}\t{{.Health}}'
	echo
	docker stats --no-stream --format 'table {{.Name}}\t{{.MemUsage}}\t{{.MemPerc}}\t{{.CPUPerc}}' $$($(DC) ps -q)

logs: ## Follow logs (asks which service and how many lines)
	@$(NEED_ENV)
	$(LIB)
	ask SERVICE "Service (empty = all)" "nginx" "" '^[a-z-]*$$'
	ask LINES "Lines of history" "100" "100" '^[0-9]+$$'
	$(DC) logs --tail "$$LINES" -f $$SERVICE

config: ## Validate compose.yaml with the current .env
	@$(NEED_ENV)
	$(DC) config -q && echo "compose.yaml OK"

##@ Projects
new-project: ## Provision DB, Redis, RabbitMQ, nginx and env file for a project (interactive)
	@$(NEED_ENV)
	$(LIB)
	ask NAME "Project name: lowercase letters, digits, hyphens" "shop" "" '^[a-z][a-z0-9-]{1,30}$$'
	ask CLIENT_DOMAIN "Client (frontend) domain, empty = none" "shop.example.com" "" '^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$$|^$$'
	CLIENT_UPSTREAM="$${CLIENT_UPSTREAM:-}"
	if [ -n "$$CLIENT_DOMAIN" ]; then
	  ask CLIENT_UPSTREAM "Client container URL (service name inside the infra network)" "http://$$NAME-web:8080" "http://$$NAME-web:8080" '^http://[a-z0-9._-]+:[0-9]+$$'
	fi
	ask API_MODE "Where is the API: own domain, a path on the client domain, or none (domain/path/none)" "domain" "domain" '^(domain|path|none)$$'
	API_DOMAIN="$${API_DOMAIN:-}"; API_PATH="$${API_PATH:-}"; API_UPSTREAM="$${API_UPSTREAM:-}"
	case "$$API_MODE" in
	  domain)
	    ask API_DOMAIN "API domain" "api.shop.example.com" "" '^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$$' ;;
	  path)
	    [ -n "$$CLIENT_DOMAIN" ] || die "an API path needs a client domain to live under"
	    ask API_PATH "API path on the client domain, no trailing slash" "/api" "/api" '^/[a-zA-Z0-9_/-]*[a-zA-Z0-9_-]$$' ;;
	esac
	if [ "$$API_MODE" != none ]; then
	  ask API_UPSTREAM "API container URL" "http://$$NAME-api:8080" "http://$$NAME-api:8080" '^http://[a-z0-9._-]+:[0-9]+$$'
	fi
	args=("$$NAME")
	[ -z "$$CLIENT_DOMAIN" ] || args+=(--client-domain "$$CLIENT_DOMAIN" --client-upstream "$$CLIENT_UPSTREAM")
	[ -z "$$API_DOMAIN" ]    || args+=(--api-domain "$$API_DOMAIN")
	[ -z "$$API_PATH" ]      || args+=(--api-path "$$API_PATH")
	[ -z "$$API_UPSTREAM" ]  || args+=(--api-upstream "$$API_UPSTREAM")
	echo >&2
	echo "About to run: scripts/new-project.sh $${args[*]}" >&2
	confirm "Create it?" || { echo "aborted"; exit 0; }
	scripts/new-project.sh "$${args[@]}"

ship: ## Build a project locally and deploy it to the server over SSH (interactive)
	$(LIB)
	ask PROJECT_DIR "Path of the project to deploy (must contain ship.conf)" "~/projects/shop" "" '.+'
	PROJECT_DIR="$${PROJECT_DIR/#\~/$$HOME}"
	[ -f "$$PROJECT_DIR/ship.conf" ] || die "$$PROJECT_DIR/ship.conf not found (see the header of scripts/ship.sh)"
	ask NO_BUILD "Skip the build and redeploy images already built for this commit? (y/n)" "n" "n" '^[yn]$$'
	flag=(); [ "$$NO_BUILD" = y ] && flag=(--no-build)
	cd "$$PROJECT_DIR"
	exec "$(CURDIR)/scripts/ship.sh" "$${flag[@]}"

##@ Edge & security
nginx-test: ## Test the nginx configuration
	@$(NEED_ENV)
	$(DC) exec -T nginx nginx -t

nginx-reload: ## Test, then reload nginx (no downtime)
	@$(NEED_ENV)
	$(DC) exec -T nginx nginx -t
	$(DC) exec -T nginx nginx -s reload
	echo "nginx reloaded"

update-arvan-ips: ## Refresh the ArvanCloud IP ranges (real-IP config + firewall list), reload nginx
	@scripts/update-arvan-ips.sh

firewall: ## Restrict the HTTP port to the CDN (dry run / apply / remove; interactive)
	$(LIB)
	ask MODE "What to do (dry-run / apply / remove)" "dry-run" "dry-run" '^(dry-run|apply|remove)$$'
	case "$$MODE" in
	  dry-run) scripts/firewall.sh ;;
	  apply)   confirm "Apply the DOCKER-USER rules now (uses sudo)?" || { echo "aborted"; exit 0; }
	           sudo scripts/firewall.sh --apply ;;
	  remove)  confirm "Remove the CDN-only rule (HTTP port becomes public again)?" || { echo "aborted"; exit 0; }
	           sudo scripts/firewall.sh --remove ;;
	esac

##@ Monitoring
prometheus-reload: ## Reload Prometheus config and rules without restarting
	@$(NEED_ENV)
	$(DC) exec -T prometheus wget -q -O /dev/null --post-data= http://127.0.0.1:9090/-/reload
	echo "prometheus reloaded"

dashboards: ## Regenerate the Grafana dashboards from scripts/gen-dashboards.py (incl. the "Project overview" template)
	@python3 scripts/gen-dashboards.py

project-dashboards: ## Per-project Grafana ORG (own filtered data sources) + full dashboard set: all projects Prometheus has seen, or PROJECTS="a b"
	@python3 scripts/project-dashboards.py $(if $(PROJECTS),$(PROJECTS),--discover)
	@python3 scripts/project-access.py $(if $(PROJECTS),$(PROJECTS),--discover)

project-access: ## (Re)create a project's Grafana org + push its dashboards, optionally add a login that sees only it: PROJECT=gapchi [LOGIN=m.ahmadi]
	@test -n "$(PROJECT)" || { echo "usage: make project-access PROJECT=name [LOGIN=login]" >&2; exit 1; }
	@python3 scripts/project-access.py $(PROJECT) $(if $(LOGIN),--user $(LOGIN))

agents-sync: ## Rebuild the skill from AGENTS.md; copy AGENTS.md + dashboard linter + example into projects: make agents-sync PROJECTS="~/p/a ~/p/b"
	@scripts/sync-agents-docs.sh $(PROJECTS)

project-dashboards-import: ## Add a project's own dashboards (JSON) to its org: make project-dashboards-import PROJECT=gapchi DIR=~/p/gapchi/deploy/grafana
	@test -n "$(PROJECT)" -a -n "$(DIR)" || { echo "usage: make project-dashboards-import PROJECT=name DIR=path" >&2; exit 1; }
	@python3 scripts/project-dashboards.py --import $(PROJECT) $(DIR)
	@python3 scripts/project-access.py $(PROJECT)

##@ Go kit (gokit/, everything runs in Docker)
new-go-project: ## Scaffold a clean-architecture Go project that uses the kit (asks where)
	$(LIB)
	ask DEST "Directory for the new project (its last element is the project name)" "~/projects/shop" "" '.+'
	NAME="$$(basename "$${DEST/#\~/$$HOME}")"
	[[ "$$NAME" =~ ^[a-z][a-z0-9-]{1,30}$$ ]] || die "project name '$$NAME' must be lowercase letters, digits, hyphens"
	ask MODULE "Go module path" "github.com/PouryDev/$$NAME" "github.com/PouryDev/$$NAME" '^[A-Za-z0-9][A-Za-z0-9._~/-]*$$'
	scripts/gokit.sh new "$$DEST" --module "$$MODULE"

gokit-test: ## Unit tests of the kit
	@scripts/gokit.sh test

gokit-integration: ## Kit tests that need PostgreSQL, PgBouncer, Redis, RabbitMQ (throw-away containers)
	@scripts/gokit.sh integration

gokit-bench: ## Kit micro-benchmarks (no database)
	@scripts/gokit.sh bench ./query ./auth ./httpx ./orm

gokit-bench-db: ## Kit benchmarks against throw-away PostgreSQL / PgBouncer (ORM vs raw pgx, bulk, preload, pagination)
	@scripts/gokit.sh bench-db

gokit-check: ## Kit: gofmt, vet, golangci-lint, tests
	@scripts/gokit.sh check

gokit-sync: ## Refresh the vendored kit copy (.kit) of a project (asks which)
	$(LIB)
	ask PROJECT "Project directory" "~/projects/shop" "" '.+'
	scripts/gokit.sh sync "$${PROJECT/#\~/$$HOME}"

gokit-jwt-key: ## Print a new Ed25519 key pair for JWT_PRIVATE_KEY / JWT_PUBLIC_KEY (or: make gokit-jwt-key ARGS=--hs256)
	@scripts/gokit.sh jwt-key $(ARGS)

##@ Financial module (mizan/, everything runs in Docker)
mizan-test: ## mizan unit tests (money, jalali, state machines, gateways, reconciliation, architecture rules)
	@scripts/mizan.sh test

mizan-integration: ## mizan tests against a throw-away PostgreSQL (ledger, payments, wallets, invoices, HTTP API)
	@scripts/mizan.sh integration

mizan-check: ## mizan: gofmt, vet, golangci-lint, tests
	@scripts/mizan.sh check

##@ Accounting UI (mizan-ui/, everything runs in Docker)
mizan-ui-up: ## Start PostgreSQL 18 + mizan API (migrations, Iranian coding, sample data) + the UI: http://localhost:5173
	@scripts/mizan-ui.sh up

mizan-ui-down: ## Stop the local UI stack (data is kept)
	@scripts/mizan-ui.sh down

mizan-ui-reset: ## Stop the local UI stack and delete its database
	@scripts/mizan-ui.sh reset

mizan-ui-logs: ## Follow the logs of the local UI stack
	@scripts/mizan-ui.sh logs

mizan-ui-install: ## npm install for the UI (needs HTTPS_PROXY=http://127.0.0.1:2020 on this network)
	@scripts/mizan-ui.sh install

mizan-ui-test: ## UI unit and component tests (vitest)
	@scripts/mizan-ui.sh test

mizan-ui-typecheck: ## UI: tsc
	@scripts/mizan-ui.sh typecheck

mizan-ui-lint: ## UI: eslint
	@scripts/mizan-ui.sh lint

mizan-ui-build: ## UI: production build (dist/)
	@scripts/mizan-ui.sh build

mizan-ui-check: ## UI: typecheck, lint, tests, build
	@scripts/mizan-ui.sh check

##@ mizan host (mizan-host/: real JWT sign-in + mizan + the UI on one origin, everything runs in Docker)
mizan-host-up: ## Build and start PostgreSQL 18 + migrations + API + UI: http://localhost:8090 (first run creates user admin, password printed once)
	@scripts/mizan-host.sh up

mizan-host-down: ## Stop the host stack (data is kept)
	@scripts/mizan-host.sh down

mizan-host-reset: ## Stop the host stack and delete its database
	@scripts/mizan-host.sh reset

mizan-host-logs: ## Follow the logs of the host stack
	@scripts/mizan-host.sh logs

mizan-host-ui: ## Rebuild mizan-ui and copy it into mizan-host/web
	@scripts/mizan-host.sh ui

mizan-host-sync: ## Refresh mizan-host's vendored copies of gokit and mizan
	@scripts/mizan-host.sh sync

mizan-host-user-create: ## Create a user: make mizan-host-user-create USERNAME=m.ahmadi ROLE=finance [NAME=...] [USER_PASSWORD=...]
	@test -n "$(USERNAME)" || { echo "usage: make mizan-host-user-create USERNAME=m.ahmadi [ROLE=admin|finance|user] [NAME=...]" >&2; exit 1; }
	@scripts/mizan-host.sh user-create "$(USERNAME)" "$(or $(ROLE),admin)" "$(or $(NAME),$(USERNAME))"

mizan-host-seed: ## Load sample data (journals over two fiscal years, invoices, wallet, cheques, payroll, inventory); skipped if journals exist
	@scripts/mizan-host.sh seed

mizan-host-test: ## mizan-host unit tests (login, refresh, lockout, roles, gateway, architecture rules)
	@scripts/mizan-host.sh test

mizan-host-check: ## mizan-host: gofmt, vet, golangci-lint, tests
	@scripts/mizan-host.sh check

mizan-host-integration: ## mizan-host against a throw-away PostgreSQL: login, JWT checks, journal through the mounted mizan API
	@scripts/mizan-host.sh integration

##@ Laravel kit (laravelkit/, everything runs in Docker)
laravelkit-test: ## Unit tests of the Laravel kit
	@scripts/laravelkit.sh test

laravelkit-integration: ## Kit tests that need Redis (throw-away container) and an OTLP collector stand-in
	@scripts/laravelkit.sh integration

laravelkit-fmt: ## Format the Laravel kit with Pint
	@scripts/laravelkit.sh fmt

laravelkit-check: ## Laravel kit: Pint (check only) + tests
	@scripts/laravelkit.sh check

laravelkit-install: ## Install the kit into an existing Laravel project (vendors .laravelkit/, asks which)
	$(LIB)
	ask PROJECT "Laravel project directory" "~/projects/shop" "" '.+'
	scripts/laravelkit.sh install "$${PROJECT/#\~/$$HOME}"

laravelkit-sync: ## Refresh the vendored kit copy (.laravelkit) of a Laravel project (asks which)
	$(LIB)
	ask PROJECT "Laravel project directory" "~/projects/shop" "" '.+'
	scripts/laravelkit.sh sync "$${PROJECT/#\~/$$HOME}"

laravelkit-jwt-key: ## Print a new Ed25519 key pair for JWT_PRIVATE_KEY / JWT_PUBLIC_KEY (or: make laravelkit-jwt-key ARGS=--hs256)
	@scripts/laravelkit.sh jwt-key $(ARGS)

##@ Infrastructure manager (manager/: independent React/Go project)
.PHONY: manager-up manager-stop manager-image manager-build manager-test manager-check manager-integration manager-shipping-integration manager-logs
manager-up: ## Build and start the manager; preserve existing project containers
	@scripts/manager.sh up
manager-stop: ## Stop the manager only; preserve policies and audit history
	@scripts/manager.sh stop
manager-image: ## Build the standalone manager image
	@scripts/manager.sh image
manager-build: ## Build Go + React using manager's own toolchain
	@scripts/manager.sh build
manager-test: ## Manager Go + React tests
	@scripts/manager.sh test
manager-check: ## Manager formatting, vet, tests and production build
	@scripts/manager.sh check
manager-integration: ## Real lifecycle integration tests with disposable containers
	@scripts/manager.sh integration
manager-shipping-integration: ## Local ship/deploy, selective rollout, rollback, loopback SSH
	@scripts/manager.sh shipping-integration
manager-logs: ## Tail manager + runtime broker logs
	@scripts/manager.sh logs

##@ Admin
psql: ## Open psql on the shared Postgres (asks which database)
	@$(NEED_ENV)
	$(LIB)
	ask DB "Database" "shop_db or postgres" "postgres" '^[a-z_][a-z0-9_]*$$'
	$(DC) exec postgres psql -U postgres -d "$$DB"

redis-cli: ## Open redis-cli on the shared Redis (as admin)
	@$(NEED_ENV)
	$(DC) exec redis redis-cli

tunnel: ## SSH tunnel to the admin UIs on the server (Grafana, Prometheus, Alertmanager, RabbitMQ, Postgres)
	$(LIB)
	ask SSH_HOST "SSH destination of the server" "deploy@203.0.113.10" "" '.+'
	echo "Tunnelling (Ctrl-C to stop):" >&2
	echo "  Grafana       http://localhost:3000" >&2
	echo "  Prometheus    http://localhost:9090" >&2
	echo "  Alertmanager  http://localhost:9093" >&2
	echo "  RabbitMQ      http://localhost:15672" >&2
	echo "  Postgres      localhost:5432" >&2
	exec ssh -N -o ExitOnForwardFailure=yes \
	  -L 3000:127.0.0.1:3000 -L 9090:127.0.0.1:9090 -L 9093:127.0.0.1:9093 \
	  -L 15672:127.0.0.1:15672 -L 5432:127.0.0.1:5432 "$$SSH_HOST"

##@ Storefront builder
.PHONY: storefront-check storefront-build storefront-image storefront-create
storefront-check: ## Storefront: architecture, domain, security, Go and browser-script checks
	@scripts/storefront.sh check
storefront-build: ## Build the storefront Go application
	@$(MAKE) -C storefront build
storefront-image: ## Build a reusable storefront image (immutable tag)
	@scripts/storefront.sh image
storefront-create: ## Provision a storefront from validated JSON on stdin (no secrets in arguments)
	@scripts/storefront.sh provision

.PHONY: storefront-integration
storefront-integration: ## Real PgBouncer integration tests in a rolled-back, isolated PostgreSQL schema
	@scripts/storefront.sh integration

.PHONY: storefront-release
storefront-release: ## Upgrade one store with health checks and image rollback: NAME=shop IMAGE=storefront-api:tag
	@python3 scripts/storefront-release.py "$(NAME)" "$(IMAGE)"

.PHONY: storefront-smoke
storefront-smoke: ## Exercise shoptest.localhost demo auth, attachments, discounts and payment (creates a labeled demo order)
	@python3 scripts/storefront-smoke.py

.PHONY: manager-package
manager-package: ## Package a project for upload in Manager (PROJECT_DIR, TAG, OUTPUT)
	@python3 scripts/manager-package.py "$(PROJECT_DIR)" --tag "$(TAG)" --output "$(OUTPUT)"

.PHONY: storefront-commerce-smoke
storefront-commerce-smoke: ## Full opt-in local demo QA; creates labeled CMS/delivery examples and a demo order
	@python3 scripts/storefront-commerce-smoke.py
