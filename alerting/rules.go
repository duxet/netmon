// Package alerting evaluates alert rules against the collected traffic after
// every collection round, keeps track of the alerts they raise, and hands
// the notifications about them to the dispatcher.
package alerting

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"

	"github.com/duxet/netmon/apps"
	"github.com/duxet/netmon/notify"
)

// Rule types.
const (
	// NewDevice fires once for every device that shows up on the network
	// after the rule was created.
	NewDevice = "new_device"
	// TrafficThreshold fires while a device's traffic over a sliding window
	// is above a threshold, and resolves when it is back under.
	TrafficThreshold = "traffic_threshold"
)

// Types lists the rule types.
var Types = []string{NewDevice, TrafficThreshold}

// IsEvent reports whether rules of a type are about events, whose alerts
// are over as soon as they are raised, rather than conditions that hold for
// a while.
func IsEvent(ruleType string) bool {
	return ruleType == NewDevice
}

// NewDeviceParams are the settings of a new_device rule.
type NewDeviceParams struct {
	// IgnoreRandomMACs skips locally administered MAC addresses, which
	// phones use as private Wi-Fi addresses and may change.
	IgnoreRandomMACs bool `json:"ignore_random_macs"`
}

// Directions of traffic, from the device's side.
const (
	Download = "download"
	Upload   = "upload"
	Total    = "total"
)

// Window bounds of traffic_threshold rules, in seconds. Data arrives once a
// minute, so shorter windows would only measure noise; 24 hours is as far
// as a sliding window is useful before calendar days are.
const (
	MinWindowSeconds = 5 * 60
	MaxWindowSeconds = 24 * 60 * 60
)

// maxScopedDevices bounds the devices a rule can be limited to.
const maxScopedDevices = 256

// TrafficParams are the settings of a traffic_threshold rule.
type TrafficParams struct {
	// Direction is download, upload or total.
	Direction      string `json:"direction"`
	ThresholdBytes uint64 `json:"threshold_bytes"`
	WindowSeconds  int64  `json:"window_s"`
	// App counts only the traffic of one app, by its name in /api/apps
	// ("Other" for traffic of no known app); empty counts all traffic.
	App string `json:"app,omitempty"`
	// MACs limits the rule to these devices; empty is every device.
	MACs []string `json:"macs,omitempty"`
}

// ResolveFraction is how far under its threshold a device's traffic has to
// fall to resolve an alert, so that traffic hovering around the threshold
// doesn't fire and resolve it every minute.
const ResolveFraction = 0.9

// ValidationError explains why a rule is invalid.
type ValidationError struct{ msg string }

func (e *ValidationError) Error() string { return e.msg }

func invalid(format string, args ...any) error {
	return &ValidationError{msg: fmt.Sprintf(format, args...)}
}

// decodeStrict decodes JSON into v, rejecting unknown fields.
func decodeStrict(raw []byte, v any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return invalid("invalid params: %v", err)
	}
	return nil
}

// ValidateParams checks the params of a rule of the given type and returns
// them normalized (e.g. MAC addresses in canonical form) as JSON. Errors are
// ValidationErrors. Empty params are the type's defaults where it has them.
func ValidateParams(ruleType string, raw json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		raw = []byte("{}")
	}

	var params any
	switch ruleType {
	case NewDevice:
		var p NewDeviceParams
		if err := decodeStrict(raw, &p); err != nil {
			return "", err
		}
		params = p

	case TrafficThreshold:
		var p TrafficParams
		if err := decodeStrict(raw, &p); err != nil {
			return "", err
		}
		if err := p.validate(); err != nil {
			return "", err
		}
		params = p

	default:
		return "", invalid("unknown rule type %q; want one of %v", ruleType, Types)
	}

	data, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (p *TrafficParams) validate() error {
	if !slices.Contains([]string{Download, Upload, Total}, p.Direction) {
		return invalid("direction must be download, upload or total")
	}
	if p.ThresholdBytes == 0 {
		return invalid("threshold_bytes must be more than 0")
	}
	if p.WindowSeconds < MinWindowSeconds || p.WindowSeconds > MaxWindowSeconds {
		return invalid("window_s must be between %d and %d seconds", MinWindowSeconds, MaxWindowSeconds)
	}
	if p.App != "" && !isApp(p.App) {
		return invalid("unknown app %q", p.App)
	}
	if len(p.MACs) > maxScopedDevices {
		return invalid("a rule can be limited to at most %d devices", maxScopedDevices)
	}

	macs := make([]string, 0, len(p.MACs))
	for _, s := range p.MACs {
		mac, err := net.ParseMAC(s)
		if err != nil {
			return invalid("invalid mac address %q", s)
		}
		macs = append(macs, mac.String())
	}
	slices.Sort(macs)
	p.MACs = slices.Compact(macs)
	return nil
}

func isApp(name string) bool {
	if name == apps.Other {
		return true
	}
	return slices.ContainsFunc(apps.Known, func(app apps.App) bool { return app.Name == name })
}

// parseParams decodes stored params, which ValidateParams normalized.
func parseParams[T any](raw string) (T, error) {
	var p T
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return p, fmt.Errorf("invalid params: %w", err)
	}
	return p, nil
}

// Severities lists the valid severities.
var Severities = []notify.Severity{notify.Info, notify.Warning, notify.Critical}

// ValidateSeverity checks a severity name.
func ValidateSeverity(severity string) error {
	if !slices.Contains(Severities, notify.Severity(severity)) {
		return invalid("severity must be info, warning or critical")
	}
	return nil
}

// MinRepeatSeconds is the shortest interval at which a firing alert can be
// sent again.
const MinRepeatSeconds = 15 * 60

// ValidateRepeat checks a repeat interval in seconds; nil is never.
func ValidateRepeat(seconds *int64) error {
	if seconds != nil && *seconds < MinRepeatSeconds {
		return invalid("repeat_interval_s must be at least %d seconds, or null to send an alert once", MinRepeatSeconds)
	}
	return nil
}

// IsValidationError reports whether err says a rule is invalid.
func IsValidationError(err error) bool {
	var v *ValidationError
	return errors.As(err, &v)
}
