# Grafana Dashboard Initiative Plan

> **Purpose:** Live source of truth for adding a secure, reproducible Grafana deployment and dashboards to `vitald`.
>
> **Collaboration mode:** The user implements each checklist point. The assistant provides guidance and reviews changes, and does not edit implementation code unless explicitly requested.
>
> **Status:** In progress — point 1 is next
> **Started:** 2026-08-16

## High-level goal

Add Grafana as a rootless, Compose-managed visualization layer over the stable PostgreSQL `analytics` schema, with least-privilege database access, version-controlled provisioning and dashboards, and systemd-managed unattended operation.

## Desired end state

- Grafana runs as a pinned container in `compose.yaml` and persists runtime state in a named volume.
- Grafana is reachable only through the explicitly selected network exposure model.
- Anonymous access is disabled and initial administrator credentials are supplied outside Git.
- A dedicated PostgreSQL login can read the `analytics` schema but cannot read internal tables directly or modify data.
- The PostgreSQL datasource and dashboard provider are provisioned from version-controlled files.
- Version-controlled dashboards cover overview, heart-rate/HRV, sleep, exercise, and pipeline freshness.
- Dashboards consistently use `Asia/Riyadh`, Grafana time-range controls, and the stable `analytics` views.
- Rootless user-systemd starts and stops Grafana independently while ordering it after PostgreSQL.
- Installation, updates, validation, troubleshooting, customization, and recovery are documented.
- Automated checks validate provisioning files, dashboard JSON/SQL contracts, Compose configuration, database permissions, and systemd units.

## Decisions to confirm

- [x] **Network exposure:** loopback-only `127.0.0.1:3107`, accessed through SSH tunneling.
- [x] **Authentication:** local Grafana administrator with anonymous access disabled.
- [x] **Image version:** select and pin an explicit Grafana OSS version during implementation; do not use `latest`.
- [x] **Dashboard timezone:** explicitly use `Asia/Riyadh`.
- [x] **Heart-rate scope:** daily minimum, average, and maximum trends from `analytics.heart_rate_daily`; intraday samples are deferred.
- [x] **Freshness policy:** dashboard coloring at 12-hour warning and 24-hour critical thresholds; alert delivery is deferred.
- [x] **State/recovery:** Git-managed provisioning is authoritative; persist but do not initially back up Grafana SQLite state.
- [x] **Editing policy:** allow UI experimentation, but export and commit every permanent dashboard change.

## Implementation checklist

### 0. Confirm architecture and acceptance criteria

- [x] Record all decisions above.
- [x] Confirm environment variable names and secret-handling rules: `GRAFANA_ADMIN_USER` (default `admin`), required `GRAFANA_ADMIN_PASSWORD`, `VITALD_GRAFANA_DB_USER` (default `vitald_grafana`), and required `VITALD_GRAFANA_DB_PASSWORD`; secrets remain only in the environment file and are never committed or rendered into dashboard JSON.
- [x] Confirm dashboard list and minimum useful panels: overview, daily heart-rate/HRV, sleep, exercise, and pipeline freshness as detailed below.
- [x] Confirm deferred scope: intraday heart-rate samples, Grafana alert delivery, external authentication, reverse-proxy/TLS exposure, plugins, and backup of disposable Grafana SQLite state.

### 1. Provision least-privilege PostgreSQL access

- [ ] Add documented environment variables for the Grafana database login.
- [ ] Add an idempotent provisioning workflow for creating/updating the login.
- [ ] Grant only database connection, `analytics` schema usage, and analytics-view `SELECT` access.
- [ ] Ensure future analytics views can be granted predictably without exposing base tables.
- [ ] Verify the role can query every analytics view.
- [ ] Verify the role cannot query `health_records`, write data, create objects, or escalate privileges.

### 2. Add the baseline Grafana Compose service

- [ ] Add a pinned Grafana OSS image to `compose.yaml`.
- [ ] Add a persistent `grafana-data` named volume.
- [ ] Configure the selected loopback/LAN/reverse-proxy exposure.
- [ ] Configure administrator secrets and disable anonymous access.
- [ ] Set timezone and safe container defaults.
- [ ] Add a health check and PostgreSQL startup dependency.
- [ ] Verify startup, login, restart persistence, health, and logs manually.

### 3. Provision the PostgreSQL datasource and dashboard provider

- [ ] Add `deploy/grafana/provisioning/datasources/` configuration with a stable datasource UID.
- [ ] Add `deploy/grafana/provisioning/dashboards/` provider configuration.
- [ ] Mount provisioning and dashboard directories read-only in Compose.
- [ ] Pass the read-only datasource password without committing it.
- [ ] Verify Grafana reports the datasource healthy.
- [ ] Verify provisioned resources reappear after deleting and recreating the Grafana container.

### 4. Build the health overview dashboard

- [ ] Add a version-controlled dashboard JSON file with a stable UID.
- [ ] Add global time-range behavior and appropriate intervals.
- [ ] Add steps, resting heart rate, HRV, sleep, exercise, calories, and weight panels.
- [ ] Preserve `NULL` as missing data rather than rendering it as zero where that would mislead.
- [ ] Use only `analytics.daily_summary` and the provisioned datasource UID.
- [ ] Validate short and long time ranges against real data.

### 5. Build heart-rate and sleep dashboards

- [ ] Add daily heart-rate minimum/average/maximum and sample-count trends.
- [ ] Add resting-heart-rate and HRV context.
- [ ] Add daily sleep totals, session count, longest session, and rolling trends.
- [ ] Add a sleep-session detail table.
- [ ] Verify date attribution, units, legends, tooltips, and missing-data behavior.

### 6. Build exercise and pipeline-health dashboards

- [ ] Add exercise frequency, duration, calories, active-zone minutes, types, and session history.
- [ ] Add one pipeline-health row/status per supported metric.
- [ ] Add latest success, checkpoint, import, archive, record-count, and failure context.
- [ ] Apply the agreed warning/critical freshness display thresholds.
- [ ] Ensure error details are useful without exposing secrets or raw health payloads.

### 7. Integrate Grafana with rootless user-systemd

- [ ] Add a separate `vitald-grafana.service` ordered after and requiring `vitald-postgres.service`.
- [ ] Extend `scripts/systemd-run.sh` with safe Grafana start/stop operations.
- [ ] Extend installation to obtain/build required images, provision database access, install the unit, and verify startup.
- [ ] Extend uninstall behavior without deleting the Grafana volume.
- [ ] Keep Grafana independently restartable and avoid reconciling active one-shot `vitald` jobs.
- [ ] Validate rendered units with `systemd-analyze verify`.

### 8. Add automated and operational validation

- [ ] Add targeted tests for database-role provisioning and denied base-table/write access.
- [ ] Validate Compose rendering for Podman and Docker-compatible configuration.
- [ ] Validate provisioning YAML and dashboard JSON.
- [ ] Check that committed dashboards reference the stable datasource UID and only `analytics` objects.
- [ ] Add or extend systemd rendering/start-stop tests.
- [ ] Run a container smoke test covering Grafana health and datasource connectivity.
- [ ] Run the full Go tests, race tests, vet, build, shell checks, and relevant integration tests.
- [ ] Perform a clean recreate/reprovision drill.

### 9. Documentation and milestone closeout

- [ ] Add a Grafana operations document covering setup, access, login, updates, logs, password rotation, dashboard editing/export, and recovery.
- [ ] Update `.env.example`, `README.md`, `docs/SCHEDULING.md`, and `docs/CURRENT_STATE.md`.
- [ ] Document security boundaries and the decision not to back up disposable Grafana SQLite state, if retained.
- [ ] Document SSH tunneling or the selected reverse-proxy/LAN access method.
- [ ] Record known limitations and future alerting/intraday work.
- [ ] Run final validation and manually inspect all dashboards.
- [ ] Mark the milestone complete and identify the next planned milestone.

## Ordering constraints

1. Decisions and acceptance criteria must be settled before implementation.
2. Read-only database access should exist before datasource provisioning.
3. The baseline Grafana container must run before provisioning is debugged.
4. Datasource and dashboard-provider UIDs/paths must stabilize before dashboard JSON is created.
5. Build the overview dashboard first to establish panel conventions reused by domain dashboards.
6. Systemd integration should follow successful manual Compose operation.
7. Full operational validation and documentation come after behavior stabilizes, but targeted tests should be added with each point.

## Risks and mitigations

- **Credential leakage:** keep passwords in `.env`, never dashboard JSON/YAML; inspect rendered output and Git status before commits.
- **Over-privileged datasource:** use a dedicated login and explicit positive/negative permission tests.
- **Provisioning drift:** treat Git as authoritative and export useful UI changes promptly.
- **Unpinned upgrades:** pin the image and test upgrades explicitly before changing versions.
- **Misleading health charts:** retain nulls, label units, document attribution, and avoid medical claims.
- **Timezone mismatch:** explicitly configure `Asia/Riyadh` and use stored `local_date` for daily panels.
- **Secret exposure in logs/process arguments:** avoid shell tracing and design provisioning so passwords are not printed.
- **Grafana SQLite loss:** retain a volume for normal restarts, keep durable dashboards/datasources in Git, and document which UI-only state is disposable.
- **Compose/systemd interference:** use separate service ownership and `--no-deps` where systemd already enforces ordering.
- **Dashboard JSON noise:** use stable UIDs, deterministic exports, and focused commits.

## Testing strategy

For each checklist point:

1. Run the smallest targeted validation first.
2. Review permissions, logs, and generated configuration manually.
3. Run related integration tests before marking the point complete.
4. Run the complete project validation only at the final point or after cross-cutting infrastructure changes.

Never run gated PostgreSQL integration tests against the production database.

## Documentation follow-ups

Documentation is a required completion gate, not optional cleanup. At minimum it must describe:

- architecture and trust boundaries;
- all non-secret environment variables and secret setup;
- first startup and access;
- systemd installation and operations;
- dashboard source-of-truth and export workflow;
- upgrade and rollback procedure;
- database password rotation;
- persistence and disaster-recovery expectations;
- troubleshooting datasource, provisioning, and container failures.

## Progress log

- 2026-08-16: Initial plan created.
- 2026-08-16: Architecture accepted with loopback port `3107` and daily min/average/max heart-rate trends; point 0 completed.
