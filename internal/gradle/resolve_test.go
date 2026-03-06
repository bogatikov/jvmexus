package gradle

import (
	"fmt"
	"strings"
	"testing"
)

func TestClassifyGradleFailure(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		stderr string
		want   string
	}{
		{name: "timeout", err: fmt.Errorf("timed out after 5s"), stderr: "", want: "timeout"},
		{name: "auth", err: fmt.Errorf("exit code 1"), stderr: "401 unauthorized from repository", want: "auth"},
		{name: "network", err: fmt.Errorf("exit code 1"), stderr: "connection refused", want: "network"},
		{name: "repository", err: fmt.Errorf("exit code 1"), stderr: "Could not GET resource from maven repository", want: "repository"},
		{name: "execution", err: fmt.Errorf("exit code 1"), stderr: "task failed", want: "execution"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyGradleFailure(tc.err, tc.stderr)
			if got != tc.want {
				t.Fatalf("classifyGradleFailure() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCompactStderr(t *testing.T) {
	input := strings.Repeat("x", 400)
	compact := compactStderr(input)
	if len(compact) > 243 {
		t.Fatalf("expected compact stderr to be truncated, got len=%d", len(compact))
	}
	if !strings.HasSuffix(compact, "...") {
		t.Fatalf("expected compact stderr to have ellipsis, got %q", compact)
	}
}

func TestIsRetryableGradleError(t *testing.T) {
	if !isRetryableGradleError(fmt.Errorf("timed out after 5s"), "") {
		t.Fatalf("expected timeout to be retryable")
	}
	if !isRetryableGradleError(fmt.Errorf("exit code 1"), "connection refused") {
		t.Fatalf("expected network error to be retryable")
	}
	if isRetryableGradleError(fmt.Errorf("exit code 1"), "401 unauthorized") {
		t.Fatalf("expected auth error to be non-retryable")
	}
}
