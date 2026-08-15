package ingest

import (
	"testing"
	"time"

	"google.golang.org/api/health/v4"
)

func TestNormalizeSleepMakesUnavailableScoreExplicit(t *testing.T) {
	location, _ := time.LoadLocation("Asia/Riyadh")
	points := []*health.DataPoint{{Name: "sleep-1", Sleep: &health.Sleep{
		Interval: &health.SessionTimeInterval{StartTime: "2026-08-01T20:00:00Z", EndTime: "2026-08-02T04:00:00Z"},
		Summary:  &health.SleepSummary{MinutesAsleep: 430}, Type: "STAGES",
	}}}
	records, err := normalizePoints("sleep", points, location)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Value == nil || *records[0].Value != 430 {
		t.Fatalf("unexpected records: %+v", records)
	}
	attributes := records[0].Attributes.(map[string]any)
	if attributes["scoreAvailable"] != false {
		t.Fatalf("sleep score availability not explicit: %+v", attributes)
	}
	if records[0].LocalDate == nil || records[0].LocalDate.Format(time.DateOnly) != "2026-08-02" {
		t.Fatalf("unexpected local date: %v", records[0].LocalDate)
	}
}

func TestNormalizeExercise(t *testing.T) {
	location, _ := time.LoadLocation("Asia/Riyadh")
	points := []*health.DataPoint{{Exercise: &health.Exercise{
		Interval:    &health.SessionTimeInterval{StartTime: "2026-08-01T12:00:00Z", EndTime: "2026-08-01T13:00:00Z"},
		DisplayName: "Run", ExerciseType: "RUNNING", MetricsSummary: &health.MetricsSummary{CaloriesKcal: 520, ActiveZoneMinutes: 44},
	}}}
	records, err := normalizePoints("exercise", points, location)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Value == nil || *records[0].Value != 60 {
		t.Fatalf("unexpected records: %+v", records)
	}
	attributes := records[0].Attributes.(map[string]any)
	if attributes["caloriesKcal"] != float64(520) || attributes["cardioLoadAvailable"] != false {
		t.Fatalf("unexpected attributes: %+v", attributes)
	}
}
