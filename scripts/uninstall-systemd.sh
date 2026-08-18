#!/usr/bin/env bash
set -Eeuo pipefail

UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
stop_postgres=false

usage() {
  cat <<'EOF'
Usage: scripts/uninstall-systemd.sh [--stop-postgres]

Disables and removes vitald user-systemd scheduling. Grafana is stopped, while
PostgreSQL is left running unless --stop-postgres is supplied. Data, volumes,
images, dashboards, and backups are never removed.
EOF
}
while [[ $# -gt 0 ]]; do
  case "$1" in
    --stop-postgres) stop_postgres=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; printf 'unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

timers=(vitald-sync.timer vitald-doctor.timer vitald-backup.timer vitald-verify-backup.timer)
jobs=(vitald-sync.service vitald-doctor.service vitald-backup.service vitald-verify-backup.service)
files=(
  vitald-postgres.service
  vitald-grafana.service
  vitald-sync.service vitald-sync.timer
  vitald-doctor.service vitald-doctor.timer
  vitald-backup.service vitald-backup.timer
  vitald-verify-backup.service vitald-verify-backup.timer
  'vitald-failure@.service'
)

systemctl --user stop "${timers[@]}" 2>/dev/null || true
for job in "${jobs[@]}"; do
  if systemctl --user is-active --quiet "$job"; then
    printf '%s is still running; wait for it to finish and rerun uninstall\n' "$job" >&2
    exit 1
  fi
done
systemctl --user disable "${timers[@]}" 2>/dev/null || true
systemctl --user disable --now vitald-grafana.service 2>/dev/null || true
if [[ "$stop_postgres" == true ]]; then
  systemctl --user disable --now vitald-postgres.service 2>/dev/null || true
else
  systemctl --user disable vitald-postgres.service 2>/dev/null || true
fi

for file in "${files[@]}"; do
  rm -f "$UNIT_DIR/$file"
done
systemctl --user daemon-reload
systemctl --user reset-failed 'vitald-*' 2>/dev/null || true

printf 'Removed vitald user-systemd units. Persistent data, dashboards, and backups were not changed.\n'
if [[ "$stop_postgres" != true ]]; then
  printf 'The PostgreSQL container was left running.\n'
fi
