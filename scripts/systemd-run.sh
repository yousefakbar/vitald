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

ensure_postgres_running() {
  # RemainAfterExit systemd units can outlive a detached container that was
  # manually stopped or removed. Probe the service, not the unit/Compose list,
  # and reconcile only PostgreSQL when necessary.
  if ! "${COMPOSE[@]}" exec -T postgres \
    pg_isready -U "${POSTGRES_USER:-vitald}" -d "${POSTGRES_DB:-vitald}" >/dev/null 2>&1; then
    "${COMPOSE[@]}" up -d --no-deps postgres
  fi
  wait_for_postgres
}

wait_for_grafana() {
  local attempt
  for ((attempt = 1; attempt <= 60; attempt++)); do
    if "${COMPOSE[@]}" exec -T grafana \
      wget -q -O /dev/null http://127.0.0.1:3000/api/health >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  printf 'Grafana did not become healthy within 60 seconds\n' >&2
  return 1
}

usage() {
  printf 'Usage: scripts/systemd-run.sh {postgres-start|postgres-stop|grafana-start|grafana-stop|sync|doctor|backup|verify-backup}\n'
}

load_runtime_environment
select_compose_command
cd "$ROOT"

case "${1:-}" in
  postgres-start)
    ensure_postgres_running
    ;;
  postgres-stop)
    "${COMPOSE[@]}" stop postgres
    ;;
  grafana-start)
    ensure_postgres_running
    "$ROOT/scripts/provision-grafana-db.sh"
    "${COMPOSE[@]}" up -d --no-deps grafana
    wait_for_grafana
    ;;
  grafana-stop)
    "${COMPOSE[@]}" stop grafana
    ;;
  sync)
    ensure_postgres_running
    exec "${COMPOSE[@]}" run --rm -T --no-deps vitald sync
    ;;
  doctor)
    ensure_postgres_running
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
