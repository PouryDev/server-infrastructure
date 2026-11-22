#!/usr/bin/env bash
# Keeps the integration contract in sync:
#   scripts/sync-agents-docs.sh            rebuild docs/project-integration/skill/server-infra/SKILL.md from AGENTS.md
#   scripts/sync-agents-docs.sh <dir>...   also copy AGENTS.md + scripts/grafana-lint.py into each project and
#                                          deploy/grafana/_example.json (never overwrites the project's own dashboards or README)
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCS="$ROOT/docs/project-integration"
SKILL="$DOCS/skill/server-infra/SKILL.md"

{
  cat <<'FRONT'
---
name: server-infra
description: Use when deploying a project on the shared server or connecting it to the shared infrastructure (compose file, ship.conf, PostgreSQL via PgBouncer, Redis, RabbitMQ, OpenTelemetry, nginx behind the CDN, health checks), and when adding the project's own metrics or Grafana dashboards (OpenTelemetry meter, deploy/grafana/*.json, scripts/grafana-lint.py, the project's isolated Grafana org, traces). Defines how a project plugs into the infrastructure, not how the project itself is built.
---

FRONT
  cat "$DOCS/AGENTS.md"
} > "$SKILL"
echo "rebuilt $SKILL"

for d in "$@"; do
  d="${d/#\~/$HOME}"
  [ -d "$d" ] || { echo "skip $d (not a directory)" >&2; continue; }
  cp "$DOCS/AGENTS.md" "$d/AGENTS.md"
  mkdir -p "$d/scripts" "$d/deploy/grafana"
  cp "$ROOT/scripts/grafana-lint.py" "$d/scripts/grafana-lint.py"
  cp "$DOCS/grafana-example.json" "$d/deploy/grafana/_example.json"
  echo "updated $d (AGENTS.md, scripts/grafana-lint.py, deploy/grafana/_example.json)"
done
