# vitald — Current Architecture and Implementation Status

> **Purpose:** This is the living handoff document for the project. It describes what the repository actually implements today, the decisions behind it, known gaps, and the next planned work. Update it whenever behavior, architecture, schema, deployment, or priorities change.
>
> **Last updated:** 2026-08-16 after implementing rootless systemd scheduling on top of commit `98d50e8`.

## 1. Project State at a Glance

`vitald` is a working single-user, self-hosted Google Health ingestion CLI written in Go. It authenticates with Google OAuth, fetches Fitbit/Google Health data, archives exact API response pages, normalizes selected fields into PostgreSQL, tracks incremental checkpoints, and records synchronization history.

The current deployment target is rootless Podman Compose on a homelab. The configured health-data timezone is `Asia/Riyadh`.

### Implemented

- [x] Go/Cobra CLI with signal-aware cancellation and build metadata
- [x] Google OAuth browser and headless-with-SSH-tunnel authorization flows
- [x] Secure, atomic OAuth token persistence and automatic token refresh
- [x] Google Health identity verification
- [x] REST client with pagination, request timeouts, retries, and rate-limit handling
- [x] Exact raw JSON page archival with SHA-256 checksums
- [x] PostgreSQL schema and embedded versioned migrations
- [x] Normalization for the initial metric set
- [x] Idempotent record upserts
- [x] Explicit `Asia/Riyadh` civil-day semantics and closed-open ranges
- [x] Manual metric fetching
- [x] Incremental multi-metric synchronization with two-day overlap
- [x] Per-metric checkpoints updated only after successful persistence
- [x] Overall and per-metric synchronization run history
- [x] PostgreSQL session advisory lock preventing concurrent `sync` executions
- [x] Recovery/finalization of stale `running` synchronization rows
- [x] `vitald doctor` offline diagnostics, JSON output, and optional online identity check
- [x] Encrypted Restic backup, retention, repository checking, and fresh-instance restore drill
- [x] Rootless systemd services and timers for PostgreSQL, sync, doctor, backup, and restore verification
- [x] Partial-failure behavior: successful metrics persist even if another fails
- [x] Text or JSON structured logging through `log/slog`
- [x] Multi-stage container image and Podman/Docker Compose deployment
- [x] Unit, race, HTTP fixture, filesystem, and PostgreSQL integration tests

### Not implemented yet

- [ ] Stable analytics SQL views
- [ ] Grafana provisioning and dashboards
- [ ] Historical backfill workflow beyond manual `fetch`
- [ ] Raw archive retention, compression, or pruning
- [ ] TimescaleDB (intentionally deferred)
- [ ] Separate HTTP API or multi-user support (not currently needed)

## 2. Architectural Decisions

### Language and CLI

- **Go 1.26.4**
- **Cobra** for commands, flags, help, and completion
- One terminating CLI process rather than an always-running daemon
- External scheduling is preferred over an internal scheduler

Go was selected for a typed implementation, simple deployment, one compiled executable, strong HTTP/OAuth support, and suitability for scheduled homelab jobs.

### Provider API and transport

- Provider: **Google Health API v4** at `https://health.googleapis.com/v4`
- Authentication: Google OAuth 2.0 with offline access
- Transport: direct REST over an OAuth-authenticated `net/http` client
- Google-generated `health/v4` Go types are used to decode payloads

Direct REST is intentional: it allows `vitald` to retain the exact response body before decoding and normalization. gRPC is not currently needed.

### Storage

- **PostgreSQL 17** is the normalized store
- A generic `health_records` table holds common typed columns plus JSONB attributes, provenance, and the individual provider record
- Exact API pages are retained on a filesystem volume
- Raw archive metadata and checksums are stored in PostgreSQL
- Migrations are embedded in the binary and applied automatically

TimescaleDB is deferred. Personal heart-rate volume is expected to be manageable in plain PostgreSQL; reconsider it only after real size and query measurements justify hypertables, compression, or continuous aggregates.

### Time semantics

- Configured IANA timezone: `Asia/Riyadh`
- CLI date ranges are closed-open: `[from, to)`
- Daily metrics use civil dates
- Sample metrics use physical RFC 3339 timestamps
- PostgreSQL timestamps use `timestamptz`; daily records use `date`

### Reliability semantics

- Exact raw response pages are written before normalization
- Archive writes use temporary files, `fsync`, and atomic rename
- Database writes use deterministic keys and `ON CONFLICT` upserts
- Checkpoints advance only after all pages for a metric are archived, normalized, and stored
- Scheduled synchronization re-fetches two days before the checkpoint to capture late provider updates
- Repeated fetches create additional raw archives but do not duplicate normalized daily/provider-identified records

## 3. Runtime Architecture

```text
┌─────────────────────────────┐
│ User / scheduler            │
│                             │
│ vitald auth/fetch/sync/...  │
└──────────────┬──────────────┘
               │
               ▼
┌─────────────────────────────┐
│ Cobra CLI                   │
│ internal/cli                │
│                             │
│ - parse and validate flags  │
│ - construct runtime         │
│ - print summaries           │
└──────────────┬──────────────┘
               │
        ┌──────┴─────────┐
        ▼                ▼
┌───────────────┐  ┌─────────────────────┐
│ Google OAuth  │  │ PostgreSQL          │
│ token store   │  │                     │
└───────┬───────┘  │ - normalized data   │
        │          │ - checkpoints       │
        ▼          │ - archive metadata  │
┌───────────────┐  │ - run history       │
│ Google Health │  └──────────▲──────────┘
│ REST API      │             │
└───────┬───────┘             │
        │ exact JSON          │ normalized records
        ▼                     │
┌───────────────┐             │
│ Ingestion     ├─────────────┘
│ orchestration │
└───────┬───────┘
        │ exact pages first
        ▼
┌─────────────────────────────┐
│ Raw filesystem archive      │
│ /data/raw/googlehealth/...  │
└─────────────────────────────┘
```

### Dependency construction

`cmd/vitald/main.go` creates a cancellation context for `SIGINT` and `SIGTERM`, then executes the Cobra root command.

For data commands, `internal/cli/runtime.go`:

1. Loads environment configuration.
2. Validates OAuth and database requirements.
3. Loads and refreshes the OAuth token.
4. Persists the refreshed token atomically.
5. Creates the authenticated HTTP client.
6. Connects to PostgreSQL.
7. Applies pending migrations.
8. Loads the configured timezone.
9. Constructs the Google client, archive, storage, and ingestion service.

`status` and `runs` need PostgreSQL but do not require a valid OAuth token. `doctor` constructs checks independently so it can report multiple configuration, token, filesystem, database, migration, and synchronization problems in one invocation.

## 4. Repository Layout

```text
cmd/vitald/main.go
    Executable entry point, signal handling, and build metadata.

internal/cli/
    Cobra commands and dependency wiring:
    auth, identity, fetch, sync, status, runs list/show, doctor.

internal/config/
    Environment-based configuration and validation.

internal/provider/googlehealth/
    OAuth flow/token store and direct Google Health REST client.

internal/archive/
    Atomic filesystem archive and SHA-256 metadata.

internal/ingest/
    Metric definitions, pagination, chunking, normalization,
    deterministic keys, archival, persistence, and checkpoints.

internal/storage/postgres/
    pgx store, embedded migrations, health records, checkpoints,
    archive metadata, and synchronization history.

internal/storage/postgres/migrations/
    001_initial.sql
    002_sync_history.sql

compose.yaml
    PostgreSQL and one-shot vitald container services with named volumes.

Dockerfile
    Multi-stage Go build; embeds version, commit, and build date.

Dockerfile.backup
    One-shot Restic and PostgreSQL-client image for backup and restore.

scripts/
    Host backup, restore, verification, systemd installation, failure handling,
    and container-side orchestration scripts.

deploy/systemd/user/
    Version-controlled rootless service templates and timer units.

docs/BACKUP_RESTORE.md
    Repository configuration, backup, fresh restore, verification, and cutover procedures.

docs/SCHEDULING.md
    Systemd installation, schedules, operation, customization, and troubleshooting.

docs/PROJECT.md
    Long-term goals, principles, and high-level intended direction.

docs/CURRENT_STATE.md
    This implementation and handoff document.
```

## 5. CLI Surface

```text
vitald auth [--no-open]
    Perform Google OAuth authorization, store the token, and verify identity.

vitald identity
    Refresh credentials and call users/me/identity.

vitald fetch <metric> --from YYYY-MM-DD --to YYYY-MM-DD
    Fetch, archive, normalize, and store one explicit range.
    Does not update checkpoints and does not create sync-run history.

vitald sync [--initial-days 30]
    Incrementally synchronize every supported metric.
    Updates checkpoints and records run/metric history.

vitald status
    Show the latest synchronization summary and all checkpoints.

vitald runs list [--limit 20]
    List recent synchronization runs.

vitald runs show <run-id>
    Show one run and all per-metric results.

vitald doctor [--json] [--online]
    Run non-destructive operational checks. By default it does not contact Google.
    --online refreshes and persists the OAuth token, then verifies identity.
```

Important behavior:

- `fetch` is for explicit debugging/backfill ranges.
- `sync` is the automation-oriented command.
- A partial sync persists successful metrics, records failures, completes the run as `partial`, and returns a non-zero exit status.
- Graceful cancellation is recorded as `cancelled`; after a hard kill, the next sync recovers abandoned `running` rows.
- `doctor` exits non-zero only when one or more checks fail; warnings retain a zero exit status.
- `doctor` does not apply pending migrations or recover stale runs. It reports them for an operator or the next data command.

## 6. Supported Metrics and Normalization

| CLI/API metric | Retrieval | Primary normalized value | Unit | Important attributes |
|---|---|---:|---|---|
| `steps` | daily rollup | daily step sum | `steps` | complete rollup object |
| `heart-rate` | list/sample | beats per minute | `bpm` | heart-rate metadata and source |
| `daily-resting-heart-rate` | list/daily | resting BPM | `bpm` | calculation metadata |
| `daily-heart-rate-variability` | list/daily | average HRV when present | `ms` | deep-sleep RMSSD, entropy, non-REM HR |
| `sleep` | list/session | minutes asleep, or interval duration fallback | `minutes` | summary, stages, type, metadata |
| `exercise` | list/session | elapsed duration | `minutes` | type, display name, active duration, calories, active-zone minutes, metrics summary |
| `total-calories` | daily rollup | total calories burned | `kcal` | complete rollup object |
| `nutrition-log` | daily rollup | total logged calories ingested | `kcal` | complete nutrition rollup |
| `weight` | list/sample | weight | `kg` | notes and original grams |

### Known upstream data gaps

The documented Google Health payloads currently used by the project do not expose:

- Fitbit sleep score
- Fitbit cardio load

The normalized attributes explicitly set `scoreAvailable: false` and `cardioLoadAvailable: false`. Active-zone minutes are retained but are not mislabeled as cardio load. Raw pages are preserved so future API fields can be reprocessed.

## 7. API Range, Pagination, and Retry Behavior

### Daily rollups

Used for `steps`, `total-calories`, and `nutrition-log`.

- Window size: one civil day
- `steps` and most daily rollups: maximum request chunk of 90 days
- `total-calories`: maximum request chunk of 14 days
- Rollup page size equals the requested civil-day count, capped at the API maximum

The page-size cap is required because Google validates `window_size_days × page_size`, even for a shorter requested range.

### List endpoints

- `sleep` and `exercise`: page size 25, matching the API maximum
- Other list metrics: page size 10,000
- Page tokens are followed until empty

### Filtering

- Heart rate and weight use physical timestamp filters
- Daily RHR and HRV use date filters
- Sleep is attributed by civil end date
- Exercise is attributed by civil start date

### Retries

- Up to five retries
- Retries transport errors, HTTP 429, and HTTP 5xx
- Exponential backoff with jitter
- Other HTTP 4xx responses fail immediately
- Response bodies are capped at 64 MiB per request

## 8. Raw Archive

Default local layout:

```text
data/raw/googlehealth/<metric>/
  <from>_<to>_<UTC-run-timestamp>/
    page-0001.json
    page-0002.json
```

Compose layout:

```text
/data/raw/googlehealth/<metric>/...
```

Each page is:

1. Written exactly as returned by Google.
2. Stored with mode `0640` under directories with mode `0750`.
3. Written through a temporary file and atomically renamed.
4. Has its path, size, SHA-256, range, page number, and archive timestamp recorded in `raw_archives`.

Retention is currently unlimited. Repeated manual fetches intentionally create new immutable archive runs.

## 9. PostgreSQL Model

### `health_records`

Generic normalized record table:

- `record_key`: deterministic SHA-256 key and primary key
- `metric`: provider/CLI metric identifier
- `provider_record_id`: Google record name where available
- `observed_at`, `ended_at`: physical sample/session timestamps
- `local_date`: daily metric date
- `value`, `unit`: main queryable numeric value
- `attributes`: metric-specific normalized JSONB
- `source`: Google provider/device provenance JSONB
- `raw`: individual decoded provider data point as JSONB
- `imported_at`: latest upsert time

Idempotency keys:

1. Provider-identified records: hash of metric and provider ID.
2. Daily records without an ID: hash of metric and local date, allowing late-value updates.
3. Other anonymous records: hash of metric, timestamps, value, and raw record.

### `raw_archives`

Metadata for exact page files. The `run_id` here is an archive-directory identifier, not the numeric synchronization run ID.

### `sync_checkpoints`

One `synced_through` timestamp per metric. Only `sync`, not manual `fetch`, advances these checkpoints.

### `sync_runs`

One row per `vitald sync` execution. Statuses:

- `running`
- `succeeded`
- `partial`
- `failed`
- `cancelled`

Stores start/completion times, initial-days setting, timezone, binary version, and a bounded error summary.

### `sync_run_metrics`

One row per metric per sync run. Stores:

- status (`running`, `succeeded`, `failed`, `cancelled`, `skipped`)
- range start/end
- checkpoint before/after
- pages archived
- records processed
- start/completion times
- bounded error message

### `sync_run_history`

View aggregating overall metric counts, pages, records, status, version, and errors for run-history display and future monitoring.

Existing pre-migration health records are not assigned to synthetic runs. History starts with the first sync after migration `002_sync_history.sql`.

## 10. Synchronization Lifecycle

```text
load configuration and credentials
    → connect/migrate PostgreSQL
    → acquire the PostgreSQL session advisory lock
    → fail immediately if another sync holds the lock
    → finalize abandoned running history from a previous process
    → create sync_runs(status=running)
    → determine exclusive end at tomorrow's Asia/Riyadh midnight
    → for each supported metric:
        read checkpoint
        choose checkpoint-minus-two-days or initial-days range
        create sync_run_metrics(status=running)
        fetch each page/chunk
        archive exact page
        store archive metadata
        normalize records
        upsert records
        update checkpoint after complete metric success
        finalize metric history
    → derive overall succeeded/partial/failed status
    → finalize sync run
```

If one metric fails, later metrics are still attempted. Completed pages and records are counted even when a later page in that metric fails. Cleanup uses a short context detached from cancellation so graceful `SIGINT`/`SIGTERM` can finalize history and release the advisory lock.

The advisory lock is held on a dedicated pooled PostgreSQL connection for the complete sync. A second sync exits before creating run history. PostgreSQL releases the lock automatically if the process or connection dies. After acquiring the lock, the next sync atomically marks abandoned `running` metrics and runs as failed before creating its own run.

## 11. Configuration and Secrets

Environment variables:

| Variable | Required | Default |
|---|---:|---|
| `GOOGLE_CLIENT_ID` | for auth/data commands | none |
| `GOOGLE_CLIENT_SECRET` | for auth/data commands | none |
| `GOOGLE_REDIRECT_URL` | no | `http://127.0.0.1:8765/callback` |
| `DATABASE_URL` | for DB/data commands | none |
| `VITALD_TIMEZONE` | no | `Asia/Riyadh` |
| `VITALD_TOKEN_PATH` | no | `~/.config/vitald/token.json` |
| `VITALD_RAW_DATA_PATH` | no | `data/raw` |
| `VITALD_LOG_FORMAT` | no | `text` |
| `VITALD_HTTP_TIMEOUT` | no | `60s` |

Backup-only environment variables:

| Variable | Required | Default |
|---|---:|---|
| `VITALD_BACKUP_REPOSITORY` | for backup/restore | none |
| `RESTIC_PASSWORD_FILE` | for backup/restore | none |
| `VITALD_BACKUP_KEEP_DAILY` | no | `7` |
| `VITALD_BACKUP_KEEP_WEEKLY` | no | `4` |
| `VITALD_BACKUP_KEEP_MONTHLY` | no | `12` |
| `VITALD_BACKUP_HOST` | no | `vitald` |
| `VITALD_BACKUP_SSH_DIR` | for SFTP keys | none |
| `VITALD_CONTAINER_ENGINE` | no | auto-detect Podman, then Docker |
| `VITALD_FAILURE_HOOK` | no | journald-only failure reporting |

OAuth scopes currently requested:

- `googlehealth.activity_and_fitness.readonly`
- `googlehealth.health_metrics_and_measurements.readonly`
- `googlehealth.sleep.readonly`
- `googlehealth.nutrition.readonly`

The OAuth token file is written atomically with mode `0600`; its parent directory is mode `0700`. Never commit `.env`, OAuth tokens, raw health data, database dumps, or exported records.

Google OAuth projects in testing mode generally issue refresh tokens that expire after seven days. Re-run `vitald auth` when required.

## 12. Container Deployment

`compose.yaml` defines:

### `postgres`

- Image: `postgres:17-alpine`
- Health check through `pg_isready`
- Named volume: `postgres-data`
- Not exposed to the host network by default

### `vitald`

- Built from the multi-stage `Dockerfile`
- Runs as an unprivileged `vitald` user
- One-shot command; default command is `status`
- Port `127.0.0.1:8765` is published for OAuth callback
- Named volume `vitald-config` mounted at `/config`
- Named volume `vitald-data` mounted at `/data`
- Connects to PostgreSQL over the Compose network

The build embeds version, Git commit, and build date. Compose build arguments can override them with `VITALD_VERSION`, `VITALD_COMMIT`, and `VITALD_BUILD_DATE`; otherwise the build reads Git metadata from the build context.

Common commands:

```bash
podman compose build vitald
podman compose up -d postgres
podman compose run --rm --service-ports vitald auth
podman compose run --rm vitald fetch steps --from 2026-08-01 --to 2026-08-08
podman compose run --rm vitald sync
podman compose run --rm vitald status
podman compose run --rm vitald runs list
podman compose run --rm vitald doctor
```

`--service-ports` is needed for `auth`, not routine fetch/sync/status commands.

### Backup services

The `backup` Compose profile contains separate read-only backup and writable fresh-restore services built from `Dockerfile.backup`. Host wrappers configure local/NAS or remote Restic repositories without adding backup tools to the application image:

```bash
./scripts/backup.sh
./scripts/backup.sh --snapshots
./scripts/restore.sh --snapshot latest --target-project vitald-restored
./scripts/verify-backup.sh --snapshot latest
```

Backups acquire the synchronization advisory lock, create a custom-format PostgreSQL dump, and snapshot it with the raw archive, OAuth token, and manifest. The default retention is 7 daily, 4 weekly, and 12 monthly snapshots. Restore refuses the production project and non-empty targets. See `docs/BACKUP_RESTORE.md`.

### Rootless systemd scheduling

`deploy/systemd/user` contains rendered-at-install service templates and fixed timer units. `scripts/install-systemd.sh` validates units, builds images once, installs them under the user manager, starts PostgreSQL, and enables timers. Scheduled jobs run prebuilt images through `--no-deps`; they never rebuild unattended or reconcile another Compose process. Calendars explicitly use `Asia/Riyadh`, persistent timers catch downtime, and an optional executable failure hook can forward alerts. See `docs/SCHEDULING.md`.

## 13. Test and Validation State

Automated checks used for the current implementation:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/vitald
```

Test coverage includes:

- date parsing and Asia/Riyadh offset handling
- physical and civil filter generation
- daily rollup request range/page-size limits
- raw response preservation
- OAuth token round-trip and file permissions
- atomic archive behavior and overwrite rejection
- sleep and exercise normalization
- stable daily record keys
- sync status derivation
- bounded UTF-8-safe run errors
- doctor report aggregation, JSON output, token diagnostics, and non-destructive archive-path checks
- systemd template rendering, unit verification, calendar parsing, clean failure-hook environment, and rootless execution wrappers
- PostgreSQL migration diagnostics, downgrade protection, synchronization-history CRUD, advisory-lock exclusion, and simulated connection-loss recovery when `VITALD_TEST_DATABASE_URL` is set

The PostgreSQL integration test has been run against a real PostgreSQL 17 container. The application and backup images have been built with rootless Podman. Systemd units are rendered and checked with `systemd-analyze verify`, and every calendar expression is validated with `systemd-analyze calendar`. The user has validated OAuth, real Google Health fetching, raw archive inspection, normalized PostgreSQL inspection, incremental sync, and run-history commands in the Compose deployment. Backup and fresh-project restore have been exercised end to end against a temporary local Restic repository, including database records, raw files, token restoration, forward migration startup, and doctor checks. Active-lock testing confirms backup exits without Compose stopping the running `vitald` container. Run the automated restore drill against the configured production repository before relying on it.

## 14. Known Limitations and Risks

1. **Scheduler installation is host-local.** The version-controlled units are implemented but must be installed explicitly, user lingering must be enabled, and the configured production repository needs a successful restore drill.
2. **Failure delivery is optional.** Failed jobs are visible in journald and systemd; email, Telegram, or another external destination requires `VITALD_FAILURE_HOOK`.
3. **No raw-data retention policy.** Provider pages remain indefinitely in the live archive; Restic snapshot retention is separate and implemented.
4. **Generic JSONB schema.** It is flexible and queryable, but analytics views are still needed before Grafana dashboards are stable and convenient.
5. **List endpoints currently use `dataPoints.list`, not the reconciled stream.** With multiple overlapping sources, future work should evaluate whether normalization should use `dataPoints:reconcile` while retaining raw source pages.
6. **Manual fetches are outside run history and synchronization locking.** Avoid manual fetches during backup. `sync_runs` tracks only `vitald sync`; `fetch` is represented through archives and imported records.
7. **Archive metadata is not linked to numeric sync-run IDs.** `raw_archives.run_id` is the archive directory timestamp identifier.
8. **OAuth is single-user and file-backed.** This matches the current project scope but is not a multi-user secret-management design. OAuth projects in testing mode can still require reauthorization every seven days.
9. **Restore is fresh-instance only.** Automated in-place restore is intentionally excluded to avoid accidental production-volume destruction; cutover is an explicit operator action.

## 15. Next Planned Work

### Immediate next milestone: stable analytics views

Define query contracts and add versioned SQL views for daily summaries, heart-rate trends, sleep, exercise, and pipeline freshness before coupling Grafana dashboards directly to the generic JSONB schema.

### Planned order

1. stable daily/heart-rate/sleep/exercise/pipeline SQL views
2. Grafana datasource and version-controlled dashboards
3. controlled historical backfill
4. evidence-based TimescaleDB evaluation

## 16. Future-Agent Handoff Checklist

At the beginning of a new session:

1. Read `docs/CURRENT_STATE.md` completely.
2. Read `docs/PROJECT.md` for long-term principles and non-goals.
3. Read `README.md` for user-facing workflows.
4. Run:

   ```bash
   git status --short
   git log --oneline -5
   go test ./...
   go vet ./...
   ```

5. Do not read or print `.env`, OAuth token files, raw health payloads, or database contents unless the user explicitly asks.
6. Preserve the raw-before-normalized ordering, closed-open ranges, explicit timezone handling, and checkpoint-after-success invariant.
7. Add a versioned SQL migration for every schema change; never modify an already-applied migration.
8. Add unit tests and, for storage changes, extend the gated PostgreSQL integration test.
9. Update this document before committing any architectural or status-changing milestone.

At the end of a milestone:

1. Run `gofmt`, tests, race tests, vet, and a build.
2. Run relevant PostgreSQL/container integration checks.
3. Update the implementation checklist, limitations, and next planned work in this document.
4. Ensure no health data, `.env`, tokens, dumps, or local exports are staged.
