#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=scripts/lib/backup-common.sh
source "$ROOT/scripts/lib/backup-common.sh"

action=backup
skip_build=${VITALD_SKIP_BUILD:-false}
usage() { printf 'Usage: scripts/backup.sh [--snapshots|--check] [--no-build]\n'; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --snapshots) action=snapshots; shift ;;
    --check) action=check; shift ;;
    --no-build) skip_build=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; printf 'unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

load_backup_environment "$ROOT"
select_compose_command
cd "$ROOT"
prepare_restic_run_args

if [[ "$skip_build" != true ]]; then
  "${COMPOSE[@]}" --profile backup build backup
fi
if [[ "$action" == backup ]]; then
  # A listed container may be stopped or stale. Probe the service itself before
  # deciding whether Compose needs to reconcile it.
  if ! "${COMPOSE[@]}" exec -T postgres \
    pg_isready -U "${POSTGRES_USER:-vitald}" -d "${POSTGRES_DB:-vitald}" >/dev/null 2>&1; then
    "${COMPOSE[@]}" up -d --no-deps postgres
  fi
  wait_for_postgres
  "${COMPOSE[@]}" --profile backup run --rm -T --no-deps "${RESTIC_RUN_ARGS[@]}" backup backup
else
  "${COMPOSE[@]}" --profile backup run --rm -T --no-deps "${RESTIC_RUN_ARGS[@]}" backup "$action"
fi
