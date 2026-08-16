#!/usr/bin/env bash

backup_project_root() {
  cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd
}

load_backup_environment() {
  local root=$1
  local env_file=${VITALD_ENV_FILE:-$root/.env}
  local configured_engine=${VITALD_CONTAINER_ENGINE:-}
  if [[ "$env_file" != /* ]]; then
    env_file="$root/$env_file"
  fi
  export VITALD_ENV_FILE="$env_file"
  if [[ -f "$env_file" ]]; then
    set -a
    # shellcheck disable=SC1090
    source "$env_file"
    set +a
  fi
  export VITALD_ENV_FILE="$env_file"
  if [[ -n "$configured_engine" ]]; then
    export VITALD_CONTAINER_ENGINE="$configured_engine"
  fi
  : "${VITALD_BACKUP_REPOSITORY:?set VITALD_BACKUP_REPOSITORY}"
  : "${RESTIC_PASSWORD_FILE:?set RESTIC_PASSWORD_FILE}"
  if [[ "$RESTIC_PASSWORD_FILE" != /* ]]; then
    RESTIC_PASSWORD_FILE="$root/$RESTIC_PASSWORD_FILE"
    export RESTIC_PASSWORD_FILE
  fi
  [[ -r "$RESTIC_PASSWORD_FILE" ]] || { printf 'backup password file is not readable: %s\n' "$RESTIC_PASSWORD_FILE" >&2; return 1; }
}

wait_for_postgres() {
  local project=${1:-}
  local args=()
  [[ -z "$project" ]] || args=(-p "$project")
  local attempt
  for ((attempt = 1; attempt <= 60; attempt++)); do
    if "${COMPOSE[@]}" "${args[@]}" exec -T postgres \
      pg_isready -U "${POSTGRES_USER:-vitald}" -d "${POSTGRES_DB:-vitald}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  printf 'PostgreSQL did not become ready within 60 seconds\n' >&2
  return 1
}

select_compose_command() {
  if [[ -n "${VITALD_CONTAINER_ENGINE:-}" ]]; then
    COMPOSE=("$VITALD_CONTAINER_ENGINE" compose)
  elif command -v podman >/dev/null 2>&1; then
    COMPOSE=(podman compose)
  elif command -v docker >/dev/null 2>&1; then
    COMPOSE=(docker compose)
  else
    printf 'podman or docker is required\n' >&2
    return 1
  fi
}

absolute_path() {
  local path=$1 directory base
  directory=$(dirname "$path")
  base=$(basename "$path")
  mkdir -p "$directory"
  directory=$(cd "$directory" && pwd)
  printf '%s/%s\n' "$directory" "$base"
}

prepare_restic_run_args() {
  local repository=$VITALD_BACKUP_REPOSITORY
  local password_file
  password_file=$(absolute_path "$RESTIC_PASSWORD_FILE")
  RESTIC_RUN_ARGS=(-e RESTIC_PASSWORD_FILE=/run/secrets/restic-password -v "$password_file:/run/secrets/restic-password:ro")

  case "$repository" in
    /*|./*|../*)
      repository=$(absolute_path "$repository")
      RESTIC_RUN_ARGS+=(-e RESTIC_REPOSITORY=/repository -v "$repository:/repository")
      ;;
    *)
      RESTIC_RUN_ARGS+=(-e "RESTIC_REPOSITORY=$repository")
      ;;
  esac

  if [[ -n "${VITALD_BACKUP_SSH_DIR:-}" ]]; then
    local ssh_dir
    ssh_dir=$(cd "$VITALD_BACKUP_SSH_DIR" && pwd)
    RESTIC_RUN_ARGS+=(-v "$ssh_dir:/root/.ssh:ro")
  fi
}

compose_project_name() {
  printf '%s\n' "${COMPOSE_PROJECT_NAME:-$(basename "$(backup_project_root)")}"
}
