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
	"strings"
	"time"

	"github.com/duxet/netmon/apps"
	"github.com/duxet/netmon/blocklists"
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
	// DeviceInactive fires while a device has had no traffic for a while,
	// and resolves when it has some again.
	DeviceInactive = "device_inactive"
	// DestinationPort fires when a device connects to one of some ports.
	DestinationPort = "destination_port"
	// DestinationCountry fires when a device exchanges traffic with a
	// country it shouldn't: one of a list, or one outside of it.
	DestinationCountry = "destination_country"
	// DeviceOnline fires when a device that was quiet for a while has
	// traffic again.
	DeviceOnline = "device_online"
	// NewDestination fires when a device exchanges traffic with a country,
	// or an autonomous system, it hasn't before.
	NewDestination = "new_destination"
	// Blocklist fires when a device exchanges traffic with an address on a
	// public blocklist, such as a botnet's command and control server.
	Blocklist = "blocklist"
)

// Types lists the rule types.
var Types = []string{NewDevice, TrafficThreshold, DeviceInactive, DestinationPort, DestinationCountry, DeviceOnline, NewDestination, Blocklist}

// IsEvent reports whether rules of a type are about events, whose alerts
// are over as soon as they are raised, rather than conditions that hold for
// a while.
func IsEvent(ruleType string) bool {
	return ruleType == NewDevice || ruleType == DestinationPort || ruleType == DestinationCountry ||
		ruleType == DeviceOnline || ruleType == NewDestination || ruleType == Blocklist
}

// EventCooldown is how long an event rule stays quiet about a subject (a
// device and port, say) after alerting about it, so that a device that
// keeps doing the same thing doesn't raise an alert every minute.
const EventCooldown = 24 * time.Hour

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
	// Network counts the internet traffic of all these devices together,
	// as one alert about the whole network, instead of each on its own.
	// Traffic between local devices doesn't count then.
	Network bool `json:"network,omitempty"`
}

// InactiveParams are the settings of a device_inactive rule.
type InactiveParams struct {
	InactiveSeconds int64 `json:"inactive_s"`
	// MACs are the devices to watch; at least one.
	MACs []string `json:"macs"`
}

// Bounds of device_inactive rules, in seconds.
const (
	MinInactiveSeconds = 5 * 60
	MaxInactiveSeconds = 7 * 24 * 60 * 60
)

// Protocols a destination_port rule can watch.
const (
	TCP = "tcp"
	UDP = "udp"
	Any = "any"
)

// maxPorts bounds the ports of a destination_port rule.
const maxPorts = 32

// PortParams are the settings of a destination_port rule.
type PortParams struct {
	Ports []int `json:"ports"`
	// Protocol is tcp, udp or any.
	Protocol string `json:"protocol"`
	// MACs limits the rule to these devices; empty is every device.
	MACs []string `json:"macs,omitempty"`
}

// Modes of destination_country rules.
const (
	// Deny alerts on traffic with the listed countries.
	Deny = "deny"
	// Allow alerts on traffic with any country not listed.
	Allow = "allow"
)

// CountryParams are the settings of a destination_country rule.
type CountryParams struct {
	// Mode is deny or allow.
	Mode string `json:"mode"`
	// Countries are ISO 3166-1 alpha-2 codes, such as "DE".
	Countries []string `json:"countries"`
	// MACs limits the rule to these devices; empty is every device.
	MACs []string `json:"macs,omitempty"`
}

// OnlineParams are the settings of a device_online rule.
type OnlineParams struct {
	// InactiveSeconds is how long a device has to have been quiet.
	InactiveSeconds int64 `json:"inactive_s"`
	// MACs limits the rule to these devices; empty is every device.
	MACs []string `json:"macs,omitempty"`
}

// What new_destination rules look at.
const (
	ByCountry = "country"
	ByASN     = "asn"
)

// Bounds of the days a new_destination rule learns from.
const (
	MinBaselineDays     = 1
	MaxBaselineDays     = 90
	DefaultBaselineDays = 30
)

// DestinationParams are the settings of a new_destination rule.
type DestinationParams struct {
	// Kind is country or asn.
	Kind string `json:"kind"`
	// BaselineDays is how far back a device's traffic tells what it usually
	// talks to.
	BaselineDays int `json:"baseline_days"`
	// MACs limits the rule to these devices; empty is every device.
	MACs []string `json:"macs,omitempty"`
}

// BlocklistParams are the settings of a blocklist rule.
type BlocklistParams struct {
	// Lists are the IDs of the lists to check, from blocklists.Lists; empty
	// is all of them.
	Lists []string `json:"lists,omitempty"`
	// MACs limits the rule to these devices; empty is every device.
	MACs []string `json:"macs,omitempty"`
}

// lists returns the lists a rule checks.
func (p BlocklistParams) lists() []string {
	if len(p.Lists) > 0 {
		return p.Lists
	}
	ids := make([]string, 0, len(blocklists.Lists))
	for _, l := range blocklists.Lists {
		ids = append(ids, l.ID)
	}
	return ids
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

	case DeviceInactive:
		var p InactiveParams
		if err := decodeStrict(raw, &p); err != nil {
			return "", err
		}
		if err := p.validate(); err != nil {
			return "", err
		}
		params = p

	case DestinationPort:
		var p PortParams
		if err := decodeStrict(raw, &p); err != nil {
			return "", err
		}
		if err := p.validate(); err != nil {
			return "", err
		}
		params = p

	case DestinationCountry:
		var p CountryParams
		if err := decodeStrict(raw, &p); err != nil {
			return "", err
		}
		if err := p.validate(); err != nil {
			return "", err
		}
		params = p

	case DeviceOnline:
		var p OnlineParams
		if err := decodeStrict(raw, &p); err != nil {
			return "", err
		}
		if p.InactiveSeconds < MinInactiveSeconds || p.InactiveSeconds > MaxInactiveSeconds {
			return "", invalid("inactive_s must be between %d and %d seconds", MinInactiveSeconds, MaxInactiveSeconds)
		}
		macs, err := normalizeMACs(p.MACs)
		if err != nil {
			return "", err
		}
		p.MACs = macs
		params = p

	case Blocklist:
		var p BlocklistParams
		if err := decodeStrict(raw, &p); err != nil {
			return "", err
		}
		for _, id := range p.Lists {
			if !blocklists.IsList(id) {
				return "", invalid("unknown blocklist %q", id)
			}
		}
		slices.Sort(p.Lists)
		p.Lists = slices.Compact(p.Lists)
		macs, err := normalizeMACs(p.MACs)
		if err != nil {
			return "", err
		}
		p.MACs = macs
		params = p

	case NewDestination:
		var p DestinationParams
		if err := decodeStrict(raw, &p); err != nil {
			return "", err
		}
		if p.Kind != ByCountry && p.Kind != ByASN {
			return "", invalid("kind must be country or asn")
		}
		if p.BaselineDays == 0 {
			p.BaselineDays = DefaultBaselineDays
		}
		if p.BaselineDays < MinBaselineDays || p.BaselineDays > MaxBaselineDays {
			return "", invalid("baseline_days must be between %d and %d", MinBaselineDays, MaxBaselineDays)
		}
		macs, err := normalizeMACs(p.MACs)
		if err != nil {
			return "", err
		}
		p.MACs = macs
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
	macs, err := normalizeMACs(p.MACs)
	p.MACs = macs
	return err
}

func (p *InactiveParams) validate() error {
	if p.InactiveSeconds < MinInactiveSeconds || p.InactiveSeconds > MaxInactiveSeconds {
		return invalid("inactive_s must be between %d and %d seconds", MinInactiveSeconds, MaxInactiveSeconds)
	}
	macs, err := normalizeMACs(p.MACs)
	if err != nil {
		return err
	}
	if len(macs) == 0 {
		return invalid("macs must name at least one device to watch")
	}
	p.MACs = macs
	return nil
}

func (p *PortParams) validate() error {
	if !slices.Contains([]string{TCP, UDP, Any}, p.Protocol) {
		return invalid("protocol must be tcp, udp or any")
	}
	if len(p.Ports) == 0 || len(p.Ports) > maxPorts {
		return invalid("ports must list 1 to %d ports", maxPorts)
	}
	for _, port := range p.Ports {
		if port < 1 || port > 65535 {
			return invalid("invalid port %d", port)
		}
	}
	slices.Sort(p.Ports)
	p.Ports = slices.Compact(p.Ports)

	macs, err := normalizeMACs(p.MACs)
	p.MACs = macs
	return err
}

func (p *CountryParams) validate() error {
	if p.Mode != Deny && p.Mode != Allow {
		return invalid("mode must be deny or allow")
	}
	if len(p.Countries) == 0 || len(p.Countries) > 300 {
		return invalid("countries must list 1 to 300 country codes")
	}
	countries := make([]string, 0, len(p.Countries))
	for _, code := range p.Countries {
		code = strings.ToUpper(strings.TrimSpace(code))
		if len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
			return invalid("invalid country code %q; want two letters, such as DE", code)
		}
		countries = append(countries, code)
	}
	slices.Sort(countries)
	p.Countries = slices.Compact(countries)

	macs, err := normalizeMACs(p.MACs)
	p.MACs = macs
	return err
}

// normalizeMACs parses MAC addresses into canonical form, sorted and
// without duplicates.
func normalizeMACs(list []string) ([]string, error) {
	if len(list) > maxScopedDevices {
		return nil, invalid("a rule can be limited to at most %d devices", maxScopedDevices)
	}
	macs := make([]string, 0, len(list))
	for _, s := range list {
		mac, err := net.ParseMAC(s)
		if err != nil {
			return nil, invalid("invalid mac address %q", s)
		}
		macs = append(macs, mac.String())
	}
	slices.Sort(macs)
	return slices.Compact(macs), nil
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

// ValidateQuietHours checks the daily hours a rule holds back its
// notifications: both or neither, as "HH:MM", and not the same.
func ValidateQuietHours(from, to string) error {
	if from == "" && to == "" {
		return nil
	}
	f, fromErr := time.Parse("15:04", from)
	t, toErr := time.Parse("15:04", to)
	if fromErr != nil || toErr != nil {
		return invalid("quiet_from and quiet_to must both be times such as 22:00 and 07:00, or both be empty")
	}
	if f.Equal(t) {
		return invalid("quiet hours must end at another time than they start")
	}
	return nil
}

// inQuietHours reports whether t, in its own time zone, is within the quiet
// hours from–to, which may run past midnight.
func inQuietHours(from, to string, t time.Time) bool {
	f, err1 := time.Parse("15:04", from)
	e, err2 := time.Parse("15:04", to)
	if err1 != nil || err2 != nil {
		return false
	}
	minute := t.Hour()*60 + t.Minute()
	start, end := f.Hour()*60+f.Minute(), e.Hour()*60+e.Minute()
	if start < end {
		return minute >= start && minute < end
	}
	return minute >= start || minute < end
}

// IsValidationError reports whether err says a rule is invalid.
func IsValidationError(err error) bool {
	var v *ValidationError
	return errors.As(err, &v)
}
