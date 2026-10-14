package models

import "time"

// Validation-prerequisite preflight deadlines (validation-prerequisites.md).
const (
	// DefaultValidationPreflightProbeTimeoutSeconds bounds one probe. Probes
	// that import a project's dependencies can take tens of seconds on a
	// loaded host.
	DefaultValidationPreflightProbeTimeoutSeconds = 60
	// DefaultValidationPreflightTimeoutSeconds bounds all probes of one check.
	DefaultValidationPreflightTimeoutSeconds = 120
	// InSessionValidationPreflightTimeout bounds a check that runs inside a
	// provider tool call (await-resubmission reclaim, in-session ops). It must
	// fit the headroom between the native await call interval and the host's
	// foreground limit (540 s against Bash's 600 s, providers.AwaitInterval),
	// so configuration cannot raise it.
	InSessionValidationPreflightTimeout = 30 * time.Second
)

// ValidationPreflightConfig sets the preflight deadlines; non-positive values
// mean the defaults.
type ValidationPreflightConfig struct {
	ProbeTimeoutSeconds int `yaml:"probe_timeout_seconds,omitempty" json:"probe_timeout_seconds,omitempty"`
	TimeoutSeconds      int `yaml:"timeout_seconds,omitempty" json:"timeout_seconds,omitempty"`
}

// EffectiveValidationPreflightLimits returns the per-probe and whole-check
// deadlines. An in-session check is capped at
// InSessionValidationPreflightTimeout, and no probe outlasts the whole check.
func EffectiveValidationPreflightLimits(config Config, inSession bool) (probe, total time.Duration) {
	probeSeconds, totalSeconds := DefaultValidationPreflightProbeTimeoutSeconds, DefaultValidationPreflightTimeoutSeconds
	if c := config.ValidationPreflight; c != nil {
		if c.ProbeTimeoutSeconds > 0 {
			probeSeconds = c.ProbeTimeoutSeconds
		}
		if c.TimeoutSeconds > 0 {
			totalSeconds = c.TimeoutSeconds
		}
	}
	probe = time.Duration(probeSeconds) * time.Second
	total = time.Duration(totalSeconds) * time.Second
	if inSession {
		total = min(total, InSessionValidationPreflightTimeout)
	}
	return min(probe, total), total
}
