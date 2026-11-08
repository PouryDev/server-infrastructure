#!/usr/bin/env bash
# Tiny interactive helpers used by the Makefile. Source it:  . scripts/lib-prompt.sh
#
#   ask VAR "Question" "example" ["default"] ["regex"]
#       Prompts as:  Question (e.g. example) [default]:
#       - A variable that is already set (environment / `make target VAR=x`) is used as is, no prompt.
#       - Re-asks until the answer matches regex (default: non-empty). Pass '^.*$' to allow empty.
#   confirm "Question"      returns 0 only for y/Y
#   die "message"

die() { echo "error: $*" >&2; exit 1; }

ask() {
  local var="$1" q="$2" ex="${3:-}" def="${4:-}" re="${5:-.+}" v
  v="${!var:-}"
  if [ -n "$v" ]; then
    [[ "$v" =~ $re ]] || die "$var='$v' is not valid"
    return 0
  fi
  local p="$q"
  [ -n "$ex" ] && p+=" (e.g. $ex)"
  [ -n "$def" ] && p+=" [$def]"
  while true; do
    printf '%s: ' "$p" >&2
    IFS= read -r v || die "no input (stdin closed)"
    v="${v:-$def}"
    if [[ "$v" =~ $re ]]; then
      printf -v "$var" '%s' "$v"
      return 0
    fi
    echo "  invalid value, try again." >&2
  done
}

confirm() {
  local a
  printf '%s [y/N]: ' "$1" >&2
  IFS= read -r a || return 1
  [[ "$a" =~ ^[Yy]$ ]]
}
