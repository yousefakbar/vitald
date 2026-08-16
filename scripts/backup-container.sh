#!/usr/bin/env bash
set -Eeuo pipefail

readonly LOCK_NAME="vitald-sync"
readonly STAGING_DIR="/staging"

log() { printf 'vitald backup: %s\n' "$*" >&2; }
die() { log "error: $*"; exit 1; }
require_env() { [[ -n "${!1:-}" ]] || die "$1 is required"; }

wait_for_database() {
  local attempt
  for ((attempt = 1; attempt <= 60; attempt++)); do
    if psql "$DATABASE_URL" -X -qAt --set=ON_ERROR_STOP=1 -c 'SELECT 1' >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  die "PostgreSQL did not become reachable within 60 seconds"
}

acquire_sync_lock() {
  coproc VITALD_LOCK { psql "$DATABASE_URL" -X -qAt --set=ON_ERROR_STOP=1; }
  LOCK_IN=${VITALD_LOCK[1]}
  LOCK_OUT=${VITALD_LOCK[0]}
  printf "SELECT pg_try_advisory_lock(hashtextextended('%s', 0));\n" "$LOCK_NAME" >&"$LOCK_IN"
  local acquired
  IFS= read -r acquired <&"$LOCK_OUT" || die "could not acquire synchronization lock"
  [[ "$acquired" == "t" ]] || die "synchronization is active; retry the backup later"
  LOCK_HELD=true
}

release_sync_lock() {
  if [[ "${LOCK_HELD:-false}" == true ]]; then
    printf "SELECT pg_advisory_unlock(hashtextextended('%s', 0));\n\\q\n" "$LOCK_NAME" >&"$LOCK_IN" 2>/dev/null || true
    exec {LOCK_IN}>&- 2>/dev/null || true
    wait "${VITALD_LOCK_PID:-}" 2>/dev/null || true
  fi
}

initialize_repository() {
  if ! restic cat config >/dev/null 2>&1; then
    log "initializing Restic repository"
    restic init
  fi
}

backup() {
  require_env DATABASE_URL
  require_env RESTIC_REPOSITORY
  require_env RESTIC_PASSWORD_FILE
  [[ -r /source/config/token.json ]] || die "OAuth token /source/config/token.json is missing or unreadable"
  [[ -d /source/data/raw ]] || die "raw archive /source/data/raw is missing"

  mkdir -p "$STAGING_DIR"
  chmod 0700 "$STAGING_DIR"
  trap 'release_sync_lock; rm -rf "$STAGING_DIR"' EXIT
  wait_for_database
  acquire_sync_lock
  initialize_repository

  local started_at server_version dump_checksum raw_bytes token_checksum
  started_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  server_version=$(psql "$DATABASE_URL" -X -qAt --set=ON_ERROR_STOP=1 -c 'SHOW server_version')

  log "creating PostgreSQL dump"
  pg_dump "$DATABASE_URL" --format=custom --no-owner --no-privileges --file="$STAGING_DIR/database.dump"
  chmod 0600 "$STAGING_DIR/database.dump"
  dump_checksum=$(sha256sum "$STAGING_DIR/database.dump" | awk '{print $1}')
  token_checksum=$(sha256sum /source/config/token.json | awk '{print $1}')
  raw_bytes=$(find /source/data/raw -type f -exec stat -c '%s' {} + | awk '{sum += $1} END {print sum + 0}')

  jq -n \
    --arg format_version "1" \
    --arg created_at "$started_at" \
    --arg vitald_version "${VITALD_VERSION:-unknown}" \
    --arg vitald_commit "${VITALD_COMMIT:-unknown}" \
    --arg postgres_version "$server_version" \
    --arg database_sha256 "$dump_checksum" \
    --arg token_sha256 "$token_checksum" \
    --argjson raw_archive_bytes "$raw_bytes" \
    '{formatVersion:$format_version,createdAt:$created_at,vitaldVersion:$vitald_version,vitaldCommit:$vitald_commit,postgresVersion:$postgres_version,databaseDump:{path:"/staging/database.dump",sha256:$database_sha256},rawArchive:{path:"/source/data/raw",sizeBytes:$raw_archive_bytes},oauthToken:{path:"/source/config/token.json",sha256:$token_sha256}}' \
    > "$STAGING_DIR/manifest.json"
  chmod 0600 "$STAGING_DIR/manifest.json"

  log "writing encrypted Restic snapshot"
  local snapshot_id
  restic backup --json --host "${VITALD_BACKUP_HOST:-vitald}" --tag vitald --tag "created-${started_at}" \
    "$STAGING_DIR/database.dump" "$STAGING_DIR/manifest.json" \
    /source/data/raw /source/config/token.json > "$STAGING_DIR/restic-result.json"
  snapshot_id=$(jq -r 'select(.message_type == "summary") | .snapshot_id' "$STAGING_DIR/restic-result.json" | tail -1)
  [[ -n "$snapshot_id" && "$snapshot_id" != "null" ]] || die "Restic did not return a snapshot ID"

  log "applying retention policy"
  restic forget --tag vitald --group-by host,paths \
    --keep-daily "${VITALD_BACKUP_KEEP_DAILY:-7}" \
    --keep-weekly "${VITALD_BACKUP_KEEP_WEEKLY:-4}" \
    --keep-monthly "${VITALD_BACKUP_KEEP_MONTHLY:-12}" \
    --prune

  printf '%s\n' "$snapshot_id"
  log "snapshot $snapshot_id completed"
}

assert_empty_restore_target() {
  local user_tables
  user_tables=$(psql "$DATABASE_URL" -X -qAt --set=ON_ERROR_STOP=1 -c \
    "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind IN ('r','p') AND n.nspname NOT IN ('pg_catalog','information_schema');")
  [[ "$user_tables" == "0" ]] || die "target database is not empty"
  [[ ! -e /source/data/raw ]] || [[ -z "$(find /source/data/raw -mindepth 1 -print -quit 2>/dev/null)" ]] || die "target raw archive is not empty"
  [[ ! -e /source/config/token.json ]] || die "target OAuth token already exists"
}

restore() {
  require_env DATABASE_URL
  require_env RESTIC_REPOSITORY
  require_env RESTIC_PASSWORD_FILE
  local snapshot=${1:-latest}

  trap 'rm -rf "$STAGING_DIR"' EXIT
  wait_for_database
  assert_empty_restore_target
  restic cat config >/dev/null
  log "restoring snapshot $snapshot into fresh volumes"
  restic restore "$snapshot" --target / \
    --include /staging/database.dump \
    --include /staging/manifest.json \
    --include /source/data/raw \
    --include /source/config/token.json

  [[ -r "$STAGING_DIR/database.dump" ]] || die "snapshot does not contain a PostgreSQL dump"
  [[ -r "$STAGING_DIR/manifest.json" ]] || die "snapshot does not contain a manifest"
  local format_version expected actual expected_token actual_token expected_raw_bytes actual_raw_bytes
  format_version=$(jq -er '.formatVersion' "$STAGING_DIR/manifest.json")
  [[ "$format_version" == "1" ]] || die "unsupported backup manifest format $format_version"
  expected=$(jq -er '.databaseDump.sha256' "$STAGING_DIR/manifest.json")
  actual=$(sha256sum "$STAGING_DIR/database.dump" | awk '{print $1}')
  [[ "$expected" == "$actual" ]] || die "PostgreSQL dump checksum does not match the manifest"
  expected_token=$(jq -er '.oauthToken.sha256' "$STAGING_DIR/manifest.json")
  actual_token=$(sha256sum /source/config/token.json | awk '{print $1}')
  [[ "$expected_token" == "$actual_token" ]] || die "OAuth token checksum does not match the manifest"
  expected_raw_bytes=$(jq -er '.rawArchive.sizeBytes' "$STAGING_DIR/manifest.json")
  actual_raw_bytes=$(find /source/data/raw -type f -exec stat -c '%s' {} + | awk '{sum += $1} END {print sum + 0}')
  [[ "$expected_raw_bytes" == "$actual_raw_bytes" ]] || die "raw archive size does not match the manifest"

  log "restoring PostgreSQL"
  pg_restore --dbname="$DATABASE_URL" --no-owner --no-privileges --exit-on-error "$STAGING_DIR/database.dump"
  log "snapshot $snapshot restored"
}

check_repository() {
  require_env RESTIC_REPOSITORY
  require_env RESTIC_PASSWORD_FILE
  restic check
}

snapshots() {
  require_env RESTIC_REPOSITORY
  require_env RESTIC_PASSWORD_FILE
  restic snapshots --tag vitald
}

case "${1:-}" in
  backup) backup ;;
  restore) shift; restore "$@" ;;
  check) check_repository ;;
  snapshots) snapshots ;;
  *) die "usage: vitald-backup {backup|restore [snapshot]|check|snapshots}" ;;
esac
