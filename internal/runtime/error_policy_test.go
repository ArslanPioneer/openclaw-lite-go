package runtime

import (
	"errors"
	"testing"
)

func TestClassifyExecutionError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ErrorKind
	}{
		{
			name: "rate limit",
			err:  errors.New("agent returned status 429: too many requests"),
			want: ErrorRateLimit,
		},
		{
			name: "timeout",
			err:  errors.New("context deadline exceeded"),
			want: ErrorTimeout,
		},
		{
			name: "auth",
			err:  errors.New("agent returned status 401: unauthorized"),
			want: ErrorAuth,
		},
		{
			name: "billing",
			err:  errors.New("agent returned status 402: payment required"),
			want: ErrorBilling,
		},
		{
			name: "format",
			err:  errors.New("tool call parse failed: invalid character"),
			want: ErrorFormat,
		},
		{
			name: "unknown",
			err:  errors.New("some random upstream issue"),
			want: ErrorUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyExecutionError(tc.err)
			if got != tc.want {
				t.Fatalf("ClassifyExecutionError() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatUserFacingExecutionError(t *testing.T) {
	tests := []ErrorKind{
		ErrorRateLimit,
		ErrorTimeout,
		ErrorAuth,
		ErrorBilling,
		ErrorFormat,
		ErrorUnknown,
	}

	for _, kind := range tests {
		got := FormatUserFacingExecutionError(kind)
		if got == "" {
			t.Fatalf("FormatUserFacingExecutionError(%q) returned empty string", kind)
		}
	}
}
