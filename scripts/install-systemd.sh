#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
TEMPLATE_DIR="$ROOT/deploy/systemd/user"
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
dry_run=false
enable_units=true
build_images=true
engine=""
env_file="$ROOT/.env"
requires_mount=""
mount_was_set=false

usage() {
  cat <<'EOF'
Usage: scripts/install-systemd.sh [options]

Options:
  --dry-run              Render and verify units without installing or building
  --no-enable            Install units without enabling or starting them
  --no-build             Do not rebuild application and backup images
  --engine podman|docker Select the container engine (default: auto-detect)
  --env-file PATH        Environment file (default: repository .env)
  --requires-mount PATH  Require this local/NAS path for backup jobs
  -h, --help             Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) dry_run=true; shift ;;
    --no-enable) enable_units=false; shift ;;
    --no-build) build_images=false; shift ;;
    --engine) engine=${2:?missing engine}; shift 2 ;;
    --env-file) env_file=${2:?missing environment file}; shift 2 ;;
    --requires-mount) requires_mount=${2:?missing mount path}; mount_was_set=true; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; printf 'unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

if [[ "$env_file" != /* ]]; then
  env_file="$ROOT/$env_file"
fi
env_file=$(realpath -m "$env_file")
[[ -r "$env_file" ]] || { printf 'environment file is not readable: %s\n' "$env_file" >&2; exit 1; }

if [[ -z "$engine" ]]; then
  if command -v podman >/dev/null 2>&1; then
    engine=podman
  elif command -v docker >/dev/null 2>&1; then
    engine=docker
  else
    printf 'podman or docker is required\n' >&2
    exit 1
  fi
fi
[[ "$engine" == podman || "$engine" == docker ]] || { printf 'engine must be podman or docker\n' >&2; exit 2; }
command -v "$engine" >/dev/null 2>&1 || { printf '%s is not installed\n' "$engine" >&2; exit 1; }

set -a
# shellcheck disable=SC1090
source "$env_file"
set +a
export VITALD_ENV_FILE="$env_file"
export VITALD_CONTAINER_ENGINE="$engine"
: "${POSTGRES_PASSWORD:?POSTGRES_PASSWORD is required in the environment file}"
: "${GRAFANA_ADMIN_PASSWORD:?GRAFANA_ADMIN_PASSWORD is required in the environment file}"
: "${GRAFANA_SECRET_KEY:?GRAFANA_SECRET_KEY is required in the environment file}"
: "${VITALD_GRAFANA_DB_PASSWORD:?VITALD_GRAFANA_DB_PASSWORD is required in the environment file}"
: "${VITALD_BACKUP_REPOSITORY:?VITALD_BACKUP_REPOSITORY is required in the environment file}"
: "${RESTIC_PASSWORD_FILE:?RESTIC_PASSWORD_FILE is required in the environment file}"
password_file=$RESTIC_PASSWORD_FILE
if [[ "$password_file" != /* ]]; then
  password_file="$ROOT/$password_file"
fi
[[ -r "$password_file" ]] || { printf 'Restic password file is not readable: %s\n' "$password_file" >&2; exit 1; }

if [[ "$mount_was_set" == false ]]; then
  case "${VITALD_BACKUP_REPOSITORY:-}" in
    /*) requires_mount=$VITALD_BACKUP_REPOSITORY ;;
    ./*|../*) requires_mount=$(realpath -m "$ROOT/$VITALD_BACKUP_REPOSITORY") ;;
  esac
elif [[ "$requires_mount" != /* ]]; then
  requires_mount=$(realpath -m "$ROOT/$requires_mount")
fi

validate_template_value() {
  local name=$1 value=$2
  [[ "$value" =~ ^[A-Za-z0-9_./:@+-]*$ ]] || {
    printf '%s contains unsupported characters for systemd unit rendering: %s\n' "$name" "$value" >&2
    exit 1
  }
}
validate_template_value "repository path" "$ROOT"
validate_template_value "environment path" "$env_file"
validate_template_value "container engine" "$engine"
validate_template_value "required mount" "$requires_mount"

staging=$(mktemp -d)
trap 'rm -rf "$staging"' EXIT
mount_directive=""
if [[ -n "$requires_mount" ]]; then
  mount_directive="RequiresMountsFor=$requires_mount"
fi

render_template() {
  local source=$1 destination=$2 content
  content=$(<"$source")
  content=${content//@VITALD_ROOT@/$ROOT}
  content=${content//@VITALD_ENV_FILE@/$env_file}
  content=${content//@VITALD_CONTAINER_ENGINE@/$engine}
  content=${content//@VITALD_MOUNT_DIRECTIVE@/$mount_directive}
  printf '%s\n' "$content" > "$destination"
}

for template in "$TEMPLATE_DIR"/*.in; do
  name=$(basename "$template" .in)
  render_template "$template" "$staging/$name"
done
for timer in "$TEMPLATE_DIR"/*.timer; do
  install -m 0644 "$timer" "$staging/$(basename "$timer")"
done

systemd-analyze verify "$staging"/*.service "$staging"/*.timer

if [[ "$dry_run" == true ]]; then
  for unit in "$staging"/*; do
    printf '\n===== %s =====\n' "$(basename "$unit")"
    cat "$unit"
  done
  printf '\nDry run complete; no images, units, or timers were changed.\n'
  exit 0
fi

if [[ "$build_images" == true ]]; then
  cd "$ROOT"
  "$engine" compose pull grafana
  "$engine" compose build vitald
  "$engine" compose --profile backup build backup restore
fi

mkdir -p "$UNIT_DIR"
for unit in "$staging"/*; do
  install -m 0644 "$unit" "$UNIT_DIR/$(basename "$unit")"
done
systemctl --user daemon-reload

if [[ "$enable_units" == true ]]; then
  # Reconcile containers even when a previous oneshot unit is still marked
  # active after its detached Compose container was removed manually.
  systemctl --user enable vitald-postgres.service
  systemctl --user restart vitald-postgres.service
  systemctl --user enable vitald-grafana.service
  systemctl --user restart vitald-grafana.service
  systemctl --user start vitald-doctor.service
  systemctl --user enable --now \
    vitald-sync.timer vitald-doctor.timer vitald-backup.timer vitald-verify-backup.timer
fi

linger=$(loginctl show-user "$USER" -p Linger --value 2>/dev/null || printf 'unknown')
printf 'Installed vitald user units in %s\n' "$UNIT_DIR"
if [[ "$enable_units" == true ]]; then
  systemctl --user list-timers 'vitald-*' --no-pager
fi
if [[ "$linger" != yes ]]; then
  printf '\nUser lingering is %s. Enable unattended user services after logout with:\n  loginctl enable-linger %s\n' "$linger" "$USER"
fi
if [[ -n "$(systemctl --user --failed --no-legend --plain 2>/dev/null || true)" ]]; then
  printf '\nThe user systemd manager already has failed units; inspect with:\n  systemctl --user --failed\n'
fi
