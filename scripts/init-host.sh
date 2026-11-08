#!/usr/bin/env bash
# One-time (and safe to re-run) preparation of the server:
#   - creates .env from .env.example with random secrets
#   - creates the data directories with the uid/gid each container runs as
#   - creates the Redis ACL file
# Usage: scripts/init-host.sh            (uses sudo for chown when not root)
set -euo pipefail
cd "$(dirname "$0")/.."

if [ ! -f .env ]; then
  while IFS= read -r line; do
    if [[ "$line" =~ ^([A-Z0-9_]+)=CHANGE_ME$ ]]; then
      echo "${BASH_REMATCH[1]}=$(openssl rand -hex 20)"
    else
      echo "$line"
    fi
  done < .env.example > .env
  chmod 600 .env
  echo "created .env with generated secrets (grafana admin password: grep GRAFANA_ADMIN_PASSWORD .env)"
fi

set -a; . ./.env; set +a
: "${DATA_DIR:?}" "${APPS_DIR:?}"

SUDO=""
[ "$(id -u)" -eq 0 ] || SUDO="sudo"

mk() { # mk <dir> <uid:gid> [mode]
  $SUDO mkdir -p "$1"
  $SUDO chown "$2" "$1"
  $SUDO chmod "${3:-750}" "$1"
}

mk "$DATA_DIR"              "root:root" 755
mk "$DATA_DIR/postgres"     70:70       700
mk "$DATA_DIR/redis"        999:1000    750
mk "$DATA_DIR/rabbitmq"     100:101     750
mk "$DATA_DIR/prometheus"   65534:65534
mk "$DATA_DIR/alertmanager" 65534:65534
mk "$DATA_DIR/grafana"      472:0
mk "$DATA_DIR/tempo"        10001:10001
mk "$DATA_DIR/apps"         "root:root" 755
mk "$APPS_DIR"              "$(id -u):$(id -g)" 750

if [ ! -e "$DATA_DIR/redis/users.acl" ]; then
  $SUDO install -o 999 -g 1000 -m 600 /dev/null "$DATA_DIR/redis/users.acl"
fi

echo "ok. next: docker compose up -d"
