#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=scripts/lib/backup-common.sh
source "$ROOT/scripts/lib/backup-common.sh"

snapshot=latest
target_project=""
skip_build=${VITALD_SKIP_BUILD:-false}
usage() {
  cat <<'EOF'
Usage: scripts/restore.sh [--snapshot ID|latest] --target-project NAME [--no-build]

Restores into a fresh Compose project. Existing/in-place projects are refused.
EOF
}
while [[ $# -gt 0 ]]; do
  case "$1" in
    --snapshot) snapshot=${2:?missing snapshot}; shift 2 ;;
    --target-project) target_project=${2:?missing target project}; shift 2 ;;
    --no-build) skip_build=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; printf 'unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done
[[ -n "$target_project" ]] || { usage >&2; exit 2; }

load_backup_environment "$ROOT"
select_compose_command
cd "$ROOT"
prepare_restic_run_args
source_project=$(compose_project_name)
[[ "$target_project" != "$source_project" ]] || { printf 'refusing in-place restore into project %s\n' "$target_project" >&2; exit 1; }

if [[ -n "$("${COMPOSE[@]}" -p "$target_project" ps -aq 2>/dev/null || true)" ]]; then
  printf 'target project %s already has containers; choose a fresh project name\n' "$target_project" >&2
  exit 1
fi

if [[ "$skip_build" != true ]]; then
  "${COMPOSE[@]}" -p "$target_project" --profile backup build restore vitald
fi
"${COMPOSE[@]}" -p "$target_project" up -d postgres
wait_for_postgres "$target_project"
"${COMPOSE[@]}" -p "$target_project" --profile backup run --rm -T --no-deps \
  "${RESTIC_RUN_ARGS[@]}" restore restore "$snapshot"

# Normal startup applies forward migrations and rejects migrations unknown to
# this binary before changing the restored database.
"${COMPOSE[@]}" -p "$target_project" run --rm -T --no-deps vitald status
"${COMPOSE[@]}" -p "$target_project" run --rm -T --no-deps vitald doctor

cat <<EOF
Restore completed into Compose project: $target_project
PostgreSQL remains running; synchronization has not been started.
Inspect with:
  ${COMPOSE[*]} -p $target_project run --rm -T --no-deps vitald status
  ${COMPOSE[*]} -p $target_project run --rm -T --no-deps vitald doctor --online
EOF
