# vitald Backup and Restore

`vitald` backups are encrypted Restic snapshots containing a PostgreSQL custom-format dump, the exact raw archive, the OAuth token, and a manifest. Restore operations are host-side infrastructure workflows rather than `vitald` subcommands because they create containers and volumes and replace database state.

## Requirements

- Podman Compose or Docker Compose
- A Restic repository destination
- A password file readable only by the operator
- Enough local/container storage for a temporary PostgreSQL dump

The backup helper image supplies Restic and PostgreSQL 17 client tools; they do not need to be installed on the host. The one-shot backup/restore services run as container root so they can preserve named-volume ownership. Their SELinux process label is disabled to support read-only host password files and local/NAS repository mounts; use only the version-controlled helper image and scripts on a trusted host.

## Configuration

Add the following to `.env`:

```dotenv
VITALD_BACKUP_REPOSITORY=./backups
RESTIC_PASSWORD_FILE=.restic-password
VITALD_BACKUP_KEEP_DAILY=7
VITALD_BACKUP_KEEP_WEEKLY=4
VITALD_BACKUP_KEEP_MONTHLY=12
VITALD_BACKUP_HOST=vitald
```

Create a strong repository password and protect it separately from the repository:

```bash
umask 077
openssl rand -base64 32 > .restic-password
```

Do not commit or place the password file inside the Restic repository. Loss of this password makes the backup unrecoverable. `VITALD_BACKUP_HOST` keeps retention grouping stable across one-shot containers; give each vitald installation a unique value if several installations share one Restic repository.

### Repository targets

Local directory:

```dotenv
VITALD_BACKUP_REPOSITORY=/srv/vitald-backups
```

Mounted NAS directory:

```dotenv
VITALD_BACKUP_REPOSITORY=/mnt/nas/backups/vitald
```

The NAS should be mounted by the host before running a backup. Local and NAS paths are mounted into the one-shot backup container as `/repository`.

SFTP:

```dotenv
VITALD_BACKUP_REPOSITORY=sftp:backup@example.net:/srv/restic/vitald
VITALD_BACKUP_SSH_DIR=/home/operator/.ssh
```

The SSH directory is mounted read-only at `/root/.ssh`. Use a dedicated key and pinned `known_hosts` entry.

S3-compatible storage:

```dotenv
VITALD_BACKUP_REPOSITORY=s3:https://s3.example.net/vitald
AWS_ACCESS_KEY_ID=...
AWS_SECRET_ACCESS_KEY=...
```

Restic supports additional repository backends. Their credential environment variables can be placed in `.env`; never commit that file.

## Create a backup

Ensure PostgreSQL is running and the OAuth token and raw archive exist, then run:

```bash
./scripts/backup.sh
```

The script:

1. Builds the dedicated backup image.
2. Starts PostgreSQL only when it is not already running, then launches the helper with `--no-deps` so Compose cannot reconcile or stop an active `vitald` process.
3. Acquires the same PostgreSQL advisory lock used by `vitald sync` and aborts if synchronization is active.
4. Creates a consistent custom-format `pg_dump` in a mode-`0700` temporary directory.
5. Records dump/token checksums, versions, archive size, and timestamp in a manifest.
6. Writes an encrypted Restic snapshot containing:
   - `/staging/database.dump`
   - `/staging/manifest.json`
   - `/source/data/raw`
   - `/source/config/token.json`
7. Applies the configured retention policy and prunes unneeded data.
8. Releases the advisory lock and removes staging files.

The snapshot ID is printed only after Restic reports success. Failed backups do not run retention.

Manual `fetch` operations are not serialized by the sync advisory lock. Avoid running `fetch` during backup. The raw-before-database ingestion ordering means an accidental concurrent fetch can at worst leave extra unreferenced raw files in the snapshot, but operationally backups should run during a quiet window.

## List snapshots

```bash
./scripts/backup.sh --snapshots
```

Run a repository integrity check without performing a restore drill with:

```bash
./scripts/backup.sh --check
```

Snapshot IDs are also printed by every successful backup.

## Restore into a fresh instance

Restores intentionally target a different Compose project. This gives PostgreSQL, raw data, and OAuth configuration fresh named volumes and leaves the production instance untouched.

```bash
./scripts/restore.sh \
  --snapshot latest \
  --target-project vitald-restored
```

A specific snapshot ID can replace `latest`.

The restore script:

1. Refuses the current Compose project and projects with existing containers.
2. Creates a fresh PostgreSQL service and fresh named volumes.
3. Restores the selected Restic snapshot.
4. Verifies the database dump against the manifest checksum.
5. Refuses a non-empty target database, archive, or token volume.
6. Runs `pg_restore` without restoring ownership or privileges.
7. Starts the restored `vitald` image through `status`, applying forward migrations.
8. Runs offline `vitald doctor`.
9. Leaves only PostgreSQL running; synchronization is not started automatically.

Inspect the result:

```bash
podman compose -p vitald-restored run --rm vitald status
podman compose -p vitald-restored run --rm vitald doctor --online
```

Use `docker compose` instead when Docker is selected. `VITALD_CONTAINER_ENGINE=docker` forces Docker when both engines are installed.

A newer `vitald` can apply forward migrations to an older restored database. An older binary refuses a database containing migration versions it does not know. If the OAuth token has expired or been revoked, run `vitald auth`; this does not affect restored health data.

### Cutover

After validating the fresh restore, stop scheduling the old project and either:

- continue using the new Compose project name, or
- perform a separately planned maintenance-window cutover.

Automated in-place restore is deliberately not provided. This prevents a typo from destroying the production volumes. Fresh restore plus explicit cutover is the supported recovery path.

## Verify backups with a restore drill

```bash
./scripts/verify-backup.sh --snapshot latest
```

This performs `restic check`, restores the selected snapshot into a uniquely named temporary Compose project, runs migrations and `doctor`, and then removes the temporary containers and volumes. Preserve the temporary project for inspection with:

```bash
./scripts/verify-backup.sh --snapshot latest --keep
```

A backup is not considered operationally verified until this restore drill has succeeded.

## Security and operational notes

- The repository contains encrypted personal health data. Protect repository credentials and metadata anyway.
- The OAuth token is included only inside the encrypted Restic snapshot.
- Store the Restic password separately and include it in the homelab secret-recovery plan.
- Restrict `.env`, password files, SSH keys, and cloud credentials to the operator.
- Test restoration after changing PostgreSQL, Restic, container, or storage infrastructure.
- Monitor backup exit status and age once systemd scheduling is added.
