# vitald

`vitald` is a self-hosted CLI for collecting, preserving, and querying personal Fitbit data through the Google Health API. It archives provider responses, normalizes useful fields into PostgreSQL, and maintains incremental synchronization checkpoints.

> This project is not intended for medical diagnosis or clinical decision support.

- [`docs/CURRENT_STATE.md`](docs/CURRENT_STATE.md): living architecture, implementation status, limitations, and agent handoff
- [`docs/PROJECT.md`](docs/PROJECT.md): long-term goals and design direction
- [`docs/BACKUP_RESTORE.md`](docs/BACKUP_RESTORE.md): encrypted backup and disaster-recovery workflow
- [`docs/SCHEDULING.md`](docs/SCHEDULING.md): rootless systemd services, timers, logs, and operations

## Supported data

- daily steps
- heart-rate samples
- daily resting heart rate
- daily HRV
- sleep sessions and duration
- exercises, duration, calories, and active-zone minutes
- daily calories burned
- daily calories ingested, when nutrition logs are available
- weight

The current Google Health schema does not expose Fitbit sleep score or cardio load. `vitald` records their unavailability explicitly and preserves raw responses so these fields can be added if the API exposes them later. Active-zone minutes are retained but are not mislabeled as cardio load.

## Data flow

```text
Google Health API
        │
        ▼
     vitald
        ├── exact JSON response archive
        ├── normalized PostgreSQL records
        ├── synchronization checkpoints
        └── synchronization run history
                         │
                         ▼
                 SQL / Grafana
```

All time ranges are closed-open (`[from, to)`). Daily boundaries default to `Asia/Riyadh` and can be changed with `VITALD_TIMEZONE`.

## Requirements

- Go 1.26.4 or later, or Docker
- PostgreSQL 17+
- A Google Cloud project with the Google Health API enabled
- A Google OAuth web client with this authorized redirect URI:

```text
http://127.0.0.1:8765/callback
```

Google Health scopes are restricted. In Google Cloud OAuth testing mode, refresh tokens generally expire after seven days.

## Configuration

Copy the example and fill in your credentials. For direct local execution, export it into the shell:

```bash
cp .env.example .env
set -a
source .env
set +a
```

Docker Compose reads `.env` automatically.

Important variables:

| Variable | Default | Purpose |
|---|---|---|
| `GOOGLE_CLIENT_ID` | required | OAuth client ID |
| `GOOGLE_CLIENT_SECRET` | required | OAuth client secret |
| `GOOGLE_REDIRECT_URL` | `http://127.0.0.1:8765/callback` | OAuth callback |
| `DATABASE_URL` | required | PostgreSQL connection URL |
| `VITALD_TIMEZONE` | `Asia/Riyadh` | IANA daily-boundary time zone |
| `VITALD_TOKEN_PATH` | `~/.config/vitald/token.json` | OAuth token file |
| `VITALD_RAW_DATA_PATH` | `data/raw` | raw archive root |
| `VITALD_LOG_FORMAT` | `text` | `text` or `json` |

Tokens are written atomically with `0600` permissions. Health data, `.env`, and tokens must not be committed.

## Local development

```bash
go mod download
go test ./...
go vet ./...
go build -o bin/vitald ./cmd/vitald
```

Authorize access. The URL is always printed. On a desktop, run:

```bash
./bin/vitald auth
# Use --no-open to prevent automatic browser launch.
```

For a headless homelab, forward the callback port and then open the printed URL on your desktop:

```bash
ssh -L 8765:127.0.0.1:8765 your-homelab
./bin/vitald auth --no-open
```

The SSH tunnel carries the browser's `127.0.0.1:8765` callback to the homelab process.

Verify authentication:

```bash
./bin/vitald identity
```

Check operational readiness without contacting Google. Add `--online` to refresh the OAuth token and verify Google Health identity, or `--json` for machine-readable output:

```bash
./bin/vitald doctor
./bin/vitald doctor --online
./bin/vitald doctor --json
```

`doctor` checks configuration, token security and refresh capability, archive writeability, PostgreSQL connectivity and migrations, the synchronization lock, and abandoned run history. It does not apply migrations or recover stale runs. Warnings retain a zero exit status; failed checks return non-zero.

Fetch one metric:

```bash
./bin/vitald fetch steps --from 2026-08-01 --to 2026-08-08
./bin/vitald fetch heart-rate --from 2026-08-01 --to 2026-08-02
```

Synchronize all metrics. The first run defaults to 30 days of history and subsequent runs use checkpoints with a two-day overlap for late provider updates:

```bash
./bin/vitald sync
./bin/vitald sync --initial-days 90
./bin/vitald status
./bin/vitald runs list
./bin/vitald runs show 1
```

Each `sync` execution records its overall status, duration, page and record counts, per-metric ranges, checkpoints, and errors. A run is marked `partial` when some metrics succeed and others fail. Interrupted runs are marked `cancelled` when graceful cleanup is possible. A PostgreSQL advisory lock prevents overlapping syncs; after a hard kill, the next sync finalizes abandoned `running` history as failed.

Database migrations are embedded in the binary and applied automatically before data commands.

## Docker Compose

```bash
cp .env.example .env
# Set GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, and POSTGRES_PASSWORD.
docker compose up -d postgres
docker compose run --rm --service-ports vitald auth
docker compose run --rm vitald sync --initial-days 30
```

The Compose deployment uses persistent volumes for PostgreSQL, raw data, and OAuth configuration.

## Backup and restore

Encrypted Restic backups include a PostgreSQL dump, raw archives, the OAuth token, and a checksum manifest. Configure `VITALD_BACKUP_REPOSITORY` and `RESTIC_PASSWORD_FILE`, then run:

```bash
./scripts/backup.sh
./scripts/backup.sh --snapshots
./scripts/verify-backup.sh --snapshot latest
```

Restore into a fresh, isolated Compose project:

```bash
./scripts/restore.sh --snapshot latest --target-project vitald-restored
```

In-place restore is intentionally unsupported. See [`docs/BACKUP_RESTORE.md`](docs/BACKUP_RESTORE.md) for local, mounted-NAS, SFTP, S3, restore, and cutover instructions.

## Storage model

Normalized records live in `health_records` with common timestamp, local-date, numeric value, unit, provenance, attributes, and original-record columns. This supports direct Grafana queries while retaining provider-specific details in JSONB.

Raw response metadata and SHA-256 checksums are stored in `raw_archives`. Raw files are retained indefinitely for now.

Synchronization history is stored in `sync_runs` and `sync_run_metrics`; the `sync_run_history` view provides aggregate status and volume totals. Existing health data is not retroactively assigned to a run—history begins with the first `sync` after the migration is applied.

Plain PostgreSQL is intentional at the current personal-data scale. TimescaleDB may be useful later for heart-rate hypertables, compression, and continuous aggregates, but adding it now would create deployment complexity without a demonstrated need.

## Repository structure

```text
cmd/vitald/                         executable entry point
internal/cli/                       Cobra commands and dependency wiring
internal/config/                    environment configuration
internal/provider/googlehealth/     OAuth and Google Health REST client
internal/archive/                   atomic raw response archive
internal/ingest/                    pagination, normalization, orchestration
internal/storage/postgres/          PostgreSQL store and embedded migrations
docs/PROJECT.md                     high-level design
```

## Scheduling

`vitald sync` remains a terminating command. Version-controlled rootless user-systemd units schedule sync every six hours, daily doctor and backup jobs, and a weekly isolated restore drill.

Inspect generated units without changing the system, then install them:

```bash
./scripts/install-systemd.sh --dry-run
./scripts/install-systemd.sh
loginctl enable-linger "$USER"
```

See [`docs/SCHEDULING.md`](docs/SCHEDULING.md) for schedules, logging, notification hooks, customization, updates, and uninstall instructions. See [`docs/CURRENT_STATE.md`](docs/CURRENT_STATE.md) for current implementation context and [`docs/PROJECT.md`](docs/PROJECT.md) for broader direction.

## License

A license has not been selected yet.
