package models

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestValidationPreflightConfigYAMLKeys(t *testing.T) {
	var config Config
	if err := yaml.Unmarshal([]byte("validation_preflight:\n  probe_timeout_seconds: 7\n  timeout_seconds: 9\n"), &config); err != nil {
		t.Fatal(err)
	}
	if c := config.ValidationPreflight; c == nil || c.ProbeTimeoutSeconds != 7 || c.TimeoutSeconds != 9 {
		t.Fatalf("state.yaml keys not decoded: %#v", c)
	}
}

func TestEffectiveValidationPreflightLimits(t *testing.T) {
	configured := func(probe, total int) Config {
		return Config{ValidationPreflight: &ValidationPreflightConfig{ProbeTimeoutSeconds: probe, TimeoutSeconds: total}}
	}
	for _, tc := range []struct {
		name         string
		config       Config
		inSession    bool
		probe, total time.Duration
	}{
		{"unset uses defaults", Config{}, false, 60 * time.Second, 120 * time.Second},
		{"non-positive uses defaults", configured(0, -5), false, 60 * time.Second, 120 * time.Second},
		{"configured values", configured(90, 300), false, 90 * time.Second, 300 * time.Second},
		{"probe capped by total", configured(90, 45), false, 45 * time.Second, 45 * time.Second},
		{"in-session defaults capped", Config{}, true, 30 * time.Second, 30 * time.Second},
		{"in-session cannot be raised", configured(90, 300), true, 30 * time.Second, 30 * time.Second},
		{"in-session smaller values kept", configured(5, 20), true, 5 * time.Second, 20 * time.Second},
	} {
		probe, total := EffectiveValidationPreflightLimits(tc.config, tc.inSession)
		if probe != tc.probe || total != tc.total {
			t.Errorf("%s: got probe %s total %s, want %s %s", tc.name, probe, total, tc.probe, tc.total)
		}
	}
}
