package postgres

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateError(t *testing.T) {
	message := strings.Repeat("health error ", 1000)
	truncated := truncateError(message)
	if utf8.RuneCountInString(truncated) != 8192 {
		t.Fatalf("got %d runes, want 8192", utf8.RuneCountInString(truncated))
	}
	if !utf8.ValidString(truncated) {
		t.Fatal("truncated error is not valid UTF-8")
	}
}

func TestTruncateErrorKeepsShortMessage(t *testing.T) {
	const message = "short error"
	if got := truncateError(message); got != message {
		t.Fatalf("got %q, want %q", got, message)
	}
}
