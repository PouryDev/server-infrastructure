#!/usr/bin/env bash
# Runs mizan-host/ - a standalone host application whose only job is to run the financial module (mizan) behind real
# JWT authentication - entirely inside Docker: nothing Go- or Node-related runs on the host.
#
#   scripts/mizan-host.sh up                  PostgreSQL 18 + migrations + API + the accounting UI -> http://localhost:8090
#   scripts/mizan-host.sh down | reset        stop (keep data) | stop and delete the database
#   scripts/mizan-host.sh logs [svc] | ps
#   scripts/mizan-host.sh user-create <username> [admin|finance|user] [display name]
#                                             USER_PASSWORD=... sets the password, otherwise a random one is printed once
#   scripts/mizan-host.sh user-passwd <username> | user-list
#   scripts/mizan-host.sh seed                sample data (journals over two fiscal years, invoices, wallet, cheques...); skipped when journals exist.
#                                             Needs the admin password in mizan-host/.admin-password, or SEED_USERNAME / SEED_PASSWORD
#   scripts/mizan-host.sh ui                  (re)build mizan-ui and copy it into mizan-host/web (done by `up` when missing)
#   scripts/mizan-host.sh sync                refresh mizan-host/.kit and .mizan from ../gokit and ../mizan
#   scripts/mizan-host.sh test | check | integration
#   scripts/mizan-host.sh make <target...>    any target of mizan-host/Makefile
#
# The first `up` creates the user "admin" with a random password, printed once and kept in mizan-host/.admin-password
# (git-ignored, mode 600). Export HTTPS_PROXY=http://127.0.0.1:2020 when something has to be downloaded.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOST="$ROOT/mizan-host"
[ -f "$ROOT/.env" ] && REGISTRY="${REGISTRY:-$(grep -E '^REGISTRY=' "$ROOT/.env" | cut -d= -f2- || true)}"
export REGISTRY="${REGISTRY:-docker.arvancloud.ir}"
GO_IMAGE="${GO_IMAGE:-$REGISTRY/library/golang:1.26-alpine}"
DB_IMAGE="${DB_IMAGE:-$REGISTRY/postgres:18.6-alpine}"
export GO_IMAGE DB_IMAGE
VOL="${GO_CACHE_VOLUME:-gokit-cache}"
GOPROXY_DEFAULT="file:///go/pkg/mod/cache/download,https://proxy.golang.org,direct"
COMPOSE=(docker compose -f "$HOST/compose.yaml" --project-directory "$HOST")

die() { echo "error: $*" >&2; exit 1; }
command -v docker >/dev/null || die "docker is required"
mh_make() { make -C "$HOST" "$@"; }

docker_go() { # docker_go <docker args> -- <command...>
  local extra=()
  while [ "$1" != "--" ]; do extra+=("$1"); shift; done; shift
  local net=()
  if [ -n "${HTTPS_PROXY:-}${HTTP_PROXY:-}" ]; then
    extra+=(-e "HTTPS_PROXY=${HTTPS_PROXY:-${HTTP_PROXY:-}}" -e "HTTP_PROXY=${HTTP_PROXY:-${HTTPS_PROXY:-}}" -e "NO_PROXY=${NO_PROXY:-}")
    case "${HTTPS_PROXY:-}${HTTP_PROXY:-}" in *127.0.0.1*|*localhost*) net=(--network host) ;; esac
  fi
  docker run --rm "${net[@]}" -u "$(id -u):$(id -g)" -v "$VOL:/go" \
    -e HOME=/tmp -e CGO_ENABLED=0 -e GOCACHE=/go/.buildcache -e GOTOOLCHAIN=local \
    -e "GOPROXY=${GOPROXY:-$GOPROXY_DEFAULT}" -e "GOFLAGS=${GOFLAGS:-}" \
    "${extra[@]}" "$@"
}

build_ui() {
  "$ROOT/scripts/mizan-ui.sh" build
  mkdir -p "$HOST/web"
  rsync -a --delete --exclude .gitkeep "$ROOT/mizan-ui/dist/" "$HOST/web/"
  echo "UI copied to mizan-host/web"
}

wait_api() { # first user, once
  if [ -z "$("${COMPOSE[@]}" run --rm --no-deps --entrypoint /user api list 2>/dev/null | tr -d '[:space:]')" ]; then
    local out pw
    out="$("${COMPOSE[@]}" run --rm --no-deps -T --entrypoint /user api create --username admin --role admin --name "مدیر سیستم" --title "مدیر")"
    pw="$(sed -n 's/^generated password (shown once): //p' <<<"$out")"
    [ -n "$pw" ] || die "could not create the first admin: $out"
    umask 077; printf '%s\n' "$pw" > "$HOST/.admin-password"
    echo "first admin created: admin / $pw   (kept in mizan-host/.admin-password)"
    "$0" seed
  fi
}

cmd="${1:-}"; [ $# -gt 0 ] && shift || true
case "$cmd" in
  up)
    mh_make prepare
    [ -f "$HOST/web/index.html" ] || build_ui
    mh_make vendor
    docker build --pull=false --build-arg "GO_IMAGE=$GO_IMAGE" -t mizan-host-api:local "$HOST"
    "${COMPOSE[@]}" up -d --wait
    wait_api
    echo; echo "UI + API: http://localhost:${HOST_PORT:-8090}   (API for scripts: POST /auth/token, then Authorization: Bearer; routes under /accounting and /v1)"
    ;;
  down)  "${COMPOSE[@]}" down ;;
  reset) "${COMPOSE[@]}" down -v; rm -f "$HOST/.admin-password" ;;
  ps)    "${COMPOSE[@]}" ps ;;
  logs)  "${COMPOSE[@]}" logs --tail 100 -f "$@" ;;
  ui)    build_ui ;;
  sync)
    "$ROOT/scripts/gokit.sh" sync "$HOST"
    rsync -a --delete --exclude cmd --exclude docs --exclude README.md "$ROOT/mizan/" "$HOST/.mizan/"
    mh_make tidy ;;
  user-create)
    [ $# -ge 1 ] || die "usage: mizan-host.sh user-create <username> [admin|finance|user] [display name]"
    "${COMPOSE[@]}" up -d --wait db
    "${COMPOSE[@]}" run --rm --no-deps -T -e USER_PASSWORD --entrypoint /user api create --username "$1" --role "${2:-admin}" --name "${3:-$1}" ;;
  user-passwd)
    [ $# -ge 1 ] || die "usage: mizan-host.sh user-passwd <username>"
    "${COMPOSE[@]}" run --rm --no-deps -T -e USER_PASSWORD --entrypoint /user api set-password --username "$1" ;;
  seed)
    [ -n "${SEED_PASSWORD:-}" ] || { [ -f "$HOST/.admin-password" ] && SEED_PASSWORD="$(cat "$HOST/.admin-password")"; }
    [ -n "${SEED_PASSWORD:-}" ] || die "set SEED_PASSWORD (and SEED_USERNAME) or keep mizan-host/.admin-password"
    "${COMPOSE[@]}" run --rm --no-deps -T -e SEED_PASSWORD="$SEED_PASSWORD" -e SEED_USERNAME="${SEED_USERNAME:-admin}" -e API_URL=http://api:8080 --entrypoint /seed api ;;
  user-list) "${COMPOSE[@]}" run --rm --no-deps -T --entrypoint /user api list ;;
  test)  mh_make test ;;
  check) mh_make check ;;
  make)  mh_make "$@" ;;
  integration) # host + mizan schema, login, JWT, journal posting against a throw-away PostgreSQL
    mh_make prepare
    itnet="mizan-host-it-$$"; itpg="$itnet-pg"
    cleanup() { docker rm -f "$itpg" >/dev/null 2>&1 || true; docker network rm "$itnet" >/dev/null 2>&1 || true; }
    trap cleanup EXIT
    docker network create "$itnet" >/dev/null
    docker run -d --name "$itpg" --network "$itnet" -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=t "$DB_IMAGE" >/dev/null
    for _ in $(seq 60); do docker exec "$itpg" pg_isready -U postgres -d t >/dev/null 2>&1 && break; sleep 1; done
    docker_go --network "$itnet" -v "$HOST:/src" -w /src \
      -e "DATABASE_URL=postgres://postgres:pw@$itpg:5432/t?sslmode=disable" \
      -- "$GO_IMAGE" go test -count=1 -p 1 -tags=integration ./... "$@"
    ;;
  *) sed -n '2,22p' "${BASH_SOURCE[0]}"; exit 1 ;;
esac
