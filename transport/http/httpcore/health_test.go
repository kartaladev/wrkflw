package httpcore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

func TestEvaluateReady(t *testing.T) {
	tests := []struct {
		name           string
		checks         []httpcore.HealthCheck
		wantStatusCode int
		wantStatus     string
		wantCheckNames []string
	}{
		{
			name: "all checks pass",
			checks: []httpcore.HealthCheck{
				httpcore.HealthCheckFunc("db", func(ctx context.Context) error {
					return nil
				}),
				httpcore.HealthCheckFunc("cache", func(ctx context.Context) error {
					return nil
				}),
			},
			wantStatusCode: 200,
			wantStatus:     "ok",
			wantCheckNames: []string{"db", "cache"},
		},
		{
			name: "one check fails",
			checks: []httpcore.HealthCheck{
				httpcore.HealthCheckFunc("db", func(ctx context.Context) error {
					return nil
				}),
				httpcore.HealthCheckFunc("cache", func(ctx context.Context) error {
					return errors.New("connection refused")
				}),
			},
			wantStatusCode: 503,
			wantStatus:     "unavailable",
			wantCheckNames: []string{"db", "cache"},
		},
		{
			name:           "no checks",
			checks:         []httpcore.HealthCheck{},
			wantStatusCode: 200,
			wantStatus:     "ok",
			wantCheckNames: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			statusCode, resp := httpcore.EvaluateReady(ctx, tt.checks)

			if statusCode != tt.wantStatusCode {
				t.Errorf("status code = %d, want %d", statusCode, tt.wantStatusCode)
			}

			// resp should be a map[string]any with Status and Checks fields
			respMap, ok := resp.(map[string]any)
			if !ok {
				t.Fatalf("response is not map[string]any, got %T", resp)
			}

			status, ok := respMap["status"].(string)
			if !ok {
				t.Fatalf("status field missing or not string, got %v", respMap["status"])
			}
			if status != tt.wantStatus {
				t.Errorf("status = %s, want %s", status, tt.wantStatus)
			}

			checks, ok := respMap["checks"].(map[string]string)
			if !ok {
				t.Fatalf("checks field missing or not map[string]string, got %v", respMap["checks"])
			}

			// Verify all expected check names are present
			for _, name := range tt.wantCheckNames {
				if _, found := checks[name]; !found {
					t.Errorf("check %q not found in response checks", name)
				}
			}

			// Verify check values are either "ok" or "unavailable"
			for name, value := range checks {
				if value != "ok" && value != "unavailable" {
					t.Errorf("check %q has unexpected value %q", name, value)
				}
			}
		})
	}
}

func TestEvaluateLive(t *testing.T) {
	ctx := t.Context()
	statusCode, resp := httpcore.EvaluateLive(ctx)

	if statusCode != 200 {
		t.Errorf("status code = %d, want 200", statusCode)
	}

	respMap, ok := resp.(map[string]any)
	if !ok {
		t.Fatalf("response is not map[string]any, got %T", resp)
	}

	status, ok := respMap["status"].(string)
	if !ok {
		t.Fatalf("status field missing or not string, got %v", respMap["status"])
	}
	if status != "ok" {
		t.Errorf("status = %s, want ok", status)
	}
}
