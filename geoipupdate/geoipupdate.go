// Package geoipupdate keeps the GeoLite2 databases up to date: it downloads
// new releases from MaxMind with the account's license key, replaces the
// files and has them opened again, without restarting netmon.
package geoipupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/geoip2-golang/v2"
)

const (
	// DefaultBaseURL is MaxMind's download service.
	DefaultBaseURL = "https://download.maxmind.com/geoip/databases"

	// checkInterval is how often new releases are looked for. MaxMind
	// releases GeoLite2 twice a week.
	checkInterval = 24 * time.Hour
	// retryInterval is how soon a failed update is tried again.
	retryInterval = time.Hour
	// maxArchiveSize bounds a download; the GeoLite2 archives are tens of
	// megabytes.
	maxArchiveSize = 512 << 20
)

// Database is a database to keep up to date.
type Database struct {
	// Edition is MaxMind's name of the database, such as GeoLite2-Country.
	Edition string
	// Path is the file it is kept in.
	Path string
	// Reload opens the file again after it was replaced; it may be nil.
	Reload func() error
}

// Status is how a database's updates go.
type Status struct {
	Edition string
	Path    string
	// Built is when the database in the file was built, zero when there is
	// no file or it can't be read.
	Built time.Time
	// CheckedAt is when a release was last looked for, zero before.
	CheckedAt time.Time
	// Error is why the last update failed, empty when it didn't.
	Error string
}

// Options configure an Updater.
type Options struct {
	AccountID  string
	LicenseKey string
	// BaseURL is where the databases are downloaded from, DefaultBaseURL
	// when empty.
	BaseURL string
	// Client makes the requests; a client with a timeout when nil.
	Client *http.Client
}

// Updater keeps databases up to date. It is safe for concurrent use.
type Updater struct {
	options   Options
	databases []Database

	mu       sync.Mutex
	statuses []Status
}

// New returns an updater of the databases. Those without a path are left
// out.
func New(options Options, databases ...Database) *Updater {
	if options.BaseURL == "" {
		options.BaseURL = DefaultBaseURL
	}
	if options.Client == nil {
		options.Client = &http.Client{Timeout: 10 * time.Minute}
	}
	// MaxMind redirects downloads to a storage service, which must not get
	// the license key. Go only leaves it out for another host name, so it
	// is left out of every redirect here.
	client := *options.Client
	checkRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		req.Header.Del("Authorization")
		if checkRedirect != nil {
			return checkRedirect(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	options.Client = &client
	u := &Updater{options: options}
	for _, db := range databases {
		if db.Path == "" {
			continue
		}
		u.databases = append(u.databases, db)
		u.statuses = append(u.statuses, Status{Edition: db.Edition, Path: db.Path, Built: built(db.Path)})
	}
	return u
}

// Statuses returns how every database's updates go.
func (u *Updater) Statuses() []Status {
	if u == nil {
		return nil
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]Status(nil), u.statuses...)
}

// Run updates the databases now, and then every day, until ctx is done. A
// failed update is tried again within the hour.
func (u *Updater) Run(ctx context.Context) {
	for {
		wait := checkInterval
		if err := u.Update(ctx); err != nil {
			wait = retryInterval
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// Update downloads the databases that MaxMind has a newer release of than the
// files, and reports the first failure.
func (u *Updater) Update(ctx context.Context) error {
	var errs []error
	for i, db := range u.databases {
		release, updated, err := u.update(ctx, db)

		u.mu.Lock()
		status := &u.statuses[i]
		status.CheckedAt = time.Now()
		status.Error = ""
		if err != nil {
			status.Error = err.Error()
		}
		if updated {
			status.Built = built(db.Path)
		}
		u.mu.Unlock()

		switch {
		case err != nil:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.Warn("Failed to update a GeoIP database", "edition", db.Edition, "path", db.Path, "err", err)
			errs = append(errs, fmt.Errorf("%s: %w", db.Edition, err))
		case updated:
			slog.Info("Updated a GeoIP database", "edition", db.Edition, "path", db.Path, "release", release.Format(time.DateOnly))
		default:
			slog.Debug("GeoIP database is up to date", "edition", db.Edition, "path", db.Path)
		}
	}
	return errors.Join(errs...)
}

// update downloads db when there is a newer release than its file, and
// returns when the release it has now was made.
func (u *Updater) update(ctx context.Context, db Database) (time.Time, bool, error) {
	release, err := u.latest(ctx, db.Edition)
	if err != nil {
		return time.Time{}, false, err
	}
	// Downloaded files are given the time of their release, so a file at
	// least as new as the release is that release or a later one.
	if info, err := os.Stat(db.Path); err == nil && !release.IsZero() && !info.ModTime().Before(release) {
		return release, false, nil
	}

	sum, err := u.checksum(ctx, db.Edition)
	if err != nil {
		return time.Time{}, false, err
	}
	temp, modified, err := u.download(ctx, db, sum)
	if err != nil {
		return time.Time{}, false, err
	}
	defer os.Remove(temp)
	if release.IsZero() {
		release = modified
	}
	if !release.IsZero() {
		if err := os.Chtimes(temp, release, release); err != nil {
			return time.Time{}, false, err
		}
	}
	if err := os.Rename(temp, db.Path); err != nil {
		return time.Time{}, false, err
	}
	if db.Reload != nil {
		if err := db.Reload(); err != nil {
			return release, true, fmt.Errorf("opening the new database: %w", err)
		}
	}
	return release, true, nil
}

func (u *Updater) url(edition, suffix string) string {
	return strings.TrimSuffix(u.options.BaseURL, "/") + "/" + url.PathEscape(edition) + "/download?suffix=" + url.QueryEscape(suffix)
}

// get requests the edition's file with suffix. The caller closes the body.
func (u *Updater) get(ctx context.Context, method, edition, suffix string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.url(edition, suffix), nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(u.options.AccountID, u.options.LicenseKey)
	req.Header.Set("User-Agent", "netmon")
	resp, err := u.options.Client.Do(req)
	if err != nil {
		// The URL says nothing the edition doesn't.
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("downloading from MaxMind: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return nil, errors.New("MaxMind refused the account ID and license key")
		case http.StatusForbidden:
			return nil, fmt.Errorf("the account can't download %s", edition)
		}
		return nil, fmt.Errorf("MaxMind answered %s", resp.Status)
	}
	return resp, nil
}

// latest returns when the newest release of edition was made, zero when
// MaxMind doesn't tell.
func (u *Updater) latest(ctx context.Context, edition string) (time.Time, error) {
	resp, err := u.get(ctx, http.MethodHead, edition, "tar.gz")
	if err != nil {
		return time.Time{}, err
	}
	_ = resp.Body.Close()
	return lastModified(resp), nil
}

func lastModified(resp *http.Response) time.Time {
	t, err := http.ParseTime(resp.Header.Get("Last-Modified"))
	if err != nil {
		return time.Time{}
	}
	return t
}

// checksum returns the SHA-256 checksum of the newest archive of edition.
func (u *Updater) checksum(ctx context.Context, edition string) ([]byte, error) {
	resp, err := u.get(ctx, http.MethodGet, edition, "tar.gz.sha256")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, err
	}
	// "<hex>  GeoLite2-Country_20260101.tar.gz"
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return nil, errors.New("MaxMind sent no checksum")
	}
	sum, err := hex.DecodeString(fields[0])
	if err != nil || len(sum) != sha256.Size {
		return nil, fmt.Errorf("MaxMind sent a checksum that isn't SHA-256: %q", fields[0])
	}
	return sum, nil
}

// download writes the newest release of db to a file next to its path, and
// returns the file and when the release was made, if MaxMind tells. The
// archive must have sum, and the database must be of db's edition.
func (u *Updater) download(ctx context.Context, db Database, sum []byte) (string, time.Time, error) {
	resp, err := u.get(ctx, http.MethodGet, db.Edition, "tar.gz")
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()

	dir := filepath.Dir(db.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", time.Time{}, err
	}
	file, err := os.CreateTemp(dir, "."+filepath.Base(db.Path)+".*")
	if err != nil {
		return "", time.Time{}, err
	}
	temp := file.Name()
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(temp)
		}
	}()

	hash := sha256.New()
	body := io.TeeReader(io.LimitReader(resp.Body, maxArchiveSize), hash)
	if err := extract(body, db.Edition+".mmdb", file); err != nil {
		return "", time.Time{}, err
	}
	// The checksum is of the whole archive, past the database too.
	if _, err := io.Copy(io.Discard, body); err != nil {
		return "", time.Time{}, err
	}
	if got := hash.Sum(nil); !equal(got, sum) {
		return "", time.Time{}, fmt.Errorf("the download's checksum is %x, not %x", got, sum)
	}
	if err := file.Close(); err != nil {
		return "", time.Time{}, err
	}
	if err := check(temp, db.Edition); err != nil {
		return "", time.Time{}, err
	}

	ok = true
	return temp, lastModified(resp), nil
}

func equal(a, b []byte) bool {
	return len(a) == len(b) && string(a) == string(b)
}

// extract copies the file named name, in any directory of the gzipped tar
// archive r, to w.
func extract(r io.Reader, name string, w io.Writer) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("reading the download: %w", err)
	}
	archive := tar.NewReader(gz)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("the download has no %s", name)
		}
		if err != nil {
			return fmt.Errorf("reading the download: %w", err)
		}
		if header.Typeflag != tar.TypeReg || path.Base(header.Name) != name {
			continue
		}
		if _, err := io.Copy(w, archive); err != nil {
			return fmt.Errorf("reading the download: %w", err)
		}
		// Read to the end of the gzip stream, so its checksum is checked.
		_, err = io.Copy(io.Discard, gz)
		return err
	}
}

// built returns when the database at path was built, zero when it can't be
// read.
func built(path string) time.Time {
	db, err := geoip2.Open(path)
	if err != nil {
		return time.Time{}
	}
	defer db.Close()
	return time.Unix(int64(db.Metadata().BuildEpoch), 0).UTC()
}

// check opens the database at path, which has to be of edition.
func check(path, edition string) error {
	db, err := geoip2.Open(path)
	if err != nil {
		return fmt.Errorf("the download isn't a database: %w", err)
	}
	defer db.Close()
	if got := db.Metadata().DatabaseType; got != edition {
		return fmt.Errorf("the download is a %s database, not %s", got, edition)
	}
	return nil
}
