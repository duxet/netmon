package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolate starts a test in an empty working directory, without any NETMON_
// variables set, so that nothing but what the test sets is read.
func isolate(t *testing.T) string {
	t.Helper()

	for _, setting := range settings() {
		t.Setenv(EnvPrefix+strings.ToUpper(setting), "")
	}
	t.Setenv(FileEnv, "")

	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

func writeFile(t *testing.T, path, content string) string {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustLoad(t *testing.T, path string) *Config {
	t.Helper()

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q) failed: %v", path, err)
	}
	return cfg
}

func TestLoadDefaults(t *testing.T) {
	isolate(t)

	cfg := mustLoad(t, "")

	want := Defaults()
	if *cfg != want {
		t.Errorf("got %+v, want the defaults %+v", *cfg, want)
	}
	if cfg.File != "" {
		t.Errorf("File = %q, want none", cfg.File)
	}
}

func TestLoadFileOverridesDefaults(t *testing.T) {
	dir := isolate(t)
	path := writeFile(t, filepath.Join(dir, "custom.yaml"), `
listen: 127.0.0.1:8080
database: /srv/netmon/flows.db
geoip_country: ""
local_dns: system
retention_days: 90
public_url: http://router.lan:8080/
interface: en5
ignore_local_traffic: false
`)

	cfg := mustLoad(t, path)

	want := Defaults()
	want.Listen = "127.0.0.1:8080"
	want.Database = "/srv/netmon/flows.db"
	want.GeoIPCountry = ""
	want.LocalDNS = "system"
	want.RetentionDays = 90
	want.PublicURL = "http://router.lan:8080/"
	want.Interface = "en5"
	want.IgnoreLocalTraffic = false
	want.File = path
	if *cfg != want {
		t.Errorf("got %+v, want %+v", *cfg, want)
	}
	if got := cfg.Retention(); got != 90*24*time.Hour {
		t.Errorf("Retention() = %v, want 90 days", got)
	}
	if got := cfg.AlertRetention(); got != 30*24*time.Hour {
		t.Errorf("AlertRetention() = %v, want the default 30 days", got)
	}
}

func TestLoadEnvOverridesFile(t *testing.T) {
	dir := isolate(t)
	path := writeFile(t, filepath.Join(dir, "custom.yaml"), `
local_dns: 10.0.0.1:53
public_dns: 1.1.1.1:53
retention_days: 90
`)
	t.Setenv("NETMON_LOCAL_DNS", "192.168.0.1")
	t.Setenv("NETMON_RETENTION_DAYS", "7")
	t.Setenv("NETMON_ALERT_RETENTION_DAYS", "0")

	cfg := mustLoad(t, path)

	if cfg.LocalDNS != "192.168.0.1" {
		t.Errorf("LocalDNS = %q, want the environment's", cfg.LocalDNS)
	}
	if cfg.PublicDNS != "1.1.1.1:53" {
		t.Errorf("PublicDNS = %q, want the file's", cfg.PublicDNS)
	}
	if cfg.RetentionDays != 7 {
		t.Errorf("RetentionDays = %d, want the environment's 7", cfg.RetentionDays)
	}
	if cfg.AlertRetentionDays != 0 {
		t.Errorf("AlertRetentionDays = %d, want 0 from the environment, not the default", cfg.AlertRetentionDays)
	}
}

func TestLoadYesOrNoFromEnv(t *testing.T) {
	dir := isolate(t)
	path := writeFile(t, filepath.Join(dir, "custom.yaml"), "ignore_local_traffic: true\n")

	for value, want := range map[string]bool{"false": false, "0": false, "true": true, "1": true} {
		t.Setenv("NETMON_IGNORE_LOCAL_TRAFFIC", value)
		if cfg := mustLoad(t, path); cfg.IgnoreLocalTraffic != want {
			t.Errorf("NETMON_IGNORE_LOCAL_TRAFFIC=%s: IgnoreLocalTraffic = %v, want %v", value, cfg.IgnoreLocalTraffic, want)
		}
	}

	t.Setenv("NETMON_IGNORE_LOCAL_TRAFFIC", "maybe")
	if _, err := Load(path); err == nil {
		t.Error("NETMON_IGNORE_LOCAL_TRAFFIC=maybe: want an error")
	}
}

func TestLoadEnvWithoutFile(t *testing.T) {
	isolate(t)
	t.Setenv("NETMON_LISTEN", ":9000")
	t.Setenv("NETMON_INTERFACE", "eth1")
	t.Setenv("NETMON_PUBLIC_URL", "http://netmon.lan:9000")

	cfg := mustLoad(t, "")

	if cfg.Listen != ":9000" || cfg.Interface != "eth1" || cfg.PublicURL != "http://netmon.lan:9000" {
		t.Errorf("got %+v, want the environment's settings", *cfg)
	}
}

func TestLoadIgnoresEmptyEnv(t *testing.T) {
	dir := isolate(t)
	path := writeFile(t, filepath.Join(dir, "custom.yaml"), "public_dns: 1.1.1.1:53\n")
	t.Setenv("NETMON_PUBLIC_DNS", "")
	t.Setenv("NETMON_LOCAL_DNS", "")

	cfg := mustLoad(t, path)

	if cfg.PublicDNS != "1.1.1.1:53" {
		t.Errorf("PublicDNS = %q, want the file's", cfg.PublicDNS)
	}
	if cfg.LocalDNS != Defaults().LocalDNS {
		t.Errorf("LocalDNS = %q, want the default", cfg.LocalDNS)
	}
}

func TestLoadIgnoresUnknownEnv(t *testing.T) {
	isolate(t)
	t.Setenv("NETMON_SOMETHING_ELSE", "1")

	mustLoad(t, "")
}

func TestLoadDefaultFile(t *testing.T) {
	dir := isolate(t)
	writeFile(t, filepath.Join(dir, DefaultFile), "retention_days: 14\n")

	cfg := mustLoad(t, "")

	if cfg.RetentionDays != 14 {
		t.Errorf("RetentionDays = %d, want 14 from %s", cfg.RetentionDays, DefaultFile)
	}
	if cfg.File != DefaultFile {
		t.Errorf("File = %q, want %q", cfg.File, DefaultFile)
	}
}

func TestLoadFileFromEnv(t *testing.T) {
	dir := isolate(t)
	writeFile(t, filepath.Join(dir, DefaultFile), "retention_days: 14\n")
	other := writeFile(t, filepath.Join(dir, "other.yaml"), "retention_days: 21\n")
	t.Setenv(FileEnv, other)

	cfg := mustLoad(t, "")

	if cfg.RetentionDays != 21 || cfg.File != other {
		t.Errorf("got retention %d from %q, want 21 from %q", cfg.RetentionDays, cfg.File, other)
	}
}

func TestLoadPathOverridesFileEnv(t *testing.T) {
	dir := isolate(t)
	fromEnv := writeFile(t, filepath.Join(dir, "env.yaml"), "retention_days: 21\n")
	fromPath := writeFile(t, filepath.Join(dir, "path.yaml"), "retention_days: 28\n")
	t.Setenv(FileEnv, fromEnv)

	cfg := mustLoad(t, fromPath)

	if cfg.RetentionDays != 28 {
		t.Errorf("RetentionDays = %d, want 28 from the path", cfg.RetentionDays)
	}
}

func TestLoadEmptyFile(t *testing.T) {
	dir := isolate(t)
	path := writeFile(t, filepath.Join(dir, "empty.yaml"), "# nothing set\n")

	cfg := mustLoad(t, path)

	want := Defaults()
	want.File = path
	if *cfg != want {
		t.Errorf("got %+v, want %+v", *cfg, want)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name string
		file string // content of the file, or "-" for a file that doesn't exist
		env  map[string]string
		want string
	}{
		{name: "missing file", file: "-", want: "no such file"},
		{name: "invalid YAML", file: "listen: [", want: "reading"},
		{name: "unknown setting", file: "retention: 3\n", want: "unknown settings: retention"},
		{name: "nested unknown setting", file: "dns:\n  local: system\n", want: "unknown settings: dns.local"},
		{name: "retention not a number", file: "retention_days: forever\n", want: "retention_days"},
		{name: "env retention not a number", env: map[string]string{"NETMON_RETENTION_DAYS": "a lot"}, want: "retention_days"},
		{name: "negative retention", file: "retention_days: -1\n", want: "retention_days must be"},
		{name: "negative alert retention", env: map[string]string{"NETMON_ALERT_RETENTION_DAYS": "-1"}, want: "alert_retention_days must be"},
		{name: "listen without a port", file: "listen: localhost\n", want: "listen must be"},
		{name: "empty database", file: "database: \"\"\n", want: "database must be"},

		// Single sign-on must be all there or not at all: a setting that is
		// silently ignored would leave netmon open to everyone.
		{name: "client id without issuer", file: "oidc_client_id: netmon\n", want: "have no effect without oidc_issuer"},
		{name: "client secret without issuer", env: map[string]string{"NETMON_OIDC_CLIENT_SECRET": "s3cret"}, want: "have no effect without oidc_issuer"},
		{name: "issuer without client id", file: "oidc_issuer: https://id.example.com\npublic_url: https://netmon.example.com\n", want: "oidc_client_id is required"},
		{name: "issuer over http", file: "oidc_issuer: http://id.example.com\noidc_client_id: netmon\npublic_url: https://netmon.example.com\n", want: "oidc_issuer must be an https://"},
		{name: "issuer without a scheme", file: "oidc_issuer: id.example.com\noidc_client_id: netmon\npublic_url: https://netmon.example.com\n", want: "oidc_issuer must be an https://"},
		{name: "issuer without public url", file: "oidc_issuer: https://id.example.com\noidc_client_id: netmon\n", want: "public_url is required"},
		{name: "public url without a scheme", file: "oidc_issuer: https://id.example.com\noidc_client_id: netmon\npublic_url: netmon.example.com\n", want: "public_url must be an http://"},
		{name: "public url with a path", file: "oidc_issuer: https://id.example.com\noidc_client_id: netmon\npublic_url: https://example.com/netmon\n", want: "public_url must be an address without a path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := isolate(t)
			for name, value := range tt.env {
				t.Setenv(name, value)
			}

			path := ""
			switch tt.file {
			case "":
			case "-":
				path = filepath.Join(dir, "missing.yaml")
			default:
				path = writeFile(t, filepath.Join(dir, "netmon.yaml"), tt.file)
			}

			cfg, err := Load(path)
			if err == nil {
				t.Fatalf("Load succeeded with %+v, want an error mentioning %q", *cfg, tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q doesn't mention %q", err, tt.want)
			}
		})
	}
}

func TestLoadOIDC(t *testing.T) {
	dir := isolate(t)
	path := writeFile(t, filepath.Join(dir, "custom.yaml"), `
public_url: https://netmon.example.com/
oidc_issuer: https://id.example.com
oidc_client_id: netmon
`)
	// The secret is the one setting that doesn't belong in a file others can read.
	t.Setenv("NETMON_OIDC_CLIENT_SECRET", "s3cret")

	cfg := mustLoad(t, path)

	if !cfg.OIDCEnabled() {
		t.Error("OIDCEnabled() = false, want true with an issuer")
	}
	if cfg.OIDCIssuer != "https://id.example.com" || cfg.OIDCClientID != "netmon" || cfg.OIDCClientSecret != "s3cret" {
		t.Errorf("got issuer %q, client %q, secret %q", cfg.OIDCIssuer, cfg.OIDCClientID, cfg.OIDCClientSecret)
	}
}

func TestLoadOIDCPublicClientAndLocalhost(t *testing.T) {
	isolate(t)
	// A public client has no secret, and a provider on this machine may be http.
	t.Setenv("NETMON_PUBLIC_URL", "http://netmon.lan:2137")
	t.Setenv("NETMON_OIDC_CLIENT_ID", "netmon")

	for _, issuer := range []string{"http://localhost:1411", "http://127.0.0.1:1411", "http://[::1]:1411"} {
		t.Setenv("NETMON_OIDC_ISSUER", issuer)
		cfg := mustLoad(t, "")
		if !cfg.OIDCEnabled() || cfg.OIDCClientSecret != "" {
			t.Errorf("issuer %q: got %+v, want single sign-on without a secret", issuer, *cfg)
		}
	}
}

func TestOIDCDisabledByDefault(t *testing.T) {
	isolate(t)

	if mustLoad(t, "").OIDCEnabled() {
		t.Error("OIDCEnabled() = true without an issuer")
	}
}

func TestLoadMissingFileFromEnv(t *testing.T) {
	dir := isolate(t)
	t.Setenv(FileEnv, filepath.Join(dir, "missing.yaml"))

	if _, err := Load(""); err == nil {
		t.Error("Load succeeded, want an error for the file NETMON_CONFIG names")
	}
}

func TestBaseURL(t *testing.T) {
	cfg := Defaults()
	cfg.PublicURL = "http://router.lan:2137/"
	if got := cfg.BaseURL(); got != "http://router.lan:2137" {
		t.Errorf("BaseURL() = %q, want the public URL without its slash", got)
	}

	hostname, err := os.Hostname()
	if err != nil {
		t.Skip("no hostname:", err)
	}

	cfg = Defaults()
	if got, want := cfg.BaseURL(), "http://"+hostname+":2137"; got != want {
		t.Errorf("BaseURL() = %q, want %q", got, want)
	}

	cfg.Listen = "192.168.1.1:8080"
	if got, want := cfg.BaseURL(), "http://"+hostname+":8080"; got != want {
		t.Errorf("BaseURL() = %q, want the port of the listen address: %q", got, want)
	}
}

func TestSettings(t *testing.T) {
	// The tags are what the file and the environment are matched against, so
	// a renamed one changes what people have configured.
	want := "listen database geoip_country geoip_asn local_dns public_dns retention_days alert_retention_days public_url oidc_issuer oidc_client_id oidc_client_secret interface ignore_local_traffic"
	if got := strings.Join(settings(), " "); got != want {
		t.Errorf("settings are %q, want %q", got, want)
	}
}
