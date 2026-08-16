#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
safe_home=$HOME
safe_path=$PATH
safe_lang=${LANG:-C.UTF-8}
unit=${1:-}
[[ "$unit" == vitald-*.service ]] || { printf 'invalid vitald failed unit: %s\n' "$unit" >&2; exit 2; }

env_file=${VITALD_ENV_FILE:-$ROOT/.env}
if [[ "$env_file" != /* ]]; then
  env_file="$ROOT/$env_file"
fi
if [[ -r "$env_file" ]]; then
  # Read only VITALD_FAILURE_HOOK. The notifier is launched with a clean
  # environment so application and repository secrets cannot leak into it.
  # shellcheck disable=SC1090
  source "$env_file"
fi

result=$(systemctl --user show "$unit" --property=Result --value 2>/dev/null || printf 'unknown')
exit_status=$(systemctl --user show "$unit" --property=ExecMainStatus --value 2>/dev/null || printf 'unknown')
printf 'vitald scheduled unit failed: unit=%s result=%s exit_status=%s\n' "$unit" "$result" "$exit_status" >&2

if [[ -n "${VITALD_FAILURE_HOOK:-}" ]]; then
  [[ "$VITALD_FAILURE_HOOK" = /* ]] || { printf 'VITALD_FAILURE_HOOK must be an absolute executable path\n' >&2; exit 1; }
  [[ -x "$VITALD_FAILURE_HOOK" ]] || { printf 'VITALD_FAILURE_HOOK is not executable: %s\n' "$VITALD_FAILURE_HOOK" >&2; exit 1; }
  exec env -i HOME="$safe_home" PATH="$safe_path" LANG="$safe_lang" \
    "$VITALD_FAILURE_HOOK" "$unit" "$result" "$exit_status"
fi
