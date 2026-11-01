package route

import (
	"context"
	"errors"
	"net"
	"github.com/metacubex/http"
	"github.com/metacubex/http/httptest"
	"strings"
	"testing"
)

func TestBridgeErrorClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"deadline", context.DeadlineExceeded, "timeout"},
		{"canceled", context.Canceled, "canceled"},
		{"dns", &net.DNSError{Err: "no such host", Name: "example.invalid"}, "dns"},
		{"tls", errors.New("tls: failed to verify certificate"), "tls_or_reality"},
		{"network", errors.New("dial tcp: connection refused"), "network"},
		{"other", errors.New("unexpected end of stream"), "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := bridgeClassifyError(tc.err); got != tc.want {
				t.Fatalf("bridgeClassifyError(%v) = %q; want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestBridgeSanitizeError(t *testing.T) {
	input := errors.New("Get https://alice:password@example.com/path: connection reset")
	got := bridgeSanitizeError(input)
	if strings.Contains(got, "password") || !strings.Contains(got, "[redacted]") {
		t.Fatalf("credentials not redacted: %q", got)
	}

	long := bridgeSanitizeError(errors.New(strings.Repeat("a", 700)))
	if len(long) > 515 {
		t.Fatalf("unbounded error length: %d", len(long))
	}
}

func TestBridgeStatusRouteUsesAPIAuthorization(t *testing.T) {
	r := router(false, "test-secret", "", Cors{})
	for _, tc := range []struct {
		name, token string
		want        int
	}{
		{"unauthenticated", "", http.StatusUnauthorized},
		{"wrong token", "Bearer wrong", http.StatusUnauthorized},
		{"authenticated", "Bearer test-secret", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/surge-bridge/status", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", tc.token)
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
