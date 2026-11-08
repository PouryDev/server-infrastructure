# Starting a new project on this infrastructure

The infrastructure contract lives in `AGENTS.md` (next to this file). It covers only how a project connects to
the shared infrastructure, not how the project itself is built.

## 1. Prepare (once per project)

```bash
APP=shop                                              # lowercase letters, digits, hyphens
mkdir -p ~/projects/$APP && cd ~/projects/$APP && git init
cp ~/projects/infrastructure/server-infrastructure/docs/project-integration/AGENTS.md ./AGENTS.md
cd ~/projects/infrastructure/server-infrastructure && make new-project
#   Project name:        shop
#   Client domain:       shop.localhost        (empty if there is no frontend)
#   Client container:    Enter (default http://shop-web:8080)
#   API location:        domain
#   API domain:          api.shop.localhost
#   API container:       Enter (default http://shop-api:8080)
```

`make new-project` writes the connection strings to `<APPS_DIR>/<app>/.env` (`~/infra-local/apps/<app>/.env` on a
development machine). The uptime probes it adds use `https://` and fail for `.localhost` domains; ignore the
resulting `EndpointDown` alerts or delete those entries from `config/prometheus/targets/http-probes.yml`.

## 2. Prompt (paste it to whatever assistant or teammate works on the project)

Fill the `<...>` parts. Add your own instructions about the project's design and implementation separately; this
prompt only covers the connection to the infrastructure.

```text
Project "<app>": <one paragraph describing what the project does. Context only.>

Connect this project to my shared infrastructure. Read ./AGENTS.md completely first; it is the contract and
its checklist (section 7) is the definition of done. How the project is designed and implemented internally is
outside the scope of that document: follow my other instructions for that.

Environment:
- The infrastructure is running on this machine in <infra repo path>. Do not modify files in that repo and do
  not run its scripts; tell me if something there needs to change.
- The project is already provisioned. Its connection strings are in <apps dir>/<app>/.env. Never print, copy
  or commit that file; the project reads the variables from its environment, and ./.env.example lists names only.
- Local domains: <app>.localhost (client) and api.<app>.localhost (API); nginx listens on port 80.
- Deploy with scripts/ship.sh (copy it from <infra repo path>/scripts/ship.sh into ./scripts/) using
  SSH_HOST=local and REMOTE_DIR=<apps dir>/<app> in ship.conf. Commit before shipping.
- Docker Hub is reached through the mirror docker.arvancloud.ir; use that prefix for base images.

Deliver:
1. The Dockerfile(s), deploy/compose.yaml and ship.conf that satisfy sections 2, 4 and 5 of AGENTS.md.
2. The project's wiring to PostgreSQL, Redis, RabbitMQ and OpenTelemetry as described in section 3
   (only the services the project actually needs), including /healthz and graceful shutdown.
3. Proof that it works after a local deploy: `curl -s -H 'Host: api.<app>.localhost' http://127.0.0.1/healthz`
   returns 200, and a request produces a trace visible in Grafana (Explore, data source Tempo; admin password:
   GRAFANA_ADMIN_PASSWORD in <infra repo path>/.env, Grafana on http://localhost:3000).
4. The AGENTS.md checklist with each item marked done / not applicable.

Tell me about anything in AGENTS.md that is wrong, missing or ambiguous instead of working around it silently.
```

## 3. Things that can get in the way

* **Docker access**: whoever builds and deploys needs permission to run `docker` and to write to `<apps dir>/<app>`.
  If the assistant's sandbox blocks that, run `scripts/ship.sh` yourself in a terminal.
* **`*.localhost`**: resolves to 127.0.0.1 in browsers and curl; otherwise send `-H 'Host: ...'`.
* **Production later**: change `SSH_HOST=local` to `SSH_HOST=deploy@your-server` and drop `REMOTE_DIR` (default
  `/srv/apps/<app>`), after the infra owner ran `make new-project` on the server with the real domains.
