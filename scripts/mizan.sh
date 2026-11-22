#!/usr/bin/env bash
# Runs everything about the financial module (mizan/) inside Docker: nothing Go-related runs on the host.
#
#   scripts/mizan.sh test | vet | fmt | lint | check     unit tests, gofmt, vet, golangci-lint
#   scripts/mizan.sh integration                         tests against a throw-away PostgreSQL
#   scripts/mizan.sh go <args>                           any go command in mizan/ (go mod tidy, go get...)
#
# mizan depends on ../gokit through a `replace` directive, so the whole repository is mounted and the
# module cache is the shared Docker volume of the kit (see gokit.sh). Export HTTPS_PROXY when a new
# dependency has to be downloaded (HTTPS_PROXY=http://127.0.0.1:2020).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[ -f "$ROOT/.env" ] && REGISTRY="${REGISTRY:-$(grep -E '^REGISTRY=' "$ROOT/.env" | cut -d= -f2- || true)}"
REGISTRY="${REGISTRY:-docker.arvancloud.ir}"
GO_IMAGE="${GO_IMAGE:-$REGISTRY/library/golang:1.26-alpine}"
DB_IMAGE="${DB_IMAGE:-$REGISTRY/postgres:18.6-alpine}"
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
  test)  prepare_volume; docker_go -v "$ROOT:/src" -w /src/mizan -- "$GO_IMAGE" go test -count=1 ./... ;;
  vet)   prepare_volume; docker_go -v "$ROOT:/src" -w /src/mizan -- "$GO_IMAGE" go vet ./... ;;
  fmt)   prepare_volume; docker_go -v "$ROOT:/src" -w /src/mizan -- "$GO_IMAGE" gofmt -w . ;;
  lint)  prepare_volume
         docker_go -v "$ROOT:/src" -w /src/mizan -e GOLANGCI_LINT_CACHE=/go/.lintcache -- "$LINT_IMAGE" golangci-lint run ./... ;;
  check) "$0" fmt; "$0" vet; "$0" lint; "$0" test ;;
  go)    prepare_volume; docker_go -v "$ROOT:/src" -w /src/mizan -- "$GO_IMAGE" go "$@" ;;
  integration)
    prepare_volume
    itnet="mizan-it-$$"; itpg="$itnet-pg"
    cleanup() { docker rm -f "$itpg" >/dev/null 2>&1 || true; docker network rm "$itnet" >/dev/null 2>&1 || true; }
    trap cleanup EXIT
    docker network create "$itnet" >/dev/null
    docker run -d --name "$itpg" --network "$itnet" -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=t "$DB_IMAGE" >/dev/null
    for _ in $(seq 60); do docker exec "$itpg" pg_isready -U postgres -d t >/dev/null 2>&1 && break; sleep 1; done
    docker_go --network "$itnet" -v "$ROOT:/src" -w /src/mizan \
      -e "MIZAN_TEST_DATABASE_URL=postgres://postgres:pw@$itpg:5432/t?sslmode=disable" \
      -- "$GO_IMAGE" go test -count=1 -p 1 ./... "$@"
    ;;
  *) sed -n '2,9p' "${BASH_SOURCE[0]}"; exit 1 ;;
esac
