#!/usr/bin/env bash
# Runs everything about the Laravel kit (laravelkit/) inside Docker: nothing PHP-related runs on the host.
#
#   scripts/laravelkit.sh test | fmt | lint | check      unit tests, Pint (fix / check only), both
#   scripts/laravelkit.sh integration                    tests against a throw-away Redis (+ a stand-in OTLP collector)
#   scripts/laravelkit.sh composer <args>                any composer command in the kit (require, update...)
#   scripts/laravelkit.sh jwt-key [--hs256]              print new JWT key material
#   scripts/laravelkit.sh install <laravel project dir>  vendor the kit into <dir>/.laravelkit and require it
#   scripts/laravelkit.sh sync <laravel project dir>     refresh <dir>/.laravelkit from this kit
#
# Composer packages live in the shared Docker volume $COMPOSER_VOLUME (default laravelkit-composer), so a
# package is downloaded once. If the network needs a proxy, export HTTPS_PROXY (for example
# HTTPS_PROXY=http://127.0.0.1:2020); only adding or updating a dependency needs the network.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KIT="$ROOT/laravelkit"
[ -f "$ROOT/.env" ] && REGISTRY="${REGISTRY:-$(grep -E '^REGISTRY=' "$ROOT/.env" | cut -d= -f2- || true)}"
REGISTRY="${REGISTRY:-docker.arvancloud.ir}"
PHP_IMAGE="${PHP_IMAGE:-$REGISTRY/php:8.4-cli-alpine}"
COMPOSER_IMAGE="${COMPOSER_IMAGE:-$REGISTRY/composer:2}"
REDIS_IMAGE="${REDIS_IMAGE:-$REGISTRY/redis:8.10.3-alpine}"
VOL="${COMPOSER_VOLUME:-laravelkit-composer}"

die() { echo "error: $*" >&2; exit 1; }
command -v docker >/dev/null || die "docker is required"

prepare_volume() {
  docker volume inspect "$VOL" >/dev/null 2>&1 || docker volume create "$VOL" >/dev/null
  docker run --rm -u 0 --entrypoint sh -v "$VOL:/c" "$COMPOSER_IMAGE" -c 'chmod 777 /c'
}

# in_docker <image> <extra docker args...> -- <command...>
in_docker() {
  local image="$1"; shift
  local extra=() net=() init=()
  [ "$image" = "$COMPOSER_IMAGE" ] || init=(--init) # the composer image already runs tini
  while [ "$1" != "--" ]; do extra+=("$1"); shift; done; shift
  if [ -n "${HTTPS_PROXY:-}${HTTP_PROXY:-}" ]; then
    extra+=(-e "HTTPS_PROXY=${HTTPS_PROXY:-${HTTP_PROXY:-}}" -e "HTTP_PROXY=${HTTP_PROXY:-${HTTPS_PROXY:-}}" -e "NO_PROXY=${NO_PROXY:-}")
    case "${HTTPS_PROXY:-}${HTTP_PROXY:-}" in *127.0.0.1*|*localhost*) net=(--network host) ;; esac
  fi
  docker run --rm "${init[@]}" "${net[@]}" -u "$(id -u):$(id -g)" -v "$VOL:/c" \
    -e HOME=/tmp -e COMPOSER_HOME=/c -e COMPOSER_CACHE_DIR=/c/cache -e COMPOSER_NO_INTERACTION=1 \
    "${extra[@]}" "$image" "$@"
}

kit_composer() { prepare_volume; in_docker "$COMPOSER_IMAGE" -v "$KIT:/app" -w /app -- composer "$@"; }

ensure_vendor() { [ -f "$KIT/vendor/autoload.php" ] || kit_composer install --no-progress --prefer-dist; }

run_php() { # run_php [extra docker args --] php-args...
  local extra=()
  while [ "${1:-}" != "--" ] && [ $# -gt 0 ] && [[ "${1:-}" == -* ]]; do extra+=("$1" "$2"); shift 2; done
  [ "${1:-}" = "--" ] && shift
  in_docker "$PHP_IMAGE" -v "$KIT:/app" -w /app "${extra[@]}" -- php "$@"
}

cmd="${1:-}"; [ $# -gt 0 ] && shift || true
case "$cmd" in
  test)  ensure_vendor; run_php -- vendor/bin/phpunit --testsuite unit "$@" ;;
  fmt)   ensure_vendor; run_php -- vendor/bin/pint "$@" ;;
  lint)  ensure_vendor; run_php -- vendor/bin/pint --test "$@" ;;
  check) "$0" lint; "$0" test ;;
  composer) kit_composer "$@" ;;
  jwt-key) ensure_vendor; run_php -- vendor/bin/testbench laravelkit:jwt-key "$@" ;;
  integration)
    ensure_vendor
    itnet="laravelkit-it-$$"; itredis="$itnet-redis"
    cleanup() { docker rm -f "$itredis" >/dev/null 2>&1 || true; docker network rm "$itnet" >/dev/null 2>&1 || true; }
    trap cleanup EXIT
    docker network create "$itnet" >/dev/null
    docker run -d --name "$itredis" --network "$itnet" "$REDIS_IMAGE" >/dev/null
    for _ in $(seq 30); do docker exec "$itredis" redis-cli ping >/dev/null 2>&1 && break; sleep 1; done
    run_php --network "$itnet" -e "LK_REDIS_HOST=$itredis" -- vendor/bin/phpunit "$@"
    ;;
  install|sync)
    [ $# -ge 1 ] || die "usage: laravelkit.sh $cmd <laravel project dir>"
    dest="$(cd "${1/#\~/$HOME}" && pwd)"
    [ -f "$dest/artisan" ] && [ -f "$dest/composer.json" ] || die "$dest is not a Laravel project (no artisan / composer.json)"
    # vendored copy, like gokit's .kit: the project builds without the kit being published anywhere
    rm -rf "$dest/.laravelkit"; mkdir -p "$dest/.laravelkit"
    tar -C "$KIT" --exclude=vendor --exclude=tests --exclude=.phpunit.cache --exclude=composer.lock --exclude=phpunit.xml --exclude=scripts-placeholder --exclude='.pint*' -cf - . | tar -C "$dest/.laravelkit" -xf -
    # Composer copies a path package only when its version changes, so the version follows the content:
    # an unchanged kit keeps its version (nothing to update), a changed one gets a new one.
    build="$(cd "$dest/.laravelkit" && find . -type f | LC_ALL=C sort | xargs cat | cksum | cut -d' ' -f1)"
    python3 - "$dest/.laravelkit/composer.json" "0.1.$build" <<'PY'
import json, sys
p, v = sys.argv[1:]
d = json.load(open(p))
d["version"] = v
json.dump(d, open(p, "w"), indent=4)
PY
    if [ "$cmd" = install ]; then
      prepare_volume
      in_docker "$COMPOSER_IMAGE" -v "$dest:/app" -w /app -- composer config repositories.laravelkit \
        '{"type":"path","url":".laravelkit","options":{"symlink":false}}'
      in_docker "$COMPOSER_IMAGE" -v "$dest:/app" -w /app -- composer require pourydev/laravelkit:@dev --no-update --no-progress
      cat <<MSG

laravelkit is vendored into $dest/.laravelkit and required in composer.json.
Next steps, in the project's own dev container (its PHP / extensions decide the install):
  composer update pourydev/laravelkit          # installs it (firebase/php-jwt is the only new required package)
  php artisan laravelkit:install               # publishes config/laravelkit.php, adds the variables to .env.example
  php artisan laravelkit:jwt-key               # JWT key pair for the environment
Docker builds: COPY .laravelkit before 'composer install' (it is a path repository).
Commit .laravelkit/ with the project; refresh it later with: make laravelkit-sync
MSG
    else
      echo "refreshed $dest/.laravelkit - now run in the project: composer update pourydev/laravelkit"
    fi
    ;;
  *) sed -n '2,12p' "${BASH_SOURCE[0]}"; exit 1 ;;
esac
