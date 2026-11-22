package delivery

// Shell used on the destination. Everything runs under the per-project flock, and
// every value passed in is a validated name, number or path argument.

// envLib installs staged env files atomically with timestamped backups.
// Variables: dir (project), stage (staging dir: MANIFEST, files/<rel>), ts.
const envLib = `
prune_backups() { # prune_backups <abs path>; keep the newest 10
  ls -1 "$1".bak.* 2>/dev/null | sort -r | tail -n +11 | while read -r old; do rm -f "$old"; done
  :
}
env_check() {
  while read -r rel want; do
    [ -n "$rel" ] || continue
    if [ -f "$dir/$rel" ]; then have="$(sha256sum "$dir/$rel" | cut -d' ' -f1)"; else have=-; fi
    [ "$have" = "$want" ] || return 1
  done < "$stage/MANIFEST"
  return 0
}
env_install() {
  : > "$stage/RESTORE"
  while read -r rel want; do
    [ -n "$rel" ] || continue
    [ -f "$stage/files/$rel" ] || continue
    parent="$(dirname "$dir/$rel")"
    mkdir -p "$parent"
    [ "$(basename "$parent")" != env.d ] || chmod 700 "$parent"
    if [ -f "$dir/$rel" ]; then
      mode="$(stat -c '%a' "$dir/$rel")"
      cp "$dir/$rel" "$dir/$rel.bak.$ts"; chmod 600 "$dir/$rel.bak.$ts"
      echo "$rel $rel.bak.$ts" >> "$stage/RESTORE"
    else
      mode=600
      echo "$rel -" >> "$stage/RESTORE"
    fi
    cp "$stage/files/$rel" "$dir/$rel.new.$$"; chmod "$mode" "$dir/$rel.new.$$"
    mv "$dir/$rel.new.$$" "$dir/$rel"
    prune_backups "$dir/$rel"
  done < "$stage/MANIFEST"
  return 0
}
env_restore() {
  [ -f "$stage/RESTORE" ] || return 0
  while read -r rel bak; do
    [ -n "$rel" ] || continue
    if [ "$bak" = - ]; then
      rm -f "$dir/$rel"
    elif [ -f "$dir/$bak" ]; then
      mode="$(stat -c '%a' "$dir/$bak")"
      cp "$dir/$bak" "$dir/$rel.new.$$"; chmod "$mode" "$dir/$rel.new.$$"; mv "$dir/$rel.new.$$" "$dir/$rel"
    fi
  done < "$stage/RESTORE"
  return 0
}
`

const writeScript = `set -eu
umask 077
dir="$1"; ts="$2"
cd "$dir"
stage="$(mktemp -d "$dir/.manager/stage.XXXXXX")"
trap 'rm -rf "$stage"' EXIT
tar -xf - -C "$stage"
` + envLib + `
env_check || { echo MANAGER_ENV_CONFLICT; exit 3; }
env_install
echo MANAGER_ENV_WRITTEN
`

// readScript prints a tar of the env store (project .env, env.d, the active
// manifest, the generated override and the last applied snapshot).
const readScript = `cd "$1" 2>/dev/null || exit 0
set --
for f in .env compose.yaml manager.override.yaml env.d .manager/applied; do [ -e "$f" ] && set -- "$@" "$f"; done
[ "$#" -gt 0 ] || exit 0
exec tar -cf - "$@"
`

// liveScript prints "<service> <memory> <nanocpus> <memswap> <cpuquota> <cpuperiod> <running> <oneoff>"
// for every container of the Compose project.
const liveScript = `ids="$(docker ps -aq --filter "label=com.docker.compose.project=$1")"
[ -n "$ids" ] || exit 0
docker inspect --format '{{index .Config.Labels "com.docker.compose.service"}} {{.HostConfig.Memory}} {{.HostConfig.NanoCpus}} {{.HostConfig.MemorySwap}} {{.HostConfig.CpuQuota}} {{.HostConfig.CpuPeriod}} {{.State.Running}} {{index .Config.Labels "com.docker.compose.oneoff"}}' $ids
`

const listScript = `cd "$1" 2>/dev/null || exit 0
for d in */; do d="${d%/}"; [ -f "$d/compose.yaml" ] && echo "$d"; done
exit 0
`

// applyScript deploys a release (kind=deploy) or recreates services after an env
// edit (kind=apply). Compose output is discarded; only status markers are printed.
const applyScript = `#!/bin/sh
set -eu
umask 077
dir="$1"; release="$2"; project="$3"; service="$4"
export DATA_DIR="$5" APPS_DIR="$6"
kind="$7"; tag="$8"; keep="$9"
ts="$(date +%s)"
stage="$release/envstage"
cd "$dir"
trap 'rm -rf "$stage"' EXIT
` + envLib + `
compose() {
  file="$1"; over="$2"; shift 2
  [ -z "$over" ] || set -- -f "$over" "$@"
  set -- --project-name "$project" --project-directory "$dir" -f "$file" "$@"
  [ ! -f "$dir/.tag.next" ] || set -- --env-file "$dir/.tag.next" "$@"
  if [ -f "$release/.env" ] && [ "$file" = "$release/compose.yaml" ]; then
    set -- --env-file "$release/.env" "$@"
  elif [ -f "$dir/.env" ]; then
    set -- --env-file "$dir/.env" "$@"
  fi
  docker compose "$@"
}
up() {
  file="$1"; over="$2"
  if [ "$kind" = apply ]; then
    set -- $(cat "$release/services")
    compose "$file" "$over" up -d --no-deps --no-build --pull never --wait --wait-timeout 120 "$@" >/dev/null 2>&1
  elif [ -n "$service" ]; then
    compose "$file" "$over" up -d --no-deps --no-build --pull never --wait --wait-timeout 120 "$service" >/dev/null 2>&1
  else
    compose "$file" "$over" up -d --no-build --pull never --wait --wait-timeout 120 >/dev/null 2>&1
  fi
}
restore_applied() {
  [ -f "$release/restore.list" ] || return 0
  while read -r rel; do
    [ -n "$rel" ] || continue
    src=""
    if [ -f "$dir/.manager/applied/$rel" ]; then src="$dir/.manager/applied/$rel"
    else src="$(ls -1 "$dir/$rel".bak.* 2>/dev/null | sort -r | head -n 1)"; fi
    [ -n "$src" ] && [ -f "$src" ] || continue
    mode=600
    if [ -f "$dir/$rel" ]; then mode="$(stat -c '%a' "$dir/$rel")"; cp "$dir/$rel" "$dir/$rel.bak.$ts"; chmod 600 "$dir/$rel.bak.$ts"; prune_backups "$dir/$rel"; fi
    cp "$src" "$dir/$rel.new.$$"; chmod "$mode" "$dir/$rel.new.$$"; mv "$dir/$rel.new.$$" "$dir/$rel"
  done < "$release/restore.list"
  return 0
}
restore_all() {
  if [ "$kind" = apply ]; then restore_applied; return 0; fi
  env_restore
  if [ "$tag" != "-" ]; then
    if [ -f "$release/tag.next.old" ]; then cp "$release/tag.next.old" "$dir/.tag.next"; else rm -f "$dir/.tag.next"; fi
  fi
  return 0
}
install_override() {
  mkdir -p "$dir/.manager"
  if [ -f "$dir/manager.override.yaml" ]; then
    cp "$dir/manager.override.yaml" "$dir/manager.override.yaml.bak.$ts"; chmod 600 "$dir/manager.override.yaml.bak.$ts"
    prune_backups "$dir/manager.override.yaml"
  fi
  cp "$release/manager.override.yaml" "$dir/.manager/override.next"; chmod 600 "$dir/.manager/override.next"
  mv "$dir/.manager/override.next" "$dir/manager.override.yaml"
}
snapshot_applied() {
  rm -rf "$dir/.manager/applied.next"; mkdir -p "$dir/.manager/applied.next/env.d"
  [ ! -f "$dir/.env" ] || cp "$dir/.env" "$dir/.manager/applied.next/.env"
  for f in "$dir"/env.d/*.env; do
    if [ -f "$f" ]; then cp "$f" "$dir/.manager/applied.next/env.d/"; fi
  done
  rm -rf "$dir/.manager/applied"; mv "$dir/.manager/applied.next" "$dir/.manager/applied"
  return 0
}
prune_images() {
  [ "$keep" -gt 0 ] 2>/dev/null || return 0
  [ -f "$release/repos" ] || return 0
  while read -r repo; do
    [ -n "$repo" ] || continue
    docker image ls "$repo" --format '{{.Tag}}' | tail -n +"$((keep + 1))" | while read -r t; do
      docker rmi "$repo:$t" >/dev/null 2>&1 || true
    done
  done < "$release/repos"
  return 0
}

mkdir -p "$dir/.manager"
prev_tag="$(cat "$dir/.tag" 2>/dev/null || true)"
if [ "$kind" = deploy ] && [ -f "$stage/MANIFEST" ]; then
  env_check || { echo MANAGER_ENV_CONFLICT; exit 1; }
  env_install
fi
if [ "$tag" != "-" ]; then
  [ ! -f "$dir/.tag.next" ] || cp "$dir/.tag.next" "$release/tag.next.old"
  printf 'TAG=%s\n' "$tag" > "$dir/.tag.next"
fi
if [ "$kind" = deploy ]; then
  if ! compose "$release/compose.yaml" "" config --format json --no-env-resolution > "$release/rendered.next" 2> "$release/preflight.err"; then
    restore_all; echo MANAGER_PREFLIGHT_FAILED; exit 1
  fi
  mv "$release/rendered.next" "$release/rendered.yaml"
  base="$release/rendered.yaml"
else
  base="$dir/compose.yaml"
fi
if ! compose "$base" "$release/manager.override.yaml" config -q --no-env-resolution >/dev/null 2> "$release/preflight.err"; then
  restore_all; echo MANAGER_PREFLIGHT_FAILED; exit 1
fi
if up "$base" "$release/manager.override.yaml"; then
  if [ "$kind" = deploy ]; then
    cp "$release/rendered.yaml" "$dir/.manager/compose.next"
    chmod 600 "$dir/.manager/compose.next"
    mv "$dir/.manager/compose.next" "$dir/compose.yaml"
    printf '%s\n' "$release" > "$dir/.manager/current"
  fi
  install_override
  snapshot_applied
  if [ "$tag" != "-" ]; then
    if [ -n "$prev_tag" ] && [ "$prev_tag" != "$tag" ]; then echo "$prev_tag" > "$dir/.tag.prev"; fi
    echo "$tag" > "$dir/.tag"
  fi
  prune_images
  echo MANAGER_DEPLOYED
else
  restore_all
  if [ -f "$dir/compose.yaml" ]; then
    if up "$dir/compose.yaml" "$release/manager.override.prev.yaml"; then echo MANAGER_ROLLED_BACK; else echo MANAGER_ROLLBACK_FAILED; fi
  else
    echo MANAGER_NO_PREVIOUS_RELEASE
  fi
  exit 1
fi
`
