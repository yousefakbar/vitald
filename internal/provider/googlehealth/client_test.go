package googlehealth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestListPreservesRawResponseAndQuery(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"dataPoints":[{"heartRate":{"beatsPerMinute":"61","sampleTime":{"physicalTime":"2026-08-01T00:00:00Z"}}}],"nextPageToken":"next"}`)
	}))
	defer server.Close()
	client := NewTestClient(server.Client(), server.URL)
	page, err := client.List(context.Background(), "heart-rate", `heart_rate.sample_time.physical_time >= "2026-08-01T00:00:00Z"`, "token", 100)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "page_token=token") || !strings.Contains(query, "page_size=100") {
		t.Fatalf("unexpected query: %s", query)
	}
	if page.NextPageToken != "next" || len(page.DataPoints) != 1 {
		t.Fatalf("unexpected page: %+v", page)
	}
	if !strings.Contains(string(page.Raw), `"nextPageToken":"next"`) {
		t.Fatal("raw response was not preserved")
	}
}

func TestDailyRollupUsesClosedOpenCivilRange(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		body = string(data)
		_, _ = io.WriteString(w, `{"rollupDataPoints":[]}`)
	}))
	defer server.Close()
	client := NewTestClient(server.Client(), server.URL)
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 7)
	if _, err := client.DailyRollup(context.Background(), "steps", from, to, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"day":1`) || !strings.Contains(body, `"day":8`) || !strings.Contains(body, `"windowSizeDays":1`) || !strings.Contains(body, `"pageSize":7`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestDailyRollupCapsPageSizeAtAPIMaximum(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		body = string(data)
		_, _ = io.WriteString(w, `{"rollupDataPoints":[]}`)
	}))
	defer server.Close()
	client := NewTestClient(server.Client(), server.URL)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := client.DailyRollup(context.Background(), "total-calories", from, from.AddDate(0, 0, 30), ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"pageSize":14`) {
		t.Fatalf("expected total-calories page size to be capped at 14: %s", body)
	}
}
