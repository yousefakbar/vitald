CREATE TABLE health_records (
    record_key text PRIMARY KEY,
    metric text NOT NULL,
    provider_record_id text,
    observed_at timestamptz,
    ended_at timestamptz,
    local_date date,
    value double precision,
    unit text,
    attributes jsonb NOT NULL DEFAULT '{}'::jsonb,
    source jsonb NOT NULL DEFAULT '{}'::jsonb,
    raw jsonb NOT NULL,
    imported_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX health_records_metric_time_idx ON health_records(metric, observed_at DESC);
CREATE INDEX health_records_metric_date_idx ON health_records(metric, local_date DESC);
CREATE UNIQUE INDEX health_records_provider_id_idx ON health_records(metric, provider_record_id)
    WHERE provider_record_id IS NOT NULL;

CREATE TABLE raw_archives (
    run_id text NOT NULL,
    metric text NOT NULL,
    page_number integer NOT NULL,
    range_start timestamptz NOT NULL,
    range_end timestamptz NOT NULL,
    path text NOT NULL,
    sha256 text NOT NULL,
    size_bytes bigint NOT NULL,
    archived_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(run_id, metric, page_number)
);

CREATE TABLE sync_checkpoints (
    metric text PRIMARY KEY,
    synced_through timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
