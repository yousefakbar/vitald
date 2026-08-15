# vitald

`vitald` is a self-hosted CLI for collecting, preserving, and querying personal Fitbit data through the Google Health API. It archives provider responses, normalizes useful fields into PostgreSQL, and maintains incremental synchronization checkpoints.

> This project is not intended for medical diagnosis or clinical decision support.

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
        └── synchronization checkpoints
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
```

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

## Storage model

Normalized records live in `health_records` with common timestamp, local-date, numeric value, unit, provenance, attributes, and original-record columns. This supports direct Grafana queries while retaining provider-specific details in JSONB.

Raw response metadata and SHA-256 checksums are stored in `raw_archives`. Raw files are retained indefinitely for now.

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

`vitald sync` is deliberately a terminating command. Run it from cron, a systemd timer, or a container scheduler rather than keeping an internal scheduler daemon alive.

See [`docs/PROJECT.md`](docs/PROJECT.md) for the broader project direction.

## License

A license has not been selected yet.
