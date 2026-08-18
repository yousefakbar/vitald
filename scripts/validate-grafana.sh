#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
run_integration=false

usage() {
  cat <<'EOF'
Usage: scripts/validate-grafana.sh [--integration]

Runs Grafana JSON/YAML/SQL contracts, Compose rendering, systemd rendering,
and mocked Grafana start/stop checks. --integration additionally starts a
fully disposable PostgreSQL and Grafana project to test permissions, health,
datasource connectivity, and clean reprovisioning.
EOF
}

case "${1:-}" in
  "") ;;
  --integration) run_integration=true ;;
  -h|--help) usage; exit 0 ;;
  *) usage >&2; exit 2 ;;
esac

for command in go jq systemd-analyze; do
  command -v "$command" >/dev/null 2>&1 || { printf '%s is required\n' "$command" >&2; exit 1; }
done

cd "$ROOT"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
printf 'test-restic-password\n' >"$tmp/restic-password"
chmod 600 "$tmp/restic-password"
cat >"$tmp/test.env" <<EOF
POSTGRES_DB=vitald
POSTGRES_USER=vitald
POSTGRES_PASSWORD=validation-postgres-password
DATABASE_URL=postgres://vitald:validation-postgres-password@127.0.0.1:5432/vitald?sslmode=disable
GRAFANA_ADMIN_USER=admin
GRAFANA_ADMIN_PASSWORD=validation-grafana-password
GRAFANA_SECRET_KEY=validation-secret-key-at-least-32-characters
VITALD_GRAFANA_DB_USER=vitald_grafana
VITALD_GRAFANA_DB_PASSWORD=validation-datasource-password
VITALD_BACKUP_REPOSITORY=$tmp/backups
RESTIC_PASSWORD_FILE=$tmp/restic-password
EOF

printf '%s\n' 'Validating provisioning and dashboard contracts...'
go test ./internal/deploy
for dashboard in deploy/grafana/dashboards/*.json; do
  jq -e . "$dashboard" >/dev/null
done

set -a
# shellcheck disable=SC1090
source "$tmp/test.env"
set +a
export VITALD_ENV_FILE="$tmp/test.env"

rendered=false
if command -v podman >/dev/null 2>&1; then
  printf '%s\n' 'Validating Compose rendering with Podman...'
  podman compose config >/dev/null
  rendered=true
fi
if command -v docker >/dev/null 2>&1; then
  printf '%s\n' 'Validating Compose rendering with Docker...'
  docker compose config >/dev/null
  rendered=true
fi
[[ "$rendered" == true ]] || { printf 'podman or docker is required for Compose validation\n' >&2; exit 1; }

engine=podman
command -v podman >/dev/null 2>&1 || engine=docker
printf '%s\n' 'Validating rendered user-systemd units...'
./scripts/install-systemd.sh --dry-run --engine "$engine" --env-file "$tmp/test.env" >/dev/null

printf '%s\n' 'Validating Grafana runtime wrapper start/stop behavior...'
mkdir "$tmp/bin"
cat >"$tmp/bin/mock-engine" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$MOCK_ENGINE_LOG"
if [[ "$*" == *"exec -T postgres psql"* ]]; then
  cat >/dev/null
fi
exit 0
EOF
chmod +x "$tmp/bin/mock-engine"
export PATH="$tmp/bin:$PATH"
export MOCK_ENGINE_LOG="$tmp/engine.log"
export VITALD_CONTAINER_ENGINE=mock-engine
./scripts/systemd-run.sh grafana-start
./scripts/systemd-run.sh grafana-stop
grep -Fq 'compose up -d --no-deps grafana' "$MOCK_ENGINE_LOG"
grep -Fq 'compose stop grafana' "$MOCK_ENGINE_LOG"
grep -Fq 'exec -T postgres psql' "$MOCK_ENGINE_LOG"
unset VITALD_CONTAINER_ENGINE MOCK_ENGINE_LOG

if command -v shellcheck >/dev/null 2>&1; then
  printf '%s\n' 'Running shellcheck...'
  shellcheck scripts/*.sh scripts/lib/*.sh
else
  printf '%s\n' 'shellcheck not installed; shell lint skipped.'
fi

if [[ "$run_integration" == true ]]; then
  "$ROOT/scripts/test-grafana-integration.sh" --engine "$engine"
fi

printf '%s\n' 'Grafana validation passed.'
