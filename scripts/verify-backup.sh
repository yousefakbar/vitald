#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=scripts/lib/backup-common.sh
source "$ROOT/scripts/lib/backup-common.sh"

snapshot=latest
keep=false
usage() {
  cat <<'EOF'
Usage: scripts/verify-backup.sh [--snapshot ID|latest] [--keep]

Checks the Restic repository and performs a restore drill into a temporary
Compose project. The temporary project is removed unless --keep is supplied.
EOF
}
while [[ $# -gt 0 ]]; do
  case "$1" in
    --snapshot) snapshot=${2:?missing snapshot}; shift 2 ;;
    --keep) keep=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; printf 'unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

load_backup_environment "$ROOT"
select_compose_command
cd "$ROOT"
prepare_restic_run_args
target="vitald-verify-$(date -u +%Y%m%d%H%M%S)-$$"

cleanup() {
  if [[ "$keep" == true ]]; then
    printf 'Verification project retained: %s\n' "$target" >&2
  else
    "${COMPOSE[@]}" -p "$target" down -v --remove-orphans >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

"${COMPOSE[@]}" --profile backup build backup
"${COMPOSE[@]}" --profile backup run --rm -T --no-deps \
  "${RESTIC_RUN_ARGS[@]}" backup check
"$ROOT/scripts/restore.sh" --snapshot "$snapshot" --target-project "$target"
printf 'Backup verification and restore drill succeeded for snapshot %s.\n' "$snapshot"
