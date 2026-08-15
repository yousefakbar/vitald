# vitald — Self-Hosted Health Data Platform

> This document describes long-term goals and design direction. For the architecture and behavior currently implemented in the repository, read [`CURRENT_STATE.md`](CURRENT_STATE.md).

## Overview

`vitald` is a self-hosted health data project for collecting, storing, analyzing, and visualizing personal health data from devices and services such as Fitbit / Google Health.

The project should make health data independently accessible outside vendor apps, preserve historical data locally, and provide a foundation for custom dashboards, trend analysis, and future automation.

This document describes the intended direction at a high level. It is deliberately implementation-oriented so that a coding agent can use it as project context while helping plan and build the system.

## Goals

- Periodically export personal health data from supported provider APIs.
- Store the data on infrastructure under the user's control.
- Preserve raw provider data where practical so it can be reprocessed later.
- Normalize useful metrics into a stable internal representation.
- Support incremental synchronization without repeatedly downloading all historical data.
- Make the collected data easy to query, inspect, and visualize.
- Build useful dashboards for health trends over days, weeks, months, and years.
- Keep the system simple enough to run reliably on a homelab.
- Make each component independently testable and replaceable.

Initial metrics of interest include:

- heart rate
- resting heart rate
- HRV
- sleep
- steps and activity
- workouts
- calories / energy expenditure
- other useful health metrics exposed by the upstream provider

The exact metric set should grow incrementally rather than being implemented all at once.

## Non-Goals

At least initially, the project is **not** intended to:

- replace the Fitbit / Google Health applications
- provide medical diagnosis or clinical decision support
- support multiple users
- become a public SaaS service
- build a large distributed architecture
- perfectly normalize every health-data provider from day one

The first priority is a reliable personal data pipeline.

## High-Level Architecture

```text
┌──────────────────────────────┐
│ Health Data Providers        │
│                              │
│ Fitbit / Google Health APIs  │
└──────────────┬───────────────┘
               │ OAuth/API
               ▼
┌──────────────────────────────┐
│ CLI Fetcher / Ingestor       │
│                              │
│ - authentication             │
│ - API client                 │
│ - pagination                 │
│ - incremental sync           │
│ - retries / rate limits      │
│ - validation                 │
└──────────────┬───────────────┘
               │
        ┌──────┴──────┐
        ▼             ▼
┌───────────────┐  ┌──────────────────┐
│ Raw Archive   │  │ Normalized Store │
│               │  │                  │
│ Provider JSON │  │ Queryable health │
│ / snapshots   │  │ metrics/events   │
└───────────────┘  └────────┬─────────┘
                            │
                            ▼
                  ┌────────────────────┐
                  │ Query / Analytics  │
                  │ Layer              │
                  └────────┬───────────┘
                           │
                           ▼
                  ┌────────────────────┐
                  │ Dashboards         │
                  │                    │
                  │ Trends, charts,    │
                  │ summaries, alerts  │
                  └────────────────────┘
```

## Core Components

### 1. CLI Fetcher / Ingestor

This is the first component to implement.

Responsibilities:

- authenticate with the upstream health API
- fetch one or more health data types
- accept explicit time ranges
- handle pagination and API limits
- retry transient failures safely
- support incremental synchronization
- persist synchronization checkpoints
- save raw API responses when useful
- transform provider-specific responses into internal records
- write normalized records to storage
- provide useful logs and exit codes

The CLI should be usable both manually and non-interactively by a scheduler.

Example conceptual commands:

```bash
vitald auth
vitald fetch heart-rate --from 2026-08-01 --to 2026-08-13
vitald fetch sleep --days 7
vitald sync
vitald status
```

The exact CLI interface is not fixed yet.

### 2. Provider Integration Layer

Provider-specific code should be isolated behind a small interface.

Conceptually:

```text
Provider
├── authenticate()
├── fetch(metric, start, end)
├── paginate(...)
└── normalize(...)
```

This prevents API-specific response formats from leaking throughout the rest of the project and makes adding another provider possible later.

The current integration uses the Google Health API v4 with Google OAuth 2.0. The identity endpoint is used as the authentication smoke test:

```text
https://health.googleapis.com/v4/users/me/identity
```

### 3. Raw Data Archive

Where practical, retain the original provider responses before normalization.

Benefits:

- data can be reprocessed when the schema changes
- bugs in normalization do not require re-fetching everything
- the original provider representation remains inspectable
- API changes are easier to debug

A simple filesystem layout is sufficient initially, for example:

```text
data/raw/
  google/
    heart-rate/
      2026-08-13.json
    sleep/
      2026-08-13.json
```

The exact format and retention policy can be decided during implementation.

### 4. Normalized Data Store

Normalized storage should provide a stable representation independent of the upstream API.

A relational database is the likely initial choice.

The schema should distinguish between concepts such as:

- time-series measurements
- daily aggregates
- interval-based records
- discrete events
- source/provider metadata

Records should retain enough provenance to answer:

- which provider produced this value?
- when was it measured?
- when was it imported?
- what upstream record did it originate from?

Schema design should begin with the first implemented metric rather than trying to model every possible health record in advance.

### 5. Analytics / Query Layer

The system should make common health questions easy to answer, for example:

- What is my resting heart-rate trend over the last 30 days?
- How has HRV changed week over week?
- How much sleep am I averaging?
- Does sleep duration correlate with HRV or resting heart rate?
- How does training volume change over time?

Initially, direct SQL queries may be sufficient. A dedicated API can be introduced later if it provides clear value.

### 6. Dashboard

The dashboard is a consumer of the normalized data rather than the core of the system.

Possible visualizations include:

- heart-rate trends
- resting heart rate
- HRV
- sleep duration and sleep stages
- steps and activity
- workout frequency and duration
- weekly / monthly rolling averages
- long-term trend comparisons

The visualization technology should remain replaceable. The ingestion and storage layers must not depend on a particular dashboard product.

### 7. Scheduler

Once manual synchronization is reliable, the CLI should run automatically on the homelab.

Conceptually:

```text
scheduler
    │
    ▼
vitald sync
    │
    ├── fetch new provider data
    ├── archive raw responses
    ├── normalize records
    ├── update database
    └── persist sync checkpoint
```

The scheduler may eventually use systemd timers, cron, or a container-oriented equivalent depending on deployment.

## Data Flow

A normal synchronization should behave approximately as follows:

1. Load configuration and credentials.
2. Determine the last successful synchronization point.
3. Request only the missing time range from the provider.
4. Handle pagination until the requested range is complete.
5. Persist raw responses.
6. Validate and normalize records.
7. Upsert records into the normalized database.
8. Update the synchronization checkpoint only after successful persistence.
9. Emit a concise synchronization summary.

The process should be **idempotent**: running the same synchronization more than once should not duplicate data or corrupt state.

## Suggested Repository Structure

This is a direction rather than a fixed requirement:

```text
vitald/
├── cmd/ or src/
│   ├── cli/
│   ├── providers/
│   │   └── google/
│   ├── ingest/
│   ├── normalize/
│   ├── storage/
│   └── config/
├── migrations/
├── tests/
├── data/
│   └── raw/
├── deploy/
├── docs/
├── .env.example
├── README.md
└── PROJECT.md
```

The exact layout should follow the conventions of the implementation language chosen for the CLI.

## Configuration and Secrets

Credentials must not be committed to Git.

Expected configuration will likely include:

```text
GOOGLE_CLIENT_ID
GOOGLE_CLIENT_SECRET
GOOGLE_REFRESH_TOKEN
DATABASE_URL
RAW_DATA_PATH
```

Long-lived synchronization should use an OAuth refresh flow rather than depending on manually copied short-lived access tokens.

Secrets should eventually be supplied through the deployment environment or another appropriate secret-management mechanism.

## Reliability Requirements

The ingestion path should prioritize correctness over complexity.

Important properties:

- idempotent writes
- explicit time zones
- deterministic normalization
- safe retries
- useful structured logging
- clear error messages
- checkpointing only after successful writes
- graceful handling of partial API failures
- testable provider responses using fixtures
- database migrations kept in version control

## Deployment Direction

The intended runtime is a self-hosted homelab.

A likely deployment shape is:

```text
Homelab Host
├── vitald sync job
├── database
├── raw data volume
└── dashboard
```

Components may run as containers, but containerization should not complicate early development. The CLI should remain easy to run directly during development.

Persistent data and credentials must live outside ephemeral containers.

## Implementation Strategy

Build vertically, one useful metric at a time.

### Phase 1 — Prove ingestion

Implement the smallest end-to-end path:

```text
OAuth
  → fetch one metric
  → print/inspect response
  → save raw data
```

### Phase 2 — Persist normalized data

Extend the same metric:

```text
fetch
  → normalize
  → database
  → query with SQL
```

### Phase 3 — Incremental sync

Add:

- sync checkpoints
- idempotent upserts
- retries
- pagination
- scheduled execution

### Phase 4 — Expand metrics

Add additional health domains individually, with tests and normalization rules for each.

### Phase 5 — Visualization

Connect the normalized store to a dashboard and build useful trend views.

### Phase 6 — Higher-Level Analysis

Once sufficient history exists, add derived metrics, correlations, summaries, anomaly detection, or alerts where useful.

## Design Principles

When making implementation decisions, prefer:

1. **Local-first** — personal health history should remain under the user's control.
2. **Simple components** — avoid unnecessary services and distributed-system complexity.
3. **Raw + normalized data** — retain original data while exposing a stable query model.
4. **Incremental ingestion** — only fetch what is missing.
5. **Idempotency** — repeated execution must be safe.
6. **Provider isolation** — upstream API details belong in provider adapters.
7. **Observability** — failures should be visible and diagnosable.
8. **Replaceable presentation** — dashboards should not dictate the storage architecture.
9. **Iterative modeling** — design schemas from real provider payloads, not speculation.
10. **Automation-friendly interfaces** — commands should work equally well for humans, scripts, and schedulers.

## Immediate Next Milestone

The initial CLI ingestion milestone is complete: OAuth, real metric fetching, exact raw archival, normalization, PostgreSQL persistence, incremental checkpoints, and synchronization run history are implemented.

The current milestone is **safe unattended synchronization**. PostgreSQL advisory locking prevents concurrent syncs, the next lock holder recovers stale `running` history left by a hard-killed process, and `vitald doctor` reports operational readiness. Remaining work is to:

- establish backup and restore procedures
- schedule `vitald sync` with a systemd timer

Only after the pipeline is safe to operate unattended should the project add stable analytics views and Grafana dashboards. See [`CURRENT_STATE.md`](CURRENT_STATE.md) for exact current behavior and acceptance criteria.

## Open Decisions

The following should be decided during implementation rather than assumed here:

- exact upstream API behavior for newly introduced metrics
- future normalized schema refinements
- whether PostgreSQL should later gain the TimescaleDB extension
- long-term raw-data retention and compression policy
- dashboard technology
- scheduler / deployment mechanism
- whether a separate HTTP API is needed
- packaging and release strategy

A coding agent should surface trade-offs for these decisions and avoid silently locking the project into unnecessary architecture.
