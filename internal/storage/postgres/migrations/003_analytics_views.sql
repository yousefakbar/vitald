-- Timestamp-based samples need the same civil-day key used by daily and
-- session metrics. Existing installations currently use Asia/Riyadh; future
-- records are assigned with the configured VITALD_TIMEZONE during ingestion.
UPDATE health_records
SET local_date = (observed_at AT TIME ZONE 'Asia/Riyadh')::date
WHERE metric IN ('heart-rate', 'weight')
  AND local_date IS NULL
  AND observed_at IS NOT NULL;

CREATE SCHEMA analytics;

CREATE VIEW analytics.sleep_sessions AS
SELECT
    record_key,
    provider_record_id,
    local_date AS sleep_date,
    observed_at AS started_at,
    ended_at,
    value AS duration_minutes,
    attributes ->> 'type' AS sleep_type,
    NULLIF(attributes ->> 'sleepScore', '')::double precision AS sleep_score,
    COALESCE((attributes ->> 'scoreAvailable')::boolean, false) AS score_available,
    imported_at
FROM health_records
WHERE metric = 'sleep';

COMMENT ON VIEW analytics.sleep_sessions IS
    'Stable session-level sleep contract; sleep_date is the local date on which the session ended.';
COMMENT ON COLUMN analytics.sleep_sessions.duration_minutes IS
    'Provider minutes asleep when available, otherwise elapsed session minutes.';
COMMENT ON COLUMN analytics.sleep_sessions.sleep_score IS
    'Provider sleep score; currently NULL because Google Health does not expose it.';

CREATE VIEW analytics.exercise_sessions AS
SELECT
    record_key,
    provider_record_id,
    local_date AS exercise_date,
    observed_at AS started_at,
    ended_at,
    value AS duration_minutes,
    attributes ->> 'exerciseType' AS exercise_type,
    attributes ->> 'displayName' AS display_name,
    NULLIF(attributes ->> 'caloriesKcal', '')::double precision AS calories_kcal,
    NULLIF(attributes ->> 'activeZoneMinutes', '')::double precision AS active_zone_minutes,
    COALESCE((attributes ->> 'cardioLoadAvailable')::boolean, false) AS cardio_load_available,
    imported_at
FROM health_records
WHERE metric = 'exercise';

COMMENT ON VIEW analytics.exercise_sessions IS
    'Stable session-level exercise contract attributed to the local date on which exercise started.';
COMMENT ON COLUMN analytics.exercise_sessions.cardio_load_available IS
    'False while Google Health does not expose Fitbit cardio load; active-zone minutes are not cardio load.';

CREATE VIEW analytics.heart_rate_daily AS
SELECT
    local_date,
    min(value) AS minimum_bpm,
    avg(value) AS average_bpm,
    max(value) AS maximum_bpm,
    count(*) AS sample_count,
    min(observed_at) AS first_sample_at,
    max(observed_at) AS last_sample_at,
    max(imported_at) AS latest_imported_at
FROM health_records
WHERE metric = 'heart-rate'
  AND local_date IS NOT NULL
  AND value IS NOT NULL
GROUP BY local_date;

COMMENT ON VIEW analytics.heart_rate_daily IS
    'Daily heart-rate sample statistics using the local date assigned during ingestion.';

CREATE VIEW analytics.daily_summary AS
WITH bounds AS (
    SELECT min(local_date) AS first_date, max(local_date) AS last_date
    FROM health_records
    WHERE local_date IS NOT NULL
),
dates AS (
    SELECT generated_at::date AS local_date
    FROM bounds
    CROSS JOIN LATERAL generate_series(first_date, last_date, interval '1 day') AS generated_at
),
daily_metrics AS (
    SELECT
        local_date,
        max(value) FILTER (WHERE metric = 'steps') AS steps,
        max(value) FILTER (WHERE metric = 'total-calories') AS calories_burned_kcal,
        max(value) FILTER (WHERE metric = 'nutrition-log') AS calories_ingested_kcal,
        max(value) FILTER (WHERE metric = 'daily-resting-heart-rate') AS resting_heart_rate_bpm,
        max(value) FILTER (WHERE metric = 'daily-heart-rate-variability') AS heart_rate_variability_ms
    FROM health_records
    WHERE metric IN (
        'steps', 'total-calories', 'nutrition-log',
        'daily-resting-heart-rate', 'daily-heart-rate-variability'
    )
      AND local_date IS NOT NULL
    GROUP BY local_date
),
sleep_daily AS (
    SELECT
        sleep_date AS local_date,
        sum(duration_minutes) AS sleep_minutes,
        count(*) AS sleep_session_count,
        max(duration_minutes) AS longest_sleep_minutes
    FROM analytics.sleep_sessions
    GROUP BY sleep_date
),
exercise_daily AS (
    SELECT
        exercise_date AS local_date,
        count(*) AS exercise_session_count,
        sum(duration_minutes) AS exercise_minutes,
        sum(calories_kcal) AS exercise_calories_kcal,
        sum(active_zone_minutes) AS active_zone_minutes
    FROM analytics.exercise_sessions
    GROUP BY exercise_date
),
latest_weight AS (
    SELECT DISTINCT ON (local_date)
        local_date,
        value AS weight_kg,
        observed_at AS weight_observed_at
    FROM health_records
    WHERE metric = 'weight'
      AND local_date IS NOT NULL
      AND value IS NOT NULL
    ORDER BY local_date, observed_at DESC, imported_at DESC, record_key
)
SELECT
    dates.local_date,
    daily_metrics.steps,
    daily_metrics.calories_burned_kcal,
    daily_metrics.calories_ingested_kcal,
    daily_metrics.resting_heart_rate_bpm,
    daily_metrics.heart_rate_variability_ms,
    heart_rate.minimum_bpm AS heart_rate_minimum_bpm,
    heart_rate.average_bpm AS heart_rate_average_bpm,
    heart_rate.maximum_bpm AS heart_rate_maximum_bpm,
    heart_rate.sample_count AS heart_rate_sample_count,
    sleep_daily.sleep_minutes,
    COALESCE(sleep_daily.sleep_session_count, 0) AS sleep_session_count,
    sleep_daily.longest_sleep_minutes,
    exercise_daily.exercise_minutes,
    COALESCE(exercise_daily.exercise_session_count, 0) AS exercise_session_count,
    exercise_daily.exercise_calories_kcal,
    exercise_daily.active_zone_minutes,
    latest_weight.weight_kg,
    latest_weight.weight_observed_at
FROM dates
LEFT JOIN daily_metrics USING (local_date)
LEFT JOIN analytics.heart_rate_daily AS heart_rate USING (local_date)
LEFT JOIN sleep_daily USING (local_date)
LEFT JOIN exercise_daily USING (local_date)
LEFT JOIN latest_weight USING (local_date)
ORDER BY dates.local_date;

COMMENT ON VIEW analytics.daily_summary IS
    'Continuous local-date series from the first through latest health record; absent measurements are NULL and session counts are zero.';
COMMENT ON COLUMN analytics.daily_summary.sleep_minutes IS
    'Sum of all sleep sessions attributed to this date.';
COMMENT ON COLUMN analytics.daily_summary.weight_kg IS
    'Latest weight sample observed on this date.';

CREATE VIEW analytics.pipeline_freshness AS
WITH supported_metrics(metric) AS (
    VALUES
        ('steps'),
        ('heart-rate'),
        ('daily-resting-heart-rate'),
        ('daily-heart-rate-variability'),
        ('sleep'),
        ('exercise'),
        ('total-calories'),
        ('nutrition-log'),
        ('weight')
),
record_state AS (
    SELECT
        metric,
        count(*) AS record_count,
        max(local_date) AS latest_local_date,
        max(observed_at) AS latest_observed_at,
        max(ended_at) AS latest_ended_at,
        max(imported_at) AS latest_imported_at
    FROM health_records
    GROUP BY metric
),
archive_state AS (
    SELECT metric, max(archived_at) AS latest_archived_at
    FROM raw_archives
    GROUP BY metric
),
latest_success AS (
    SELECT
        metric,
        max(completed_at) FILTER (WHERE status = 'succeeded') AS latest_successful_sync_at
    FROM sync_run_metrics
    GROUP BY metric
)
SELECT
    supported.metric,
    checkpoint.synced_through AS checkpoint_at,
    now() - checkpoint.synced_through AS checkpoint_age,
    COALESCE(records.record_count, 0) AS record_count,
    records.latest_local_date,
    records.latest_observed_at,
    records.latest_ended_at,
    records.latest_imported_at,
    now() - records.latest_imported_at AS import_age,
    archives.latest_archived_at,
    latest_success.latest_successful_sync_at,
    now() - latest_success.latest_successful_sync_at AS successful_sync_age,
    recent.sync_run_id AS latest_sync_run_id,
    recent.run_status AS latest_run_status,
    recent.metric_status AS latest_metric_status,
    recent.started_at AS latest_metric_started_at,
    recent.completed_at AS latest_metric_completed_at,
    recent.error_message AS latest_metric_error
FROM supported_metrics AS supported
LEFT JOIN sync_checkpoints AS checkpoint ON checkpoint.metric = supported.metric
LEFT JOIN record_state AS records ON records.metric = supported.metric
LEFT JOIN archive_state AS archives ON archives.metric = supported.metric
LEFT JOIN latest_success ON latest_success.metric = supported.metric
LEFT JOIN LATERAL (
    SELECT
        metric_run.sync_run_id,
        run.status AS run_status,
        metric_run.status AS metric_status,
        metric_run.started_at,
        metric_run.completed_at,
        metric_run.error_message
    FROM sync_run_metrics AS metric_run
    JOIN sync_runs AS run ON run.id = metric_run.sync_run_id
    WHERE metric_run.metric = supported.metric
    ORDER BY metric_run.started_at DESC, metric_run.sync_run_id DESC
    LIMIT 1
) AS recent ON true
ORDER BY supported.metric;

COMMENT ON VIEW analytics.pipeline_freshness IS
    'One row for every supported metric with descriptive ingestion, checkpoint, archive, and synchronization freshness data.';
COMMENT ON COLUMN analytics.pipeline_freshness.checkpoint_at IS
    'Exclusive synchronized-through boundary; it may be later than the current instant for daily synchronization ranges.';
COMMENT ON COLUMN analytics.pipeline_freshness.latest_successful_sync_at IS
    'Latest successful per-metric completion, including successful metrics within an overall partial run.';
