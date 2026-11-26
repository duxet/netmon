package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustEdit(t *testing.T, data string, set map[string]any, unset ...string) string {
	t.Helper()
	out, err := editYAML([]byte(data), set, unset)
	if err != nil {
		t.Fatalf("editYAML failed: %v", err)
	}
	return string(out)
}

func TestEditYAMLKeepsTheText(t *testing.T) {
	in := `# netmon configuration

# The router knows the names of DHCP clients.
local_dns: 192.168.1.1:54   # dnsmasq

public_dns: "8.8.8.8:53"
retention_days: 90
# interface: en5
`
	got := mustEdit(t, in,
		map[string]any{"local_dns": "10.0.0.1", "retention_days": 30, "public_url": "http://router.lan:2137"},
		"public_dns")

	want := `# netmon configuration

# The router knows the names of DHCP clients.
local_dns: 10.0.0.1 # dnsmasq

retention_days: 30
# interface: en5
public_url: http://router.lan:2137
`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestEditYAMLQuotesWhereNeeded(t *testing.T) {
	got := mustEdit(t, "", map[string]any{"geoip_country": "", "local_dns": "system", "interface": "123"})
	want := "geoip_country: \"\"\nlocal_dns: system\ninterface: \"123\"\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEditYAMLOfFilesWithoutSettings(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"empty":                   {"", "retention_days: 7\n"},
		"comments only":           {"# local_dns: system\n", "# local_dns: system\nretention_days: 7\n"},
		"without a final newline": {"local_dns: system", "local_dns: system\nretention_days: 7\n"},
		"windows line endings":    {"local_dns: system\r\nretention_days: 1\r\n", "local_dns: system\r\nretention_days: 7\r\n"},
	} {
		if got := mustEdit(t, tc.in, map[string]any{"retention_days": 7}); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

// Values the text can't be changed in place for are written anew, comments
// and all, rather than half-changed.
func TestEditYAMLRewritesWhatItCantEditInPlace(t *testing.T) {
	for name, in := range map[string]string{
		"block scalar":  "# names\nlocal_dns: >-\n  192.168.1.1\nretention_days: 90\n",
		"flow mapping":  "{local_dns: 192.168.1.1, retention_days: 90}\n",
		"empty value":   "local_dns:\nretention_days: 90\n",
		"null document": "~\n",
	} {
		got := mustEdit(t, in, map[string]any{"local_dns": "10.0.0.1"})
		values, err := expected([]byte(got), nil, nil)
		if err != nil || values["local_dns"] != "10.0.0.1" {
			t.Errorf("%s: got %q, want local_dns changed", name, got)
		}
		if strings.Contains(in, "retention_days") && values["retention_days"] != 90 {
			t.Errorf("%s: got %q, want retention_days kept", name, got)
		}
		if strings.HasPrefix(in, "#") && !strings.Contains(got, "# names") {
			t.Errorf("%s: got %q, want the comment kept", name, got)
		}
	}
}

func TestEditYAMLErrors(t *testing.T) {
	for _, in := range []string{"- local_dns\n", "local_dns: [\n", "just text\n"} {
		if out, err := editYAML([]byte(in), map[string]any{"local_dns": "system"}, nil); err == nil {
			t.Errorf("editYAML(%q) = %q, want an error", in, out)
		}
	}
}

func TestEveryKnownSettingHasInfo(t *testing.T) {
	for _, key := range settings() {
		if _, ok := infos[key]; !ok {
			t.Errorf("setting %s has no entry in infos: decide whether the web UI may change it", key)
		}
	}
}

func setting(t *testing.T, state State, key string) Setting {
	t.Helper()
	for _, s := range state.Settings {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("setting %s not in the state", key)
	return Setting{}
}

// newEditor writes the file, loads it as main does and returns an editor
// for it, with the configurations apply was called with.
func newEditor(t *testing.T, content string) (*Editor, string, *[]Config) {
	t.Helper()
	dir := isolate(t)
	path := filepath.Join(dir, "netmon.yaml")
	if content != "" {
		writeFile(t, path, content)
	}
	cfg := mustLoad(t, path)
	var applied []Config
	return NewEditor(path, *cfg, func(c Config) { applied = append(applied, c) }), path, &applied
}

func TestEditorState(t *testing.T) {
	isolate(t)
	t.Setenv("NETMON_PUBLIC_DNS", "1.1.1.1")
	t.Setenv("NETMON_OIDC_CLIENT_SECRET", "s3cret")
	t.Setenv("NETMON_OIDC_ISSUER", "https://id.example.com")
	t.Setenv("NETMON_OIDC_CLIENT_ID", "netmon")
	dir, _ := os.Getwd()
	path := writeFile(t, filepath.Join(dir, DefaultFile), "local_dns: system\npublic_url: https://netmon.example.com\n")
	editor := NewEditor("", *mustLoad(t, ""), nil)

	state, err := editor.State()
	if err != nil {
		t.Fatal(err)
	}
	if state.File != DefaultFile || !state.Exists {
		t.Errorf("file = %q (exists %v), want %s", state.File, state.Exists, path)
	}

	local := setting(t, state, "local_dns")
	if local.Value != "system" || local.File != "system" || !local.InFile || local.Env != "" ||
		local.Default != "192.168.1.1:54" || !local.Live || local.Locked != "" || local.Restart {
		t.Errorf("local_dns = %+v, want system from the file", local)
	}
	if public := setting(t, state, "public_dns"); public.Value != "1.1.1.1" || public.Env != "NETMON_PUBLIC_DNS" || public.InFile {
		t.Errorf("public_dns = %+v, want it from the environment", public)
	}
	if retention := setting(t, state, "retention_days"); retention.Value != 0 || retention.InFile || retention.Env != "" {
		t.Errorf("retention_days = %+v, want the default", retention)
	}

	// Secrets never leave the editor, and single sign-on can't be changed.
	secret := setting(t, state, "oidc_client_secret")
	if secret.Value != nil || secret.Running != nil || secret.Default != nil || !secret.Set || !secret.Secret || secret.Locked == "" {
		t.Errorf("oidc_client_secret = %+v, want only that it is set", secret)
	}
	if setting(t, state, "listen").Locked == "" || setting(t, state, "oidc_issuer").Locked == "" {
		t.Error("listen and oidc_issuer can be changed, want them locked")
	}
	// With single sign-on, the public URL is where the provider sends people back to.
	if setting(t, state, "public_url").Locked == "" {
		t.Error("public_url can be changed with single sign-on on")
	}
}

func TestEditorUpdate(t *testing.T) {
	editor, path, applied := newEditor(t, "# Reverse DNS\nlocal_dns: 192.168.1.1 # router\ninterface: en0\n")

	state, err := editor.Update(map[string]any{
		"local_dns":      "  10.0.0.1:53 ",
		"retention_days": float64(14),
		"interface":      "en5",
		"geoip_country":  "/srv/GeoLite2-Country.mmdb",
	})
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	want := "# Reverse DNS\nlocal_dns: 10.0.0.1:53 # router\ninterface: en5\ngeoip_country: /srv/GeoLite2-Country.mmdb\nretention_days: 14\n"
	if string(data) != want {
		t.Errorf("file =\n%s\nwant\n%s", data, want)
	}

	// Settings that apply right away do; the others wait for a restart.
	running := editor.Running()
	if running.LocalDNS != "10.0.0.1:53" || running.RetentionDays != 14 {
		t.Errorf("running = %+v, want the new DNS server and retention", running)
	}
	if running.Interface != "en0" || running.GeoIPCountry != Defaults().GeoIPCountry {
		t.Errorf("running interface %q, GeoIP %q; want them unchanged until a restart", running.Interface, running.GeoIPCountry)
	}
	if len(*applied) != 1 || (*applied)[0] != running {
		t.Errorf("applied %+v, want the running configuration once", *applied)
	}
	if s := setting(t, state, "interface"); !s.Restart || s.Value != "en5" || s.Running != "en0" {
		t.Errorf("interface = %+v, want en5 waiting for a restart", s)
	}
	if s := setting(t, state, "local_dns"); s.Restart {
		t.Errorf("local_dns = %+v, want it applied", s)
	}

	// nil takes a setting out of the file, back to its default.
	state, err = editor.Update(map[string]any{"interface": nil, "local_dns": nil})
	if err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "# Reverse DNS\ngeoip_country: /srv/GeoLite2-Country.mmdb\nretention_days: 14\n" {
		t.Errorf("file after reset =\n%s", data)
	}
	if s := setting(t, state, "local_dns"); s.InFile || s.Value != Defaults().LocalDNS || editor.Running().LocalDNS != Defaults().LocalDNS {
		t.Errorf("local_dns after reset = %+v, running %q", s, editor.Running().LocalDNS)
	}
	// netmon started with en0 from the file, so the default needs a restart.
	if s := setting(t, state, "interface"); !s.Restart || s.Value != "" || s.Running != "en0" {
		t.Errorf("interface back to the default = %+v, want it waiting for a restart", s)
	}
}

func TestEditorRefusesChanges(t *testing.T) {
	const content = "local_dns: system\n"
	editor, path, applied := newEditor(t, content)
	t.Setenv("NETMON_PUBLIC_DNS", "1.1.1.1")

	for _, tc := range []struct {
		changes map[string]any
		key     string
		want    string
	}{
		{map[string]any{"dns": "system"}, "dns", "there is no setting dns"},
		{map[string]any{"listen": ":8080"}, "listen", "can only be changed in the file"},
		{map[string]any{"oidc_issuer": "https://id.example.com"}, "oidc_issuer", "can only be changed in the file"},
		{map[string]any{"public_dns": "9.9.9.9"}, "public_dns", "set by the environment variable NETMON_PUBLIC_DNS"},
		{map[string]any{"retention_days": "90"}, "retention_days", "must be a whole number"},
		{map[string]any{"retention_days": 1.5}, "retention_days", "must be a whole number"},
		{map[string]any{"local_dns": 53.0}, "local_dns", "must be text"},
		{map[string]any{"ignore_local_traffic": "no"}, "ignore_local_traffic", "must be true or false"},
		// Values netmon wouldn't start with are refused as a start would refuse them.
		{map[string]any{"retention_days": -1.0}, "retention_days", "retention_days must be"},
		{map[string]any{"local_dns": "http://1.1.1.1"}, "local_dns", "local_dns must be host, host:port or system"},
		{map[string]any{"local_dns": "1.1.1.1:99999"}, "local_dns", "local_dns must be"},
		{map[string]any{"public_url": "router.lan:2137"}, "public_url", "public_url must be an http://"},
	} {
		_, err := editor.Update(tc.changes)
		var invalidErr *InvalidError
		if !errors.As(err, &invalidErr) || !strings.Contains(err.Error(), tc.want) || invalidErr.Key != tc.key {
			t.Errorf("Update(%v) = %v, want an InvalidError for %q mentioning %q", tc.changes, err, tc.key, tc.want)
		}
	}

	if data, _ := os.ReadFile(path); string(data) != content {
		t.Errorf("file = %q after refused changes, want it untouched", data)
	}
	if len(*applied) != 0 {
		t.Errorf("applied %+v after refused changes", *applied)
	}
}

func TestEditorUpdatesYesOrNoSettings(t *testing.T) {
	editor, path, applied := newEditor(t, "# netmon\n")
	if !editor.Running().IgnoreLocalTraffic {
		t.Fatal("local traffic is not ignored by default")
	}

	state, err := editor.Update(map[string]any{"ignore_local_traffic": false})
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "# netmon\nignore_local_traffic: false\n" {
		t.Errorf("file = %q, want the setting turned off", data)
	}
	if editor.Running().IgnoreLocalTraffic || len(*applied) != 1 || (*applied)[0].IgnoreLocalTraffic {
		t.Errorf("applied %+v, want local traffic recorded right away", *applied)
	}
	if s := setting(t, state, "ignore_local_traffic"); s.Value != false || s.Default != true || s.File != false || s.Restart {
		t.Errorf("ignore_local_traffic = %+v, want it off in the file and applied", s)
	}

	// Back to the default takes it out of the file.
	if _, err := editor.Update(map[string]any{"ignore_local_traffic": nil}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "# netmon\n" {
		t.Errorf("file = %q, want the setting taken out", data)
	}
	if !editor.Running().IgnoreLocalTraffic {
		t.Error("local traffic is not ignored after going back to the default")
	}
}

func TestEditorCreatesTheFile(t *testing.T) {
	isolate(t)
	editor := NewEditor("", *mustLoad(t, ""), nil)
	if state, err := editor.State(); err != nil || state.Exists {
		t.Fatalf("state = %+v, %v; want no file yet", state, err)
	}

	state, err := editor.Update(map[string]any{"public_dns": "9.9.9.9"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(DefaultFile)
	if err != nil || !state.Exists {
		t.Fatalf("no %s after saving: %v", DefaultFile, err)
	}
	// It may come to hold the client secret.
	if info.Mode().Perm() != 0o600 {
		t.Errorf("new file mode = %v, want 0600", info.Mode().Perm())
	}
	if cfg := mustLoad(t, ""); cfg.PublicDNS != "9.9.9.9" {
		t.Errorf("loaded public_dns = %q, want the saved one", cfg.PublicDNS)
	}
}

func TestEditorKeepsModeAndLinks(t *testing.T) {
	dir := isolate(t)
	real := writeFile(t, filepath.Join(dir, "real.yaml"), "local_dns: system\n")
	if err := os.Chmod(real, 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "netmon.yaml")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	editor := NewEditor(link, *mustLoad(t, link), nil)

	if _, err := editor.Update(map[string]any{"retention_days": 7.0}); err != nil {
		t.Fatal(err)
	}

	if target, err := os.Readlink(link); err != nil || target != real {
		t.Errorf("link = %q, %v; want it still pointing to %s", target, err, real)
	}
	info, _ := os.Stat(real)
	data, _ := os.ReadFile(real)
	if info.Mode().Perm() != 0o640 || string(data) != "local_dns: system\nretention_days: 7\n" {
		t.Errorf("file = %q with mode %v, want the change with mode 0640", data, info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Errorf("directory has %d entries, want no temporary file left", len(entries))
	}
}

// A file someone broke by hand is reported, not overwritten.
func TestEditorWithABrokenFile(t *testing.T) {
	editor, path, _ := newEditor(t, "local_dns: system\n")
	writeFile(t, path, "local_dns: system\nretention: 3\n")

	if _, err := editor.State(); err == nil || !strings.Contains(err.Error(), "unknown settings: retention") {
		t.Errorf("State() error = %v, want the unknown setting", err)
	}
	if _, err := editor.Update(map[string]any{"public_dns": "9.9.9.9"}); err == nil {
		t.Error("Update succeeded on a broken file")
	}
	if data, _ := os.ReadFile(path); string(data) != "local_dns: system\nretention: 3\n" {
		t.Errorf("file = %q, want it left as it was", data)
	}
}

func TestValidateDNS(t *testing.T) {
	for _, ok := range []string{"", "system", "192.168.1.1", "192.168.1.1:54", "dns.lan", "router", "fd00::1", "[fd00::1]:53", "dns.example.com."} {
		if err := validateDNS("local_dns", ok); err != nil {
			t.Errorf("validateDNS(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"http://1.1.1.1", "1.1.1.1:", "1.1.1.1:abc", "1.1.1.1:0", "[fd00::1]", "two words", "-dns.lan", "dns..lan"} {
		if err := validateDNS("local_dns", bad); err == nil {
			t.Errorf("validateDNS(%q) succeeded", bad)
		}
	}
}

// A file in a directory netmon can't write to, such as /etc, is written in
// place rather than replaced.
func TestEditorWritesInPlaceWithoutTheDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to any directory")
	}
	editor, path, _ := newEditor(t, "local_dns: system\n")
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, err := editor.Update(map[string]any{"retention_days": 3.0}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "local_dns: system\nretention_days: 3\n" {
		t.Errorf("file = %q", data)
	}

	// Without write access to the file either, saving fails and says so.
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := editor.Update(map[string]any{"retention_days": 4.0}); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("Update of a read-only file = %v, want permission denied", err)
	}
}
