package cli

import (
	"testing"

	"github.com/yousefakbar/vitald/internal/storage/postgres"
)

func TestSyncRunStatus(t *testing.T) {
	tests := []struct {
		name              string
		succeeded, failed int
		want              postgres.RunStatus
	}{
		{"all succeeded", 9, 0, postgres.RunStatusSucceeded},
		{"partial", 8, 1, postgres.RunStatusPartial},
		{"all failed", 0, 9, postgres.RunStatusFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := syncRunStatus(test.succeeded, test.failed); got != test.want {
				t.Fatalf("syncRunStatus(%d, %d) = %q, want %q", test.succeeded, test.failed, got, test.want)
			}
		})
	}
}
