#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=scripts/lib/backup-common.sh
source "$ROOT/scripts/lib/backup-common.sh"

action=backup
case "${1:-}" in
  "") ;;
  --snapshots) action=snapshots ;;
  --check) action=check ;;
  -h|--help)
    printf 'Usage: scripts/backup.sh [--snapshots|--check]\n'
    exit 0
    ;;
  *) printf 'unknown argument: %s\n' "$1" >&2; exit 2 ;;
esac

load_backup_environment "$ROOT"
select_compose_command
cd "$ROOT"
prepare_restic_run_args

"${COMPOSE[@]}" --profile backup build backup
if [[ "$action" == backup ]]; then
  if [[ -z "$("${COMPOSE[@]}" ps -q postgres 2>/dev/null || true)" ]]; then
    "${COMPOSE[@]}" up -d postgres
  fi
  wait_for_postgres
  "${COMPOSE[@]}" --profile backup run --rm -T --no-deps "${RESTIC_RUN_ARGS[@]}" backup backup
else
  "${COMPOSE[@]}" --profile backup run --rm -T --no-deps "${RESTIC_RUN_ARGS[@]}" backup "$action"
fi
