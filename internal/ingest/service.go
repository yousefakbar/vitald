package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/yousefakbar/vitald/internal/archive"
	"github.com/yousefakbar/vitald/internal/provider/googlehealth"
	"github.com/yousefakbar/vitald/internal/storage/postgres"
	"google.golang.org/api/health/v4"
)

var Metrics = []string{
	"steps", "heart-rate", "daily-resting-heart-rate", "daily-heart-rate-variability",
	"sleep", "exercise", "total-calories", "nutrition-log", "weight",
}

var metricSet = func() map[string]bool {
	result := make(map[string]bool, len(Metrics))
	for _, metric := range Metrics {
		result[metric] = true
	}
	return result
}()

type Service struct {
	API      *googlehealth.Client
	Archive  archive.Filesystem
	Store    *postgres.Store
	Location *time.Location
	Logger   *slog.Logger
}

type Summary struct {
	Metric         string
	Pages, Records int
	Files          []string
}

func SupportedMetric(metric string) bool { return metricSet[metric] }

func (s *Service) Fetch(ctx context.Context, metric string, from, to time.Time, checkpoint bool) (Summary, error) {
	if !SupportedMetric(metric) {
		return Summary{}, fmt.Errorf("unsupported metric %q", metric)
	}
	if !from.Before(to) {
		return Summary{}, fmt.Errorf("start date must be before end date")
	}
	chunkDays := 0
	if isDailyRollup(metric) {
		chunkDays = 90
	}
	if metric == "total-calories" {
		chunkDays = 14
	}
	if chunkDays == 0 {
		chunkDays = int(to.Sub(from).Hours()/24) + 1
	}
	total := Summary{Metric: metric}
	for start := from; start.Before(to); {
		end := start.AddDate(0, 0, chunkDays)
		if end.After(to) {
			end = to
		}
		summary, err := s.fetchRange(ctx, metric, start, end)
		if err != nil {
			return total, err
		}
		total.Pages += summary.Pages
		total.Records += summary.Records
		total.Files = append(total.Files, summary.Files...)
		start = end
	}
	if checkpoint {
		if err := s.Store.SetCheckpoint(ctx, metric, to); err != nil {
			return total, err
		}
	}
	return total, nil
}

func (s *Service) fetchRange(ctx context.Context, metric string, from, to time.Time) (Summary, error) {
	runID := archive.RunID(from, to)
	summary := Summary{Metric: metric}
	pageToken := ""
	for {
		pageNumber := summary.Pages + 1
		var raw []byte
		var next string
		var points []*health.DataPoint
		var rollups []*health.DailyRollupDataPoint
		if isDailyRollup(metric) {
			page, err := s.API.DailyRollup(ctx, metric, from, to, pageToken)
			if err != nil {
				return summary, fmt.Errorf("fetch %s page %d: %w", metric, pageNumber, err)
			}
			raw, next, rollups = page.Raw, page.NextPageToken, page.DataPoints
		} else {
			page, err := s.API.List(ctx, metric, filterFor(metric, from, to), pageToken, pageSize(metric))
			if err != nil {
				return summary, fmt.Errorf("fetch %s page %d: %w", metric, pageNumber, err)
			}
			raw, next, points = page.Raw, page.NextPageToken, page.DataPoints
		}
		file, err := s.Archive.Save(ctx, metric, runID, pageNumber, raw)
		if err != nil {
			return summary, err
		}
		if err := s.Store.SaveArchive(ctx, postgres.Archive{RunID: runID, Metric: metric, Page: pageNumber, Path: file.Path, Checksum: file.Checksum, Size: file.Size, From: from, To: to}); err != nil {
			return summary, err
		}
		var records []postgres.Record
		if isDailyRollup(metric) {
			records, err = normalizeRollups(metric, rollups, s.Location)
		} else {
			records, err = normalizePoints(metric, points, s.Location)
		}
		if err != nil {
			return summary, fmt.Errorf("normalize %s page %d (raw response retained at %s): %w", metric, pageNumber, file.Path, err)
		}
		if err := s.Store.UpsertRecords(ctx, records); err != nil {
			return summary, err
		}
		summary.Pages++
		summary.Records += len(records)
		summary.Files = append(summary.Files, file.Path)
		s.Logger.Info("ingested page", "metric", metric, "page", pageNumber, "records", len(records), "archive", file.Path)
		if next == "" {
			break
		}
		pageToken = next
	}
	return summary, nil
}

func isDailyRollup(metric string) bool {
	return metric == "steps" || metric == "total-calories" || metric == "nutrition-log"
}
func pageSize(metric string) int {
	if metric == "sleep" || metric == "exercise" {
		return 25
	}
	return 10000
}

func filterFor(metric string, from, to time.Time) string {
	switch metric {
	case "heart-rate":
		return googlehealth.PhysicalFilter(metric, "sample_time.physical_time", from, to)
	case "daily-resting-heart-rate", "daily-heart-rate-variability":
		return googlehealth.CivilFilter(metric, "date", from, to)
	case "sleep":
		return googlehealth.CivilFilter(metric, "interval.civil_end_time", from, to)
	case "exercise":
		return googlehealth.CivilFilter(metric, "interval.civil_start_time", from, to)
	case "weight":
		return googlehealth.PhysicalFilter(metric, "sample_time.physical_time", from, to)
	default:
		return ""
	}
}

func normalizePoints(metric string, points []*health.DataPoint, location *time.Location) ([]postgres.Record, error) {
	records := make([]postgres.Record, 0, len(points))
	for _, point := range points {
		if point == nil {
			continue
		}
		record := postgres.Record{Metric: metric, ProviderRecordID: point.Name, Source: point.DataSource, Raw: point}
		var err error
		switch metric {
		case "heart-rate":
			if point.HeartRate == nil || point.HeartRate.SampleTime == nil {
				continue
			}
			record.ObservedAt, err = parseTimePtr(point.HeartRate.SampleTime.PhysicalTime)
			value := float64(point.HeartRate.BeatsPerMinute)
			record.Value, record.Unit = &value, "bpm"
			record.Attributes = point.HeartRate.Metadata
		case "daily-resting-heart-rate":
			if point.DailyRestingHeartRate == nil {
				continue
			}
			record.LocalDate, err = datePtr(point.DailyRestingHeartRate.Date, location)
			value := float64(point.DailyRestingHeartRate.BeatsPerMinute)
			record.Value, record.Unit = &value, "bpm"
			record.Attributes = point.DailyRestingHeartRate.DailyRestingHeartRateMetadata
		case "daily-heart-rate-variability":
			if point.DailyHeartRateVariability == nil {
				continue
			}
			hrv := point.DailyHeartRateVariability
			record.LocalDate, err = datePtr(hrv.Date, location)
			record.Unit = "ms"
			if hrv.AverageHeartRateVariabilityMilliseconds > 0 {
				value := hrv.AverageHeartRateVariabilityMilliseconds
				record.Value = &value
			}
			record.Attributes = map[string]any{"deepSleepRmssdMs": hrv.DeepSleepRootMeanSquareOfSuccessiveDifferencesMilliseconds, "entropy": hrv.Entropy, "nonRemHeartRateBpm": hrv.NonRemHeartRateBeatsPerMinute}
		case "sleep":
			if point.Sleep == nil || point.Sleep.Interval == nil {
				continue
			}
			sleep := point.Sleep
			record.ObservedAt, err = parseTimePtr(sleep.Interval.StartTime)
			if err == nil {
				record.EndedAt, err = parseTimePtr(sleep.Interval.EndTime)
			}
			minutes := durationMinutes(record.ObservedAt, record.EndedAt)
			if sleep.Summary != nil {
				minutes = float64(sleep.Summary.MinutesAsleep)
			}
			record.Value, record.Unit = &minutes, "minutes"
			record.LocalDate = localDate(record.EndedAt, location)
			record.Attributes = map[string]any{"durationMinutes": minutes, "sleepScore": nil, "scoreAvailable": false, "type": sleep.Type, "metadata": sleep.Metadata, "summary": sleep.Summary, "stages": sleep.Stages}
		case "exercise":
			if point.Exercise == nil || point.Exercise.Interval == nil {
				continue
			}
			exercise := point.Exercise
			record.ObservedAt, err = parseTimePtr(exercise.Interval.StartTime)
			if err == nil {
				record.EndedAt, err = parseTimePtr(exercise.Interval.EndTime)
			}
			duration := durationMinutes(record.ObservedAt, record.EndedAt)
			record.Value, record.Unit = &duration, "minutes"
			attributes := map[string]any{"displayName": exercise.DisplayName, "exerciseType": exercise.ExerciseType, "durationMinutes": duration, "activeDuration": exercise.ActiveDuration, "cardioLoad": nil, "cardioLoadAvailable": false, "metrics": exercise.MetricsSummary}
			if exercise.MetricsSummary != nil {
				attributes["caloriesKcal"] = exercise.MetricsSummary.CaloriesKcal
				attributes["activeZoneMinutes"] = exercise.MetricsSummary.ActiveZoneMinutes
			}
			record.Attributes = attributes
			record.LocalDate = localDate(record.ObservedAt, location)
		case "weight":
			if point.Weight == nil || point.Weight.SampleTime == nil {
				continue
			}
			record.ObservedAt, err = parseTimePtr(point.Weight.SampleTime.PhysicalTime)
			value := point.Weight.WeightGrams / 1000
			record.Value, record.Unit = &value, "kg"
			record.Attributes = map[string]any{"notes": point.Weight.Notes, "weightGrams": point.Weight.WeightGrams}
		}
		if err != nil {
			return nil, fmt.Errorf("normalize %s: %w", metric, err)
		}
		record.Key, err = recordKey(record)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func normalizeRollups(metric string, points []*health.DailyRollupDataPoint, location *time.Location) ([]postgres.Record, error) {
	records := make([]postgres.Record, 0, len(points))
	for _, point := range points {
		if point == nil {
			continue
		}
		date, err := civilDatePtr(point.CivilStartTime, location)
		if err != nil {
			return nil, err
		}
		record := postgres.Record{Metric: metric, LocalDate: date, Raw: point, Attributes: point}
		var value float64
		switch metric {
		case "steps":
			if point.Steps == nil {
				continue
			}
			value = float64(point.Steps.CountSum)
			record.Unit = "steps"
		case "total-calories":
			if point.TotalCalories == nil {
				continue
			}
			value = point.TotalCalories.KcalSum
			record.Unit = "kcal"
		case "nutrition-log":
			if point.NutritionLog == nil || point.NutritionLog.Energy == nil {
				continue
			}
			value = point.NutritionLog.Energy.KcalSum
			record.Unit = "kcal"
		}
		record.Value = &value
		record.Key, err = recordKey(record)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func recordKey(record postgres.Record) (string, error) {
	if record.ProviderRecordID != "" {
		sum := sha256.Sum256([]byte(record.Metric + "\x00" + record.ProviderRecordID))
		return hex.EncodeToString(sum[:]), nil
	}
	if record.LocalDate != nil && record.ObservedAt == nil {
		sum := sha256.Sum256([]byte(record.Metric + "\x00" + record.LocalDate.Format(time.DateOnly)))
		return hex.EncodeToString(sum[:]), nil
	}
	data, err := json.Marshal(struct {
		Metric        string
		At, End, Date *time.Time
		Value         *float64
		Raw           any
	}{record.Metric, record.ObservedAt, record.EndedAt, record.LocalDate, record.Value, record.Raw})
	if err != nil {
		return "", fmt.Errorf("build record key: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func parseTimePtr(value string) (*time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
func datePtr(value *health.Date, loc *time.Location) (*time.Time, error) {
	if value == nil {
		return nil, fmt.Errorf("date is missing")
	}
	date := time.Date(int(value.Year), time.Month(value.Month), int(value.Day), 0, 0, 0, 0, loc)
	return &date, nil
}
func civilDatePtr(value *health.CivilDateTime, loc *time.Location) (*time.Time, error) {
	if value == nil {
		return nil, fmt.Errorf("civil date is missing")
	}
	return datePtr(value.Date, loc)
}
func localDate(value *time.Time, loc *time.Location) *time.Time {
	if value == nil {
		return nil
	}
	local := value.In(loc)
	date := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return &date
}
func durationMinutes(start, end *time.Time) float64 {
	if start == nil || end == nil {
		return 0
	}
	return end.Sub(*start).Minutes()
}
