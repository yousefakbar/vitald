package ingest

import (
	"testing"
	"time"

	"github.com/yousefakbar/vitald/internal/storage/postgres"
)

func TestDailyRecordKeyIsStableWhenValueChanges(t *testing.T) {
	date := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	first, second := 1000.0, 1200.0
	key1, err := recordKey(postgres.Record{Metric: "steps", LocalDate: &date, Value: &first, Raw: map[string]any{"count": first}})
	if err != nil {
		t.Fatal(err)
	}
	key2, err := recordKey(postgres.Record{Metric: "steps", LocalDate: &date, Value: &second, Raw: map[string]any{"count": second}})
	if err != nil {
		t.Fatal(err)
	}
	if key1 != key2 {
		t.Fatalf("daily update produced a new key: %s != %s", key1, key2)
	}
}
