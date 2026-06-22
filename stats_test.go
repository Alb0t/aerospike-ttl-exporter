package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestScanErrMsg(t *testing.T) {
	t.Run("nil error -> empty string (success)", func(t *testing.T) {
		if got := scanErrMsg("ns1", "foo", nil); got != "" {
			t.Errorf("scanErrMsg(nil) = %q, want \"\"", got)
		}
	})

	t.Run("non-nil error -> non-empty, identifies set and is non-fatal", func(t *testing.T) {
		// The whole point: a scan error returns a message for the caller to log
		// and skip the set, instead of logrus.Fatal taking the process down. If
		// this called Fatal, the test binary would exit here.
		got := scanErrMsg("myns", "", fmt.Errorf("command execution timed out"))
		if got == "" {
			t.Fatal("expected non-empty error message")
		}
		if !strings.Contains(got, "myns") {
			t.Errorf("message %q should name the namespace", got)
		}
		if !strings.Contains(got, "timed out") {
			t.Errorf("message %q should carry the underlying error", got)
		}
	})
}
