package googlehealth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/health/v4"
)

const defaultBaseURL = "https://health.googleapis.com/v4"

type Client struct {
	httpClient *http.Client
	baseURL    string
	maxRetries int
}

type DataPage struct {
	Raw           []byte
	DataPoints    []*health.DataPoint
	NextPageToken string
}

type RollupPage struct {
	Raw           []byte
	DataPoints    []*health.DailyRollupDataPoint
	NextPageToken string
}

func NewClient(httpClient *http.Client) *Client {
	return &Client{httpClient: httpClient, baseURL: defaultBaseURL, maxRetries: 5}
}

// NewTestClient is intended for tests that use an httptest server.
func NewTestClient(httpClient *http.Client, baseURL string) *Client {
	return &Client{httpClient: httpClient, baseURL: strings.TrimRight(baseURL, "/"), maxRetries: 0}
}

func (c *Client) Identity(ctx context.Context) (*health.Identity, error) {
	raw, err := c.do(ctx, http.MethodGet, c.baseURL+"/users/me/identity", nil)
	if err != nil {
		return nil, err
	}
	var identity health.Identity
	if err := json.Unmarshal(raw, &identity); err != nil {
		return nil, fmt.Errorf("decode identity response: %w", err)
	}
	return &identity, nil
}

func (c *Client) List(ctx context.Context, metric, filter, pageToken string, pageSize int) (DataPage, error) {
	endpoint, err := url.Parse(c.baseURL + "/users/me/dataTypes/" + url.PathEscape(metric) + "/dataPoints")
	if err != nil {
		return DataPage{}, err
	}
	query := endpoint.Query()
	query.Set("filter", filter)
	query.Set("page_size", strconv.Itoa(pageSize))
	if pageToken != "" {
		query.Set("page_token", pageToken)
	}
	endpoint.RawQuery = query.Encode()
	raw, err := c.do(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return DataPage{}, err
	}
	var response health.ListDataPointsResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return DataPage{}, fmt.Errorf("decode %s response: %w", metric, err)
	}
	return DataPage{Raw: raw, DataPoints: response.DataPoints, NextPageToken: response.NextPageToken}, nil
}

func (c *Client) DailyRollup(ctx context.Context, metric string, from, to time.Time, pageToken string) (RollupPage, error) {
	pageSize := civilDays(from, to)
	maxDays := int64(90)
	if metric == "total-calories" {
		maxDays = 14
	}
	if pageSize > maxDays {
		pageSize = maxDays
	}
	if pageSize < 1 {
		pageSize = 1
	}
	request := &health.DailyRollUpDataPointsRequest{
		Range:          &health.CivilTimeInterval{Start: civilDate(from), End: civilDate(to)},
		WindowSizeDays: 1,
		PageSize:       pageSize,
		PageToken:      pageToken,
	}
	body, err := json.Marshal(request)
	if err != nil {
		return RollupPage{}, fmt.Errorf("encode daily rollup request: %w", err)
	}
	raw, err := c.do(ctx, http.MethodPost, c.baseURL+"/users/me/dataTypes/"+url.PathEscape(metric)+"/dataPoints:dailyRollUp", body)
	if err != nil {
		return RollupPage{}, err
	}
	var response struct {
		RollupDataPoints []*health.DailyRollupDataPoint `json:"rollupDataPoints"`
		NextPageToken    string                         `json:"nextPageToken"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return RollupPage{}, fmt.Errorf("decode %s rollup response: %w", metric, err)
	}
	return RollupPage{Raw: raw, DataPoints: response.RollupDataPoints, NextPageToken: response.NextPageToken}, nil
}

func (c *Client) do(ctx context.Context, method, target string, body []byte) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(1<<(attempt-1))*time.Second + time.Duration(rand.IntN(500))*time.Millisecond
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, target, reader)
		if err != nil {
			return nil, fmt.Errorf("create Google Health request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		closeErr := resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if closeErr != nil {
			lastErr = closeErr
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return data, nil
		}
		apiErr := fmt.Errorf("Google Health API returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return nil, apiErr
		}
		lastErr = apiErr
	}
	return nil, fmt.Errorf("Google Health request failed after retries: %w", lastErr)
}

func civilDate(value time.Time) *health.CivilDateTime {
	return &health.CivilDateTime{Date: &health.Date{Year: int64(value.Year()), Month: int64(value.Month()), Day: int64(value.Day())}}
}

func civilDays(from, to time.Time) int64 {
	var days int64
	for day := from; day.Before(to); day = day.AddDate(0, 0, 1) {
		days++
	}
	return days
}

func CivilFilter(metric, field string, from, to time.Time) string {
	return fmt.Sprintf(`%s.%s >= %q AND %s.%s < %q`, strings.ReplaceAll(metric, "-", "_"), field, from.Format(time.DateOnly), strings.ReplaceAll(metric, "-", "_"), field, to.Format(time.DateOnly))
}

func PhysicalFilter(metric, field string, from, to time.Time) string {
	return fmt.Sprintf(`%s.%s >= %q AND %s.%s < %q`, strings.ReplaceAll(metric, "-", "_"), field, from.UTC().Format(time.RFC3339), strings.ReplaceAll(metric, "-", "_"), field, to.UTC().Format(time.RFC3339))
}
