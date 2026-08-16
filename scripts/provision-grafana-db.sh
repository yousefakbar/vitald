#!/usr/bin/env bash
set -Eeuo pipefail

# Project root
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# Reuse Compose selection and PostgreSQL readiness helpers.
# shellcheck source=scripts/lib/backup-common.sh
source "$ROOT/scripts/lib/backup-common.sh"

load_environment() {
	local env_file=${VITALD_ENV_FILE:-$ROOT/.env}
	local configured_engine=${VITALD_CONTAINER_ENGINE:-}

	# If not absolute path, make relative to $ROOT
	if [[ "$env_file" != /* ]]; then
		env_file="$ROOT/$env_file"
	fi

	# Check if $env_file is readable
	[[ -r "$env_file" ]] || {
		printf 'environment file is not readable: %s\n' "$env_file" >&2
		return 1
	}

	export VITALD_ENV_FILE="$env_file"

	# Export all variables in the env file upon sourcing
	set -a
	# shellcheck disable=SC1090
	source "$env_file"
	set +a

	export VITALD_ENV_FILE="$env_file"

	# Explicit shell/systemd override should win over the value in $env_file
	if [[ -n "$configured_engine" ]]; then
		export VITALD_CONTAINER_ENGINE="$configured_engine"
	fi
}

sql_literal() {
	local value=$1

	# SQL string literals escape a single quote by doubling it
	value=${value//\'/\'\'}

	printf "'%s'" "$value"
}

load_environment
select_compose_command

cd "$ROOT"

# Set DB-related variables from environment
grafana_user=${VITALD_GRAFANA_DB_USER:-vitald_grafana}
grafana_password=${VITALD_GRAFANA_DB_PASSWORD:-}
postgres_user=${POSTGRES_USER:-vitald}
postgres_db=${POSTGRES_DB:-vitald}

# Ensure Grafana password is set
[[ -n "$grafana_password" ]] || {
	printf 'VITALD_GRAFANA_DB_PASSWORD is required\n' >&2
	exit 1
}

# Ensure Grafana user is set correctly
[[ "$grafana_user" =~ ^[a-z_][a-z0-9_]*$ ]] || {
	printf 'VITALD_GRAFANA_DB_USER must match ^[a-z_][a-z0-9_]*$\n' >&2
	exit 1
}

# Ensure PostgreSQL is running, otherwise start it here
if [[ -z "$("${COMPOSE[@]}" ps -q postgres 2>/dev/null || true)" ]]; then
	"${COMPOSE[@]}" up -d --no-deps postgres
fi

wait_for_postgres

role_literal=$(sql_literal "$grafana_user")
password_literal=$(sql_literal "$grafana_password")
database_literal=$(sql_literal "$postgres_db")

"${COMPOSE[@]}" exec -T postgres \
	psql \
		-v ON_ERROR_STOP=1 \
		-U "$postgres_user" \
		-d "$postgres_db" <<SQL
DO \$provision\$
DECLARE
	role_name text := $role_literal;
	role_password text := $password_literal;
	database_name text := $database_literal;
BEGIN
	IF NOT EXISTS (
		SELECT 1
		FROM pg_roles
		WHERE rolname = role_name
	) THEN
		EXECUTE format('CREATE ROLE %I LOGIN', role_name);
	END IF;

	EXECUTE format(
		'ALTER ROLE %I LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 5 PASSWORD %L',
		role_name,
		role_password
	);

	-- Remove any accidental direct privileges left by an earlier setup.
	EXECUTE format(
		'REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM %I',
		role_name
	);

	EXECUTE format(
		'REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM %I',
		role_name
	);

	EXECUTE format(
		'REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA analytics FROM %I',
		role_name
	);

	EXECUTE format(
		'GRANT CONNECT ON DATABASE %I TO %I',
		database_name,
		role_name
	);

	EXECUTE format(
		'GRANT USAGE ON SCHEMA analytics TO %I',
		role_name
	);

	EXECUTE format(
		'GRANT SELECT ON ALL TABLES IN SCHEMA analytics TO %I',
		role_name
	);

	-- Applies to future analytics views created by the current migration owner.
	EXECUTE format(
		'ALTER DEFAULT PRIVILEGES IN SCHEMA analytics GRANT SELECT ON TABLES TO %I',
		role_name
	);

	EXECUTE format(
		'ALTER ROLE %I IN DATABASE %I SET default_transaction_read_only = on',
		role_name,
		database_name
	);

	EXECUTE format(
		'ALTER ROLE %I IN DATABASE %I SET statement_timeout = %L',
		role_name,
		database_name,
		'30s'
	);
END
\$provision\$;
SQL

printf 'Provisioned read-only Grafana database role: %s\n' "$grafana_user"
