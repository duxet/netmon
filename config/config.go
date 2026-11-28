// Package config loads netmon's settings.
//
// Settings come from three places, each overriding the one before it:
//
//  1. the defaults in [Defaults],
//  2. a YAML file, if there is one (see [Load]),
//  3. NETMON_* environment variables, such as NETMON_RETENTION_DAYS for the
//     retention_days setting.
//
// Every setting is a top-level key whose environment variable is its name in
// upper case with the NETMON_ prefix.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/netip"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/duxet/netmon/logging"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/v2"

	"github.com/duxet/netmon/unifi"
)

const (
	// EnvPrefix starts the name of every environment variable that sets a
	// setting.
	EnvPrefix = "NETMON_"

	// FileEnv names the environment variable that points to the YAML file.
	FileEnv = EnvPrefix + "CONFIG"

	// DefaultFile is the YAML file looked for in the working directory when
	// no other one is given. It is optional, unlike a file that is asked for.
	DefaultFile = "netmon.yaml"
)

// Config is netmon's configuration. The koanf tag of a field is its key in the
// YAML file.
type Config struct {
	// Listen is the address the API and web UI are served on, as "host:port"
	// or ":port".
	Listen string `koanf:"listen"`

	// Database is the path of the database file. It is created if needed.
	Database string `koanf:"database"`

	// GeoIPCountry is the path of the GeoLite2-Country database. Without the
	// file, or when it is empty, flows have no country.
	GeoIPCountry string `koanf:"geoip_country"`

	// GeoIPASN is the path of the GeoLite2-ASN database. Without the file, or
	// when it is empty, remote hosts have no autonomous system and apps are
	// recognized by their built-in prefixes alone.
	GeoIPASN string `koanf:"geoip_asn"`

	// LocalDNS and PublicDNS are the servers that reverse lookups of local and
	// public addresses go to: "host", "host:port" or "system" for the
	// operating system's resolver.
	LocalDNS  string `koanf:"local_dns"`
	PublicDNS string `koanf:"public_dns"`

	// RetentionDays is how long hourly flow data is kept, with 0 keeping it
	// forever. AlertRetentionDays is the same for resolved alerts.
	RetentionDays      int `koanf:"retention_days"`
	AlertRetentionDays int `koanf:"alert_retention_days"`

	// HourlyDays is how long traffic is kept per hour: older hours are summed
	// up per day. 0 keeps every hour.
	HourlyDays int `koanf:"hourly_days"`

	// PublicURL is where people reach the web UI, which notifications link
	// to. By default it is the machine's hostname on the Listen port, see
	// [Config.BaseURL].
	PublicURL string `koanf:"public_url"`

	// OIDCIssuer turns on single sign-on with an OpenID Connect provider such
	// as Pocket ID: the provider's issuer URL, which has to be written as the
	// provider does, since ID tokens are matched against it. Without it netmon
	// has no authentication. OIDCClientID is required with it; OIDCClientSecret
	// is empty for a public client, which authenticates with PKCE alone.
	OIDCIssuer       string `koanf:"oidc_issuer"`
	OIDCClientID     string `koanf:"oidc_client_id"`
	OIDCClientSecret string `koanf:"oidc_client_secret"`

	// UniFiURL turns on reading clients from a UniFi Network Application: the
	// address of the console or of the self-hosted application, such as
	// https://192.168.1.1. It needs UniFiAPIKey, made in UniFi Network under
	// Settings, Control Plane, Integrations, for the Integration API, or else
	// UniFiUsername and UniFiPassword of a local account, to sign in as the
	// web UI does, which works with every Network Application. UniFiSite is
	// the site's name or internal reference. UniFiFingerprint is the SHA-256
	// fingerprint of the controller's certificate, to trust a self-signed one,
	// as UniFi's are.
	UniFiURL         string `koanf:"unifi_url"`
	UniFiAPIKey      string `koanf:"unifi_api_key"`
	UniFiUsername    string `koanf:"unifi_username"`
	UniFiPassword    string `koanf:"unifi_password"`
	UniFiSite        string `koanf:"unifi_site"`
	UniFiFingerprint string `koanf:"unifi_fingerprint"`

	// Interface is the network interface to capture on, on macOS. By default
	// it is the one the default route goes through. Linux doesn't capture
	// packets, so ignores it.
	Interface string `koanf:"interface"`

	// IgnoreLocalTraffic leaves out traffic between two local addresses, such
	// as a laptop's backups to a NAS, so that only traffic to and from the
	// internet is recorded. Local addresses are those in the private ranges,
	// along with loopback, link-local, multicast and broadcast ones, and those
	// in the blocks of the networks set up in the web UI.
	IgnoreLocalTraffic bool `koanf:"ignore_local_traffic"`

	// LogLevel is the least severe kind of message that is logged: debug,
	// info, warn or error. At debug, every request is logged.
	LogLevel string `koanf:"log_level"`

	// MemoryLimit bounds the memory the database uses, such as 256MB, 1GiB
	// or 50% (of the machine's). Empty is DuckDB's default, 80%.
	MemoryLimit string `koanf:"memory_limit"`

	// Threads is how many threads the database runs queries on, with 0
	// using every core.
	Threads int `koanf:"threads"`

	// DNSLog is dnsmasq's log, with log-queries on, to learn from the names
	// clients look up which names the addresses they then talk to are for:
	// a file, or logread for OpenWrt's system log. Empty learns none (but on
	// macOS, where they come from the packets captured).
	DNSLog string `koanf:"dns_log"`

	// DHCPLeases are the lease files of the DHCP server, of dnsmasq, ISC
	// dhcpd, Kea or macOS, to name devices by the names they asked for:
	// paths separated by commas, "auto" for the first of the usual places
	// there is, or empty (or "off") for none.
	DHCPLeases string `koanf:"dhcp_leases"`

	// MDNS names devices by the names they announce over multicast DNS, such
	// as Johns-MacBook.local, asking them now and then to.
	MDNS bool `koanf:"mdns"`

	// MQTTBroker turns on publishing devices to Home Assistant over MQTT:
	// the broker, such as tcp://homeassistant.lan:1883 or ssl://broker:8883.
	// MQTTUsername and MQTTPassword log in to it.
	MQTTBroker   string `koanf:"mqtt_broker"`
	MQTTUsername string `koanf:"mqtt_username"`
	MQTTPassword string `koanf:"mqtt_password"`
	// MQTTTopic is where netmon publishes, and MQTTDiscoveryPrefix where
	// Home Assistant looks for devices to set up.
	MQTTTopic           string `koanf:"mqtt_topic"`
	MQTTDiscoveryPrefix string `koanf:"mqtt_discovery_prefix"`
	// MQTTAllDevices publishes every device seen in the last day, instead
	// of only the ones given a name or an icon.
	MQTTAllDevices bool `koanf:"mqtt_all_devices"`

	// AllowedHosts are more names, separated by commas, that netmon answers
	// to besides addresses, names on the local network and the public URL's,
	// such as netmon.example.com; "*" allows any.
	AllowedHosts string `koanf:"allowed_hosts"`

	// File is the YAML file the configuration was read from, empty when there
	// was none.
	File string `koanf:"-"`
}

// Defaults returns the configuration without a file or environment variables.
func Defaults() Config {
	return Config{
		Listen:              ":2137",
		Database:            "flows.db",
		GeoIPCountry:        "GeoLite2-Country.mmdb",
		GeoIPASN:            "GeoLite2-ASN.mmdb",
		LocalDNS:            "192.168.1.1:54",
		PublicDNS:           "8.8.8.8:53",
		AlertRetentionDays:  30,
		HourlyDays:          90,
		IgnoreLocalTraffic:  true,
		DHCPLeases:          "auto",
		MDNS:                true,
		LogLevel:            "info",
		MQTTTopic:           "netmon",
		MQTTDiscoveryPrefix: "homeassistant",
		UniFiSite:           "default",
	}
}

// Retention is how long hourly flow data is kept, or 0 for forever.
func (c Config) Retention() time.Duration {
	return time.Duration(c.RetentionDays) * 24 * time.Hour
}

// HourlyRetention is how long traffic is kept per hour before it is summed up
// per day, or 0 for forever.
func (c Config) HourlyRetention() time.Duration {
	return time.Duration(c.HourlyDays) * 24 * time.Hour
}

// AlertRetention is how long resolved alerts are kept, or 0 for forever.
func (c Config) AlertRetention() time.Duration {
	return time.Duration(c.AlertRetentionDays) * 24 * time.Hour
}

// BaseURL is where people reach the web UI, without a trailing slash: the
// public URL, or else the machine's hostname on the port netmon listens on.
// It is empty when neither is known.
func (c Config) BaseURL() string {
	if c.PublicURL != "" {
		return strings.TrimSuffix(c.PublicURL, "/")
	}

	hostname, err := os.Hostname()
	if err != nil {
		return ""
	}
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return ""
	}

	return "http://" + net.JoinHostPort(hostname, port)
}

// AllowedHostList returns AllowedHosts as a list of lowercase names.
func (c Config) AllowedHostList() []string {
	var hosts []string
	for _, host := range strings.FieldsFunc(c.AllowedHosts, func(r rune) bool { return r == ',' || r == ' ' }) {
		hosts = append(hosts, strings.ToLower(strings.TrimSuffix(host, ".")))
	}
	return hosts
}

// OIDCEnabled reports whether people have to sign in with an OpenID Connect
// provider.
func (c Config) OIDCEnabled() bool {
	return c.OIDCIssuer != ""
}

// UniFiEnabled reports whether clients are read from a UniFi Network
// Application.
func (c Config) UniFiEnabled() bool {
	return c.UniFiURL != ""
}

// validateUniFi checks the UniFi settings.
func (c Config) validateUniFi() error {
	if !c.UniFiEnabled() {
		if c.UniFiAPIKey != "" || c.UniFiUsername != "" || c.UniFiPassword != "" || c.UniFiFingerprint != "" {
			return keyErrorf("unifi_url", "unifi_api_key, unifi_username, unifi_password and unifi_fingerprint "+
				"have no effect without unifi_url: set the URL to read clients from UniFi, or remove them")
		}
		return nil
	}
	if _, err := unifi.ParseURL(c.UniFiURL); err != nil {
		return keyErrorf("unifi_url", "unifi_url must be an https:// URL, such as https://192.168.1.1: %q", c.UniFiURL)
	}
	login := c.UniFiUsername != "" || c.UniFiPassword != ""
	switch {
	case c.UniFiAPIKey != "" && login:
		return keyErrorf("unifi_api_key", "use either unifi_api_key, or unifi_username and unifi_password, not both")
	case login && c.UniFiUsername == "":
		return keyErrorf("unifi_username", "unifi_username is required with unifi_password")
	case login && c.UniFiPassword == "":
		return keyErrorf("unifi_password", "unifi_password is required with unifi_username")
	case !login && c.UniFiAPIKey == "":
		return keyErrorf("unifi_username", "unifi_url needs unifi_username and unifi_password of a local UniFi account, "+
			"or unifi_api_key, made in UniFi Network under Settings, Control Plane, Integrations")
	}
	if c.UniFiSite == "" {
		return keyErrorf("unifi_site", "unifi_site must be the name of a site, such as default")
	}
	if c.UniFiFingerprint != "" {
		if _, err := unifi.ParseFingerprint(c.UniFiFingerprint); err != nil {
			return keyErrorf("unifi_fingerprint", "%w", err)
		}
	}
	return nil
}

// validateOIDC checks the single sign-on settings. A half-configured setup is
// an error rather than a way to leave netmon open without anyone noticing.
func (c Config) validateOIDC() error {
	if !c.OIDCEnabled() {
		if c.OIDCClientID != "" || c.OIDCClientSecret != "" {
			return errors.New("oidc_client_id and oidc_client_secret have no effect without oidc_issuer: " +
				"set the issuer to turn single sign-on on, or remove them")
		}
		return nil
	}

	// Discovery and the keys that sign ID tokens come over this URL, so it must
	// not be readable or changeable on the way, except on this machine.
	issuer, err := url.Parse(c.OIDCIssuer)
	if err != nil || issuer.Host == "" || (issuer.Scheme != "https" && !(issuer.Scheme == "http" && isLoopback(issuer.Hostname()))) {
		return fmt.Errorf("oidc_issuer must be an https:// URL (http:// only for localhost): %q", c.OIDCIssuer)
	}
	if c.OIDCClientID == "" {
		return errors.New("oidc_client_id is required with oidc_issuer")
	}

	// The address people sign in at is what the provider sends them back to, so
	// guessing it from the hostname would only work by accident.
	if c.PublicURL == "" {
		return errors.New("public_url is required with oidc_issuer: the provider sends people back to <public_url>/auth/callback")
	}
	public, err := url.Parse(c.PublicURL)
	if err != nil || public.Host == "" || (public.Scheme != "http" && public.Scheme != "https") {
		return fmt.Errorf("public_url must be an http:// or https:// URL: %q", c.PublicURL)
	}
	if (public.Path != "" && public.Path != "/") || public.RawQuery != "" || public.Fragment != "" {
		return fmt.Errorf("public_url must be an address without a path, such as https://netmon.example.com: %q", c.PublicURL)
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.IsLoopback()
}

// keyError is a problem with one setting's value.
type keyError struct {
	key string
	err error
}

func (e *keyError) Error() string { return e.err.Error() }
func (e *keyError) Unwrap() error { return e.err }

func keyErrorf(key, format string, args ...any) error {
	return &keyError{key: key, err: fmt.Errorf(format, args...)}
}

// memoryLimitPattern matches the amounts of memory DuckDB takes, such as
// 256MB, 1.5 GiB or 50%.
var memoryLimitPattern = regexp.MustCompile(`(?i)^\s*\d+(\.\d+)?\s*(b|kb|mb|gb|tb|kib|mib|gib|tib|%)\s*$`)

// hostnamePattern matches DNS names such as dns.lan or router.
var hostnamePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*\.?$`)

// validateDNS checks a DNS server setting: "host", "host:port", or "system"
// (or empty) for the operating system's resolver.
func validateDNS(key, value string) error {
	if value == "" || value == "system" {
		return nil
	}

	host, port, err := net.SplitHostPort(value)
	hasPort := err == nil
	if !hasPort {
		host = value
	}
	_, ipErr := netip.ParseAddr(host)
	valid := ipErr == nil || hostnamePattern.MatchString(host)
	if hasPort {
		n, err := strconv.Atoi(port)
		valid = valid && err == nil && n >= 1 && n <= 65535
	}
	if !valid {
		return keyErrorf(key, "%s must be host, host:port or system, such as 192.168.1.1 or 192.168.1.1:53: %q", key, value)
	}
	return nil
}

// validate checks the settings that can be wrong in a way that would only
// show up later, or not at all.
func (c Config) validate() error {
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return keyErrorf("listen", "listen must be an address like :2137 or 127.0.0.1:2137: %w", err)
	}
	if c.Database == "" {
		return keyErrorf("database", "database must be the path of a file")
	}
	if err := validateDNS("local_dns", c.LocalDNS); err != nil {
		return err
	}
	if err := validateDNS("public_dns", c.PublicDNS); err != nil {
		return err
	}
	// Notifications link to it, which only works with the scheme and host.
	if c.PublicURL != "" {
		public, err := url.Parse(c.PublicURL)
		if err != nil || public.Host == "" || (public.Scheme != "http" && public.Scheme != "https") {
			return keyErrorf("public_url", "public_url must be an http:// or https:// URL, such as http://router.lan:2137: %q", c.PublicURL)
		}
	}
	if c.RetentionDays < 0 {
		return keyErrorf("retention_days", "retention_days must be a number of days, with 0 keeping everything: %d", c.RetentionDays)
	}
	if c.HourlyDays < 0 {
		return keyErrorf("hourly_days", "hourly_days must be a number of days, with 0 keeping every hour: %d", c.HourlyDays)
	}
	if c.AlertRetentionDays < 0 {
		return keyErrorf("alert_retention_days", "alert_retention_days must be a number of days, with 0 keeping everything: %d", c.AlertRetentionDays)
	}
	if c.MemoryLimit != "" && !memoryLimitPattern.MatchString(c.MemoryLimit) {
		return keyErrorf("memory_limit", "memory_limit must be an amount such as 256MB or 1GiB, or a share of the memory such as 50%%: %q", c.MemoryLimit)
	}
	if c.Threads < 0 {
		return keyErrorf("threads", "threads must be a number of threads, with 0 using every core: %d", c.Threads)
	}
	if c.MQTTBroker != "" {
		broker, err := url.Parse(c.MQTTBroker)
		if err != nil || broker.Host == "" || !slices.Contains([]string{"tcp", "ssl", "tls", "mqtt", "mqtts", "ws", "wss"}, broker.Scheme) {
			return keyErrorf("mqtt_broker", "mqtt_broker must be an address such as tcp://homeassistant.lan:1883 or ssl://broker:8883: %q", c.MQTTBroker)
		}
	}
	for key, topic := range map[string]string{"mqtt_topic": c.MQTTTopic, "mqtt_discovery_prefix": c.MQTTDiscoveryPrefix} {
		if topic == "" || strings.ContainsAny(topic, "+#") || strings.HasPrefix(topic, "/") || strings.HasSuffix(topic, "/") {
			return keyErrorf(key, "%s must be a topic such as netmon, without + or #, or a / at either end: %q", key, topic)
		}
	}
	for _, host := range c.AllowedHostList() {
		if host != "*" && !hostnamePattern.MatchString(host) {
			return keyErrorf("allowed_hosts", "allowed_hosts must be names such as netmon.example.com, separated by commas, or *: %q", host)
		}
	}
	if _, ok := logging.Parse(c.LogLevel); !ok {
		return keyErrorf("log_level", "log_level must be one of %s: %q", strings.Join(logging.Levels, ", "), c.LogLevel)
	}
	if err := c.validateUniFi(); err != nil {
		return err
	}
	return c.validateOIDC()
}

// Load reads the configuration.
//
// The YAML file is path, or when that is empty the one NETMON_CONFIG names.
// A file that is asked for in either way has to exist. Without one, netmon.yaml
// in the working directory is read if there is such a file. Environment
// variables override what the file sets.
//
// A key in the file that isn't a setting is an error, so that a typo doesn't
// go unnoticed. Environment variables that aren't settings are ignored, as
// are empty ones.
func Load(path string) (*Config, error) {
	path, required := resolve(path)
	data, exists, err := readFile(path, required)
	if err != nil {
		return nil, err
	}
	return parse(path, data, exists)
}

// resolve returns the YAML file Load reads for path, and whether it has to
// exist.
func resolve(path string) (string, bool) {
	if path != "" {
		return path, true
	}
	if path = os.Getenv(FileEnv); path != "" {
		return path, true
	}
	return DefaultFile, false
}

// readFile returns the contents of the YAML file at path, and whether there
// is one. A missing file is only an error when it is required.
func readFile(path string, required bool) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) && !required {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", path, err)
	}
	return data, true, nil
}

// parse reads the configuration from the contents of the YAML file at path,
// when it exists, and the environment, and checks it.
func parse(path string, data []byte, exists bool) (*Config, error) {
	k := koanf.New(".")
	if exists {
		if err := loadFile(k, path, data); err != nil {
			return nil, err
		}
	}

	err := k.Load(env.Provider(".", env.Opt{Prefix: EnvPrefix, TransformFunc: envSetting}), nil)
	if err != nil {
		return nil, fmt.Errorf("reading environment variables: %w", err)
	}

	cfg := Defaults()
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	if exists {
		cfg.File = path
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// fileContents is a koanf provider for a file that was read already.
type fileContents []byte

func (b fileContents) ReadBytes() ([]byte, error) { return b, nil }

func (b fileContents) Read() (map[string]any, error) {
	return nil, errors.New("file contents need a parser")
}

// readSettings parses the contents of the YAML file at path, and checks that
// it only has settings.
func readSettings(path string, data []byte) (*koanf.Koanf, error) {
	fileK := koanf.New(".")
	if err := fileK.Load(fileContents(data), yaml.Parser()); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var unknown []string
	for _, key := range fileK.Keys() {
		if !isSetting(key) {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("%s has unknown settings: %s (known settings: %s)",
			path, strings.Join(unknown, ", "), strings.Join(settings(), ", "))
	}
	return fileK, nil
}

// loadFile merges the settings in the contents of the YAML file at path into k.
func loadFile(k *koanf.Koanf, path string, data []byte) error {
	fileK, err := readSettings(path, data)
	if err != nil {
		return err
	}
	if err := k.Merge(fileK); err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	return nil
}

// envSetting maps the environment variable NETMON_RETENTION_DAYS to the
// setting retention_days. It skips variables that aren't settings, such as
// NETMON_CONFIG, and empty ones, so that an unset option in a service
// definition falls back to the file or the default.
func envSetting(name, value string) (string, any) {
	key := strings.ToLower(strings.TrimPrefix(name, EnvPrefix))
	if value == "" || !isSetting(key) {
		return "", nil
	}
	return key, value
}

func isSetting(key string) bool {
	return slices.Contains(settings(), key)
}

// settings lists the keys of the settings, from the koanf tags of Config.
func settings() []string {
	var keys []string
	for field := range reflect.TypeFor[Config]().Fields() {
		if key := field.Tag.Get("koanf"); key != "" && key != "-" {
			keys = append(keys, key)
		}
	}
	return keys
}
