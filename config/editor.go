package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"strings"
	"sync"
)

// info is what the web UI's configuration page needs to know about a setting.
type info struct {
	// live settings apply as soon as they are saved; the others when netmon
	// starts again.
	live bool
	// locked says why a setting can only be changed in the file, if it can.
	locked string
	// secret settings are never shown, only whether they are set.
	secret bool
}

const ssoLocked = "Single sign-on decides who can use netmon, so it is only set in the file, " +
	"where nobody can turn it off or point it at another provider from the web UI."

const unifiLocked = "netmon sends the API key to this controller, so the UniFi settings are only set in the file, " +
	"where nobody can send the key elsewhere from the web UI."

// infos has an entry for every setting.
var infos = map[string]info{
	"listen":               {locked: "netmon would have to start again on the new address, which could leave the web UI out of reach."},
	"database":             {locked: "A new path would start netmon with an empty database."},
	"geoip_country":        {},
	"geoip_asn":            {},
	"local_dns":            {live: true},
	"public_dns":           {live: true},
	"retention_days":       {live: true},
	"alert_retention_days": {live: true},
	"public_url":           {live: true},
	"oidc_issuer":          {locked: ssoLocked},
	"oidc_client_id":       {locked: ssoLocked},
	"oidc_client_secret":   {locked: ssoLocked, secret: true},
	"unifi_url":            {locked: unifiLocked},
	"unifi_api_key":        {locked: unifiLocked, secret: true},
	"unifi_site":           {locked: unifiLocked},
	"unifi_fingerprint":    {locked: unifiLocked},
	"interface":            {},
	"ignore_local_traffic": {live: true},
}

// InvalidError is a change the editor refuses, such as a value of the wrong
// type or one netmon wouldn't start with.
type InvalidError struct {
	// Key is the setting it is about, when it is about one.
	Key string
	Err error
}

func (e *InvalidError) Error() string { return e.Err.Error() }
func (e *InvalidError) Unwrap() error { return e.Err }

func invalid(key, format string, args ...any) error {
	return &InvalidError{Key: key, Err: fmt.Errorf(format, args...)}
}

// Setting is a setting as the configuration page shows it.
type Setting struct {
	Key string
	// Value is what netmon would start with now: from the environment, the
	// file or the default. Running is what it runs with, which differs from
	// Value until it starts again for settings that don't apply right away.
	Value   any
	Running any
	Default any
	// File is the file's value, when InFile.
	File   any
	InFile bool
	// Env is the environment variable that sets the setting, overriding the
	// file; empty when none does.
	Env string
	// Live settings apply as soon as they are saved.
	Live bool
	// Locked says why the setting can't be changed through the editor; it is
	// empty when it can.
	Locked string
	// Secret settings have no values here, only Set, which tells whether
	// they have one.
	Secret bool
	Set    bool
	// Restart tells whether netmon has to start again to run with Value.
	Restart bool
}

// State is the configuration as the configuration page shows it.
type State struct {
	// File is the YAML file the settings are saved to, and Exists whether
	// there is one yet.
	File   string
	Exists bool
	// DefaultPublicURL is where notifications link to without public_url.
	DefaultPublicURL string
	Settings         []Setting
}

// Editor changes the settings in the YAML file and applies the ones that can
// be to the running netmon. It is safe for concurrent use.
type Editor struct {
	path  string
	apply func(Config)

	mu      sync.Mutex
	running Config
}

// NewEditor returns an editor of the file Load reads for path, for a netmon
// running with running. After each change, apply is called with the
// configuration to run with: running, with the settings that apply right away
// changed. It must not call the editor.
func NewEditor(path string, running Config, apply func(Config)) *Editor {
	path, _ = resolve(path)
	return &Editor{path: path, running: running, apply: apply}
}

// File is the YAML file the editor changes.
func (e *Editor) File() string {
	return e.path
}

// Running returns the configuration netmon runs with.
func (e *Editor) Running() Config {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

// State reads the file and returns every setting as it stands.
func (e *Editor) State() (State, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	data, exists, err := readFile(e.path, false)
	if err != nil {
		return State{}, err
	}
	return e.state(data, exists)
}

// Update changes settings in the file: each to its value, a string, a
// number or a bool as the setting is, or with nil back to its default, by
// taking it out of the file. The whole configuration is checked as netmon would check
// it when starting with it, and nothing is written unless it passes. The
// settings that apply right away then do.
func (e *Editor) Update(changes map[string]any) (State, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(changes) == 0 {
		data, exists, err := readFile(e.path, false)
		if err != nil {
			return State{}, err
		}
		return e.state(data, exists)
	}

	set := map[string]any{}
	var unset []string
	for key, value := range changes {
		if !isSetting(key) {
			return State{}, invalid(key, "there is no setting %s", key)
		}
		if reason := e.locked(key); reason != "" {
			return State{}, invalid(key, "%s can only be changed in the file: %s", key, reason)
		}
		if env := envVar(key); env != "" {
			return State{}, invalid(key, "%s is set by the environment variable %s, which overrides the file", key, env)
		}
		if value == nil {
			unset = append(unset, key)
			continue
		}
		v, err := coerce(key, value)
		if err != nil {
			return State{}, err
		}
		set[key] = v
	}

	data, _, err := readFile(e.path, false)
	if err != nil {
		return State{}, err
	}
	updated, err := editYAML(data, set, unset)
	if err != nil {
		return State{}, &InvalidError{Err: fmt.Errorf("%s: %w", e.path, err)}
	}
	cfg, err := parse(e.path, updated, true)
	if err != nil {
		// A problem with one setting is reported as about it, without the
		// "invalid configuration" of a start.
		if ke := (*keyError)(nil); errors.As(err, &ke) {
			return State{}, &InvalidError{Key: ke.key, Err: ke}
		}
		return State{}, &InvalidError{Err: err}
	}

	if err := replaceFile(e.path, updated); err != nil {
		return State{}, fmt.Errorf("saving %s: %w", e.path, err)
	}

	next := e.running
	for _, key := range settings() {
		if infos[key].live {
			setField(&next, key, field(*cfg, key))
		}
	}
	e.running = next
	if e.apply != nil {
		e.apply(next)
	}

	return e.state(updated, true)
}

// locked says why a setting can't be changed through the editor.
func (e *Editor) locked(key string) string {
	if key == "public_url" && e.running.OIDCEnabled() {
		return "Single sign-on sends people back to this address, so change it in the file, " +
			"along with the callback URL at the provider."
	}
	return infos[key].locked
}

func (e *Editor) state(data []byte, exists bool) (State, error) {
	cfg, err := parse(e.path, data, exists)
	if err != nil {
		return State{}, err
	}

	var inFile map[string]any
	if exists {
		fileK, err := readSettings(e.path, data)
		if err != nil {
			return State{}, err
		}
		inFile = fileK.All()
	}

	fallback := e.running
	fallback.PublicURL = ""
	state := State{File: e.path, Exists: exists, DefaultPublicURL: fallback.BaseURL()}
	defaults := Defaults()
	for _, key := range settings() {
		in := infos[key]
		value, running := field(*cfg, key), field(e.running, key)
		s := Setting{
			Key:     key,
			Env:     envVar(key),
			Live:    in.live,
			Locked:  e.locked(key),
			Secret:  in.secret,
			Restart: value != running,
		}
		fileValue, ok := inFile[key]
		s.InFile = ok
		if in.secret {
			s.Set = value != ""
		} else {
			s.Value, s.Running, s.Default = value, running, field(defaults, key)
			if ok {
				s.File = fileValue
			}
		}
		state.Settings = append(state.Settings, s)
	}
	return state, nil
}

// envVar returns the environment variable that sets a setting, if it is set.
func envVar(key string) string {
	name := EnvPrefix + strings.ToUpper(key)
	if os.Getenv(name) == "" {
		return ""
	}
	return name
}

// fieldOf returns the Config field of a setting.
func fieldOf(key string) reflect.StructField {
	for field := range reflect.TypeFor[Config]().Fields() {
		if field.Tag.Get("koanf") == key {
			return field
		}
	}
	panic("config: no setting " + key)
}

func field(cfg Config, key string) any {
	return reflect.ValueOf(cfg).FieldByIndex(fieldOf(key).Index).Interface()
}

func setField(cfg *Config, key string, value any) {
	reflect.ValueOf(cfg).Elem().FieldByIndex(fieldOf(key).Index).Set(reflect.ValueOf(value))
}

// coerce checks that a value suits a setting, and returns it as the setting's
// type. Numbers may come as float64, as from JSON, if they are whole; yes or
// no settings as true or false.
func coerce(key string, value any) (any, error) {
	switch fieldOf(key).Type.Kind() {
	case reflect.Int:
		switch v := value.(type) {
		case int:
			return v, nil
		case float64:
			if v == math.Trunc(v) && math.Abs(v) <= math.MaxInt32 {
				return int(v), nil
			}
		}
		return nil, invalid(key, "%s must be a whole number", key)
	case reflect.Bool:
		if v, ok := value.(bool); ok {
			return v, nil
		}
		return nil, invalid(key, "%s must be true or false", key)
	case reflect.String:
		if v, ok := value.(string); ok {
			return strings.TrimSpace(v), nil
		}
		return nil, invalid(key, "%s must be text", key)
	default:
		return nil, invalid(key, "%s can't be changed here", key)
	}
}
