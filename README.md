# vitald

`vitald` is a self-hosted health data platform for collecting, preserving, and analyzing personal health data from Fitbit devices through the Google Health API.

The project aims to keep health history independently accessible outside vendor applications while providing a reliable foundation for custom queries, dashboards, and long-term trend analysis.

> `vitald` is an early-stage personal project. It is not intended for medical diagnosis or clinical decision support.

## Goals

- Fetch health and fitness data through the Google Health API.
- Preserve original provider responses for inspection and reprocessing.
- Normalize useful metrics into a stable, queryable representation.
- Support incremental and idempotent synchronization.
- Run reliably on a self-hosted homelab.
- Provide data for SQL analysis and replaceable dashboard tools such as Grafana.

Initial metrics of interest include steps, heart rate, resting heart rate, HRV, sleep, workouts, and energy expenditure.

## Architecture

```text
Google Health API
        │
        ▼
  vitald CLI ingestor
        │
        ├──► Raw JSON archive
        │
        └──► Normalized database
                    │
                    ▼
             Queries and dashboards
```

The ingestion pipeline is the first priority. Database storage, scheduled synchronization, and dashboards will be introduced after fetching and archiving one metric works reliably.

See [`docs/PROJECT.md`](docs/PROJECT.md) for the complete project direction and design principles.

## Current status

The repository currently contains the initial Go and Cobra CLI scaffold. The `fetch steps` command validates a date range and prints the intended operation; it does not call the Google Health API yet.

Planned initial command structure:

```text
vitald
├── auth
├── fetch
│   └── steps
├── sync
└── status
```

## Requirements

- Go 1.26.4 or later
- A Google Cloud project with the Google Health API enabled (required once API integration is implemented)
- OAuth 2.0 credentials with the necessary Google Health read scopes

## Development

Clone the repository and download its dependencies:

```bash
git clone https://github.com/yousefakbar/vitald.git
cd vitald
go mod download
```

Run the CLI directly:

```bash
go run ./cmd/vitald --help
```

Build a local executable:

```bash
go build -o bin/vitald ./cmd/vitald
./bin/vitald --help
```

Run the current placeholder command:

```bash
go run ./cmd/vitald fetch steps \
  --from 2026-08-01 \
  --to 2026-08-08
```

Date ranges use a half-open interval: `--from` is inclusive and `--to` is exclusive.

Run checks:

```bash
go test ./...
go vet ./...
```

## Repository structure

```text
.
├── cmd/vitald/       # executable entry point
├── internal/cli/     # Cobra commands and argument handling
├── docs/             # project design and architecture
├── go.mod
└── README.md
```

Provider integration, raw archival, normalization, and storage packages will be added incrementally as the first end-to-end ingestion path is implemented.

## Roadmap

1. Implement Google OAuth 2.0 authorization and refresh-token handling.
2. Fetch steps for an explicit time range.
3. Archive raw API responses locally.
4. Add fixture-based parsing and normalization tests.
5. Persist normalized records in PostgreSQL.
6. Add checkpoints, retries, pagination, and idempotent synchronization.
7. Expand support to heart rate, HRV, sleep, and other metrics.
8. Connect the normalized store to dashboards.

## Data and secrets

Do not commit OAuth credentials, access tokens, refresh tokens, health data, or local configuration. The repository ignores `.env`, `*.local`, and the local `data/` directory by default.

## License

A license has not been selected yet.
