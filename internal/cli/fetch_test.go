package cli

import (
	"testing"
	"time"
)

func TestParseDateUsesConfiguredLocation(t *testing.T) {
	location, err := time.LoadLocation("Asia/Riyadh")
	if err != nil {
		t.Fatal(err)
	}
	value, err := parseDate("2026-08-01", location)
	if err != nil {
		t.Fatal(err)
	}
	_, offset := value.Zone()
	if offset != 3*60*60 {
		t.Fatalf("expected UTC+3, got %d", offset)
	}
	if value.Format(time.DateOnly) != "2026-08-01" {
		t.Fatalf("unexpected date: %s", value)
	}
}

func TestParseDateRejectsTimestamp(t *testing.T) {
	if _, err := parseDate("2026-08-01T00:00:00Z", time.UTC); err == nil {
		t.Fatal("expected timestamp to be rejected")
	}
}
