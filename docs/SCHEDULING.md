# vitald Unattended Scheduling

`vitald` uses rootless user-systemd services and timers. The CLI remains a terminating process: each timer launches one command, records its result in journald, and exits.

## Unit layout

| Unit | Purpose |
|---|---|
| `vitald-postgres.service` | Starts and owns the persistent Compose PostgreSQL container |
| `vitald-sync.service` / `.timer` | Synchronizes all metrics every six hours |
| `vitald-doctor.service` / `.timer` | Runs daily offline operational checks |
| `vitald-backup.service` / `.timer` | Creates a daily encrypted Restic backup |
| `vitald-verify-backup.service` / `.timer` | Performs a weekly isolated restore drill |
| `vitald-failure@.service` | Logs a failed job and invokes an optional notification hook |

Timers contain schedules; services contain commands. The generated services use absolute repository and environment-file paths and are installed under `~/.config/systemd/user`.

## Default schedule

All calendars explicitly use `Asia/Riyadh`, independently of the host timezone.

| Job | Calendar |
|---|---|
| Sync | 00:15, 06:15, 12:15, and 18:15 daily |
| Doctor | 02:00 daily |
| Backup | 03:00 daily |
| Restore verification | 04:00 every Sunday |

Each timer has a stable randomized delay of up to ten minutes, one-minute accuracy, and `Persistent=true`. A persistent timer runs a missed job after the user manager returns. Systemd coalesces missed activations into one run rather than replaying every missed interval.

Scheduled Compose runs use `--no-deps`, so they cannot reconcile or stop another one-shot container. The Compose file also disables podman-compose's shared-pod mode. Without that setting, a configuration-hash change (including a changed interpolated environment value) can make a one-off `run` replace the shared pod and stop unrelated long-lived containers such as PostgreSQL and Grafana. Services instead run as separate containers on the same Compose network, matching Docker Compose's lifecycle isolation.

The backup unit is ordered after a systemd-managed sync, and verification is ordered after backup. PostgreSQL advisory locking remains the final exclusion mechanism for manual or otherwise external overlap. If a manual sync is still active when backup starts, backup fails rather than stopping sync.

## Prerequisites

Before installation:

1. Complete OAuth authorization and at least one successful sync.
2. Configure and test `VITALD_BACKUP_REPOSITORY` and `RESTIC_PASSWORD_FILE`.
3. Run a manual restore drill:

   ```bash
   ./scripts/verify-backup.sh --snapshot latest
   ```

4. Ensure `.env`, token, raw archive, and repository credentials have appropriate permissions.

The installer supports Podman by default and Docker with `--engine docker`.

## Inspect before installation

Render and validate every unit without building images or changing systemd:

```bash
./scripts/install-systemd.sh --dry-run
```

The dry run uses `systemd-analyze verify` and prints the exact generated units. Review repository paths, environment paths, mount requirements, commands, and calendars.

## Install

```bash
./scripts/install-systemd.sh
```

The installer:

1. Loads the configured environment file.
2. Builds the `vitald`, `backup`, and `restore` images once.
3. Renders `.in` templates with absolute paths.
4. Verifies all generated units.
5. Installs them under the user systemd configuration directory.
6. Reloads the user manager.
7. Enables and starts PostgreSQL.
8. Requires one successful offline doctor check before enabling all timers.

Scheduled jobs pass `--no-build`; they use the images built during installation and never compile unattended working-tree changes.

Useful installer options:

```bash
./scripts/install-systemd.sh --no-enable
./scripts/install-systemd.sh --no-build
./scripts/install-systemd.sh --engine docker
./scripts/install-systemd.sh --env-file /absolute/path/vitald.env
./scripts/install-systemd.sh --requires-mount /mnt/nas
```

`--no-enable` installs but does not start anything. `--no-build` is appropriate after changing only unit configuration. Relative environment-file paths are resolved from the repository root.

## User lingering

Rootless user timers normally stop after logout unless lingering is enabled. The installer reports current status but does not alter login policy.

Enable it explicitly:

```bash
loginctl enable-linger "$USER"
```

Confirm:

```bash
loginctl show-user "$USER" -p Linger
```

On systems requiring administrator authorization, run the equivalent command through the local administrative process.

## Local and NAS repositories

When `VITALD_BACKUP_REPOSITORY` is a local filesystem path, the installer automatically adds `RequiresMountsFor=` to backup and verification services. For a mounted NAS, the jobs therefore wait for the path's backing mount.

Override detection explicitly when needed:

```bash
./scripts/install-systemd.sh --requires-mount /mnt/nas
```

Remote Restic repositories such as SFTP and S3 do not receive a mount requirement. Their normal network failures are recorded as failed service runs.

## Operate and inspect

List upcoming jobs:

```bash
systemctl --user list-timers 'vitald-*'
```

Inspect unit state:

```bash
systemctl --user status vitald-postgres.service
systemctl --user status vitald-sync.timer
systemctl --user --failed
```

Run jobs immediately:

```bash
systemctl --user start vitald-doctor.service
systemctl --user start vitald-sync.service
systemctl --user start vitald-backup.service
systemctl --user start vitald-verify-backup.service
```

Follow logs:

```bash
journalctl --user -u vitald-sync.service -f
journalctl --user -u vitald-backup.service --since today
journalctl --user -u vitald-verify-backup.service --since '7 days ago'
```

Show logs from the most recent invocation:

```bash
journalctl --user -u vitald-sync.service -n 100 --no-pager
```

Service output may contain provider or operational error messages. Treat scheduler logs as private operational data.

## Failure notifications

Every scheduled job has an `OnFailure=` handler. Without further configuration, it writes the unit name, systemd result, and exit status to journald.

To invoke an external notifier, add an absolute executable path to `.env`:

```dotenv
VITALD_FAILURE_HOOK=/home/yha/.local/bin/notify-vitald-failure
```

The executable receives three arguments:

```text
<unit-name> <systemd-result> <exit-status>
```

Example interface:

```bash
#!/usr/bin/env bash
unit=$1
result=$2
status=$3
# Send a message through the operator's preferred notification system.
```

The hook must be executable. It receives no OAuth token, database URL, Restic password, or journal contents from vitald. The notifier is responsible for protecting its own credentials.

## Customize schedules

Prefer systemd drop-ins so repository updates do not overwrite local schedules. For example:

```bash
systemctl --user edit vitald-sync.timer
```

Override the calendar:

```ini
[Timer]
OnCalendar=
OnCalendar=*-*-* 01,07,13,19:00:00 Asia/Riyadh
```

Then reload and restart the timer:

```bash
systemctl --user daemon-reload
systemctl --user restart vitald-sync.timer
systemctl --user list-timers 'vitald-*'
```

The empty `OnCalendar=` line clears the template value before adding the replacement.

## Update deployment

After pulling or changing application code:

```bash
./scripts/install-systemd.sh
```

This rebuilds images, refreshes generated units, reloads systemd, and leaves persistent data untouched. Running timers remain enabled.

For unit-only changes:

```bash
./scripts/install-systemd.sh --no-build
```

## Uninstall scheduling

```bash
./scripts/uninstall-systemd.sh
```

This disables timers and removes generated user units. It deliberately leaves PostgreSQL running and never removes containers, images, volumes, raw data, OAuth tokens, or Restic repositories.

Stop PostgreSQL as part of uninstall only when explicitly intended:

```bash
./scripts/uninstall-systemd.sh --stop-postgres
```

Uninstall refuses to remove units while a vitald scheduled job is still active.

## Troubleshooting

### User manager is degraded

Inspect failed units:

```bash
systemctl --user --failed
```

An unrelated failed user unit does not necessarily prevent vitald timers from running. The vitald units intentionally do not depend on `network-online.target`; provider and repository connection failures remain visible through normal command exit statuses and journald.

### PostgreSQL is unavailable

```bash
systemctl --user restart vitald-postgres.service
journalctl --user -u vitald-postgres.service -n 100 --no-pager
```

The startup wrapper waits up to 60 seconds for PostgreSQL readiness.

### Backup collided with sync

The backup exits with:

```text
synchronization is active; retry the backup later
```

This is safe and does not stop sync. Start the backup service again after synchronization finishes. A future notification hook can alert on repeated collisions.

### Verify generated configuration

```bash
./scripts/install-systemd.sh --dry-run
systemd-analyze calendar '*-*-* 00,06,12,18:15:00 Asia/Riyadh'
```
