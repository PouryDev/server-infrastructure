#!/usr/bin/env bash
# Runs everything about the Go kit (gokit/) inside Docker: nothing Go-related runs on the host.
#
#   scripts/gokit.sh test | vet | fmt | lint | check | integration | bench   work on the kit itself
#   scripts/gokit.sh go <args>                                            any go command in the kit (go get, go mod tidy...)
#   scripts/gokit.sh new <dir> [--module M] [--kit-mode remote ...]       scaffold a project (see gokit/README.md)
#   scripts/gokit.sh sync <dir>                                           refresh <dir>/.kit from this kit
#   scripts/gokit.sh jwt-key [--hs256]                                    print new JWT key material
#
# Go modules live in the shared Docker volume $GO_CACHE_VOLUME (default gokit-cache), so a module is
# downloaded once for all projects. Lookups try that cache first and the network only as a fallback; if the
# network needs a proxy, export HTTPS_PROXY (for example HTTPS_PROXY=http://127.0.0.1:2020).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KIT="$ROOT/gokit"
[ -f "$ROOT/.env" ] && REGISTRY="${REGISTRY:-$(grep -E '^REGISTRY=' "$ROOT/.env" | cut -d= -f2- || true)}"
REGISTRY="${REGISTRY:-docker.arvancloud.ir}"
GO_IMAGE="${GO_IMAGE:-$REGISTRY/library/golang:1.26-alpine}"
DB_IMAGE="${DB_IMAGE:-$REGISTRY/postgres:18.6-alpine}"
REDIS_IMAGE="${REDIS_IMAGE:-$REGISTRY/redis:8.10.3-alpine}"
MQ_IMAGE="${MQ_IMAGE:-$REGISTRY/rabbitmq:4.3.7-management-alpine}"
PGBOUNCER_IMAGE="${PGBOUNCER_IMAGE:-$REGISTRY/edoburu/pgbouncer:v1.26.1-p0}"
LINT_IMAGE="${LINT_IMAGE:-$REGISTRY/golangci/golangci-lint:latest-alpine}"
VOL="${GO_CACHE_VOLUME:-gokit-cache}"
GOPROXY_DEFAULT="file:///go/pkg/mod/cache/download,https://proxy.golang.org,direct"

die() { echo "error: $*" >&2; exit 1; }
command -v docker >/dev/null || die "docker is required"

prepare_volume() {
  docker volume inspect "$VOL" >/dev/null 2>&1 || docker volume create "$VOL" >/dev/null
  docker run --rm -u 0 --entrypoint sh -v "$VOL:/go" "$GO_IMAGE" -c \
    'test -f /go/.gokit-perms || { mkdir -p /go/.buildcache && chmod -R a+rwX /go && touch /go/.gokit-perms; }'
}

# docker_go <extra docker args...> -- <command...>
docker_go() {
  local extra=()
  while [ "$1" != "--" ]; do extra+=("$1"); shift; done; shift
  local net=()
  if [ -n "${HTTPS_PROXY:-}${HTTP_PROXY:-}" ]; then
    extra+=(-e "HTTPS_PROXY=${HTTPS_PROXY:-${HTTP_PROXY:-}}" -e "HTTP_PROXY=${HTTP_PROXY:-${HTTPS_PROXY:-}}" -e "NO_PROXY=${NO_PROXY:-}")
    case "${HTTPS_PROXY:-}${HTTP_PROXY:-}" in *127.0.0.1*|*localhost*) net=(--network host) ;; esac
  fi
  docker run --rm "${net[@]}" -u "$(id -u):$(id -g)" -v "$VOL:/go" \
    -e HOME=/tmp -e CGO_ENABLED=0 -e GOCACHE=/go/.buildcache -e GOTOOLCHAIN=local \
    -e "GOPROXY=${GOPROXY:-$GOPROXY_DEFAULT}" -e "GOFLAGS=${GOFLAGS:-}" ${GOSUMDB:+-e "GOSUMDB=$GOSUMDB"} \
    "${extra[@]}" "$@"
}

cmd="${1:-}"; [ $# -gt 0 ] && shift || true
case "$cmd" in
  test)  prepare_volume; docker_go -v "$KIT:/src" -w /src -- "$GO_IMAGE" go test -count=1 ./... ;;
  vet)   prepare_volume; docker_go -v "$KIT:/src" -w /src -- "$GO_IMAGE" go vet ./... ;;
  fmt)   prepare_volume; docker_go -v "$KIT:/src" -w /src -- "$GO_IMAGE" gofmt -w . ;;
  lint)  prepare_volume
         docker_go -v "$KIT:/src" -w /src -e GOLANGCI_LINT_CACHE=/go/.lintcache -- "$LINT_IMAGE" golangci-lint run ./... ;;
  check) "$0" fmt; "$0" vet; "$0" lint; "$0" test ;;
  integration|bench-db)  # kit tests / benchmarks that need PostgreSQL, against a throw-away container
    prepare_volume
    itnet="gokit-it-$$"; itpg="$itnet-pg"; itredis="$itnet-redis"; itmq="$itnet-mq"; itpgb="$itnet-pgb"
    cleanup() { docker rm -f "$itpg" "$itredis" "$itmq" "$itpgb" >/dev/null 2>&1 || true; docker network rm "$itnet" >/dev/null 2>&1 || true; }
    trap cleanup EXIT
    docker network create "$itnet" >/dev/null
    docker run -d --name "$itpg" --network "$itnet" -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=t "$DB_IMAGE" >/dev/null
    docker run -d --name "$itredis" --network "$itnet" "$REDIS_IMAGE" >/dev/null
    [ "$cmd" = bench-db ] || docker run -d --name "$itmq" --network "$itnet" "$MQ_IMAGE" >/dev/null
    for _ in $(seq 60); do docker exec "$itpg" pg_isready -U postgres -d t >/dev/null 2>&1 && break; sleep 1; done
    # A PgBouncer configured like the shared infrastructure's (transaction pooling, prepared statements on).
    docker run -d --name "$itpgb" --network "$itnet" -e "DATABASE_URL=postgres://postgres:pw@$itpg:5432" \
      -e AUTH_TYPE=scram-sha-256 -e POOL_MODE=transaction -e MAX_PREPARED_STATEMENTS=200 -e DEFAULT_POOL_SIZE=10 \
      -e MAX_CLIENT_CONN=500 -e MAX_DB_CONNECTIONS=25 "$PGBOUNCER_IMAGE" >/dev/null
    for _ in $(seq 30); do docker exec "$itpg" pg_isready -h "$itpgb" -p 5432 >/dev/null 2>&1 && break; sleep 1; done
    if [ "$cmd" != bench-db ]; then
      for _ in $(seq 90); do docker exec "$itmq" rabbitmq-diagnostics -q ping >/dev/null 2>&1 && break; sleep 1; done
    fi
    gotest=(go test -count=1 ./postgres ./migrate ./orm/... ./redisx ./amqpx ./notify/...)
    [ "$cmd" = bench-db ] && gotest=(go test -run '^$' -bench "${BENCH:-.}" -benchmem -benchtime "${BENCHTIME:-1s}" ./orm ./postgres ./redisx)
    docker_go --network "$itnet" -v "$KIT:/src" -w /src \
      -e "GOKIT_TEST_DATABASE_URL=postgres://postgres:pw@$itpg:5432/t?sslmode=disable" \
      -e "GOKIT_TEST_PGBOUNCER_URL=postgres://postgres:pw@$itpgb:5432/t?sslmode=disable" \
      -e "GOKIT_TEST_REDIS_URL=redis://$itredis:6379/0" -e "GOKIT_TEST_AMQP_URL=amqp://guest:guest@$itmq:5672/" \
      -- "$GO_IMAGE" "${gotest[@]}"
    ;;
  go)    prepare_volume; docker_go -v "$KIT:/src" -w /src -- "$GO_IMAGE" go "$@" ;;   # any go command in the kit
  bench) prepare_volume; docker_go -v "$KIT:/src" -w /src -- "$GO_IMAGE" go test -run '^$' -bench "${BENCH:-.}" -benchmem "${@:-./...}" ;;
  jwt-key) prepare_volume; docker_go -v "$KIT:/kit:ro" -w /kit -- "$GO_IMAGE" go run ./cmd/gokit jwt-key "$@" ;;
  sync)
    [ $# -ge 1 ] || die "usage: gokit.sh sync <project dir>"
    dest="$(cd "$1" && pwd)"; [ -f "$dest/go.mod" ] || die "$dest is not a Go project"
    prepare_volume
    docker_go -v "$KIT:/kit:ro" -v "$dest:/project" -w /kit -- "$GO_IMAGE" go run ./cmd/gokit sync /project --kit-src /kit
    echo "now run in the project: make tidy"
    ;;
  new)
    [ $# -ge 1 ] || die "usage: gokit.sh new <dir> [--module M] [--kit-mode local|remote] ..."
    dest="$1"; shift
    dest="${dest/#\~/$HOME}"
    [ ! -e "$dest" ] || [ -z "$(ls -A "$dest" 2>/dev/null)" ] || die "$dest exists and is not empty"
    mkdir -p "$dest"; dest="$(cd "$dest" && pwd)"
    prepare_volume
    docker_go -v "$KIT:/kit:ro" -v "$dest:/project" -w /kit -- "$GO_IMAGE" \
      go run ./cmd/gokit new /project --name "$(basename "$dest")" --kit-src /kit \
      --go-image "$GO_IMAGE" --db-image "$DB_IMAGE" --lint-image "$LINT_IMAGE" --redis-image "$REDIS_IMAGE" "$@"
    # Files the infrastructure repo owns: the deploy script and the integration contract.
    mkdir -p "$dest/scripts"
    cp "$ROOT/scripts/ship.sh" "$dest/scripts/ship.sh"
    cp "$ROOT/docs/project-integration/AGENTS.md" "$dest/AGENTS.md"
    [ -d "$dest/.git" ] || git -C "$dest" init -q 2>/dev/null || true
    echo "resolving dependencies (go mod tidy)..."
    docker_go -v "$dest:/src" -w /src -- "$GO_IMAGE" go mod tidy
    cat <<MSG

Next steps:
  cd $dest
  make init && make up && make migrate-up      # then: curl -s localhost:8080/healthz
  make check                                   # fmt, vet, lint, tests
See $dest/README.md (layout, API, first deploy).
MSG
    ;;
  *) sed -n '2,10p' "${BASH_SOURCE[0]}"; exit 1 ;;
esac
