# Grafana: dashboards of storefront

The infrastructure gives this project its own Grafana **org** `storefront` (switch org in the profile menu). Its data sources are
filtered to this project at the Prometheus proxy, so nobody in the org can read another project's metrics.
It already holds a generated set: Overview, HTTP API, Database, Notifications, Go runtime, Containers & uptime,
**Traces & latency** (how long every request takes, where the time goes, and the traces themselves) and Custom metrics (a browser for the project's own metrics). Nothing to do for those.

## Your own dashboards

Two ways, mix them freely:

1. **In Grafana**: in the org, New dashboard, save. Stored in Grafana, editable, never overwritten. (The generated ones are
   overwritten on each infrastructure run: "Save as" to keep a variant.)
2. **As code** (versioned, what an assistant should do): put dashboard JSON in this directory. Start from `_example.json` (files starting with `_`
   are templates and are never imported): copy it, rename it for its purpose (`orders.json`), replace the example metrics with yours.
   Then `python3 scripts/grafana-lint.py deploy/grafana` (offline; fix every ERROR) and ask the infrastructure owner to run, in the infrastructure repo,
   `make project-dashboards-import PROJECT=storefront DIR=<path to this directory>`. It lints again and pushes the files into the org as `custom-*.json`;
   uids are rewritten to `p-storefront-c-<file>` so projects never collide. This directory is the source of truth for imported dashboards: delete a file,
   rerun, the dashboard is removed. Rerun after every change.

Rules for the JSON (the linter enforces them): data sources only by uid, `prometheus` (this app's metrics, default), `prometheus-infra` (its containers and
uptime probes), `tempo` (its traces); no `${DS_...}` placeholders (export WITHOUT "Export for sharing externally"); no project filter is needed because the
data sources already return only this project. The full contract is the "Dashboards and your own metrics" section of `AGENTS.md`.

## Your own metrics

Create instruments with the OpenTelemetry meter (`otel.Meter("storefront").Int64Counter("orders.created")`). They reach
Prometheus with `job="storefront/<service>"`, `orders.created` becoming `orders_created_total`. The **Custom metrics**
dashboard lists them as soon as they are exported; build panels on them in your own dashboards. Filter by
`job=~"storefront/.*"` so a panel never mixes in other projects.

## Access

`make project-access PROJECT=storefront LOGIN=someone` (infrastructure repo) creates an Editor login that belongs to this org only.
Traces are isolated too (own Tempo tenant); there is no log backend.
