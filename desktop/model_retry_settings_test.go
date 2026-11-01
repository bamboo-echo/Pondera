package main

import (
	"testing"

	"pondera/internal/config"
)

func TestSetRetryCountConfigBounds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   int
		want    int
		wantErr bool
	}{
		{"off", 0, 0, false},
		{"default", 3, 3, false},
		{"max", config.MaxRetryCount, config.MaxRetryCount, false},
		{"negative", -1, 0, true},
		{"over max", config.MaxRetryCount + 1, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			err := setRetryCountConfig(cfg, tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("value %d accepted", tc.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("value %d: %v", tc.value, err)
			}
			if cfg.Agent.RetryCount != tc.want {
				t.Fatalf("RetryCount = %d, want %d", cfg.Agent.RetryCount, tc.want)
			}
		})
	}
}

func TestDesktopRetryCountViewClamps(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{-1, 0}, {3, 3}, {config.MaxRetryCount + 5, config.MaxRetryCount}} {
		if got := desktopRetryCount(tc.in); got != tc.want {
			t.Errorf("desktopRetryCount(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
