#!/usr/bin/env bash
# Storefront lifecycle. All builds/tests go through make and Docker.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
case "${1:-}" in
  check) make -C storefront check build; docker run --rm -v "$ROOT/storefront:/src:ro" -w /src docker.arvancloud.ir/library/node:22-alpine node --check internal/adapter/web/assets/app.js ;;
  integration)
    set -a; . "$ROOT/.env"; set +a
    test -f "$APPS_DIR/shoptest/.env" || { echo 'Create shoptest before integration tests' >&2; exit 1; }
    docker run --rm --network infra -u "$(id -u):$(id -g)" -v "$ROOT/storefront:/src" -v gokit-cache:/go -w /src --env-file "$APPS_DIR/shoptest/.env" -e GOCACHE=/go/.buildcache -e CGO_ENABLED=0 -e GOPROXY=file:///go/pkg/mod/cache/download,off docker.arvancloud.ir/library/golang:1.26-alpine go test -count=1 -tags integration ./internal/adapter/store ;;
  image) make -C storefront image TAG="${STOREFRONT_TAG:-$(git rev-parse --short=10 HEAD)-$(date +%s)}" ;;
  provision) exec python3 scripts/storefront-provision.py ;;
  *) echo 'usage: scripts/storefront.sh check|image|provision' >&2; exit 1 ;;
esac
