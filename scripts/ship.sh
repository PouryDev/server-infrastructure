#!/usr/bin/env bash
# Build images on THIS machine, ship them to the server over SSH (no registry), deploy with
# docker compose, and roll back automatically if the new version does not become healthy.
#
# Run from a project's repository root. Needs a ship.conf there (bash syntax):
#
#   APP=myapp                               # same name used with scripts/new-project.sh
#   SSH_HOST=deploy@my-server               # anything `ssh` accepts; "local" = deploy on this same machine
#                                           #   (infra running locally: no ssh/copy, REMOTE_DIR must be set)
#   COMPOSE_FILE=deploy/compose.yaml        # copied to the server as $REMOTE_DIR/compose.yaml
#   IMAGES=("api=./backend" "web=./client:./client/Dockerfile.prod")   # service=context[:dockerfile]
#   # optional: REMOTE_DIR=/srv/apps/$APP  PLATFORM=linux/amd64  KEEP_TAGS=3  BUILD_ARGS=("--build-arg" "X=y")
#
# Images are named  <APP>-<service>:<git-sha>  - the compose file must use
#   image: <APP>-<service>:${TAG}
#
# Usage: ship.sh [--no-build]   (--no-build re-deploys the tag of the current commit as-is)
set -euo pipefail

CONF="${SHIP_CONF:-./ship.conf}"
[ -f "$CONF" ] || { echo "missing $CONF (see header of $0)" >&2; exit 1; }
BUILD_ARGS=()
# shellcheck disable=SC1090
source "$CONF"
: "${APP:?APP not set in ship.conf}" "${SSH_HOST:?SSH_HOST not set}" "${IMAGES:?IMAGES not set}"
REMOTE_DIR="${REMOTE_DIR:-/srv/apps/$APP}"
COMPOSE_FILE="${COMPOSE_FILE:-deploy/compose.yaml}"
PLATFORM="${PLATFORM:-linux/amd64}"
KEEP_TAGS="${KEEP_TAGS:-3}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-120}"
[ -f "$COMPOSE_FILE" ] || { echo "compose file $COMPOSE_FILE not found" >&2; exit 1; }

TAG="$(git rev-parse --short=10 HEAD)"
if [ -n "$(git status --porcelain)" ]; then TAG="$TAG-dirty$(date +%s)"; echo "warning: uncommitted changes, tagging $TAG"; fi
BUILD=1; [ "${1:-}" = "--no-build" ] && BUILD=0

LOCAL=0; [ "$SSH_HOST" = local ] && LOCAL=1
[ "$LOCAL" -eq 0 ] || grep -q '^REMOTE_DIR=' "$CONF" \
  || { echo "SSH_HOST=local needs REMOTE_DIR in ship.conf (use APPS_DIR/<app> from the infra .env)" >&2; exit 1; }

remote() { # remote <shell command>
  if [ "$LOCAL" -eq 1 ]; then bash -c "$1"; else ssh -o BatchMode=yes -o ServerAliveInterval=30 "$SSH_HOST" "$1"; fi
}
remote_script() { # remote_script <args...>; the script comes on stdin
  if [ "$LOCAL" -eq 1 ]; then bash -s -- "$@"; else ssh -o BatchMode=yes -o ServerAliveInterval=30 "$SSH_HOST" bash -s -- "$@"; fi
}

for spec in "${IMAGES[@]}"; do
  svc="${spec%%=*}"; rest="${spec#*=}"; ctx="${rest%%:*}"; dockerfile=""
  [[ "$rest" == *:* ]] && dockerfile="${rest#*:}"
  img="$APP-$svc:$TAG"
  if [ "$BUILD" -eq 1 ]; then
    echo "==> build $img ($PLATFORM)"
    docker build --platform "$PLATFORM" ${dockerfile:+-f "$dockerfile"} "${BUILD_ARGS[@]}" -t "$img" "$ctx"
  fi
  if [ "$LOCAL" -eq 0 ]; then
    echo "==> ship $img"
    docker save "$img" | gzip -3 | remote 'gunzip | docker load' >/dev/null
  fi
done

echo "==> deploy $APP:$TAG on $([ "$LOCAL" -eq 1 ] && echo "this machine" || echo "$SSH_HOST")"
remote "mkdir -p '$REMOTE_DIR'"
if [ "$LOCAL" -eq 1 ]; then cp "$COMPOSE_FILE" "$REMOTE_DIR/compose.yaml"; else scp -q "$COMPOSE_FILE" "$SSH_HOST:$REMOTE_DIR/compose.yaml"; fi

# Runs on the server. Deploys $TAG; on failure restores the previous tag.
remote_script "$APP" "$REMOTE_DIR" "$TAG" "$KEEP_TAGS" "$WAIT_TIMEOUT" <<'REMOTE'
set -uo pipefail
APP="$1"; DIR="$2"; TAG="$3"; KEEP="$4"; WAIT="$5"
cd "$DIR"
PREV="$(cat .tag 2>/dev/null || true)"
up() { # up <tag>
  local envs=(); [ -f .env ] && envs+=(--env-file .env)
  echo "TAG=$1" > .tag.next
  docker compose -p "$APP" "${envs[@]}" --env-file .tag.next -f compose.yaml \
    up -d --wait --wait-timeout "$WAIT" --remove-orphans
}
if up "$TAG"; then
  echo "$TAG" > .tag; [ -n "$PREV" ] && [ "$PREV" != "$TAG" ] && echo "$PREV" > .tag.prev
  # Keep the newest $KEEP images per service, never the running ones.
  for repo in $(docker image ls --filter "reference=$APP-*" --format '{{.Repository}}' | sort -u); do
    docker image ls "$repo" --format '{{.Tag}}' | tail -n +"$((KEEP + 1))" | while read -r t; do
      docker rmi "$repo:$t" >/dev/null 2>&1 || true
    done
  done
  echo "deployed $TAG (previous: ${PREV:-none})"
else
  echo "!! $TAG did not become healthy" >&2
  docker compose -p "$APP" logs --tail 40 >&2 || true
  if [ -n "$PREV" ] && [ "$PREV" != "$TAG" ]; then
    echo "!! rolling back to $PREV" >&2
    up "$PREV" && echo "rolled back to $PREV" >&2
  fi
  exit 1
fi
REMOTE
