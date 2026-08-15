package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/yousefakbar/vitald/internal/config"
	"github.com/yousefakbar/vitald/internal/provider/googlehealth"
	"github.com/yousefakbar/vitald/internal/storage/postgres"
	"golang.org/x/oauth2"
)

type doctorStatus string

const (
	doctorOK      doctorStatus = "ok"
	doctorWarning doctorStatus = "warning"
	doctorFailed  doctorStatus = "failed"
)

type doctorCheck struct {
	Name    string       `json:"name"`
	Status  doctorStatus `json:"status"`
	Message string       `json:"message"`
}

type doctorSummary struct {
	OK       int `json:"ok"`
	Warnings int `json:"warnings"`
	Failed   int `json:"failed"`
}

type doctorReport struct {
	Status  doctorStatus  `json:"status"`
	Checks  []doctorCheck `json:"checks"`
	Summary doctorSummary `json:"summary"`
}

type doctorFailure struct{ count int }

func (e doctorFailure) Error() string { return fmt.Sprintf("doctor found %d failed check(s)", e.count) }

func newDoctorCommand() *cobra.Command {
	var jsonOutput bool
	var online bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check vitald configuration and operational dependencies",
		RunE: func(cmd *cobra.Command, args []string) error {
			report := runDoctor(cmd.Context(), online)
			if err := printDoctorReport(cmd.OutOrStdout(), report, jsonOutput); err != nil {
				return err
			}
			if report.Summary.Failed > 0 {
				return doctorFailure{count: report.Summary.Failed}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "emit machine-readable JSON")
	cmd.Flags().BoolVar(&online, "online", false, "refresh OAuth credentials and verify Google Health identity")
	return cmd
}

func runDoctor(ctx context.Context, online bool) doctorReport {
	var checks []doctorCheck
	cfg, err := config.Load()
	if err != nil {
		return buildDoctorReport([]doctorCheck{{Name: "configuration", Status: doctorFailed, Message: err.Error()}})
	}
	checks = append(checks, doctorCheck{Name: "configuration", Status: doctorOK, Message: fmt.Sprintf("configuration is valid; timezone %s", cfg.Timezone)})

	oauthReady := true
	if err := cfg.ValidateOAuth(); err != nil {
		oauthReady = false
		checks = append(checks, doctorCheck{Name: "oauth_credentials", Status: doctorFailed, Message: err.Error()})
	} else {
		checks = append(checks, doctorCheck{Name: "oauth_credentials", Status: doctorOK, Message: "Google OAuth client credentials are configured"})
	}

	tokenStore := googlehealth.TokenStore{Path: cfg.TokenPath}
	token, tokenReady, tokenChecks := checkDoctorToken(tokenStore)
	checks = append(checks, tokenChecks...)
	checks = append(checks, checkRawArchive(cfg.RawDataPath))

	if cfg.DatabaseURL == "" {
		checks = append(checks, doctorCheck{Name: "database", Status: doctorFailed, Message: "DATABASE_URL is required"})
	} else {
		store, openErr := postgres.Open(ctx, cfg.DatabaseURL)
		if openErr != nil {
			checks = append(checks, doctorCheck{Name: "database", Status: doctorFailed, Message: openErr.Error()})
		} else {
			checks = append(checks, doctorCheck{Name: "database", Status: doctorOK, Message: "PostgreSQL connection succeeded"})
			checks = append(checks, checkMigrationsAndSync(ctx, store)...)
			store.Close()
		}
	}

	if online {
		if !oauthReady || !tokenReady {
			checks = append(checks, doctorCheck{Name: "google_health", Status: doctorFailed, Message: "online check requires valid OAuth credentials and token"})
		} else {
			checks = append(checks, checkGoogleHealth(ctx, cfg, tokenStore, token))
		}
	}
	return buildDoctorReport(checks)
}

func checkDoctorToken(store googlehealth.TokenStore) (*oauth2.Token, bool, []doctorCheck) {
	info, err := os.Stat(store.Path)
	if err != nil {
		return nil, false, []doctorCheck{{Name: "oauth_token", Status: doctorFailed, Message: tokenStatError(err)}}
	}
	if !info.Mode().IsRegular() {
		return nil, false, []doctorCheck{{Name: "oauth_token", Status: doctorFailed, Message: "OAuth token path is not a regular file"}}
	}

	checks := make([]doctorCheck, 0, 3)
	directoryInfo, directoryErr := os.Stat(filepath.Dir(store.Path))
	if directoryErr != nil {
		checks = append(checks, doctorCheck{Name: "oauth_token_directory_permissions", Status: doctorWarning, Message: fmt.Sprintf("could not inspect OAuth token directory permissions: %v", directoryErr)})
	} else if directoryInfo.Mode().Perm() != 0o700 {
		checks = append(checks, doctorCheck{Name: "oauth_token_directory_permissions", Status: doctorWarning, Message: fmt.Sprintf("OAuth token directory permissions are %04o; expected 0700", directoryInfo.Mode().Perm())})
	} else {
		checks = append(checks, doctorCheck{Name: "oauth_token_directory_permissions", Status: doctorOK, Message: fmt.Sprintf("OAuth token directory permissions are %04o", directoryInfo.Mode().Perm())})
	}
	if info.Mode().Perm() != 0o600 {
		checks = append(checks, doctorCheck{Name: "oauth_token_permissions", Status: doctorWarning, Message: fmt.Sprintf("OAuth token permissions are %04o; expected 0600", info.Mode().Perm())})
	} else {
		checks = append(checks, doctorCheck{Name: "oauth_token_permissions", Status: doctorOK, Message: fmt.Sprintf("OAuth token permissions are %04o", info.Mode().Perm())})
	}
	token, err := store.Load()
	if err != nil {
		return nil, false, append(checks, doctorCheck{Name: "oauth_token", Status: doctorFailed, Message: err.Error()})
	}
	if token.RefreshToken == "" {
		return token, false, append(checks, doctorCheck{Name: "oauth_token", Status: doctorFailed, Message: "OAuth token has no refresh token; run 'vitald auth'"})
	}
	message := "OAuth token is readable and contains a refresh token"
	if !token.Expiry.IsZero() && time.Now().After(token.Expiry) {
		message += "; access token requires refresh"
	}
	checks = append(checks, doctorCheck{Name: "oauth_token", Status: doctorOK, Message: message})
	return token, true, checks
}

func tokenStatError(err error) string {
	if errors.Is(err, os.ErrNotExist) {
		return "OAuth token not found; run 'vitald auth'"
	}
	return fmt.Sprintf("inspect OAuth token: %v", err)
}

func checkRawArchive(path string) doctorCheck {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return doctorCheck{Name: "raw_archive", Status: doctorFailed, Message: "raw archive path is not a directory"}
		}
		if err := testDirectoryWrite(path); err != nil {
			return doctorCheck{Name: "raw_archive", Status: doctorFailed, Message: fmt.Sprintf("raw archive path is not writable: %v", err)}
		}
		return doctorCheck{Name: "raw_archive", Status: doctorOK, Message: fmt.Sprintf("raw archive path %s is writable", path)}
	}
	if !errors.Is(err, os.ErrNotExist) {
		return doctorCheck{Name: "raw_archive", Status: doctorFailed, Message: fmt.Sprintf("inspect raw archive path: %v", err)}
	}

	ancestor, ancestorErr := existingAncestor(path)
	if ancestorErr != nil {
		return doctorCheck{Name: "raw_archive", Status: doctorFailed, Message: ancestorErr.Error()}
	}
	if err := testDirectoryWrite(ancestor); err != nil {
		return doctorCheck{Name: "raw_archive", Status: doctorFailed, Message: fmt.Sprintf("raw archive path does not exist and parent %s is not writable: %v", ancestor, err)}
	}
	return doctorCheck{Name: "raw_archive", Status: doctorWarning, Message: fmt.Sprintf("raw archive path %s does not exist; parent %s is writable", path, ancestor)}
}

func existingAncestor(path string) (string, error) {
	current := filepath.Clean(path)
	for {
		current = filepath.Dir(current)
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return "", fmt.Errorf("raw archive parent %s is not a directory", current)
			}
			return current, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect raw archive parent: %w", err)
		}
		next := filepath.Dir(current)
		if next == current {
			return "", fmt.Errorf("no existing parent found for raw archive path %s", path)
		}
	}
}

func testDirectoryWrite(path string) error {
	file, err := os.CreateTemp(path, ".vitald-doctor-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := file.Write([]byte("vitald doctor\n")); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func checkMigrationsAndSync(ctx context.Context, store *postgres.Store) []doctorCheck {
	migrationStatus, err := store.MigrationStatus(ctx)
	if err != nil {
		return []doctorCheck{{Name: "migrations", Status: doctorFailed, Message: err.Error()}}
	}
	if len(migrationStatus.Unknown) > 0 {
		return []doctorCheck{{Name: "migrations", Status: doctorFailed, Message: fmt.Sprintf("database contains migrations unknown to this binary: %s", strings.Join(migrationStatus.Unknown, ", "))}}
	}
	if len(migrationStatus.Pending) > 0 {
		return []doctorCheck{{Name: "migrations", Status: doctorFailed, Message: fmt.Sprintf("pending migrations: %s; run a database command to apply them", strings.Join(migrationStatus.Pending, ", "))}}
	}
	checks := []doctorCheck{{Name: "migrations", Status: doctorOK, Message: fmt.Sprintf("all %d embedded migrations are applied", len(migrationStatus.Applied))}}

	lease, acquired, err := store.TryAcquireSyncLease(ctx)
	if err != nil {
		return append(checks, doctorCheck{Name: "synchronization", Status: doctorFailed, Message: err.Error()})
	}
	running, runningErr := store.RunningSyncStatus(ctx)
	if !acquired {
		message := "synchronization lock is currently held"
		if runningErr == nil {
			message = fmt.Sprintf("synchronization is active (%d running run(s), %d metric(s))", running.Runs, running.Metrics)
		}
		return append(checks, doctorCheck{Name: "synchronization", Status: doctorWarning, Message: message})
	}
	defer lease.Release(context.Background())
	if runningErr != nil {
		return append(checks, doctorCheck{Name: "synchronization", Status: doctorFailed, Message: runningErr.Error()})
	}
	if running.Runs > 0 || running.Metrics > 0 {
		return append(checks, doctorCheck{Name: "synchronization", Status: doctorFailed, Message: fmt.Sprintf("found abandoned history: %d running run(s), %d metric(s); the next sync will recover it", running.Runs, running.Metrics)})
	}
	return append(checks, doctorCheck{Name: "synchronization", Status: doctorOK, Message: "synchronization lock is available and no abandoned history was found"})
}

func checkGoogleHealth(ctx context.Context, cfg config.Config, store googlehealth.TokenStore, token *oauth2.Token) doctorCheck {
	oauthCfg := googlehealth.OAuthConfig(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL)
	source := oauthCfg.TokenSource(ctx, token)
	fresh, err := source.Token()
	if err != nil {
		return doctorCheck{Name: "google_health", Status: doctorFailed, Message: fmt.Sprintf("refresh OAuth token: %v", err)}
	}
	if err := store.Save(fresh); err != nil {
		return doctorCheck{Name: "google_health", Status: doctorFailed, Message: err.Error()}
	}
	httpClient := oauth2.NewClient(ctx, oauth2.ReuseTokenSource(fresh, source))
	httpClient.Timeout = cfg.HTTPTimeout
	identity, err := googlehealth.NewClient(httpClient).Identity(ctx)
	if err != nil {
		return doctorCheck{Name: "google_health", Status: doctorFailed, Message: err.Error()}
	}
	if identity.HealthUserId == "" {
		return doctorCheck{Name: "google_health", Status: doctorWarning, Message: "Google Health identity succeeded but returned no user ID"}
	}
	return doctorCheck{Name: "google_health", Status: doctorOK, Message: "OAuth refresh and Google Health identity succeeded"}
}

func buildDoctorReport(checks []doctorCheck) doctorReport {
	report := doctorReport{Status: doctorOK, Checks: checks}
	for _, check := range checks {
		switch check.Status {
		case doctorOK:
			report.Summary.OK++
		case doctorWarning:
			report.Summary.Warnings++
		case doctorFailed:
			report.Summary.Failed++
		}
	}
	if report.Summary.Failed > 0 {
		report.Status = doctorFailed
	} else if report.Summary.Warnings > 0 {
		report.Status = doctorWarning
	}
	return report
}

func printDoctorReport(out io.Writer, report doctorReport, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return fmt.Errorf("encode doctor report: %w", err)
		}
		return nil
	}
	fmt.Fprintln(out, "vitald doctor")
	for _, check := range report.Checks {
		fmt.Fprintf(out, "  %-7s %-24s %s\n", strings.ToUpper(string(check.Status)), check.Name, check.Message)
	}
	fmt.Fprintf(out, "\nSummary: %d ok, %d warning(s), %d failed\n", report.Summary.OK, report.Summary.Warnings, report.Summary.Failed)
	return nil
}
