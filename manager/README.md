# Infrastructure manager

A standalone Go + React/TypeScript application in this repository. Its own `Makefile`
builds the Go executable and production UI. The root stack starts both the manager
and its restricted Docker broker alongside shared services.

## Build and run

All Go and Node commands run inside Docker, using the existing image mirror and Go
cache. No Go/Node installation is needed on the host.

```sh
# Once, from the repository root. Input is private; credentials are never printed.
python3 scripts/manager-credentials.py
make manager-check
make manager-up              # start manager only; test and reload nginx
# Or: make up               # build manager, then start the entire infra stack

# Independent project commands:
make -C manager build
make -C manager check
make -C manager up
make -C manager stop         # keeps data; expiry enforcement stops while manager is down
make manager-integration    # disposable containers; never mutates real projects
```

The nginx virtual host is `manager.localhost`. No host port is exposed by the manager
or broker. On a different nginx port or HTTPS origin, set `MANAGER_ORIGIN` in the root
`.env` to the exact browser origin. Set `MANAGER_SECURE_COOKIE=true` with HTTPS.
`manager/.env` is ignored and mode 0600: only a salted PBKDF2-SHA256 hash (600,000
iterations), username, and random broker token are stored. No default password is
in source control. Sessions are opaque, revocable, HttpOnly, SameSite Strict, and
expire after eight hours; manager restarts require signing in again.

## Discovery and lifecycle

* Every Compose container attached to the `infra` network is discovered, including
  stopped containers. Grouping uses `com.docker.compose.project`, never name guessing.
  Compose service/replica IDs are preserved. Containers outside this network are not
  implicitly enrolled. Provisioning without deployed containers does not create a row.
* Shared `infra` services, the manager and broker are always protected. One-off jobs
  and migration/seed/init/setup services are visible but never started/stopped.
  Set `infra.manager.managed: "false"` on a service to opt it out.
* Projects initially have unlimited validity. Setting an expiry does not resume a
  manually suspended project. **Renew** sets a future expiry (or unlimited validity),
  clears manual suspension and starts only container IDs recorded in durable stop
  intents. It never starts a service already stopped by its owner.
* Every 15 seconds, and immediately after a policy change, the reconciler discovers
  current containers and applies policy. Reconciliation is serialized and bounded;
  Docker grace periods and slow responses can extend the interval. Each stop gets a
  20-second grace period. A failed action is audited and retried on subsequent passes.
* Expired replacements/redeployments are stopped again. Policies and stop intents
  survive manager/host restarts. Docker `unless-stopped` respects explicit stops.
  Other processes with Docker access can temporarily restart/recreate a project;
  enforcement resumes on the next successful pass, not as an admission controller.
* No container/image/volume/database/key/queue is removed. PostgreSQL, Redis and
  RabbitMQ remain shared and running. Stopping app containers frees their active
  CPU/RAM, but retained files, cache, queues and database connections may continue
  using shared resources until normal cleanup/connection timeouts. An expiry is a
  reversible runtime suspension, not revocation of shared-service credentials.
* Removing containers externally leaves configured policy visible as missing.
  A future deployment with the same project name inherits that policy. A new ID is
  never started merely because a previous service with that name was stopped.

## Metrics and honest attribution

Docker supplies per-container CPU (% of one core), working-set RAM (usage minus
inactive file cache), memory limit, and RX/TX bytes since container start. Network
counts include internal traffic and are not billed/public bandwidth. Offline
containers have no live sample. Missing/partial live samples are shown as unavailable,
not fabricated zeroes. Shared infrastructure services have their own view: lifecycle
controls remain protected, while CPU/RAM budgets can be edited as described below.

Prometheus is sampled independently every 30 seconds; an outage does not block
expiration. The UI shows sample time and partial-availability warnings. Project
mapping follows the existing provisioning convention:

| Resource | Attribution |
| --- | --- |
| PostgreSQL connections, database bytes, transaction rate | database name, with `_` mapped to `-` |
| RabbitMQ queue memory and queued messages | per-object metrics with project vhost |
| Redis operations/sec | app OTel `db_client_operation_duration_seconds_count{db_system_name="redis"}` with `job=project/service` |
| HTTP requests/sec | app OTel `job=project/service` |
| External response check | blackbox `project` label |

Projects using custom DB/vhost names or lacking instrumentation show unavailable
metrics. Global Redis memory cannot be attributed to key-prefix tenants by its current
exporter. The manager does not scan production keyspaces or mislabel global Redis
memory as per-project usage. Shared CPU/RAM cannot be divided exactly between tenants.
The `rabbitmq-projects` scrape job adds only queue memory/messages from `/metrics/per-object`.
Reload Prometheus after updating its configuration (`make prometheus-reload`).

## Architecture and persistence

```text
cmd/manager                 composition, lifecycle, graceful shutdown
internal/domain             entities and policy rules; standard library only
internal/application        use cases and consumer-owned Runtime/Repository/Metrics ports
internal/adapters/docker    Unix-socket engine adapter and management boundary
internal/adapters/gateway   authenticated, sanitized runtime protocol
internal/adapters/sqlite    transactional policy + append-only audit persistence
internal/adapters/metrics   bounded Prometheus queries
internal/transport/httpapi  login, sessions, CSRF, JSON API, static SPA
web/src                     React views, typed API client and formatting helpers
```

Dependencies point inward; an architecture test enforces the domain/application
boundary. Small ports separate lifecycle orchestration from Docker, monitoring,
persistence and delivery. A volume `infra_manager-data` holds SQLite WAL data;
policy writes and audit events share a transaction with `synchronous=FULL`.
Storage failures block lifecycle mutations rather than silently losing intent.
Successful/failed engine operations and authentication are also recorded. Audit is
cursor-paginated, project-filterable and has no delete endpoint or automatic retention.
Back up the volume with a SQLite-aware backup, or stop the manager before copying all
database/WAL files. Do not run two manager replicas against the same volume.

The web/API container is non-root, read-only, capped, and has no Docker socket.
The lifecycle broker, the shipping runner and the optional local build runner have the
socket (a read-only mount still permits Docker API commands).
Its internal network has no published port; the protocol permits sanitized inventory
and start/stop of enrolled, non-protected containers only. It rechecks eligibility
for every action. The lifecycle broker has no exec/create/remove/update/proxy endpoints; resource
updates use the separate, narrowly scoped `manager-resources` broker below. Access to
the Docker daemon remains privileged infrastructure access; protect this component.

Login rate limits, request-size limits, exact Origin checks, CSRF tokens, host
validation and CSP apply. Authentication failures are generic. No password/hash,
container environment or filesystem mount is returned by the API. Crawling/indexing
is discouraged with nginx/API `X-Robots-Tag`, `robots.txt`, HTML robots metadata and
`no-store` on all responses; access control is enforced by authentication.

## Validation

`make -C manager check` checks Go formatting, vet, unit/security/architecture tests,
React component/formatting tests, TypeScript and the Vite/Go production builds.
`make manager-integration` creates only uniquely named disposable application
containers on `infra`; it tests real discovery, expiry, manager restart, redeploy,
manual suspend, renewal, preserved IDs/data and audit persistence. Its cleanup trap
removes only its own fixtures. It requires Docker and the existing infra network.

## Shipping from the panel

The **ارسال و Deploy** view manages one durable global destination: local/SSH mode,
host/IP, SSH port/user, private-key filename, infrastructure directory, apps directory
and data directory. Saving settings does not contact the destination. All targets,
previews, jobs and audit events persist in the manager volume. Default is the same-host
test sandbox under `/tmp/infra-manager-target`; no real server is preconfigured.

Choose an infrastructure/project source and all services or one Compose service.
For one service, an alternative local image can be selected. **Image only** transfers
any selected tagged local image without changing containers. Sources are the root
infrastructure compose and `APPS_DIR/<project>/compose.yaml`; no arbitrary filesystem
path or shell command is accepted from the browser. Source `.env` and `.tag.next`
resolve local image references. In this view images must already exist locally: no
build, pull, registry push or orphan removal happens (the separate **ساخت و ارسال**
view below builds first). Manually changed CPU/RAM limits on the server are kept by
default (see "Limits kept from the server").

Preparation uses local Docker/Compose and read-only inspection of the destination (the
env store, the active manifest and the limits of running containers; see below). The
preview lists exact image IDs,
architecture, destination, selected services, configuration filenames and warnings.
It expires after 15 minutes, is single-use, and becomes invalid if the target revision
changes. Execution re-renders source config and checks its fingerprint/image IDs before
connecting. The runner pins release-specific content-addressed image tags, streams
`docker image save` into destination `docker image load`, and verifies destination IDs.
Architecture mismatches are rejected before transfer. Supported destination is Linux
with Docker Engine, Docker Compose v2+ supporting `--wait`/`--pull`, tar, sh and flock.

For SSH, put a dedicated private key and trusted `known_hosts` in ignored `manager/ssh/`
(private key mode 0600). The filename is selected globally in the panel. Key contents
are never uploaded via the browser, sent to the destination, copied into images or
included in audit/API output. Connections require strict host-key verification and
batch public-key authentication; no automatic host-key acceptance, password prompting
or agent forwarding occurs. **بررسی اتصال** explicitly checks connectivity, architecture,
Docker and Compose. Deployment is a separate explicit preview/execute action.

Full deployments apply the project; single-service deployments merge only the selected
service into the destination's current manifest and use `--no-deps`. Existing services
must already be deployed before a service-only update. Configuration and image pins
for unselected services are retained. Per-project `flock` prevents competing deployments
on that destination. Compose validates config before changing containers, waits up to
120 seconds for readiness, and on failure re-applies the previous manifest if one
exists. A failed rollback is reported explicitly. No previous release means there is
no automatic rollback target; first-run failures remain visible for inspection.

Successful releases atomically activate `compose.yaml`; versioned config files and
prior image tags are retained for recovery. SSH/data transfer interruption can leave
destination work running: jobs are marked failed/interrupted for manual inspection,
and never automatically replay an in-flight job after manager restart. Queued jobs
resume; only one job may be active. Browser closure does not cancel a job. Expired or
manually suspended local project policies block deployment until renewal.

Destination env files are preserved by default and must exist when referenced. The
explicit **ارسال فایل‌های env محلی** checkbox transmits source env/secret files into the
private release, without overwriting existing destination env files. Bootstrap the
destination DB/Redis/RabbitMQ data directories/ownership and required env files first
(the existing `make init`/project provisioning workflows). Data files, volume contents,
database migrations and shared credentials are not implicitly copied or provisioned.
Compose-defined one-shot migration services can run during a whole-project deployment;
service-only deploy intentionally does not start dependencies.

Known data/apps roots are rebased to destination directories. Relative config binds
are bundled into the release; symlinks, paths outside approved roots, oversized
bundles and private SSH keys are rejected. The local test mode uses a prefixed Compose
project, isolated network/volume names and rebased data paths; it strips published
ports, host networking, host PID mode and explicit container names to avoid collisions.
It is a deployment sandbox, not a replacement for provisioning a complete second
infrastructure stack. Its project containers do not join the live shared network.

The shipping runner is a separate capped, read-only-root service with egress, source
read-only mounts, a read-only SSH-key mount and access to the Docker socket. The web
container has none of these privileges. The runner image uses a digest-pinned official
Docker CLI image including Compose/OpenSSH. Plans containing env data are private
SQLite records; restrict and back up the manager volume accordingly. Job/audit output
contains sanitized stages rather than raw commands, env values or application logs.

```sh
make manager-shipping-integration
# If Alpine package downloads for the disposable SSH test need a proxy:
# HTTPS_PROXY=http://127.0.0.1:2020 make manager-shipping-integration
```

This creates only uniquely named fixtures and a loopback-published disposable SSH
target. It tests actual image save/load, full deployment, selected-service deployment,
preserved unrelated containers/config, env/config transfer, image-only transfer,
health-failure rollback, and real SSH transfer to the same machine. It also builds a
disposable Git project from its working tree (clean and dirty tags, build args from the
env store), applies env edits with project and service scope, preserves manual
CPU/memory limits (plain and `deploy.resources` styles, and the compose-limits opt-out),
edits and applies env files with backups, and verifies the env/tag rollback of a failed
deployment and of a failed apply. Cleanup removes
only fixture containers/files. It never reads a configured real SSH destination.

## Storefront studio

The **فروشگاه‌ساز** view launches independent Go SSR stores on the manager's current infrastructure host. The four-step launcher selects identity/domain/image, one of ten distinct design styles and an independent brand color, Iranian owner/developer phone numbers, optional modules and (for live stores) Kavenegar OTP configuration. It records audit intent and submits a typed request to `manager-storefront`; web/API receives neither a Docker socket nor infrastructure credentials.

The separate capped provisioner reuses `make storefront-create`, stores durable jobs and serializes shared nginx changes. Its bearer protocol has only catalog/create endpoints. Existing project sources, databases and containers are not arbitrary browser inputs. Read [../storefront/README.md](../storefront/README.md) for creation/release commands, demo access, credentials, crash recovery and live payment prerequisites. The initial visible sample is `http://shoptest.localhost`.

## Editing CPU and memory limits

Use **تنظیم منابع** on a project's service row or on **زیرساخت مشترک**. The editor
reads configured limits from Docker inspect (including stopped containers), shows
all replicas, offers RAM/CPU presets, and requires a fresh review before applying.
Memory is in MiB; CPU is a fraction/multiple of one core. Both must stay finite
and positive (6–1,048,576 MiB, 0.01–1,024 CPU, two decimal places for CPU).
Unlimited existing limits are shown explicitly and can be replaced with a budget.

The dedicated `manager-resources` broker edits only the chosen service's
`mem_limit`/`cpus` and existing `deploy.resources.limits.memory`/`cpus` fields.
It preserves other services, comments, YAML anchors and formatting; inherited
limits get explicit overrides. JSON manifests produced by project deployment are also supported:
only the selected scalar byte spans change, preserving every other byte (including
Unicode and escaped env values). An explicit memory+swap ceiling is raised only
when needed to keep it at least the requested RAM limit. Other flow-style YAML service/limit maps, mappings that themselves define shared anchors,
and multiline limit scalars are rejected. Compose validates the result before any container update.
Docker applies the limits directly to all existing replicas, without restart,
start/stop, recreation or deployment. Service-level resource editing is allowed
for infrastructure too; lifecycle protection still prevents its start/stop.
One-off and migration/setup services are excluded.

The editor offers three explicit modes:

* **Local** (default): save Compose and apply Docker limits, without Git writes or
  network operations. Existing local edits are preserved.
* **Commit**: additionally commit just the reviewed Compose file on the current
  branch. The repository must be clean, have a branch and track that file. Hooks
  and commit signing are disabled for this unattended operation. The author is
  `Infrastructure Manager <manager@localhost>`; unrelated staged files are never
  included.
* **Commit and push**: additionally publish that commit to the same-named branch
  on `origin`. Disabled unless `MANAGER_RESOURCE_PUSH_ENABLED=true`. The branch
  must have a same-named origin upstream and match it after fetching; existing
  ahead/behind commits block the change. No force pushes or automatic merges.
  Failed pushes leave the applied limits and local commit in place and report
  partial success so the existing commit can be pushed manually.

The web container has no Git credentials, source mounts or Docker socket.
`manager-resources` has narrowly typed preview/apply endpoints on the private
control network, an authenticated bearer protocol, source write mounts, and the
Docker socket. Protect it as privileged infrastructure. Login, exact Origin,
CSRF, strict JSON decoding and append-only intent/result audit apply. Requests
continue with bounded timeouts after a browser disconnect. Previews fingerprint
source bytes, Git HEAD, requested values/mode and actual replica identities/limits;
intervening changes require a new review. Operations are serialized. Failures
before a successful commit attempt to restore source and every attempted
container's previous limits; incomplete rollback is reported explicitly. A process
or host crash is not a cross-system transaction: inspect intent-only audit records
and actual state before retrying. No operation is automatically replayed.

Sources default to `/source/infra/compose.yaml` and
`/source/apps/<compose-project>/compose.yaml` (root repository and `APPS_DIR` mounts).
Deployed app directories may not be Git repositories. To persist in the **actual
source repository**, bind-mount it and explicitly register its path and Compose
filename in an ignored Compose override. Browser requests never select filesystem
paths. For example, in an ignored `manager/resources.local.yaml`:

```yaml
services:
  manager-resources:
    environment:
      MANAGER_RESOURCE_SOURCES: >-
        {"vetrix":{"root":"/source/repos/vetrix","compose":"deploy/compose.go.yaml"}}
    volumes:
      - /home/admin1/projects/didchi/vetrix:/source/repos/vetrix
```

Use the same `-f compose.yaml -f manager/resources.local.yaml` arguments whenever
starting this broker. Do not commit machine-specific paths or credentials. Each
registered root must be a complete Git repository for commit mode (not a worktree
whose `.git` points outside the mount). Compose `.env`/`.tag.next` in the repo and
Compose directories are loaded privately for validation, without returning values
to the browser. Multi-file Compose overlays and services defined in another file
are not automatically rewritten: register the authoritative file. Keep it
consistent with your deployment pipeline so the next deployment uses these limits.

For optional SSH push, put a dedicated write-enabled `id_ed25519` (0600) and pinned
`known_hosts` in ignored `manager/resource-ssh/`. This mount is separate from
shipping credentials. SSH uses batch mode, strict host-key checking and no agent
forwarding. No browser uploads, prompts or host-key auto-acceptance. HTTPS remotes
without unattended credentials fail rather than prompting. Local testing needs no
keys and must leave `MANAGER_RESOURCE_PUSH_ENABLED=false`.

A memory decrease may trigger application OOM. Increasing a container budget does
not change application-specific caps such as Redis `maxmemory` or `GOMEMLIMIT`.
Stopped replicas remain stopped. Newly created replicas inherit the saved Compose
limits when the deployment uses that source; concurrent deployments should wait
until the resource change completes. No migrations, images or service data are
modified. Current Docker memory/swap behavior is retained where possible; a swap
ceiling below a new RAM ceiling is raised to the RAM ceiling.

```sh
make -C manager check
make -C manager resources-integration
```

The integration test creates uniquely named disposable containers on `infra` and
temporary Git repositories. It verifies real Docker NanoCPU, CFS-quota and previously
unlimited CPU changes, actual local automatic commits, and real rollback after a
simulated commit failure. It never configures a remote or executes `git push`.
Unit tests simulate the push command boundary without contacting any Git server.

## Build & Ship from a local working tree

The **ساخت و ارسال** view builds images from the CURRENT working tree of a project on
this machine (committed or not; no Git state is required) and ships them through the
same pipeline as above: `image save | load`, destination image-ID verification,
`docker compose -p <app> ... up -d --wait --wait-timeout 120`, automatic rollback to the
previous manifest/tag, per-project `flock`, one active job across build, ship and env
jobs, durable jobs that are never replayed after a restart, and sanitized stage output.

```text
browser ──► manager (no socket, no sources)
              ├── manager-builder  (socket + READ-ONLY sources, no SSH keys)   build only
              └── manager-shipper  (socket + READ-ONLY sources + SSH keys)     plan, ship, env store
```

Flow: **preview** (nothing is built: tag, images to build, services, env diff by NAME,
limits kept from the server, warnings) → confirm → job: build (`docker build --platform
linux/amd64`, `--build-arg NAME` with values from the process environment only) → plan
re-render and fingerprint check → transfer → verify → deploy. The job fails before any
destination change if the tree changed between preview and build.

* **Tag** (like `scripts/ship.sh`): short Git SHA, plus `-dirty<epoch>` when the tree has
  uncommitted or untracked changes in the registered subdirectory; a timestamp without
  Git. Images are `<app>-<service>:<tag>`. The shipped manifest pins those exact refs.
  The destination also gets `.tag`, `.tag.next` (`TAG=...`) and `.tag.prev` like
  `ship.sh`. The newest `KEEP_TAGS` (default 3) tags of the built repositories are kept
  on a remote destination; the same-host sandbox shares the builder's image store and is
  never pruned.
* **Sources are an owner allowlist.** The browser sends an ID, never a path. Register
  them in an ignored override (below). Each source has a root, an optional subdirectory
  (bodyai's `api/`) and a `ship.conf`-style spec in that directory.
* **Spec** (`ship.conf`, parsed without executing it; unrelated shell such as `SSH_HOST`
  is ignored, `$`/backticks in the keys below are rejected): `APP`, `COMPOSE_FILE`
  (default `deploy/compose.yaml`, relative to the subdirectory), `IMAGES=("svc=ctx[:dockerfile]" ...)`,
  `PLATFORM` (default `linux/amd64`), `KEEP_TAGS`, and `BUILD_ARGS` limited to
  `--build-arg NAME` (value comes from the destination's project `.env`, including edits
  made in the same preview), `--build-arg NAME=literal` and `--network host|default`.
  Build-arg values are never logged, stored in plans, audited or returned. A failed build
  reports the last build-output lines with those values redacted.
* **Scope.** All services, or one Compose service (`--no-deps`; only the image it uses is
  built). Images not built from the source (stock images) are not transferred and must
  already exist on the destination. `infra` is never a build target; suspended or
  expired projects are blocked like every other deployment.
* **Proxy.** `MANAGER_BUILD_PROXY` (e.g. `http://127.0.0.1:2020`) is passed to builds as
  `HTTP(S)_PROXY` build args; a loopback proxy also selects `--network host` for the
  build, like `scripts/manager.sh` does for its own Docker commands and like your
  `ship.conf` files (`--network host`).

### Setup on the machine that holds the sources

Create the ignored `manager/builder.local.yaml` (never committed; machine specific):

```yaml
x-sources: &sources >-
  {"gapchi":{"root":"/source/repos/gapchi"},
   "bodyai":{"root":"/source/repos/bodyai","subdir":"api"}}
services:
  manager-builder:
    environment:
      MANAGER_BUILD_SOURCES: *sources
      MANAGER_BUILD_PROXY: http://127.0.0.1:2020   # optional
    volumes:
      - /home/admin1/projects/didchi/gapchi:/source/repos/gapchi:ro
      - /home/admin1/projects/didchi/bodyai:/source/repos/bodyai:ro
  manager-shipper:
    environment:
      MANAGER_BUILD_SOURCES: *sources
    volumes:
      - /home/admin1/projects/didchi/gapchi:/source/repos/gapchi:ro
      - /home/admin1/projects/didchi/bodyai:/source/repos/bodyai:ro
```

`scripts/manager.sh up` adds `-f manager/builder.local.yaml` and starts `manager-builder`
(compose profile `build`) only when that file exists; the builder is never started on a
machine without it. `scripts/manager.sh image` builds `infra-manager-builder:local`.
The shipper needs the same mounts because it renders the project's Compose file and
bundles relative config files; the builder needs them for the build contexts.

## Environment variables of deployed projects

The **متغیرهای محیطی** view edits the DESTINATION's env files through the shipping
runner (strict host-key SSH, same key mount; the same-host sandbox works identically).
The web container never sees keys or files. Typed runner operations only: list projects,
read the store, write a reviewed change set.

| Scope | File on the destination | Used by |
| --- | --- | --- |
| Project | `<APPS_DIR>/<project>/.env` | every service whose `env_file` lists it |
| One service | `<APPS_DIR>/<project>/env.d/<service>.env` | that service only; wins over `.env` |

Service files are wired by a generated override, `manager.override.yaml`, passed as an
extra `-f` to every manager-driven `docker compose` call; it appends
`env_file: [<project dir>/env.d/<service>.env]` to exactly the services that have one.
The project's own `compose.yaml` is never edited. If you run Compose by hand on the
server use `-f compose.yaml -f manager.override.yaml`.

* **Writes** are atomic and under the per-project `flock`: timestamped backup
  (`.env.bak.<epoch>`, last 10 kept), temp file + rename, file mode preserved (new files
  0600, `env.d` 0700), comments, order, blank and unknown lines kept, CRLF kept, last
  duplicate wins. Names match `^[A-Z_][A-Z0-9_]*$`; newlines/NUL are rejected.
  Values use the `env_file` quoting rules: bare when safe, otherwise `'single quoted'`
  (literal, no interpolation); a value containing both `'` and `$` is rejected.
  Edits carry the revision (hash of all env files) you loaded: a concurrent change gives
  a conflict instead of overwriting it.
* **Masking and audit.** List responses never contain values (every value is masked;
  names that look sensitive are flagged). **Reveal** is an explicit, rate-limited,
  audited request for one value. The audit log records the scope, action and variable
  NAMES only: never values or hashes of values.
* **Never `infra`**, never one-shot services (names containing migrate/migration/seed/
  setup/init, `restart: "no"`, or `infra.manager.managed: "false"`).
* **Edit during a deploy.** In the Build & Ship review you can add/edit/remove
  variables (project or service scope). They are applied atomically with that deployment:
  backup, write, up. If the deployment fails the env files, `.tag.next` and the manifest
  are restored together and the previous containers are recreated.

### Apply env to a running project

**اعمال روی سرویس‌ها** recreates containers without building: all eligible services or
one. The preview lists the services expected to be recreated and the variable NAMES that
differ from the last applied snapshot (`.manager/applied/` on the destination, written
after every successful deploy/apply; without it every name is listed). Execution is
`docker compose ... up -d --no-deps --wait <services>`, so Compose recreates only
containers whose configuration really changed. On failure the variable files that
affect the selected services are restored from the snapshot (the failed edit stays as a
`.bak.<epoch>`), then the services are recreated again. Expired/suspended projects
are blocked, as are `infra` and one-shot services. The estimate of affected services
follows `env_file` references in the destination manifest; Compose decides the rest.

## Limits kept from the server

If you changed a container's CPU/memory on the server (**تنظیم منابع** or `docker update`),
a deployment must not silently reset it. For every deploy (Build & Ship, the Ship view,
Apply env) the runner inspects the project's containers on the destination
(`Memory`, `NanoCpus`, `MemorySwap`; running replicas preferred) and compares them with
what the local Compose file would apply:

* live limit set and different from Compose → pinned in the generated
  `manager.override.yaml` (`mem_limit`/`cpus`/`memswap_limit`, or
  `deploy.resources.limits.*` when the base file uses that field);
* live limit unlimited → never pinned: Compose applies its own value, or none;
* Docker's default swap (2x memory) is not an owner decision and is not pinned; a swap
  ceiling below the pinned memory is raised to it.

The preview shows "limits kept from the server: <service> memory …, CPU …". The
checkbox **use limits from compose instead** (default OFF) skips preservation for that
deployment. In service-only deployments the other services keep their live values in
the override. The destination `compose.yaml` itself never contains the pinned numbers;
they live in `manager.override.yaml`, regenerated on each operation, so a later manual
`docker update` is picked up by the next one. Rollback re-creates containers with the
previous env wiring and their live limits.

## Deploying the manager itself, risks and limits

* The server needs `manager`, `manager-shipper` (new image) and the existing runners; it
  does not need `manager-builder` (profile `build`, never started without
  `builder.local.yaml`). No new volume is required.
* `manager-builder` and `manager-shipper` hold the Docker socket and read your source
  trees (read-only mounts still allow Docker API access): treat them as privileged.
  Source mounts are read-only; the build context is whatever is on disk, including
  untracked files that are not in `.dockerignore`.
* The env store holds plaintext values on the destination (as `.env` always did). Pending
  edits and the env of a build job are kept in the private SQLite plan (like the existing
  includeEnv bundles): protect and back up the manager volume. Staged env files are removed
  from the destination release directory when the script ends; it keeps the generated
  overrides and `preflight.err` (Compose warnings/errors) for inspection.
* A job interrupted by a manager restart is marked `interrupted` and never replayed; a
  build already in progress keeps running in the builder until Docker finishes it.
* Cross-system atomicity is best effort: a crash between the destination write and the
  audit record can leave a record `intent` without a result. Inspect before retrying.
* Local builds can differ from CI builds (host-specific files, untracked files). The
  `-dirty<epoch>` tag makes this visible. BuildKit cache is shared with the host Docker.
* Apply env estimates affected services from `env_file` references; variables used only
  for Compose interpolation (`${VAR}` in a hand-written compose.yaml) are not tracked.


## Automatic local build → SSH destination deployment

In **دیپلوی پروژه → ساخت محلی و دیپلوی خودکار با SSH**, the local manager builds
an owner-registered working tree, streams the immutable images over its existing
pinned-host SSH transport, and asks the destination manager to provision/deploy it.
No manual image build, archive upload, commit, push or image registry is required.
The destination must run this Manager version, including `manager-storefront` and
`scripts/project-command.py`. Register the project read-only in the ignored
`manager/builder.local.yaml` for both builder and shipper, then start the builder
with `docker compose --profile build -f compose.yaml -f manager/builder.local.yaml up -d manager-builder manager-shipper`.
A project Dockerfile must build all its own assets; host-side prebuild hooks are not run.

Configure **ارسال و Deploy → تنظیم مقصد سراسری** once (SSH host, private-key file
already installed in `manager/ssh`, trusted `known_hosts`, and actual infra/apps/data
paths). For the current server these paths are `/srv/infra`, `/srv/apps`, `/srv/data`.
In the project form choose the public domain, HTTP service/port and optional migration
service. Enter intentional destination env edits before previewing. The review shows
names of env edits only. Confirming runs build, image-ID verification, shared-resource
provisioning on the first deployment, migration, Compose health checks and nginx routing.
The domain is saved on the destination and reused on later builds; it remains editable.

Container limits are always taken from the destination immediately before recreation:
memory, swap/reservation, CPU, CPU shares/affinity and PID limit. New services use the
bundle limits. A failed rollout restores the last healthy containers and runtime env
while retaining the destination's current limits. Database migrations are never
rolled back automatically. A restart interrupts in-flight jobs rather than replaying
provisioning or migrations blindly. Inspect the recorded phase before retrying.

The destination stores configuration under `<APPS_DIR>/<app>/.manager/project.json`,
app overrides in `.runtime.env` (0600), and validated candidate/last healthy manifests
under `.manager/`. The shared `.env` is preserved. Env values are never returned in
catalogs, previews or audit events. Deleting an override exposes the underlying
shared/default value again. Explicit defaults in Compose are overridden by intentional
app env edits; dollar signs and quotes remain literal.

### Same-host management and optional offline package

**دیپلوی پروژه → پروژه‌های همین سرور / بستهٔ آماده** edits domain/env of existing
applications without requiring their source. Saving settings changes nginx after a
successful `nginx -t`; env edits take effect on the next explicit deploy. Duplicate
domains, reserved project names, host mounts outside project data, published ports,
privileged containers and non-project images are rejected. Existing source-less
projects retain their original env files and receive app overrides last.

For an optional offline upload, first build the immutable images, then run in infra:

```sh
make manager-package PROJECT_DIR=/path/to/project TAG=your-tag OUTPUT=/tmp/project.tar
```

The exclusive-created 0600 tar contains only `project.json` and `images.tar`; local env,
source code and application/database data are excluded. The authenticated streaming
upload is limited to 2 GiB. Uploads stage images and do not deploy them. Upload staging
and persistent operation journals live in the existing `storefront-jobs` volume under
`/data/projects`; keep that volume when updating Manager. A package with a conflicting
immutable image tag is rejected. Both classic Docker and containerd OCI image IDs are
verified. Nginx's upload location uses `snippets/manager-upload.conf` with the usual
forwarding headers and longer streaming timeouts.

Verification:

```sh
python3 -m unittest discover -s scripts -p test_project_deployments.py -v
make manager-check
docker compose exec -T manager-storefront python3 - < manager/tests/project-deployment-smoke.py
```

The smoke test uses only the disposable `manager-deploy-check` application, verifies
literal env, domain edits, live CPU/memory/PID retention and rollback, and cleans it up.
It does not create shared database users or touch application data.

On the current destination, shared nginx listens on host port **8088** (container
port 80). Configure the CDN's HTTP origin as `185.105.239.9:8088`; host port 80
belongs to a separate existing application and must not be repurposed. The manager
public endpoint is `https://pourydev.ir`.

The shipper's writable tmpfs is `/tmp/shipper`; `TMPDIR` must point there in Compose.
Image verification compares Docker IDs when the stores agree, otherwise a SHA-256
identity over platform, complete runtime config and all RootFS layers. This permits
classic/containerd transfers while rejecting changes to config or filesystem data.

Automatic project previews add a random suffix to the source tag, including clean
commits, so env/build-arg changes never reuse a rollback tag. Shipping refuses to
overwrite an existing destination tag with different runtime content. Optional
real-application transfer verification is enabled with `MANAGER_NATIVE_TEST_IMAGE`
and the explicit `MANAGER_NATIVE_TEST_TARGET` JSON when using
`scripts/manager.sh native-integration -test.run TestActualApplicationScratchImageTransfer`.

Domain edits preserve existing API routes, aliases and custom nginx directives.
Automatic launches stage the route until the containers are healthy. A change
to the HTTP service/port is also staged when editing an existing project.
A bounded same-network HTTP check through nginx must pass before success is
recorded. Rollback restores and verifies the last healthy nginx route as well
as the containers and runtime env. A failed first release is never promoted
to the healthy rollback snapshot.

### Deployment progress and required configuration

The build/ship view shows confirmed pipeline stages, not a timer-based percentage.
BuildKit `rawjson` reports completed/discovered vertices (the discovered total can grow).
SSH transfer reports bytes consumed from `docker save`; the total archive size is
unknown, so that stage remains indeterminate until the remote image is verified.
Failed jobs retain their completed stages and mark the active stage as failed.

A service may declare `infra.manager.required-env: APP_KEY,PUBLIC_URL` in its Compose
labels. The destination resolves its own environment and checks these names before
migrations or service recreation. Only missing names are reported; values remain private.
The local builder uses `DOCKER_CONFIG=/tmp/docker` on its existing tmpfs so Buildx
can write client metadata while the root filesystem remains read-only.


## Shared outbound VPN client (Xray)

Open **اتصال VPN** in Manager, paste a single `vless://`, `vmess://`, `trojan://`
or `ss://` link, then choose **ذخیره و اعمال**. **تست اتصال VPN** sends an HTTPS
request through the proxy to Google's fixed 204 endpoint. Running means the core
is listening; only a successful test verifies external reachability. Subscription
URLs and insecure TLS validation bypasses are rejected. Supported transports are
RAW/TCP, WebSocket, gRPC, XHTTP and HTTPUpgrade, with TLS or compatible REALITY.
Unsupported link options are rejected instead of silently discarded.

The `xray` service uses the existing `ghcr.io/xtls/xray-core:26.10.31` binary
through the `vpn` target of `manager/Dockerfile`. Set `XRAY_IMAGE` in the root
`.env` to change it; `make manager-image` / `make manager-up` build it. No Docker
socket or elevated network capabilities are needed. The broker API is internal,
token authenticated, and exposed to users only through Manager's authenticated,
CSRF-protected endpoints. The private `infra_xray-data` volume stores the generated
configuration with mode `0600`; include it in protected backups. The original
link is never returned to the browser or audit log. Configuration is tested by
the actual core before replacement; a failed start or write restores the previous
runtime. State survives container recreation, and a crashed core is restarted.
Disabling keeps the saved configuration but closes proxy listeners.

Opt in **per service** on the `infra` network, using Manager's environment editor
(select the service scope, `env.d/<service>.env`) or that service's Compose
definition, then apply/recreate that service:

```dotenv
HTTP_PROXY=http://xray:10809
HTTPS_PROXY=http://xray:10809
ALL_PROXY=socks5h://xray:10808
NO_PROXY=localhost,127.0.0.1,::1,pgbouncer,postgres,redis,rabbitmq,otel-collector
```

Add the project's internal hostnames/domains to `NO_PROXY`; set lowercase variants
if the application's HTTP library requires them. The application must support
proxy settings. Other services keep their current egress path. These settings do
not change host routing, Docker daemon image pulls or Docker build networking.
For host tools use `http://127.0.0.1:10809` or `socks5h://127.0.0.1:10808`;
`XRAY_HTTP_PORT` and `XRAY_SOCKS_PORT` override these loopback ports. SOCKS UDP is
available within Docker, but is not published on the host. The service starts
unconfigured with no proxy listener; the Manager remains usable before import.

Run the deterministic real-core integration test (local VMess server and HTTP
fixture, no real link or internet required) with `make -C manager vpn-integration`.
It verifies traffic, rejected changes, disabling, persistence and crash recovery.
