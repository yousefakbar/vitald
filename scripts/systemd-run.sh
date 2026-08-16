#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=scripts/lib/backup-common.sh
source "$ROOT/scripts/lib/backup-common.sh"

load_runtime_environment() {
  local env_file=${VITALD_ENV_FILE:-$ROOT/.env}
  local configured_engine=${VITALD_CONTAINER_ENGINE:-}
  if [[ "$env_file" != /* ]]; then
    env_file="$ROOT/$env_file"
  fi
  [[ -r "$env_file" ]] || { printf 'vitald systemd environment file is not readable: %s\n' "$env_file" >&2; return 1; }
  export VITALD_ENV_FILE="$env_file"
  set -a
  # shellcheck disable=SC1090
  source "$env_file"
  set +a
  export VITALD_ENV_FILE="$env_file"
  if [[ -n "$configured_engine" ]]; then
    export VITALD_CONTAINER_ENGINE="$configured_engine"
  fi
}

usage() {
  printf 'Usage: scripts/systemd-run.sh {postgres-start|postgres-stop|sync|doctor|backup|verify-backup}\n'
}

load_runtime_environment
select_compose_command
cd "$ROOT"

case "${1:-}" in
  postgres-start)
    if [[ -z "$("${COMPOSE[@]}" ps -q postgres 2>/dev/null || true)" ]]; then
      "${COMPOSE[@]}" up -d postgres
    fi
    wait_for_postgres
    ;;
  postgres-stop)
    "${COMPOSE[@]}" stop postgres
    ;;
  sync)
    wait_for_postgres
    exec "${COMPOSE[@]}" run --rm -T --no-deps vitald sync
    ;;
  doctor)
    wait_for_postgres
    exec "${COMPOSE[@]}" run --rm -T --no-deps vitald doctor
    ;;
  backup)
    exec "$ROOT/scripts/backup.sh" --no-build
    ;;
  verify-backup)
    exec "$ROOT/scripts/verify-backup.sh" --snapshot latest --no-build
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac
