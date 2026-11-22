# server-infrastructure

Shared infrastructure for every project on one server (4 cores, 8 GB RAM, 100 GB SSD), started with a
single `make up` (`docker compose up -d`). Other projects join the `infra` Docker network and use these services.

```
 visitors ─▶ ArvanCloud CDN (TLS) ─▶ :80 nginx ─▶ project containers (own network alias, `infra` net)
                                                     │
        ┌──────────────── network `infra` ───────────┼───────────────────────────────┐
        │  pgbouncer ─▶ postgres        redis        rabbitmq        otel-collector  │
        └──────────────────────────────────────────────────────────────┬─────────────┘
                                                                       ▼
        network `infra-monitoring`:  prometheus ◀─ exporters   tempo ◀─┘   grafana   alertmanager   blackbox
```

| Service | Image | Memory cap | Notes |
|---|---|---|---|
| postgres | postgres:18.6-alpine | 1280 MB | tuned for SSD + 8 GB, `pg_stat_statements`, only reachable via PgBouncer (and `127.0.0.1:5432` for admin) |
| pgbouncer | edoburu/pgbouncer | 64 MB | transaction pooling, wildcard DB, auth through `auth_query` (new projects need no config change) |
| redis | redis:8.10.3-alpine | 640 MB | `maxmemory 384mb`, `volatile-lru`, AOF, ACL user per project (key prefix) |
| rabbitmq | rabbitmq:4.3.7-management | 512 MB | watermark 300 MB, vhost + user per project, management UI on `127.0.0.1:15672` |
| nginx | nginx:1.30.6-alpine | 128 MB | the only public port; one `conf.d/<project>.conf` each; real IP from the CDN |
| otel-collector | otel/opentelemetry-collector-contrib | 256 MB | OTLP in; tail sampling; traces -> Tempo, metrics -> Prometheus |
| tempo | grafana/tempo:3.1.1 | 384 MB | single binary, local disk, 72 h retention |
| prometheus | prom/prometheus:v3.15.1 | 512 MB | 10 d / 6 GB retention, OTLP receiver, 42 alert rules |
| alertmanager | prom/alertmanager | 64 MB | **no receiver configured yet** (see below) |
| grafana | grafana/grafana:13.2.5 | 384 MB | 7 provisioned dashboards + one per project, `127.0.0.1:3000` |
| exporters | node, cAdvisor, postgres, redis, nginx, blackbox | ~450 MB total | |

Caps add up to ~4.7 GB, but idle usage measured on a test run is only ~0.65 GB; the caps are ceilings,
not reservations. Budget: infra <= ~3.5 GB under load, leaving ~4 GB for projects.

## Infrastructure manager (`manager/`)

Standalone React/TypeScript + Go project with its own Makefile and clean-architecture
boundaries. It discovers shared-network projects, reports live Docker resources and
attributable shared-service metrics, and supports reversible suspension/expiration,
renewal, automatic reconciliation and a durable audit log. Initialize private
credentials with `python3 scripts/manager-credentials.py`, then `make manager-up`;
`make up` also builds and starts it alongside the shared stack. No credential is
printed. `make -C manager check` validates both applications; see
[`manager/README.md`](manager/README.md) for lifecycle semantics, security and metric limits.

## Shared VPN client (Xray)

Manager's **اتصال VPN** page imports a single V2Ray/Xray connection link and can
activate, disable and test it. Services opt in through `HTTP_PROXY` / `HTTPS_PROXY`
(`http://xray:10809`) or `ALL_PROXY` (`socks5h://xray:10808`) on the `infra` network;
set `NO_PROXY` for internal services. The host has loopback-only proxy ports 10809
and 10808. Configuration persists in `infra_xray-data`. See the [VPN setup and
supported links](manager/README.md#shared-outbound-vpn-client-xray).

## Go kit (`gokit/`)

Everything that repeats between my Go projects and has no business logic lives in `gokit/`: generic filtering /
sorting / pagination with allow and deny lists, an ORM with relations and **migrations generated from the models**,
stateless (optionally revocable) JWT auth, PostgreSQL / Redis / RabbitMQ clients, opt-in caching and Redis read models, OpenTelemetry, HTTP plumbing,
graceful shutdown. `make new-go-project` generates a clean-architecture project that already uses
it (Makefile, Dockerfile, dev and deploy compose files, tests, an architecture test that enforces the layer rules).
**Go never runs on the host**: every command goes through Docker, with the module cache in the shared volume
`gokit-cache`. See `gokit/README.md`.

## mizan host (`mizan-host/`)

A standalone host application whose only job is to run `mizan` behind **real authentication** (gokit JWT, argon2id, users table, roles `finance` / `admin` / `user`,
login lock after 5 failures) and to serve `mizan-ui` on the same origin. `make mizan-host-up` -> http://localhost:8090 (first run creates user `admin` with a random
password printed once); `make mizan-host-user-create USERNAME=... ROLE=finance`; `make mizan-host-check`, `make mizan-host-integration`. mizan routes: `/v1/...` and the alias
`/accounting/...`. See `mizan-host/README.md`.

## Accounting UI (`mizan-ui/`)

The web UI of `mizan/` for accountants (React + TypeScript, RTL, Vazirmatn). `make mizan-ui-up` starts PostgreSQL 18, the mizan API (migrations, Iranian chart,
sample data) and the UI at http://localhost:5173, all in Docker; `make mizan-ui-check` runs typecheck, lint, tests and the build. The sign-in is a dev-only stand-in
(`mizan/cmd/mizan-dev`: httpOnly-cookie JWT, same origin). See `mizan-ui/README.md`.

## Laravel kit (`laravelkit/`)

The Laravel twin of `gokit/`, for the Laravel projects that already exist: a Composer package that you install into a
project with `make laravelkit-install` (it vendors the kit into the project's `.laravelkit/`). It uses what Laravel
already gives (Eloquent, cache, queue, mail, HTTP client, `retry`) and adds only what repeats: the same JSON error body
and `{items, meta}` list shape as the Go services, filtering / sorting / pagination with allow and deny lists, stateless
(optionally revocable) JWT that Go and Laravel services can verify for each other, the shared-Redis cache contract,
OpenTelemetry, SMS + email with sync and async drivers. **PHP never runs on the host**: every command goes through Docker.
See `laravelkit/README.md`.

## Makefile

`make` lists every target. Targets marked *asks* prompt one question at a time, with an example in parentheses and the default in brackets,
and show what they will run before doing anything destructive. A value can be passed up front to skip its question:
`make new-project NAME=shop CLIENT_DOMAIN=shop.example.com`.

| Target | What it does |
|---|---|
| `make init` | first-time setup: `.env` with random secrets, data directories (uses sudo) |
| `make up` / `update` / `pull` | start or update the stack / pull images and recreate what changed / pull only |
| `make down` *asks* | stop all shared services (confirms first) |
| `make restart` *asks* | restart one service |
| `make ps` / `status` | container health / health plus memory and CPU of each container |
| `make logs` *asks* | follow logs of a service (or all) |
| `make config` | validate `compose.yaml` with the current `.env` |
| `make new-project` *asks* | provision DB, Redis, RabbitMQ, nginx blocks, probes and env file for a project |
| `make ship` *asks* | build a project locally and deploy it to the server over SSH |
| `make nginx-test` / `nginx-reload` | test / test and reload the nginx config |
| `make update-arvan-ips` | refresh CDN ranges (real-IP config + firewall list) and reload nginx |
| `make firewall` *asks* | CDN-only HTTP port: dry run / apply / remove |
| `make prometheus-reload` | reload Prometheus config and rules |
| `make dashboards` | regenerate the Grafana dashboards (incl. the Project overview template) |
| `make project-dashboards` | per-project Grafana org with filtered data sources and a full dashboard set: all projects Prometheus has seen, or `PROJECTS="a b"` |
| `make project-access` | (re)create a project's org and push its dashboards; `LOGIN=someone` adds a login that sees only that org |
| `make agents-sync` | rebuild the skill from AGENTS.md; `PROJECTS="dir ..."` also copies AGENTS.md, the dashboard linter and an example into projects |
| `make project-dashboards-import` | add a project's own dashboard JSON to its folder: `PROJECT=x DIR=path` |
| `make new-go-project` *asks* | scaffold a Go project (clean architecture) that uses `gokit/`; also `gokit-check`, `gokit-integration`, `gokit-bench`, `gokit-bench-db`, `gokit-sync`, `gokit-jwt-key` |
| `make laravelkit-install` *asks* | install the Laravel kit into an existing Laravel project; also `laravelkit-sync`, `laravelkit-check`, `laravelkit-test`, `laravelkit-integration`, `laravelkit-fmt`, `laravelkit-jwt-key` |
| `make psql` *asks* / `redis-cli` | shell on the shared Postgres / Redis |
| `make tunnel` *asks* | SSH tunnel to Grafana, Prometheus, Alertmanager, RabbitMQ and Postgres |

## First start (on the server)

```bash
git clone <this repo> /srv/infrastructure && cd /srv/infrastructure
make init       # .env with random secrets, data dirs with the right owners (/srv/data)
make pull       # or load the images you pulled elsewhere; edit REGISTRY in .env if needed
make up         # then `make status`: everything should become healthy
```

`.env.example` documents every variable. `REGISTRY` is the mirror prefix (default `docker.arvancloud.ir`);
`CADVISOR_IMAGE` is separate because cAdvisor comes from a ghcr mirror. Grafana's generated admin password:
`grep GRAFANA_ADMIN_PASSWORD .env`.

Reaching the admin UIs (all bound to `127.0.0.1` on the server): run `make tunnel` on your machine and open
Grafana `localhost:3000`, Prometheus `localhost:9090`, Alertmanager `localhost:9093`, RabbitMQ `localhost:15672`
(Postgres is on `localhost:5432`).

## Running it on a development machine

Same stack, no root needed: `INFRA_DATA_DIR=$HOME/infra-local/data INFRA_APPS_DIR=$HOME/infra-local/apps make init`, then set a free
`PROMETHEUS_PORT` etc. in `.env` if a default port is taken, and `make up`. `scripts/init-host.sh` falls back to a throw-away container for
`chown` when there is no passwordless sudo. Projects deploy to it with `SSH_HOST=local` in their `ship.conf` (no SSH, same build/rollback flow).
To start a new project, see `docs/project-integration/new-project-prompt.md` (preparation steps + a ready-to-paste prompt).

## Host preparation (once, as root)

```bash
sudo cp host/99-infra.conf /etc/sysctl.d/ && sudo sysctl --system                  # kernel tuning
sudo cp host/disable-thp.service /etc/systemd/system/ && sudo systemctl enable --now disable-thp
sudo cp host/daemon.json /etc/docker/daemon.json   # MERGE if you already have one; then: sudo systemctl restart docker
```

* **Swap**: add 1-2 GB swap (or zram) as a cushion: `fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile`.
* **ufw + node_exporter**: `ufw allow from 10.99.250.0/24 to 10.99.250.1 port 9100 proto tcp` (Prometheus scrapes the host's NIC stats through the monitoring bridge gateway).
* **CDN-only HTTP** (important): the CDN hits the server IP directly, so anyone who finds the IP can bypass it, and ufw does not filter Docker-published ports.
  `make update-arvan-ips` refreshes the CDN ranges; `make firewall` shows the `DOCKER-USER` rules first (dry run) and applies them after confirmation;
  `host/infra-firewall.service` re-applies them on boot/Docker restart (edit the paths to your install dir).
* The **real client IP** relies on the CDN appending to `X-Forwarded-For`. After the first real request, check the `remote_addr` in
  `make logs` (service `nginx`) is the visitor's address, not an Arvan IP.

## Adding a project

On the server, from this repo, run `make new-project` and answer the questions (name, client domain, API domain or path, container URLs).
It is equivalent to:

```bash
scripts/new-project.sh shop \
  --client-domain shop.example.com --client-upstream http://shop-web:8080 \
  --api-domain api.shop.example.com --api-upstream http://shop-api:8080
```

Creates the Postgres role/db, Redis ACL user, RabbitMQ vhost/user, the nginx blocks (tested + reloaded), uptime probes and
`/srv/apps/shop/.env` with every connection string. Then, from the developer machine, `make ship` (asks for the project path) builds locally and
deploys over SSH with automatic rollback. The project needs a `ship.conf` (format in the header of `scripts/ship.sh` and in `docs/project-integration/AGENTS.md`).

**Give the project's developers (and any assistant working on it) `docs/project-integration/AGENTS.md`**: copy it to the project root. It describes only how a
project connects to this infrastructure, not how to build the project. `docs/project-integration/skill/server-infra/` is the same content packaged as an
installable skill, for tooling that supports skills. After editing `AGENTS.md` run `make agents-sync` (rebuilds the skill) or
`make agents-sync PROJECTS="~/p/a ~/p/b"` to also copy `AGENTS.md`, `scripts/grafana-lint.py` and `deploy/grafana/_example.json` into projects.
`scripts/grafana-lint.py` checks a project's own dashboards offline; the section "Dashboards and your own metrics" of `AGENTS.md` is the contract for them.

## Monitoring and alerting

* After editing alert rules or `prometheus.yml`: `make prometheus-reload`. Dashboards are provisioned from `config/grafana/dashboards/` (re-read every 60 s).
* Grafana folder **Infrastructure**: Server, Containers, PostgreSQL, Redis & RabbitMQ, Edge & uptime, Applications (OTel RED metrics per service).
* **Projects**: every project has its **own Grafana org** (switch org in Grafana's profile menu; admins see all) with a generated set:
  Overview, HTTP API, Database, Notifications, Go runtime, Containers & uptime, **Traces & latency** and Custom metrics (lists the project's own metrics and plots the ones you
  pick). `make project-dashboards` builds them for every project Prometheus has seen (or `PROJECTS="a b"`). A project is the `service.namespace`
  resource attribute (`telemetry.Config.Project`, set by `gokit new`; `new-project.sh` sets it through `OTEL_RESOURCE_ATTRIBUTES`);
  Prometheus turns it into `job="<project>/<service>"`.
  * **Isolation is enforced on the data**, not in the dashboards: the org's only data sources go through `label-proxy-job` / `label-proxy-project`
    (prom-label-proxy) with a secret per-org header, and the proxy ANDs `job=~"<project>/.*"` (apps) or `project="<project>"` (containers, uptime probes;
    `project` is relabelled from the compose project in `prometheus.yml`) into every query and label API. A hand-written query in Explore cannot read another
    project. Members cannot edit or add data sources (org Editor, not Admin). There is no log backend.
  * **Traces**: Tempo is multi-tenant. The collector routes every span to the tenant named like its `service.namespace` (`config/otel/tenants.yaml`, written by
    `scripts/project-access.py`, collector restarted when it changes) and each project org's `tempo` source sends that tenant, so a project sees only its own traces.
    *Traces & latency* shows how long every request takes (span metrics computed from ALL spans before sampling: exact, isolated like any metric), which
    operations (db, outbound calls) take the time, and the traces (Tempo keeps every error, every slow trace and `TRACE_SAMPLE_PERCENT` of the rest). Spans of a
    project with no route yet land in tenant `single-tenant` (data source `Tempo (spans without a project)`); admins read all tenants with `Tempo (all projects)`.
  * **Finding the orgs**: the admin's home dashboard (org 1) is **Projects**, with links that open each project's org.
  * **Own dashboards**: build them in the org in the Grafana UI (never touched by the scripts), or keep JSON in the project's `deploy/grafana/` and run
    `make project-dashboards-import PROJECT=x DIR=path` (files become `custom-*.json`). The generated ones are overwritten on every run: "Save as" to keep a variant.
  * **Logins**: `make project-access PROJECT=x LOGIN=someone` creates an Editor that belongs to that org only (password printed once);
    `scripts/project-access.py x --remove` deletes the org. Add a project later: `make project-dashboards PROJECTS=x`.
  * *Project overview* (org 1, folder Infrastructure) is the admin's all-projects view with a Project and Service picker.
  Dashboards are generated by `scripts/gen-dashboards.py` and `scripts/project-dashboards.py` (both use `scripts/dashlib.py`) (grafana.com is not reachable from every network, so nothing is downloaded).
* Traces: Grafana -> Explore -> *Tempo (all projects)* (admin) or the project org's *Traces & latency* dashboard. Services appear by `service.name`; tenants by `service.namespace`.
* 42 alert rules in `config/prometheus/rules/` (host CPU/RAM/swap/disk incl. fill prediction, OOM kills, containers, Postgres, Redis, RabbitMQ,
  nginx, external probes, pipeline health).
* **Notifications are not wired yet**: pick a channel in `config/alertmanager/alertmanager.yml` (Telegram example is there) and
  `make restart` (service `alertmanager`). Until then alerts are only visible in Alertmanager/Grafana.
* **Dead-man switch**: the always-firing `Watchdog` alert is routed to the `watchdog` receiver. Point it at an external heartbeat service
  (healthchecks.io / UptimeRobot) so you are told when the server or the whole monitoring stack dies; local monitoring cannot report that.

## Tuning notes

All values assume 4 shared cores / 8 GB and are a starting point. Re-check after a week of real traffic with the Grafana dashboards, then adjust:

* **Postgres**: `shared_buffers 768MB`, `effective_cache_size 2560MB`, `work_mem 8MB`, `random_page_cost 1.1` (SSD), `jit off`, `wal_compression lz4`,
  15 min checkpoints, aggressive autovacuum. Watch "Temp files" (raise `work_mem`) and cache hit ratio (raise `shared_buffers`).
* **PgBouncer**: 10 server connections per database (+3 reserve), 25 max per database, `max_connections=120` on Postgres.
* **Redis**: if "Evicted keys" climbs, either the cache TTLs are too long or `maxmemory` must grow.
* **RabbitMQ**: 2 Erlang schedulers and no busy-waiting (low idle CPU); raise the watermark only after looking at real queue memory.
* **nginx**: proxying to backends uses Docker DNS with variables (no upstream keepalive pool) so a missing project can never stop nginx
  from starting. If you later need backend keepalive for a very hot service, give that service its own `upstream {}` block.
* Benchmarks to run on the server: `pgbench` (Postgres), `redis-benchmark`, `rabbitmq-perf-test`, `wrk`/`k6` against nginx.

## Hardening applied

Only nginx is public (and only to the CDN once `firewall.sh` is applied); every other port is `127.0.0.1` or internal. All containers: memory limits,
`no-new-privileges`, `cap_drop: ALL` (+ the few caps needed), read-only root fs where the image allows it, log rotation, pinned image tags.
Per-project isolation: separate DB/role (cross-database connects revoked), Redis ACL by key prefix and without admin commands, RabbitMQ vhost with limits.

Known trade-offs: cAdvisor mounts the Docker socket (read access to container metadata; it runs as root, treat it as sensitive), and projects share one
Docker network (they can reach each other's containers by name; authentication is per resource, not per network).

## Not included (yet)

* **Backups** (deliberately skipped). All state lives under `/srv/data`, so adding `pg_dump`/restic later is a small job. Until then a disk failure or an
  accidental delete loses data.
* Loki (central logs) and Alertmanager receivers; Garage/MinIO for object storage (uploads use bind mounts under `/srv/data/apps/<app>`).
* TLS at the origin (not needed: the CDN terminates TLS).

## Layout

```
compose.yaml            all services, networks, limits
config/                 per-service configuration (postgres, redis, rabbitmq, nginx, otel, tempo, prometheus + rules, alertmanager, grafana, blackbox, firewall)
Makefile                shortcuts for everything below; interactive where input is needed (`make help`)
scripts/                init-host, new-project, ship, update-arvan-ips, firewall, gen-dashboards, project-dashboards, project-access, dashlib, lib-prompt (used by the Makefile)
host/                   sysctl, THP, docker daemon.json, firewall unit
docs/project-integration/   AGENTS.md + skill for the projects that deploy here
```

## Storefront product and manager launcher

`storefront/` adds a Go SSR store product generated with `make new-go-project`. In the manager, **فروشگاه‌ساز** creates isolated instances on the current local/server host, with twenty design presets, mobile-only authentication, optional modules and separate customer/seller/developer access. `http://shoptest.localhost` is the local demo. See [storefront/README.md](storefront/README.md) for credentials, provider integration evidence and operating boundaries.

Commands: `make storefront-check`, `make storefront-integration`, `make storefront-smoke`, `make storefront-image`, `make storefront-create` (JSON stdin), and `make storefront-release NAME=shop IMAGE=storefront-api:tag`. Build/start the launcher using `make manager-up`. No accounting/Mizan module is used.
