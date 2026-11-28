package geoipupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/duxet/netmon/internal/geoiptest"
	"github.com/duxet/netmon/lookup"
)

// release is what the fake MaxMind serves for an edition.
type release struct {
	archive  []byte
	modified time.Time
	checksum string
}

// fakeMaxMind serves releases as MaxMind's download service does, and counts
// the downloads of archives.
type fakeMaxMind struct {
	t *testing.T

	mu        sync.Mutex
	releases  map[string]release
	downloads map[string]int
	// storage is where downloads are redirected to, as MaxMind does, when
	// set. It must not be sent the credentials.
	storage string
}

func (f *fakeMaxMind) set(edition string, r release) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases[edition] = r
}

func (f *fakeMaxMind) downloaded(edition string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.downloads[edition]
}

func (f *fakeMaxMind) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if user, key, ok := r.BasicAuth(); !ok || user != "1234" || key != "secret" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if f.storage != "" {
		http.Redirect(w, r, f.storage+r.URL.RequestURI(), http.StatusFound)
		return
	}
	f.serve(w, r)
}

// serveStorage serves downloads redirected to the storage.
func (f *fakeMaxMind) serveStorage(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "" {
		f.t.Error("the storage was sent the credentials")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	f.serve(w, r)
}

func (f *fakeMaxMind) serve(w http.ResponseWriter, r *http.Request) {
	edition, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/download")
	f.mu.Lock()
	rel, found := f.releases[edition]
	f.mu.Unlock()
	if !ok || !found {
		http.NotFound(w, r)
		return
	}

	switch r.URL.Query().Get("suffix") {
	case "tar.gz":
		w.Header().Set("Last-Modified", rel.modified.UTC().Format(http.TimeFormat))
		if r.Method == http.MethodGet {
			f.mu.Lock()
			f.downloads[edition]++
			f.mu.Unlock()
			_, _ = w.Write(rel.archive)
		}
	case "tar.gz.sha256":
		_, _ = fmt.Fprintf(w, "%s  %s_20260101.tar.gz\n", rel.checksum, edition)
	default:
		http.NotFound(w, r)
	}
}

func newFake(t *testing.T) (*fakeMaxMind, *httptest.Server) {
	f := &fakeMaxMind{t: t, releases: map[string]release{}, downloads: map[string]int{}}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	storage := httptest.NewServer(http.HandlerFunc(f.serveStorage))
	t.Cleanup(storage.Close)
	f.storage = storage.URL
	return f, server
}

// archive packs the database at path as MaxMind does, in a directory named
// after the edition and the release.
func archive(t *testing.T, edition, path string) release {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	dir := edition + "_20260101/"
	files := []struct {
		name string
		data []byte
	}{
		{dir + "COPYRIGHT.txt", []byte("Copyright")},
		{dir + edition + ".mmdb", data},
		{dir + "LICENSE.txt", []byte("License")},
	}
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return release{archive: buf.Bytes(), checksum: fmt.Sprintf("%x", sum), modified: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
}

func country(t *testing.T, countries *lookup.Countries, addr string) string {
	t.Helper()
	code, _, _ := countries.Lookup(netip.MustParseAddr(addr))
	return code
}

func TestUpdateDownloadsAndReloads(t *testing.T) {
	fake, server := newFake(t)
	first := archive(t, "GeoLite2-Country", geoiptest.Countries(t, geoiptest.Country{Network: "1.0.0.0/8", Code: "AU", Name: "Australia"}))
	fake.set("GeoLite2-Country", first)

	path := filepath.Join(t.TempDir(), "data", "GeoLite2-Country.mmdb")
	countries := lookup.OpenCountries(path)
	defer countries.Close()
	if countries.Available() {
		t.Fatal("countries are available before the database was downloaded")
	}

	u := New(Options{AccountID: "1234", LicenseKey: "secret", BaseURL: server.URL},
		Database{Edition: "GeoLite2-Country", Path: path, Reload: countries.Reload},
		Database{Edition: "GeoLite2-ASN", Path: ""})
	if err := u.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := country(t, countries, "1.1.1.1"); got != "AU" {
		t.Fatalf("country after the first download = %q, want AU", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(first.modified) {
		t.Errorf("file time = %v, want the release's %v", info.ModTime(), first.modified)
	}
	statuses := u.Statuses()
	if len(statuses) != 1 || statuses[0].Error != "" || statuses[0].Built.IsZero() || statuses[0].CheckedAt.IsZero() {
		t.Errorf("statuses = %+v, want one checked database without an error", statuses)
	}

	// Without a newer release nothing is downloaded.
	if err := u.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := fake.downloaded("GeoLite2-Country"); n != 1 {
		t.Errorf("downloads = %d, want 1 without a newer release", n)
	}

	// A newer release replaces the database while it is open.
	second := archive(t, "GeoLite2-Country", geoiptest.Countries(t, geoiptest.Country{Network: "1.0.0.0/8", Code: "NZ", Name: "New Zealand"}))
	second.modified = first.modified.Add(72 * time.Hour)
	fake.set("GeoLite2-Country", second)
	if err := u.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := country(t, countries, "1.1.1.1"); got != "NZ" {
		t.Errorf("country after the update = %q, want NZ", got)
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*"))
	if len(leftovers) != 0 {
		t.Errorf("temporary files left: %v", leftovers)
	}
}

func TestUpdateKeepsTheDatabaseOnFailures(t *testing.T) {
	good := geoiptest.Countries(t, geoiptest.Country{Network: "1.0.0.0/8", Code: "AU", Name: "Australia"})
	for name, tc := range map[string]struct {
		release func(release) release
		options Options
		want    string
	}{
		"wrong license key": {
			release: func(r release) release { return r },
			options: Options{AccountID: "1234", LicenseKey: "wrong"},
			want:    "refused the account ID and license key",
		},
		"wrong checksum": {
			release: func(r release) release { r.checksum = strings.Repeat("0", 64); return r },
			want:    "checksum",
		},
		"not an archive": {
			release: func(r release) release {
				r.archive = []byte("not gzip")
				sum := sha256.Sum256(r.archive)
				r.checksum = fmt.Sprintf("%x", sum)
				return r
			},
			want: "reading the download",
		},
		"another edition": {
			release: func(r release) release {
				asns := archive(t, "GeoLite2-Country", geoiptest.ASNs(t, geoiptest.ASN{Network: "1.0.0.0/8", Number: 13335, Organization: "Cloudflare"}))
				asns.modified = r.modified
				return asns
			},
			want: "GeoLite2-ASN database, not GeoLite2-Country",
		},
	} {
		t.Run(name, func(t *testing.T) {
			fake, server := newFake(t)
			r := archive(t, "GeoLite2-Country", good)
			r.modified = time.Now().Add(time.Hour)
			fake.set("GeoLite2-Country", tc.release(r))

			path := filepath.Join(t.TempDir(), "GeoLite2-Country.mmdb")
			data, _ := os.ReadFile(good)
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			countries := lookup.OpenCountries(path)
			defer countries.Close()

			options := tc.options
			if options.AccountID == "" {
				options = Options{AccountID: "1234", LicenseKey: "secret"}
			}
			options.BaseURL = server.URL
			u := New(options, Database{Edition: "GeoLite2-Country", Path: path, Reload: countries.Reload})
			err := u.Update(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Update = %v, want an error about %q", err, tc.want)
			}
			if status := u.Statuses()[0]; !strings.Contains(status.Error, tc.want) {
				t.Errorf("status error = %q, want %q", status.Error, tc.want)
			}
			if got := country(t, countries, "1.1.1.1"); got != "AU" {
				t.Errorf("country = %q, want the old database's AU", got)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(after, data) {
				t.Error("the file was changed")
			}
			leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*"))
			if len(leftovers) != 0 {
				t.Errorf("temporary files left: %v", leftovers)
			}
		})
	}
}
