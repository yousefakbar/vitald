package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/yousefakbar/vitald/internal/provider/googlehealth"
	"golang.org/x/oauth2"
)

func TestBuildDoctorReport(t *testing.T) {
	report := buildDoctorReport([]doctorCheck{
		{Name: "one", Status: doctorOK, Message: "ok"},
		{Name: "two", Status: doctorWarning, Message: "warning"},
		{Name: "three", Status: doctorFailed, Message: "failed"},
	})
	if report.Status != doctorFailed || report.Summary.OK != 1 || report.Summary.Warnings != 1 || report.Summary.Failed != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestPrintDoctorReportJSON(t *testing.T) {
	want := buildDoctorReport([]doctorCheck{{Name: "configuration", Status: doctorOK, Message: "valid"}})
	var output bytes.Buffer
	if err := printDoctorReport(&output, want, true); err != nil {
		t.Fatal(err)
	}
	var got doctorReport
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != doctorOK || len(got.Checks) != 1 || got.Checks[0].Name != "configuration" {
		t.Fatalf("unexpected JSON report: %+v", got)
	}
}

func TestCheckDoctorToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	store := googlehealth.TokenStore{Path: path}
	if err := store.Save(&oauth2.Token{AccessToken: "access", RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
	_, ready, checks := checkDoctorToken(store)
	if !ready || findDoctorCheck(checks, "oauth_token").Status != doctorOK || findDoctorCheck(checks, "oauth_token_permissions").Status != doctorOK {
		t.Fatalf("unexpected token checks: %+v", checks)
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	_, ready, checks = checkDoctorToken(store)
	if !ready || findDoctorCheck(checks, "oauth_token_permissions").Status != doctorWarning {
		t.Fatalf("expected permissions warning: %+v", checks)
	}
}

func TestCheckDoctorTokenRequiresRefreshToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	store := googlehealth.TokenStore{Path: path}
	if err := store.Save(&oauth2.Token{AccessToken: "access"}); err != nil {
		t.Fatal(err)
	}
	_, ready, checks := checkDoctorToken(store)
	if ready || findDoctorCheck(checks, "oauth_token").Status != doctorFailed {
		t.Fatalf("expected missing refresh token failure: %+v", checks)
	}
}

func TestCheckRawArchive(t *testing.T) {
	root := t.TempDir()
	if check := checkRawArchive(root); check.Status != doctorOK {
		t.Fatalf("existing writable archive check: %+v", check)
	}
	missing := filepath.Join(root, "missing", "raw")
	if check := checkRawArchive(missing); check.Status != doctorWarning {
		t.Fatalf("missing archive check: %+v", check)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("doctor unexpectedly created archive path: %v", err)
	}
}

func findDoctorCheck(checks []doctorCheck, name string) doctorCheck {
	for _, check := range checks {
		if check.Name == name {
			return check
		}
	}
	return doctorCheck{}
}
