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
| grafana | grafana/grafana:13.2.5 | 384 MB | 6 provisioned dashboards, `127.0.0.1:3000` |
| exporters | node, cAdvisor, postgres, redis, nginx, blackbox | ~450 MB total | |

Caps add up to ~4.7 GB, but idle usage measured on a test run is only ~0.65 GB; the caps are ceilings,
not reservations. Budget: infra <= ~3.5 GB under load, leaving ~4 GB for projects.

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
| `make dashboards` | regenerate the Grafana dashboards |
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
installable skill, for tooling that supports skills.

## Monitoring and alerting

* After editing alert rules or `prometheus.yml`: `make prometheus-reload`. Dashboards are provisioned from `config/grafana/dashboards/` (re-read every 60 s).
* Grafana folder **Infrastructure**: Server, Containers, PostgreSQL, Redis & RabbitMQ, Edge & uptime, Applications (OTel RED metrics per service).
  Dashboards are generated by `scripts/gen-dashboards.py` (grafana.com is not reachable from every network, so nothing is downloaded).
* Traces: Grafana -> Explore -> Tempo. Services appear by `service.name`.
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
scripts/                init-host, new-project, ship, update-arvan-ips, firewall, gen-dashboards, lib-prompt (used by the Makefile)
host/                   sysctl, THP, docker daemon.json, firewall unit
docs/project-integration/   AGENTS.md + skill for the projects that deploy here
```
