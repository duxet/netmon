package api

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duxet/netmon/auth"
	"github.com/duxet/netmon/config"
	"github.com/gofiber/fiber/v3"
)

// newConfigApp serves an editor of a YAML file with content, and returns
// the file and the configurations the editor applied.
func newConfigApp(t *testing.T, content string) (*fiber.App, string, *[]config.Config) {
	t.Helper()
	for _, key := range []string{"LOCAL_DNS", "PUBLIC_DNS", "RETENTION_DAYS", "PUBLIC_URL", "INTERFACE"} {
		t.Setenv(config.EnvPrefix+key, "")
	}
	path := filepath.Join(t.TempDir(), "netmon.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	var applied []config.Config
	editor := config.NewEditor(path, *cfg, func(c config.Config) { applied = append(applied, c) })
	app, _ := newTestAppWith(t, Options{Config: editor})
	return app, path, &applied
}

func settingOf(t *testing.T, dto ConfigDTO, key string) ConfigSettingDTO {
	t.Helper()
	for _, s := range dto.Settings {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("setting %s not listed", key)
	return ConfigSettingDTO{}
}

func TestGetConfig(t *testing.T) {
	app, path, _ := newConfigApp(t, "# DNS\nlocal_dns: system\n")
	t.Setenv("NETMON_PUBLIC_DNS", "9.9.9.9")

	var dto ConfigDTO
	if status, _ := get(t, app, "/api/config", &dto); status != fiber.StatusOK {
		t.Fatalf("GET /api/config = %d", status)
	}
	if dto.File != path || !dto.Exists || dto.OS == "" || len(dto.Settings) != 19 {
		t.Errorf("config = %+v", dto)
	}
	if s := settingOf(t, dto, "local_dns"); s.Value != "system" || s.File != "system" || !s.InFile || !s.Live {
		t.Errorf("local_dns = %+v, want system from the file", s)
	}
	if s := settingOf(t, dto, "public_dns"); s.Value != "9.9.9.9" || s.Env != "NETMON_PUBLIC_DNS" {
		t.Errorf("public_dns = %+v, want it from the environment", s)
	}
	// Numbers are numbers.
	if s := settingOf(t, dto, "alert_retention_days"); s.Value != float64(30) || s.Default != float64(30) {
		t.Errorf("alert_retention_days = %+v, want 30", s)
	}
}

func TestPutConfig(t *testing.T) {
	app, path, applied := newConfigApp(t, "# DNS\nlocal_dns: system # for now\nretention_days: 90\n")

	status, body := do(t, app, "PUT", "/api/config", `{"local_dns": "192.168.1.1:53", "retention_days": null, "interface": "en5"}`)
	if status != fiber.StatusOK {
		t.Fatalf("PUT /api/config = %d: %s", status, body)
	}
	var dto ConfigDTO
	if err := json.Unmarshal([]byte(body), &dto); err != nil {
		t.Fatal(err)
	}
	if s := settingOf(t, dto, "local_dns"); s.Value != "192.168.1.1:53" || s.Running != "192.168.1.1:53" || s.Restart {
		t.Errorf("local_dns = %+v, want it saved and applied", s)
	}
	if s := settingOf(t, dto, "interface"); s.Value != "en5" || s.Running != "" || !s.Restart {
		t.Errorf("interface = %+v, want it saved for the next start", s)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "# DNS\nlocal_dns: 192.168.1.1:53 # for now\ninterface: en5\n" {
		t.Errorf("file =\n%s", data)
	}
	if len(*applied) != 1 || (*applied)[0].LocalDNS != "192.168.1.1:53" || (*applied)[0].RetentionDays != 0 {
		t.Errorf("applied %+v, want the new DNS server and the default retention", *applied)
	}

	// No changes write nothing.
	if status, _ := do(t, app, "PUT", "/api/config", `{}`); status != fiber.StatusOK || len(*applied) != 1 {
		t.Errorf("empty PUT = %d, applied %d times", status, len(*applied))
	}
}

func TestPutConfigErrors(t *testing.T) {
	const content = "local_dns: system\n"
	app, path, _ := newConfigApp(t, content)

	for _, tc := range []struct {
		body, setting, want string
	}{
		{`{"local_dns": "not a server"}`, "local_dns", "local_dns must be host"},
		{`{"retention_days": "ninety"}`, "retention_days", "must be a whole number"},
		{`{"listen": ":8080"}`, "listen", "can only be changed in the file"},
		{`{"oidc_client_secret": "s3cret"}`, "oidc_client_secret", "can only be changed in the file"},
		{`{"nope": 1}`, "nope", "there is no setting nope"},
	} {
		status, body := do(t, app, "PUT", "/api/config", tc.body)
		var dto ConfigErrorDTO
		_ = json.Unmarshal([]byte(body), &dto)
		if status != fiber.StatusBadRequest || dto.Setting != tc.setting || !strings.Contains(dto.Error, tc.want) {
			t.Errorf("PUT %s = %d %s, want 400 for %q mentioning %q", tc.body, status, body, tc.setting, tc.want)
		}
	}
	for _, body := range []string{"", "[1]", "null", `{"local_dns":`} {
		if status, _ := do(t, app, "PUT", "/api/config", body); status != fiber.StatusBadRequest {
			t.Errorf("PUT %q = %d, want 400", body, status)
		}
	}

	if data, _ := os.ReadFile(path); string(data) != content {
		t.Errorf("file = %q after refused changes, want it untouched", data)
	}

	// A file that can't be written is the server's problem, and says why.
	if err := os.RemoveAll(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	status, body := do(t, app, "PUT", "/api/config", `{"public_dns": "9.9.9.9"}`)
	if status != fiber.StatusInternalServerError || !strings.Contains(body, "saving "+path) {
		t.Errorf("PUT without the file's directory = %d %s, want 500 with the reason", status, body)
	}
}

// The configuration is behind the sign-in like everything else.
func TestConfigNeedsSignIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netmon.yaml")
	app, _ := newTestAppFor(t, func(db *sql.DB) Options {
		authn, err := auth.New(db, auth.Options{Issuer: "https://id.example.com", ClientID: "netmon", BaseURL: "http://netmon.lan:2137"})
		if err != nil {
			t.Fatal(err)
		}
		return Options{Auth: authn, Config: config.NewEditor(path, config.Defaults(), nil)}
	})

	for _, method := range []string{"GET", "PUT"} {
		if status, _ := do(t, app, method, "/api/config", `{"local_dns": "system"}`); status != fiber.StatusUnauthorized {
			t.Errorf("%s /api/config without a session = %d, want 401", method, status)
		}
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the file was written without a session")
	}
}
