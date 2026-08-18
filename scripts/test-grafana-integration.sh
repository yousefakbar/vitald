#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
engine=""

usage() {
  cat <<'EOF'
Usage: scripts/test-grafana-integration.sh [--engine podman|docker]

Runs destructive permission and Grafana smoke tests only in a newly generated,
disposable Compose project. The production project and its volumes are not used.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --engine) engine=${2:?missing engine}; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
if [[ -z "$engine" ]]; then
  if command -v podman >/dev/null 2>&1; then engine=podman
  elif command -v docker >/dev/null 2>&1; then engine=docker
  else printf 'podman or docker is required\n' >&2; exit 1
  fi
fi
[[ "$engine" == podman || "$engine" == docker ]] || { printf 'engine must be podman or docker\n' >&2; exit 2; }
command -v "$engine" >/dev/null 2>&1 || { printf '%s is not installed\n' "$engine" >&2; exit 1; }
command -v jq >/dev/null 2>&1 || { printf 'jq is required\n' >&2; exit 1; }

project="vitald-grafana-test-$$"
tmp=$(mktemp -d)
cleanup() {
  "$engine" compose -f "$tmp/compose.yaml" -p "$project" down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

postgres_password="integration-postgres-$$"
grafana_password="integration-grafana-$$"
datasource_password="integration-datasource-$$"
grafana_image=$(grep -A3 '^  grafana:$' "$ROOT/compose.yaml" | awk '$1 == "image:" { print $2; exit }')
[[ -n "$grafana_image" && "$grafana_image" != *:latest ]] || { printf 'could not find a pinned Grafana image\n' >&2; exit 1; }
cat >"$tmp/test.env" <<EOF
POSTGRES_DB=vitald
POSTGRES_USER=vitald
POSTGRES_PASSWORD=$postgres_password
GRAFANA_ADMIN_USER=admin
GRAFANA_ADMIN_PASSWORD=$grafana_password
GRAFANA_SECRET_KEY=integration-secret-key-at-least-32-characters-$$
VITALD_GRAFANA_DB_USER=vitald_grafana
VITALD_GRAFANA_DB_PASSWORD=$datasource_password
EOF
cp -a "$ROOT/deploy/grafana" "$tmp/grafana"
cat >"$tmp/compose.yaml" <<EOF
services:
  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_DB: \${POSTGRES_DB}
      POSTGRES_USER: \${POSTGRES_USER}
      POSTGRES_PASSWORD: \${POSTGRES_PASSWORD}
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U \$\${POSTGRES_USER} -d \$\${POSTGRES_DB}"]
      interval: 2s
      timeout: 3s
      retries: 30
    volumes:
      - postgres-data:/var/lib/postgresql/data
  grafana:
    image: $grafana_image
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      VITALD_GRAFANA_DB_NAME: \${POSTGRES_DB}
      VITALD_GRAFANA_DB_USER: \${VITALD_GRAFANA_DB_USER}
      VITALD_GRAFANA_DB_PASSWORD: \${VITALD_GRAFANA_DB_PASSWORD}
      GF_SECURITY_ADMIN_USER: \${GRAFANA_ADMIN_USER}
      GF_SECURITY_ADMIN_PASSWORD: \${GRAFANA_ADMIN_PASSWORD}
      GF_SECURITY_SECRET_KEY: \${GRAFANA_SECRET_KEY}
      GF_AUTH_ANONYMOUS_ENABLED: "false"
      GF_USERS_ALLOW_SIGN_UP: "false"
      GF_DATE_FORMATS_DEFAULT_TIMEZONE: Asia/Riyadh
      GF_PLUGINS_PLUGIN_ADMIN_ENABLED: "false"
      GF_PLUGINS_PREINSTALL_DISABLED: "true"
      TZ: Asia/Riyadh
    healthcheck:
      test: ["CMD-SHELL", "wget -q -O /dev/null http://127.0.0.1:3000/api/health"]
      interval: 2s
      timeout: 3s
      retries: 60
      start_period: 10s
    volumes:
      - grafana-data:/var/lib/grafana
      - $tmp/grafana/provisioning/datasources:/etc/grafana/provisioning/datasources:ro,Z
      - $tmp/grafana/provisioning/dashboards:/etc/grafana/provisioning/dashboards:ro,Z
      - $tmp/grafana/dashboards:/etc/grafana/dashboards:ro,Z
volumes:
  postgres-data:
  grafana-data:
EOF

set -a
# shellcheck disable=SC1090
source "$tmp/test.env"
set +a
export COMPOSE_FILE="$tmp/compose.yaml"
export COMPOSE_PROJECT_NAME="$project"
export VITALD_ENV_FILE="$tmp/test.env"
export VITALD_CONTAINER_ENGINE="$engine"
COMPOSE=("$engine" compose -f "$tmp/compose.yaml" -p "$project")

wait_healthy() {
  local service=$1 attempts=${2:-60}
  for ((i=1; i<=attempts; i++)); do
    case "$service" in
      postgres)
        if "${COMPOSE[@]}" exec -T postgres pg_isready -U vitald -d vitald >/dev/null 2>&1; then return 0; fi
        ;;
      grafana)
        if "${COMPOSE[@]}" exec -T grafana wget -q -O /dev/null http://127.0.0.1:3000/api/health >/dev/null 2>&1; then return 0; fi
        ;;
    esac
    sleep 1
  done
  printf '%s did not become healthy\n' "$service" >&2
  "${COMPOSE[@]}" logs "$service" >&2 || true
  return 1
}

apply_migrations() {
  local migration
  for migration in "$ROOT"/internal/storage/postgres/migrations/*.sql; do
    "${COMPOSE[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -U vitald -d vitald <"$migration" >/dev/null
  done
}

role_psql() {
  "${COMPOSE[@]}" exec -T -e "PGPASSWORD=$datasource_password" postgres \
    psql -v ON_ERROR_STOP=1 -h 127.0.0.1 -U vitald_grafana -d vitald "$@"
}

expect_role_denied() {
  local sql=$1
  if role_psql -c "$sql" >/dev/null 2>&1; then
    printf 'Grafana role unexpectedly succeeded: %s\n' "$sql" >&2
    return 1
  fi
}

provision_and_test_role() {
  "$ROOT/scripts/provision-grafana-db.sh" >/dev/null
  "$ROOT/scripts/provision-grafana-db.sh" >/dev/null

  local attributes settings view
  attributes=$("${COMPOSE[@]}" exec -T postgres psql -At -U vitald -d vitald -c \
    "SELECT rolsuper||':'||rolcreatedb||':'||rolcreaterole||':'||rolinherit||':'||rolreplication||':'||rolbypassrls||':'||rolconnlimit FROM pg_roles WHERE rolname='vitald_grafana'")
  [[ "$attributes" == "false:false:false:false:false:false:5" ]] || { printf 'unexpected Grafana role attributes: %s\n' "$attributes" >&2; return 1; }
  settings=$("${COMPOSE[@]}" exec -T postgres psql -At -U vitald -d vitald -c \
    "SELECT array_to_string(setconfig, ',') FROM pg_db_role_setting s JOIN pg_roles r ON r.oid=s.setrole JOIN pg_database d ON d.oid=s.setdatabase WHERE r.rolname='vitald_grafana' AND d.datname='vitald'")
  [[ "$settings" == *"default_transaction_read_only=on"* && "$settings" == *"statement_timeout=30s"* ]] || { printf 'missing Grafana role settings\n' >&2; return 1; }

  while IFS= read -r view; do
    role_psql -c "SELECT * FROM analytics.\"$view\" LIMIT 0" >/dev/null
  done < <("${COMPOSE[@]}" exec -T postgres psql -At -U vitald -d vitald -c \
    "SELECT table_name FROM information_schema.views WHERE table_schema='analytics' ORDER BY table_name")

  expect_role_denied 'SELECT * FROM health_records LIMIT 1'
  expect_role_denied "INSERT INTO health_records(record_key, metric, raw) VALUES ('forbidden', 'steps', '{}')"
  expect_role_denied 'UPDATE health_records SET metric=metric'
  expect_role_denied 'DELETE FROM health_records'
  expect_role_denied 'CREATE TABLE analytics.forbidden(id integer)'
  expect_role_denied 'CREATE TABLE public.forbidden(id integer)'
  expect_role_denied 'CREATE ROLE forbidden'

  "${COMPOSE[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -U vitald -d vitald -c \
    'CREATE OR REPLACE VIEW analytics.grafana_future_test AS SELECT 1 AS value' >/dev/null
  role_psql -c 'SELECT * FROM analytics.grafana_future_test' >/dev/null
}

start_and_test_grafana() {
  "${COMPOSE[@]}" up -d grafana >/dev/null
  wait_healthy grafana 90
  local auth response
  auth=$(printf 'admin:%s' "$grafana_password" | base64 | tr -d '\n')
  if ! response=$("${COMPOSE[@]}" exec -T grafana wget -q -O - \
    --header "Authorization: Basic $auth" \
    http://127.0.0.1:3000/api/datasources/uid/vitald-postgres/health); then
    printf '%s\n' 'Grafana datasource health request failed' >&2
    "${COMPOSE[@]}" logs grafana >&2 || true
    return 1
  fi
  [[ "$response" == *'"status":"OK"'* ]] || { printf 'Grafana datasource health failed: %s\n' "$response" >&2; return 1; }

  response=$("${COMPOSE[@]}" exec -T grafana wget -q -O - \
    --header "Authorization: Basic $auth" \
    'http://127.0.0.1:3000/api/search?folderUIDs=vitald')
  [[ "$(jq 'length' <<<"$response")" == 5 ]] || { printf 'expected five provisioned dashboards, got: %s\n' "$response" >&2; return 1; }
}

printf '%s\n' "Starting disposable Grafana integration project $project..."
"${COMPOSE[@]}" up -d postgres >/dev/null
wait_healthy postgres
apply_migrations
provision_and_test_role
start_and_test_grafana

printf '%s\n' 'Performing clean recreate and reprovision drill...'
"${COMPOSE[@]}" down -v --remove-orphans >/dev/null
"${COMPOSE[@]}" up -d postgres >/dev/null
wait_healthy postgres
apply_migrations
provision_and_test_role
start_and_test_grafana

printf '%s\n' 'Disposable Grafana integration, permissions, and recreate tests passed.'
