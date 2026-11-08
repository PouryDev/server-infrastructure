#!/usr/bin/env bash
# One-time (and safe to re-run) preparation of the host:
#   - creates .env from .env.example with random secrets
#   - creates the data directories with the uid/gid each container runs as
#   - creates the Redis ACL file
#
# Needs root only to chown the data dirs. Without root/passwordless sudo it chowns through a
# throw-away container instead, so a developer machine works too.
#
# Optional overrides when .env is first created (e.g. for a local development machine):
#   INFRA_DATA_DIR=$HOME/infra-local/data INFRA_APPS_DIR=$HOME/infra-local/apps scripts/init-host.sh
set -euo pipefail
cd "$(dirname "$0")/.."

if [ ! -f .env ]; then
  while IFS= read -r line; do
    if [[ "$line" =~ ^([A-Z0-9_]+)=CHANGE_ME$ ]]; then
      echo "${BASH_REMATCH[1]}=$(openssl rand -hex 20)"
    elif [[ "$line" =~ ^DATA_DIR= && -n "${INFRA_DATA_DIR:-}" ]]; then
      echo "DATA_DIR=$INFRA_DATA_DIR"
    elif [[ "$line" =~ ^APPS_DIR= && -n "${INFRA_APPS_DIR:-}" ]]; then
      echo "APPS_DIR=$INFRA_APPS_DIR"
    else
      echo "$line"
    fi
  done < .env.example > .env
  chmod 600 .env
  echo "created .env with generated secrets (grafana admin password: grep GRAFANA_ADMIN_PASSWORD .env)"
fi

set -a; . ./.env; set +a
: "${DATA_DIR:?}" "${APPS_DIR:?}"

HELPER_IMAGE="${HELPER_IMAGE:-alpine:3.20}"
SUDO=""
HELPER=0
if [ "$(id -u)" -ne 0 ]; then
  if sudo -n true 2>/dev/null; then SUDO="sudo"; else HELPER=1; fi
fi

# own <dir> <uid:gid> <mode>: create dir, set owner and mode
own() {
  if [ "$HELPER" -eq 1 ]; then
    mkdir -p "$1"
    docker run --rm -v "$1:/t" "$HELPER_IMAGE" sh -c "chown $2 /t && chmod $3 /t"
  else
    $SUDO mkdir -p "$1"; $SUDO chown "$2" "$1"; $SUDO chmod "$3" "$1"
  fi
}

mk() { own "$1" "$2" "${3:-750}"; }

mk "$DATA_DIR"              "$(id -u):$(id -g)" 755
mk "$DATA_DIR/postgres"     70:70       700
mk "$DATA_DIR/redis"        999:1000    750
mk "$DATA_DIR/rabbitmq"     100:101     750
mk "$DATA_DIR/prometheus"   65534:65534
mk "$DATA_DIR/alertmanager" 65534:65534
mk "$DATA_DIR/grafana"      472:0
mk "$DATA_DIR/tempo"        10001:10001
mk "$DATA_DIR/apps"         "$(id -u):$(id -g)" 755
mk "$APPS_DIR"              "$(id -u):$(id -g)" 750

if [ ! -e "$DATA_DIR/redis/users.acl" ]; then
  if [ "$HELPER" -eq 1 ]; then
    docker run --rm -v "$DATA_DIR/redis:/t" "$HELPER_IMAGE" sh -c 'touch /t/users.acl && chown 999:1000 /t/users.acl && chmod 600 /t/users.acl'
  else
    $SUDO install -o 999 -g 1000 -m 600 /dev/null "$DATA_DIR/redis/users.acl"
  fi
fi

echo "ok. next: make up"
