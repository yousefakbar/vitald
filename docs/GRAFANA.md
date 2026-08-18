# vitald Grafana Operations

Grafana is the version-controlled visualization layer over vitald's read-only `analytics` PostgreSQL schema. It is intended for personal operational and trend review, not medical diagnosis or clinical decision support.

## Architecture and security boundary

- Grafana OSS is pinned to `grafana/grafana:13.1.0` in `compose.yaml`.
- Host access is loopback-only at `127.0.0.1:3107`; there is no LAN or Internet listener.
- Anonymous access, public sign-up, organization creation, Gravatar, update checks, reporting, and plugin administration/preinstallation are disabled.
- A dedicated PostgreSQL login can connect, use `analytics`, and select analytics views. It cannot read base tables, write data, create objects, or administer roles.
- The datasource and dashboards are provisioned from `deploy/grafana/`. Git is their durable source of truth.
- `grafana-data` persists Grafana's SQLite database and normal UI state. It is intentionally excluded from vitald backups. UI-only users, sessions, preferences, and unexported edits are disposable.
- PostgreSQL, raw archives, OAuth credentials, `.env`, and Grafana credentials remain sensitive even though the dashboard database role is read-only.

SSH protects the transport when Grafana is accessed remotely. Do not publish port 3107 on a non-loopback interface without first choosing and documenting a TLS-authenticated reverse proxy.

## Configuration

Copy `.env.example` to `.env` and set unique values:

```dotenv
GRAFANA_ADMIN_USER=admin
GRAFANA_ADMIN_PASSWORD=<long-random-password>
GRAFANA_SECRET_KEY=<at-least-32-random-characters>
VITALD_GRAFANA_DB_USER=vitald_grafana
VITALD_GRAFANA_DB_PASSWORD=<different-long-random-password>
```

`GRAFANA_ADMIN_PASSWORD`, `GRAFANA_SECRET_KEY`, and `VITALD_GRAFANA_DB_PASSWORD` are required. Keep `.env` mode `0600` and never commit it. The secret key protects encrypted Grafana state and should remain stable across container recreation.

## First startup

For manual Compose operation:

```bash
podman compose up -d postgres
./scripts/provision-grafana-db.sh
podman compose up -d grafana
podman compose ps
```

Use `docker compose` instead where Docker is the selected engine. Provisioning is idempotent and updates the database password and defensive role settings on every run.

For unattended rootless operation, the normal installation path performs image pulls, database-role provisioning, startup, and health checks:

```bash
./scripts/install-systemd.sh --dry-run
./scripts/install-systemd.sh
systemctl --user status vitald-grafana.service
```

The Grafana unit requires and starts after `vitald-postgres.service`, but remains independently restartable.

## Access and login

On the Grafana host, open `http://127.0.0.1:3107`. From another machine, create an SSH tunnel:

```bash
ssh -N -L 3107:127.0.0.1:3107 your-homelab
```

Then open `http://127.0.0.1:3107` locally and sign in with `GRAFANA_ADMIN_USER` and the configured initial password. The environment password initializes a new Grafana database; it does not continually overwrite a password changed later in the UI.

## Dashboards and source-of-truth workflow

The provisioned folder contains:

- health overview;
- heart rate and HRV;
- sleep;
- exercise; and
- pipeline health.

All dashboards use `Asia/Riyadh`, Grafana's selected time range, datasource UID `vitald-postgres`, and only stable `analytics` views. Missing measurements remain gaps rather than zeroes. Pipeline freshness is warning at 12 hours and critical at 24 hours.

UI experimentation is allowed, but permanent changes must be exported and committed:

1. Edit and visually verify the dashboard in Grafana.
2. Use **Dashboard settings → JSON model** or **Share → Export**.
3. Preserve the existing dashboard UID, `Asia/Riyadh`, and datasource UID.
4. Remove export-only inputs or environment-specific metadata and replace the corresponding file under `deploy/grafana/dashboards/`.
5. Run `./scripts/validate-grafana.sh` and review the diff before committing.
6. Restart Grafana or wait for the provider's 30-second scan.

An unexported UI edit can be replaced by the provisioned file and is not part of disaster recovery.

## Routine operation and logs

```bash
systemctl --user restart vitald-grafana.service
systemctl --user stop vitald-grafana.service
systemctl --user start vitald-grafana.service
systemctl --user status vitald-grafana.service
journalctl --user -u vitald-grafana.service -n 100 --no-pager
podman compose logs --tail 100 grafana
podman compose exec grafana wget -q -O - http://127.0.0.1:3000/api/health
```

Use the selected Docker command instead of Podman where applicable. Avoid sharing logs without review; query errors and operational metadata can be private.

## Password and key rotation

### Datasource database password

1. Put a new random `VITALD_GRAFANA_DB_PASSWORD` in `.env`.
2. Restart the systemd unit:

   ```bash
   systemctl --user restart vitald-grafana.service
   ```

The startup wrapper updates the PostgreSQL role, recreates Grafana with the new environment, and reprovisions the encrypted datasource password. Confirm datasource health afterward. For manual Compose operation, run `./scripts/provision-grafana-db.sh` followed by `podman compose up -d --force-recreate grafana`.

### Administrator password

Change it while signed in under **Administration → Users and access**, or reset it locally:

```bash
podman compose exec grafana grafana cli admin reset-admin-password '<new-password>'
```

Update `GRAFANA_ADMIN_PASSWORD` as recovery documentation for a future clean Grafana volume, while remembering that changing the environment alone does not reset an existing SQLite user's password. Avoid placing real passwords in shell history; an interactive UI change is preferred.

### Grafana secret key

Do not routinely rotate `GRAFANA_SECRET_KEY`. Changing it invalidates encrypted Grafana SQLite values. If rotation is necessary, update `.env`, remove disposable Grafana state as described under recovery, and let provisioning recreate the datasource. Export any wanted UI-only state first.

## Updates, rollback, and validation

Grafana upgrades are explicit; never change the image to `latest`.

1. Read release notes and back up/export any wanted UI-only configuration.
2. Change the pinned image in `compose.yaml`; the disposable smoke test reads that same pin.
3. Run:

   ```bash
   ./scripts/validate-grafana.sh
   ./scripts/validate-grafana.sh --integration
   go test ./...
   go test -race ./...
   go vet ./...
   go build ./cmd/vitald
   ```

4. Apply with `./scripts/install-systemd.sh` and inspect health, logs, datasource status, and every dashboard.

To roll back, restore the previous pinned image and committed provisioning/dashboard files, then rerun the installer. Grafana SQLite migrations may not support arbitrary downgrades. If an old image cannot read the current disposable SQLite state, perform clean Grafana-state recovery.

The integration validation creates a uniquely named disposable Compose project, verifies database grants and denials, checks Grafana and datasource health, and repeats the test after deleting its containers and volumes. It never targets the production project.

## Recovery

Provisioning files in Git are authoritative. To recover from corrupt or unwanted Grafana state:

1. Export any still-accessible UI-only changes you want to retain.
2. Stop Grafana and identify the Compose project name.
3. Remove **only** that project's `grafana-data` volume. Do not remove `postgres-data`, `vitald-data`, or `vitald-config`.
4. Run `./scripts/provision-grafana-db.sh` and start Grafana again, or restart `vitald-grafana.service`.
5. Log in with the configured initial administrator credentials and verify the datasource and dashboards.

The datasource and five committed dashboards reappear automatically. UI-created users, sessions, preferences, annotations, alert definitions, and unexported dashboard edits do not. This is the accepted recovery model; Grafana SQLite is persisted for convenience but is not backed up.

## Troubleshooting

### Grafana does not become healthy

```bash
podman compose ps grafana
podman compose logs --tail 200 grafana
systemctl --user status vitald-postgres.service vitald-grafana.service
```

Check volume permissions, provisioning syntax, the pinned image pull, and whether port 3107 is already in use.

### Datasource is unhealthy

Run `./scripts/provision-grafana-db.sh`, confirm PostgreSQL health, and restart Grafana. Verify that both database-role variables match the values passed to the container. Do not print passwords while diagnosing.

### Dashboards are missing or stale

Inspect Grafana logs for provisioning errors, validate files with `./scripts/validate-grafana.sh`, and confirm the three `deploy/grafana` bind mounts are present and read-only. Provisioned files are rescanned every 30 seconds.

### Login fails

An existing Grafana volume retains its current administrator password even if `.env` changes. Use the local CLI reset command or intentionally recover the disposable Grafana volume.

## Deferred work

Intraday heart-rate panels, Grafana alert delivery, external authentication, plugins, reverse-proxy/TLS exposure, and Grafana SQLite backup remain deliberately out of scope. Reconsider them only with a concrete operational requirement.
