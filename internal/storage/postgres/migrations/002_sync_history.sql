CREATE TABLE sync_runs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    status text NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'succeeded', 'partial', 'failed', 'cancelled')),
    initial_days integer NOT NULL CHECK (initial_days > 0),
    timezone text NOT NULL,
    vitald_version text,
    error_message text,
    CHECK (
        (status = 'running' AND completed_at IS NULL)
        OR (status <> 'running' AND completed_at IS NOT NULL)
    )
);

CREATE INDEX sync_runs_started_at_idx ON sync_runs (started_at DESC);
CREATE INDEX sync_runs_status_idx ON sync_runs (status, started_at DESC);

CREATE TABLE sync_run_metrics (
    sync_run_id bigint NOT NULL REFERENCES sync_runs(id) ON DELETE CASCADE,
    metric text NOT NULL,
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    status text NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'succeeded', 'failed', 'cancelled', 'skipped')),
    range_start timestamptz,
    range_end timestamptz,
    checkpoint_before timestamptz,
    checkpoint_after timestamptz,
    pages_archived integer NOT NULL DEFAULT 0 CHECK (pages_archived >= 0),
    records_processed integer NOT NULL DEFAULT 0 CHECK (records_processed >= 0),
    error_message text,
    PRIMARY KEY (sync_run_id, metric),
    CHECK (
        (status = 'running' AND completed_at IS NULL)
        OR (status <> 'running' AND completed_at IS NOT NULL)
    )
);

CREATE INDEX sync_run_metrics_metric_idx ON sync_run_metrics (metric, started_at DESC);
CREATE INDEX sync_run_metrics_failed_idx ON sync_run_metrics (started_at DESC)
    WHERE status = 'failed';

CREATE VIEW sync_run_history AS
SELECT
    run.id,
    run.started_at,
    run.completed_at,
    run.status,
    run.initial_days,
    run.timezone,
    run.vitald_version,
    count(metric.metric) AS metrics_total,
    count(*) FILTER (WHERE metric.status = 'succeeded') AS metrics_succeeded,
    count(*) FILTER (WHERE metric.status = 'failed') AS metrics_failed,
    count(*) FILTER (WHERE metric.status = 'cancelled') AS metrics_cancelled,
    count(*) FILTER (WHERE metric.status = 'skipped') AS metrics_skipped,
    coalesce(sum(metric.pages_archived), 0) AS pages_archived,
    coalesce(sum(metric.records_processed), 0) AS records_processed,
    run.error_message
FROM sync_runs AS run
LEFT JOIN sync_run_metrics AS metric ON metric.sync_run_id = run.id
GROUP BY run.id;
