package ingest

import (
	"strings"
	"testing"
	"time"
)

func TestFilterForHeartRateUsesPhysicalBoundaries(t *testing.T) {
	location, _ := time.LoadLocation("Asia/Riyadh")
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, location)
	to := from.AddDate(0, 0, 1)
	filter := filterFor("heart-rate", from, to)
	if !strings.Contains(filter, `heart_rate.sample_time.physical_time`) || !strings.Contains(filter, `2026-07-31T21:00:00Z`) {
		t.Fatalf("unexpected filter: %s", filter)
	}
}

func TestFilterForSleepUsesCivilEndDate(t *testing.T) {
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	filter := filterFor("sleep", from, from.AddDate(0, 0, 1))
	if !strings.Contains(filter, `sleep.interval.civil_end_time`) || !strings.Contains(filter, `"2026-08-02"`) {
		t.Fatalf("unexpected filter: %s", filter)
	}
}
