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
        nginx-test nginx-reload prometheus-reload dashboards \
        psql redis-cli tunnel

help: ## Show this help
	@awk 'BEGIN{FS=":.*## "} /^##@/{printf "\n\033[1m%s\033[0m\n", substr($$0,5)} /^[a-zA-Z_-]+:.*## /{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo

##@ Stack
init: ## First-time setup: .env with random secrets + data directories (uses sudo)
	@scripts/init-host.sh

up: ## Start / update all shared services
	@$(NEED_ENV)
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
	$(DC) pull

update: ## Pull images and recreate whatever changed
	@$(NEED_ENV)
	$(DC) pull
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

dashboards: ## Regenerate the Grafana dashboards from scripts/gen-dashboards.py
	@python3 scripts/gen-dashboards.py

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
