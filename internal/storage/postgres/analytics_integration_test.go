package postgres

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestAnalyticsViewsIntegration(t *testing.T) {
	databaseURL := os.Getenv("VITALD_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VITALD_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	const insertRecord = `
INSERT INTO health_records
    (record_key, metric, provider_record_id, observed_at, ended_at, local_date, value, unit, attributes, raw, imported_at)
VALUES
    ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8, $9::jsonb, '{}'::jsonb, $10)`
	date := time.Date(2036, 8, 10, 0, 0, 0, 0, time.UTC)
	importedAt := time.Date(2036, 8, 11, 1, 0, 0, 0, time.UTC)
	records := []struct {
		key, metric, providerID string
		observed, ended         any
		value                   float64
		unit, attributes        string
	}{
		{"analytics-steps", "steps", "", nil, nil, 8000, "steps", `{}`},
		{"analytics-rhr", "daily-resting-heart-rate", "", nil, nil, 58, "bpm", `{}`},
		{"analytics-heart-1", "heart-rate", "heart-1", date.Add(8 * time.Hour), nil, 60, "bpm", `{}`},
		{"analytics-heart-2", "heart-rate", "heart-2", date.Add(9 * time.Hour), nil, 80, "bpm", `{}`},
		{"analytics-sleep-1", "sleep", "sleep-1", date.Add(-7 * time.Hour), date.Add(time.Hour), 420, "minutes", `{"type":"STAGES","scoreAvailable":false}`},
		{"analytics-sleep-2", "sleep", "sleep-2", date.Add(12 * time.Hour), date.Add(13 * time.Hour), 45, "minutes", `{"type":"NAP","scoreAvailable":false}`},
		{"analytics-exercise", "exercise", "exercise-1", date.Add(15 * time.Hour), date.Add(16 * time.Hour), 60, "minutes", `{"exerciseType":"RUNNING","displayName":"Run","caloriesKcal":500,"activeZoneMinutes":40,"cardioLoadAvailable":false}`},
		{"analytics-weight-1", "weight", "weight-1", date.Add(7 * time.Hour), nil, 71, "kg", `{}`},
		{"analytics-weight-2", "weight", "weight-2", date.Add(18 * time.Hour), nil, 70, "kg", `{}`},
	}
	for _, record := range records {
		if _, err := tx.Exec(ctx, insertRecord, record.key, record.metric, record.providerID, record.observed, record.ended, date, record.value, record.unit, record.attributes, importedAt); err != nil {
			t.Fatal(err)
		}
	}

	var steps, averageBPM, sleepMinutes, longestSleep, exerciseMinutes, exerciseCalories, activeZoneMinutes, weight float64
	var heartSamples, sleepSessions, exerciseSessions int64
	if err := tx.QueryRow(ctx, `
SELECT steps, heart_rate_average_bpm, heart_rate_sample_count,
       sleep_minutes, sleep_session_count, longest_sleep_minutes,
       exercise_minutes, exercise_session_count, exercise_calories_kcal,
       active_zone_minutes, weight_kg
FROM analytics.daily_summary
WHERE local_date = $1`, date).Scan(
		&steps, &averageBPM, &heartSamples, &sleepMinutes, &sleepSessions, &longestSleep,
		&exerciseMinutes, &exerciseSessions, &exerciseCalories, &activeZoneMinutes, &weight,
	); err != nil {
		t.Fatal(err)
	}
	if steps != 8000 || averageBPM != 70 || heartSamples != 2 || sleepMinutes != 465 || sleepSessions != 2 || longestSleep != 420 || exerciseMinutes != 60 || exerciseSessions != 1 || exerciseCalories != 500 || activeZoneMinutes != 40 || weight != 70 {
		t.Fatalf("unexpected daily summary: steps=%v averageBPM=%v heartSamples=%v sleepMinutes=%v sleepSessions=%v longestSleep=%v exerciseMinutes=%v exerciseSessions=%v exerciseCalories=%v activeZoneMinutes=%v weight=%v",
			steps, averageBPM, heartSamples, sleepMinutes, sleepSessions, longestSleep, exerciseMinutes, exerciseSessions, exerciseCalories, activeZoneMinutes, weight)
	}

	var sleepRows, exerciseRows int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM analytics.sleep_sessions WHERE sleep_date = $1`, date).Scan(&sleepRows); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM analytics.exercise_sessions WHERE exercise_date = $1`, date).Scan(&exerciseRows); err != nil {
		t.Fatal(err)
	}
	if sleepRows != 2 || exerciseRows != 1 {
		t.Fatalf("unexpected session counts: sleep=%d exercise=%d", sleepRows, exerciseRows)
	}

	var freshnessRows int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM analytics.pipeline_freshness`).Scan(&freshnessRows); err != nil {
		t.Fatal(err)
	}
	if freshnessRows != 9 {
		t.Fatalf("unexpected pipeline freshness row count: %d", freshnessRows)
	}
}
