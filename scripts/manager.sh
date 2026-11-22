#!/usr/bin/env bash
# Standalone React/Go manager; all Go and Node commands run in Docker.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP="$ROOT/manager"
cd "$ROOT"
if [ -f .env ]; then REGISTRY="${REGISTRY:-$(sed -n 's/^REGISTRY=//p' .env)}"; fi
REGISTRY="${REGISTRY:-docker.arvancloud.ir}"
if [ -f .env ]; then XRAY_IMAGE="${XRAY_IMAGE:-$(sed -n 's/^XRAY_IMAGE=//p' .env)}"; fi
XRAY_IMAGE="${XRAY_IMAGE:-ghcr.io/xtls/xray-core:26.10.31}"
GO_IMAGE="${GO_IMAGE:-$REGISTRY/library/golang:1.26-alpine}"
NODE_IMAGE="${NODE_IMAGE:-$REGISTRY/library/node:22-alpine}"
GO_CACHE_VOLUME="${GO_CACHE_VOLUME:-gokit-cache}"

docker_run() {
  local options=()
  if [ -n "${HTTPS_PROXY:-}${HTTP_PROXY:-}" ]; then
    options+=(-e "HTTPS_PROXY=${HTTPS_PROXY:-${HTTP_PROXY:-}}" -e "HTTP_PROXY=${HTTP_PROXY:-${HTTPS_PROXY:-}}")
    case "${HTTPS_PROXY:-}${HTTP_PROXY:-}" in *127.0.0.1*|*localhost*) options+=(--network host) ;; esac
  fi
  docker run --rm "${options[@]}" -u "$(id -u):$(id -g)" -e HOME=/tmp "$@"
}
go_run() {
  docker_run -v "$APP:/src" -w /src -v "$GO_CACHE_VOLUME:/go" \
    -e CGO_ENABLED=0 -e GOCACHE=/go/.buildcache -e GOTOOLCHAIN=local -e GOFLAGS=-mod=mod \
    -e "GOPROXY=${GOPROXY:-file:///go/pkg/mod/cache/download,https://proxy.golang.org,direct}" \
    "$GO_IMAGE" "$@"
}
node_run() {
  docker_run -v "$APP/web:/app" -w /app -v manager-npm-cache:/npm-cache \
    -e npm_config_cache=/npm-cache -e CI=1 "$NODE_IMAGE" "$@"
}
prepare() {
  docker volume create "$GO_CACHE_VOLUME" >/dev/null
  docker volume create manager-npm-cache >/dev/null
  docker run --rm -u 0 -v "$GO_CACHE_VOLUME:/go" -v manager-npm-cache:/npm-cache "$GO_IMAGE" sh -c 'mkdir -p /go/.buildcache; chmod 1777 /go /go/.buildcache /npm-cache'
}
ensure_credentials() {
  if [ ! -f "$APP/.env" ]; then
    echo 'Manager credentials missing. Run: python3 scripts/manager-credentials.py (password is read privately).' >&2
    exit 1
  fi
}
cmd="${1:-help}"
case "$cmd" in
  install) prepare; node_run npm ci --no-audit --no-fund ;;
  npm) shift; prepare; node_run npm "$@" ;;
  tidy) prepare; go_run go mod tidy ;;
  fmt) prepare; go_run gofmt -w cmd internal ;;
  build) prepare; [ -d "$APP/web/node_modules" ] || "$0" install; node_run npm run build; go_run go build -trimpath -o bin/manager ./cmd/manager ;;
  image) mkdir -p "$APP/ssh" "$APP/resource-ssh" /tmp/infra-manager-target; "$0" build; go_run go mod vendor
      docker build --target app --build-arg "GO_IMAGE=$GO_IMAGE" -t infra-manager:local "$APP"
      docker build --target shipper --build-arg "GO_IMAGE=$GO_IMAGE" -t infra-manager-shipper:local "$APP"
      docker build --target resources --build-arg "GO_IMAGE=$GO_IMAGE" -t infra-manager-resources:local "$APP"
      docker build --target builder --build-arg "GO_IMAGE=$GO_IMAGE" -t infra-manager-builder:local "$APP"
      docker compose build xray manager-storefront ;;
  up) ensure_credentials; mkdir -p "$APP/ssh" "$APP/resource-ssh" /tmp/infra-manager-target; "$0" image
      # The local builder starts only when sources are registered in the ignored builder.local.yaml.
      files=(-f compose.yaml); services=(xray manager-storefront manager-runtime manager-shipper manager-resources manager)
      if [ -f "$APP/builder.local.yaml" ]; then files+=(-f "$APP/builder.local.yaml"); services+=(manager-builder); fi
      docker compose --profile build "${files[@]}" up -d --no-deps --wait "${services[@]}"
      docker compose restart manager-storefront
      docker compose exec -T nginx nginx -t
      docker compose exec -T nginx nginx -s reload ;;
  stop) docker compose --profile build stop manager manager-runtime manager-shipper manager-resources manager-builder ;;
  logs) docker compose --profile build logs --tail 100 -f manager xray manager-runtime manager-shipper manager-resources manager-builder ;;
  test) prepare; [ -d "$APP/web/node_modules" ] || "$0" install; go_run go test -count=1 ./...; node_run npm test ;;
  integration) prepare
      docker run --rm -u "$(id -u):$(id -g)" --group-add "$(stat -c '%g' /var/run/docker.sock)" -v "$APP:/src" -w /src -v "$GO_CACHE_VOLUME:/go" \
        -v /var/run/docker.sock:/var/run/docker.sock \
        -e CGO_ENABLED=0 -e GOCACHE=/go/.buildcache -e GOTOOLCHAIN=local \
        -e GOPROXY=file:///go/pkg/mod/cache/download,off \
        "$GO_IMAGE" go test -tags integration -count=1 -v -timeout 5m ./internal/integration ;;
  vpn-integration) prepare
      go_run go test -c -tags vpnintegration -o bin/vpn-integration ./internal/adapters/vpn
      docker run --rm --entrypoint /tests/vpn-integration \
        -v "$APP/bin/vpn-integration:/tests/vpn-integration:ro" \
        -e MANAGER_TEST_XRAY=/usr/local/bin/xray \
        "${XRAY_IMAGE:-ghcr.io/xtls/xray-core:26.10.31}" -test.v -test.timeout 45s ;;
  resources-integration) prepare
      go_run go test -c -tags resourceintegration -o bin/resources-integration ./internal/adapters/resources
      docker run --rm --entrypoint /src/bin/resources-integration \
        -v "$APP:/src:ro" -v /var/run/docker.sock:/var/run/docker.sock \
        infra-manager-shipper:local -test.v -test.timeout 5m ;;
  shipping-integration) prepare
      mkdir -p /tmp/infra-manager-target
      go_run go test -c -tags shippingintegration -o bin/shipping-integration ./internal/adapters/delivery
      build_net=(); proxy_args=()
      if [ -n "${HTTPS_PROXY:-}${HTTP_PROXY:-}" ]; then
        proxy_args+=(--build-arg "HTTPS_PROXY=${HTTPS_PROXY:-${HTTP_PROXY:-}}" --build-arg "HTTP_PROXY=${HTTP_PROXY:-${HTTPS_PROXY:-}}")
        case "${HTTPS_PROXY:-}${HTTP_PROXY:-}" in *127.0.0.1*|*localhost*) build_net=(--network host) ;; esac
      fi
      docker build "${build_net[@]}" "${proxy_args[@]}" --target shipping-test-target -t infra-manager-ssh-test:local "$APP"
      fixture_dir="$(mktemp -d /tmp/infra-manager-target/ssh-it.XXXXXX)"
      ssh_fixture="manager-shipping-ssh-$$"
      cleanup_shipping() {
        docker rm -f "$ssh_fixture" >/dev/null 2>&1 || true
        docker run --rm --entrypoint sh -v "$fixture_dir:$fixture_dir" infra-manager-shipper:local -c 'rm -f "$1/id_ed25519" "$1/id_ed25519.pub" "$1/known_hosts"' -- "$fixture_dir"
        rmdir "$fixture_dir"
      }
      trap cleanup_shipping EXIT
      docker run --rm --entrypoint ssh-keygen -v "$fixture_dir:$fixture_dir" infra-manager-shipper:local -q -t ed25519 -N '' -f "$fixture_dir/id_ed25519"
      docker run -d --name "$ssh_fixture" -p 127.0.0.1::22 \
        -v "$fixture_dir/id_ed25519.pub:/root/.ssh/authorized_keys:ro" \
        -v /var/run/docker.sock:/var/run/docker.sock \
        -v /tmp/infra-manager-target:/tmp/infra-manager-target infra-manager-ssh-test:local >/dev/null
      ssh_port="$(docker port "$ssh_fixture" 22/tcp)"; ssh_port="${ssh_port##*:}"
      host_public="$(docker exec "$ssh_fixture" cat /etc/ssh/ssh_host_ed25519_key.pub)"
      printf '[127.0.0.1]:%s %s\n' "$ssh_port" "$host_public" > "$fixture_dir/known_hosts"
      docker run --rm --network host --entrypoint /src/bin/shipping-integration \
        -v "$APP:/src" -v /var/run/docker.sock:/var/run/docker.sock \
        -v /tmp/infra-manager-target:/tmp/infra-manager-target \
        -e "MANAGER_TEST_SSH_KEYS=$fixture_dir" -e "MANAGER_TEST_SSH_PORT=$ssh_port" \
        infra-manager-shipper:local -test.v -test.timeout 5m ;;
  native-integration) shift; prepare
      test -n "${MANAGER_NATIVE_TEST_TARGET:-}" || { echo 'Set an explicit MANAGER_NATIVE_TEST_TARGET JSON destination'; exit 1; }
      go_run go test -c -tags nativedeployintegration -o bin/native-integration ./internal/adapters/delivery
      docker run --rm --entrypoint /src/bin/native-integration -v "$APP:/src" -v /var/run/docker.sock:/var/run/docker.sock \
        -v "$APP/ssh:/keys:ro" -e MANAGER_NATIVE_TEST_TARGET -e MANAGER_NATIVE_TEST_IMAGE -e MANAGER_NATIVE_TEST_KEYS=/keys \
        infra-manager-shipper:local -test.v -test.timeout 6m "$@" ;;
  check) prepare; [ -d "$APP/web/node_modules" ] || "$0" install
      test -z "$(go_run gofmt -l cmd internal)" || { echo 'Go formatting check failed' >&2; exit 1; }
      python3 -m unittest discover -s "$ROOT/scripts" -p test_project_deployments.py
      go_run go vet ./...; "$0" test; "$0" build ;;
  *) echo 'Usage: scripts/manager.sh install|build|image|up|stop|logs|test|check|integration|shipping-integration|resources-integration|vpn-integration|fmt|tidy|npm ...'; exit 1 ;;
esac
