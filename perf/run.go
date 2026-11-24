package main

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing/fstest"
	"time"

	"github.com/duckdb/duckdb-go/v2"
	"github.com/duxet/netmon/alerting"
	"github.com/duxet/netmon/api"
	"github.com/duxet/netmon/lookup"
	"github.com/duxet/netmon/notify"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v3"
)

type result struct {
	Section  string  `json:"section"`
	Name     string  `json:"name"`
	Runs     int     `json:"runs"`
	FirstMs  float64 `json:"first_ms"`
	MedianMs float64 `json:"median_ms"`
	MaxMs    float64 `json:"max_ms"`
	// Bytes is the size of the response.
	Bytes int `json:"bytes,omitempty"`
	// AllocMB is what the Go side allocated per run.
	AllocMB float64 `json:"alloc_mb"`
	// PeakRSSMB is the process's peak resident memory while it ran.
	PeakRSSMB float64 `json:"peak_rss_mb"`
	Note      string  `json:"note,omitempty"`
}

type countingDispatcher struct{ n atomic.Int64 }

func (d *countingDispatcher) Dispatch(ns []notify.Notification) { d.n.Add(int64(len(ns))) }

type bench struct {
	db         *sql.DB
	app        *fiber.App
	engine     *alerting.Engine
	dispatcher *countingDispatcher
	clock      atomic.Pointer[time.Time]
	end        time.Time
	days       int
	runs       int
	results    []result
	// laptop is the device with the most traffic, iot one with little.
	laptop, iot string
	macs        []string
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// resetPeakRSS starts measuring the peak resident memory anew, after giving
// the memory Go no longer uses back to the system, so that an earlier case
// doesn't count towards this one.
func resetPeakRSS() {
	debug.FreeOSMemory()
	_ = os.WriteFile("/proc/self/clear_refs", []byte("5"), 0)
}

func procStatus(field string) float64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, field+":") {
			kb, _ := strconv.ParseFloat(strings.Fields(line)[1], 64)
			return kb / 1024
		}
	}
	return 0
}

// measure runs f once to warm up, then up to b.runs times (fewer when it is
// slow), and records how long it took.
func (b *bench) measure(section, name string, f func() (int, string)) result {
	resetPeakRSS()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	t := time.Now()
	size, note := f()
	first := time.Since(t)

	runs := b.runs
	switch {
	case first > 20*time.Second:
		runs = 0
	case first > 3*time.Second:
		runs = min(runs, 2)
	}
	durations := []time.Duration{first}
	if runs > 0 {
		durations = durations[:0]
	}
	for range runs {
		t := time.Now()
		size, note = f()
		durations = append(durations, time.Since(t))
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	slices.Sort(durations)

	r := result{
		Section: section, Name: name, Runs: len(durations), FirstMs: ms(first),
		MedianMs: ms(durations[len(durations)/2]), MaxMs: ms(durations[len(durations)-1]),
		Bytes: size, AllocMB: float64(after.TotalAlloc-before.TotalAlloc) / float64(1+runs) / 1e6,
		PeakRSSMB: procStatus("VmHWM"), Note: note,
	}
	b.record(r)
	return r
}

func (b *bench) record(r result) {
	b.results = append(b.results, r)
	rss := ""
	if r.PeakRSSMB > 0 {
		rss = fmt.Sprintf("%.0f", r.PeakRSSMB)
	}
	fmt.Printf("| %-10s | %-62s | %9.1f | %9.1f | %8s | %8.1f | %7s | %s\n",
		r.Section, r.Name, r.FirstMs, r.MedianMs, size(r.Bytes), r.AllocMB, rss, r.Note)
}

func size(n int) string {
	switch {
	case n == 0:
		return ""
	case n < 1e3:
		return strconv.Itoa(n) + " B"
	case n < 1e6:
		return fmt.Sprintf("%.0f kB", float64(n)/1e3)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	}
}

func header(title string) {
	fmt.Printf("\n## %s\n\n", title)
	fmt.Println("| section    | case                                                           |  first ms | median ms |     size | alloc MB | peak RSS MB | note |")
	fmt.Println("|------------|----------------------------------------------------------------|-----------|-----------|----------|----------|---------|------|")
}

// request makes a request to the app and returns the size of the response,
// and the number of items or flows in it.
func (b *bench) request(method, path, body string) (int, string) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.app.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		log.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		log.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, data)
	}

	note := ""
	if total := resp.Header.Get("X-Total-Count"); total != "" {
		note = total + " in all"
	} else if bytes.HasPrefix(data, []byte("[")) {
		var items []json.RawMessage
		if json.Unmarshal(data, &items) == nil {
			note = strconv.Itoa(len(items)) + " items"
		}
	}
	return len(data), note
}

func (b *bench) get(path string) func() (int, string) {
	return func() (int, string) { return b.request("GET", path, "") }
}

// concurrently makes requests at once, as the browser does for a page, and
// returns the total size.
func (b *bench) concurrently(paths []string) func() (int, string) {
	return func() (int, string) {
		var wg sync.WaitGroup
		var total atomic.Int64
		for _, p := range paths {
			wg.Go(func() {
				n, _ := b.request("GET", p, "")
				total.Add(int64(n))
			})
		}
		wg.Wait()
		return int(total.Load()), strconv.Itoa(len(paths)) + " requests"
	}
}

func withQuery(path string, params ...string) string {
	q := url.Values{}
	for i := 0; i+1 < len(params); i += 2 {
		if params[i+1] != "" {
			q.Set(params[i], params[i+1])
		}
	}
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

var ranges = []struct {
	key   string
	hours int
}{{"24h", 24}, {"7d", 24 * 7}, {"30d", 24 * 30}, {"all", 0}}

func (b *bench) from(hours int) string {
	if hours == 0 {
		return ""
	}
	return b.end.Add(-time.Duration(hours) * time.Hour).Format(time.RFC3339)
}

// dashboardPage and clientPage are the requests the web UI makes when the
// pages open (the sidebar lists the devices and the firing alerts).
func (b *bench) dashboardPage(from string) []string {
	return []string{
		withQuery("/api/traffic-measurements", "from", from),
		withQuery("/api/stats", "from", from),
		withQuery("/api/clients", "from", from),
		withQuery("/api/apps", "from", from),
		"/api/alerts?state=firing&limit=100",
	}
}

func (b *bench) clientPage(from, mac string) []string {
	return []string{
		withQuery("/api/flows/summary", "mac", mac, "from", from, "top", "7"),
		withQuery("/api/traffic-measurements", "mac", mac, "from", from),
		"/api/clients",
		withQuery("/api/clients/"+mac+"/flows", "from", from, "limit", "15", "offset", "0"),
		withQuery("/api/apps", "mac", mac, "from", from),
		withQuery("/api/clients", "from", from),
		"/api/alerts?state=firing&limit=100",
	}
}

func (b *bench) apiSection() {
	header("API")
	for _, r := range ranges {
		from := b.from(r.hours)
		b.measure("dashboard", "stats "+r.key, b.get(withQuery("/api/stats", "from", from)))
		b.measure("dashboard", "traffic-measurements "+r.key, b.get(withQuery("/api/traffic-measurements", "from", from)))
		b.measure("dashboard", "clients "+r.key, b.get(withQuery("/api/clients", "from", from)))
		b.measure("dashboard", "apps "+r.key, b.get(withQuery("/api/apps", "from", from)))
		b.measure("dashboard", "traffic-measurements app=Netflix "+r.key, b.get(withQuery("/api/traffic-measurements", "from", from, "app", "Netflix")))
		b.measure("page", "dashboard page "+r.key, b.concurrently(b.dashboardPage(from)))
	}
	for _, device := range []struct{ name, mac string }{{"laptop", b.laptop}, {"iot", b.iot}} {
		for _, r := range ranges {
			from := b.from(r.hours)
			mac := device.mac
			b.measure("client", device.name+" flows/summary "+r.key, b.get(withQuery("/api/flows/summary", "mac", mac, "from", from, "top", "7")))
			b.measure("client", device.name+" flows page "+r.key, b.get(withQuery("/api/clients/"+mac+"/flows", "from", from, "limit", "15", "offset", "0")))
			b.measure("client", device.name+" traffic-measurements "+r.key, b.get(withQuery("/api/traffic-measurements", "mac", mac, "from", from)))
			b.measure("client", device.name+" apps "+r.key, b.get(withQuery("/api/apps", "mac", mac, "from", from)))
			b.measure("page", device.name+" client page "+r.key, b.concurrently(b.clientPage(from, mac)))
		}
	}
	for _, r := range ranges {
		b.measure("network", "flows limit=50 "+r.key, b.get(withQuery("/api/flows", "from", b.from(r.hours), "limit", "50")))
		b.measure("network", "flows/summary "+r.key, b.get(withQuery("/api/flows/summary", "from", b.from(r.hours))))
	}
	b.measure("other", "devices", b.get("/api/devices"))
	b.measure("other", "alerts firing", b.get("/api/alerts?state=firing&limit=100"))
	b.measure("other", "alerts history", b.get("/api/alerts?limit=50"))
	b.measure("other", "alert-rules", b.get("/api/alert-rules"))
}

type ruleSpec struct {
	name, typ string
	params    any
}

func (b *bench) ruleSpecs() map[string]ruleSpec {
	return map[string]ruleSpec{
		"new_device": {"New device", alerting.NewDevice, alerting.NewDeviceParams{}},
		"inactive 5 devices": {"Quiet", alerting.DeviceInactive,
			alerting.InactiveParams{InactiveSeconds: 3600, MACs: b.macs[:min(5, len(b.macs))]}},
		"traffic total 1h": {"Heavy hour", alerting.TrafficThreshold,
			alerting.TrafficParams{Direction: alerting.Total, ThresholdBytes: 1e9, WindowSeconds: 3600}},
		"traffic total 24h": {"Heavy day", alerting.TrafficThreshold,
			alerting.TrafficParams{Direction: alerting.Total, ThresholdBytes: 5e9, WindowSeconds: 86400}},
		"traffic network download 24h": {"Data cap", alerting.TrafficThreshold,
			alerting.TrafficParams{Direction: alerting.Download, ThresholdBytes: 50e9, WindowSeconds: 86400, Network: true}},
		"traffic app=Netflix 24h": {"Netflix", alerting.TrafficThreshold,
			alerting.TrafficParams{Direction: alerting.Download, ThresholdBytes: 2e9, WindowSeconds: 86400, App: "Netflix"}},
		"traffic one device 24h": {"Laptop", alerting.TrafficThreshold,
			alerting.TrafficParams{Direction: alerting.Upload, ThresholdBytes: 1e9, WindowSeconds: 86400, MACs: []string{b.laptop}}},
		"port 23,2323,445": {"Telnet", alerting.DestinationPort,
			alerting.PortParams{Ports: []int{23, 2323, 445}, Protocol: alerting.Any}},
		"country deny CN,RU": {"Countries", alerting.DestinationCountry,
			alerting.CountryParams{Mode: alerting.Deny, Countries: []string{"CN", "RU"}}},
		"country allow 5": {"Only home", alerting.DestinationCountry,
			alerting.CountryParams{Mode: alerting.Allow, Countries: []string{"US", "DE", "NL", "IE", "PL"}}},
	}
}

var mix = []string{"new_device", "inactive 5 devices", "traffic total 1h", "traffic network download 24h",
	"traffic app=Netflix 24h", "traffic one device 24h", "port 23,2323,445", "country deny CN,RU"}

func (b *bench) exec(q string, args ...any) {
	if _, err := b.db.Exec(q, args...); err != nil {
		log.Fatalf("%s: %v", q, err)
	}
}

// setRules replaces the alert rules, and forgets the alerts of earlier ones.
func (b *bench) setRules(specs []ruleSpec) {
	b.exec(`DELETE FROM alert_rule_channels`)
	b.exec(`DELETE FROM alert_rules`)
	b.exec(`DELETE FROM alerts WHERE rule_id < 9000`)
	repeat := int64(3600)
	for i, s := range specs {
		params, _ := json.Marshal(s.params)
		_, err := storage.CreateRule(b.db, storage.RuleRecord{
			Name: fmt.Sprintf("%s %d", s.name, i), Type: s.typ, Params: string(params), Severity: "warning",
			RepeatIntervalSeconds: &repeat, NotifyOnResolve: true, Enabled: true,
		}, b.end.Add(-48*time.Hour))
		if err != nil {
			log.Fatal(err)
		}
	}
}

func (b *bench) setNow(t time.Time) { b.clock.Store(&t) }

// evaluate times the first evaluation of a set of rules, which looks back
// up to an hour for events and raises every alert, and the ones after it,
// a minute apart, as when netmon runs.
func (b *bench) evaluate(name string, specs []ruleSpec) {
	b.setRules(specs)
	ctx := context.Background()

	resetPeakRSS()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	b.dispatcher.n.Store(0)
	b.setNow(b.end)
	t := time.Now()
	b.engine.Evaluate(ctx)
	first := time.Since(t)
	firstSent := b.dispatcher.n.Swap(0)

	var durations []time.Duration
	for i := range b.runs {
		b.setNow(b.end.Add(time.Duration(i+1) * time.Minute))
		t := time.Now()
		b.engine.Evaluate(ctx)
		durations = append(durations, time.Since(t))
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	slices.Sort(durations)

	var firing int
	_ = b.db.QueryRow(`SELECT count(*) FROM alerts WHERE rule_id < 9000 AND state = 'firing'`).Scan(&firing)
	b.record(result{
		Section: "alerts", Name: name, Runs: len(durations), FirstMs: ms(first),
		MedianMs: ms(durations[len(durations)/2]), MaxMs: ms(durations[len(durations)-1]),
		AllocMB:   float64(after.TotalAlloc-before.TotalAlloc) / float64(1+len(durations)) / 1e6,
		PeakRSSMB: procStatus("VmHWM"),
		Note:      fmt.Sprintf("%d rules; first run sent %d, later %d; %d firing", len(specs), firstSent, b.dispatcher.n.Load(), firing),
	})
}

func (b *bench) alertsSection() {
	header("Alert rules (engine.Evaluate, every minute)")
	specs := b.ruleSpecs()
	b.evaluate("no rules (devices sync only)", nil)
	for _, name := range slices.Sorted(maps.Keys(specs)) {
		b.evaluate(name, []ruleSpec{specs[name]})
	}
	var mixed []ruleSpec
	for _, name := range mix {
		mixed = append(mixed, specs[name])
	}
	b.evaluate("mix of 8 rules", mixed)
	for _, n := range []int{10, 25, 50} {
		var many []ruleSpec
		for i := range n {
			s := specs["traffic total 24h"]
			p := s.params.(alerting.TrafficParams)
			p.ThresholdBytes += uint64(i) * 1e8
			many = append(many, ruleSpec{s.name, s.typ, p})
		}
		b.evaluate(fmt.Sprintf("%d x traffic total 24h", n), many)
	}

	header("Alert rule previews (POST /api/alert-rules/preview)")
	for _, name := range []string{"new_device", "traffic total 24h", "traffic network download 24h",
		"traffic app=Netflix 24h", "port 23,2323,445", "country allow 5"} {
		s := specs[name]
		body, _ := json.Marshal(map[string]any{"type": s.typ, "params": s.params, "severity": "warning", "channel_ids": []int{}})
		b.measure("preview", name, func() (int, string) { return b.request("POST", "/api/alert-rules/preview", string(body)) })
	}
	b.setRules(nil)
}

// contentionSection measures the dashboard while alert rules are evaluated
// back to back, against the dashboard alone.
func (b *bench) contentionSection(seconds int) {
	header(fmt.Sprintf("Dashboard (7d) while rules are evaluated (%ds each)", seconds))
	specs := b.ruleSpecs()
	var mixed []ruleSpec
	for _, name := range mix {
		mixed = append(mixed, specs[name])
	}
	page := b.concurrently(b.dashboardPage(b.from(24 * 7)))

	loadPage := func(note string) {
		var durations []time.Duration
		deadline := time.Now().Add(time.Duration(seconds) * time.Second)
		for time.Now().Before(deadline) {
			t := time.Now()
			page()
			durations = append(durations, time.Since(t))
		}
		slices.Sort(durations)
		p95 := durations[len(durations)*95/100]
		b.record(result{Section: "contention", Name: "dashboard page 7d, " + note, Runs: len(durations),
			FirstMs: ms(durations[0]), MedianMs: ms(durations[len(durations)/2]), MaxMs: ms(durations[len(durations)-1]),
			PeakRSSMB: procStatus("VmHWM"), Note: fmt.Sprintf("p95 %.0f ms (first = fastest)", ms(p95))})
	}

	loadPage("alone")
	for _, set := range []struct {
		name  string
		rules []ruleSpec
	}{{"mix of 8 rules", mixed}, {"25 x traffic 24h", slices.Repeat([]ruleSpec{specs["traffic total 24h"]}, 25)}} {
		b.setRules(set.rules)
		stop := make(chan struct{})
		var evals []time.Duration
		done := make(chan struct{})
		go func() {
			defer close(done)
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				b.setNow(b.end.Add(time.Duration(i) * time.Minute))
				t := time.Now()
				b.engine.Evaluate(context.Background())
				evals = append(evals, time.Since(t))
			}
		}()
		loadPage("while evaluating " + set.name + " back to back")
		close(stop)
		<-done
		slices.Sort(evals)
		b.record(result{Section: "contention", Name: "evaluations of " + set.name + " meanwhile", Runs: len(evals),
			FirstMs: ms(evals[0]), MedianMs: ms(evals[len(evals)/2]), MaxMs: ms(evals[len(evals)-1]),
			Note: fmt.Sprintf("busy %.1f%% of each minute", 100*ms(evals[len(evals)/2])/60000)})
	}
	b.setRules(nil)
}

// writeSection times what the collector does every minute and compaction
// every hour. It changes the data, so it runs last.
func (b *bench) writeSection() {
	header("Writes (collector every minute, compaction every hour)")

	var perMinute int
	_ = b.db.QueryRow(`SELECT (count(*) / count(DISTINCT date_trunc('minute', created_at)))::INT FROM flows`).Scan(&perMinute)
	var mac []byte
	if devices := must(storage.GetDevices(b.db)); len(devices) > 0 {
		mac = devices[0].MACAddress.HardwareAddr
	}
	for _, n := range []int{perMinute, 2000, 10000} {
		b.measure("collector", fmt.Sprintf("insert %d flows row by row (one minute)", n), func() (int, string) {
			b.flush(n, mac)
			return 0, ""
		})
	}
	b.measure("collector", "SyncDevices (last 5 minutes)", func() (int, string) {
		if err := storage.SyncDevices(b.db, b.end.Add(-5*time.Minute)); err != nil {
			log.Fatal(err)
		}
		return 0, ""
	})

	once := func(name string, f func() string) {
		resetPeakRSS()
		t := time.Now()
		note := f()
		d := time.Since(t)
		b.record(result{Section: "compact", Name: name, Runs: 1, FirstMs: ms(d), MedianMs: ms(d), MaxMs: ms(d),
			PeakRSSMB: procStatus("VmHWM"), Note: note})
	}
	compact := func(now time.Time, retention time.Duration) func() string {
		return func() string {
			r, err := storage.Compact(b.db, now, retention)
			if err != nil {
				log.Fatal(err)
			}
			return fmt.Sprintf("rolled %d rows into %d, expired %d", r.Removed, r.RolledUp, r.Expired)
		}
	}
	once("Compact: roll up an hour", compact(b.end.Add(time.Hour), 0))
	once("Compact: roll up an hour, expire the oldest hour",
		compact(b.end.Add(2*time.Hour), time.Duration(b.days*24)*time.Hour))
	if b.days > 90 {
		once("Compact: first run with retention_days=90", compact(b.end.Add(3*time.Hour), 90*24*time.Hour))
	}
	once("ExpireAlerts (30 days)", func() string {
		n, err := storage.ExpireAlerts(b.db, b.end, 30*24*time.Hour)
		if err != nil {
			log.Fatal(err)
		}
		return fmt.Sprintf("expired %d", n)
	})
}

// flush writes n flows as the Linux collector does: a prepared INSERT per
// row, in one transaction.
func (b *bench) flush(n int, mac []byte) {
	tx, err := b.db.Begin()
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`INSERT INTO flows VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, current_timestamp)`)
	if err != nil {
		log.Fatal(err)
	}
	defer stmt.Close()
	for i := range n {
		ip := []byte{93, byte(i >> 16), byte(i >> 8), byte(i)}
		if _, err := stmt.Exec(mac, nil, []byte{192, 168, 1, 10}, ip, 6, 443, 1000, 10, 100, 2); err != nil {
			log.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}
}

// quietLog drops the line the API logs for every request.
type quietLog struct{}

func (quietLog) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("Returning ")) {
		return len(p), nil
	}
	return os.Stderr.Write(p)
}

func copyFile(src, dst string) {
	in, err := os.Open(src)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		log.Fatal(err)
	}
	if err := out.Close(); err != nil {
		log.Fatal(err)
	}
}

func runCommand(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	path := fs.String("db", "flows.db", "database made by seed; it is copied, not changed")
	geoip := fs.String("geoip", "", "GeoLite2-Country.mmdb for countries")
	threads := fs.Int("threads", 0, "DuckDB threads (default: all cores)")
	memory := fs.String("memory-limit", "", "DuckDB memory_limit, e.g. 512MB (default: 80% of RAM)")
	runs := fs.Int("runs", 5, "timed runs per case, after a warm-up")
	sections := fs.String("sections", "api,alerts,contention,write", "what to measure")
	contention := fs.Int("contention-seconds", 20, "how long each contention measurement lasts")
	out := fs.String("json", "", "write the results to this file")
	_ = fs.Parse(args)

	log.SetOutput(quietLog{})

	work := *path + ".work"
	_ = os.Remove(work)
	_ = os.Remove(work + ".wal")
	copyFile(*path, work)
	defer os.Remove(work)

	connector, err := duckdb.NewConnector(work, func(execer driver.ExecerContext) error {
		var boot []string
		if *threads > 0 {
			boot = append(boot, fmt.Sprintf("SET threads = %d", *threads))
		}
		if *memory != "" {
			boot = append(boot, fmt.Sprintf("SET memory_limit = '%s'", *memory))
		}
		for _, q := range boot {
			if _, err := execer.ExecContext(context.Background(), q, nil); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()

	b := &bench{db: db, runs: *runs, dispatcher: &countingDispatcher{}}
	var devices int
	if err := db.QueryRow(`SELECT end_time, devices, days FROM bench_meta`).Scan(&b.end, &devices, &b.days); err != nil {
		log.Fatal("not a database made by perf seed: ", err)
	}
	b.setNow(b.end)

	// The busiest device of the last week, and the quietest that has traffic.
	if err := db.QueryRow(`SELECT arg_max(mac, t), arg_min(mac, t) FROM (
			SELECT src_mac AS mac, sum(in_bytes + out_bytes) AS t FROM all_flows
			WHERE src_mac IS NOT NULL AND created_at >= ?::TIMESTAMPTZ GROUP BY ALL)`, b.end.Add(-7*24*time.Hour),
	).Scan(&b.laptop, &b.iot); err != nil {
		log.Fatal(err)
	}
	b.laptop, b.iot = macString(b.laptop), macString(b.iot)
	for _, d := range must(storage.GetDevices(db)) {
		b.macs = append(b.macs, d.MACAddress.String())
	}

	dns := serveNXDOMAIN()
	hostnames := lookup.NewHostnames(dns, dns)
	countries := lookup.OpenCountries(*geoip)
	defer countries.Close()
	b.engine = alerting.NewEngine(db, b.dispatcher, alerting.Options{
		Hostnames: hostnames, Countries: countries, PublicURL: "http://netmon.lan:2137",
		Now: func() time.Time { return *b.clock.Load() },
	})
	b.app = api.CreateHTTPApp(db, fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}},
		api.Options{Countries: countries, Hostnames: hostnames, Alerts: b.engine})

	var hourly, minute, alerts int64
	var threadsSetting, memorySetting string
	_ = db.QueryRow(`SELECT (SELECT count(*) FROM flows_hourly), (SELECT count(*) FROM flows), (SELECT count(*) FROM alerts),
		current_setting('threads')::VARCHAR, current_setting('memory_limit')`).Scan(&hourly, &minute, &alerts, &threadsSetting, &memorySetting)
	info, _ := os.Stat(*path)
	fmt.Printf("# %s: %d devices, %d days, %d hourly rows, %d per-minute rows, %d alerts, %.0f MB\n",
		*path, devices, b.days, hourly, minute, alerts, float64(info.Size())/1e6)
	fmt.Printf("# DuckDB threads=%s memory_limit=%s, GOMAXPROCS=%d, GeoIP=%v, data ends %s\n",
		threadsSetting, memorySetting, runtime.GOMAXPROCS(0), countries.Available(), b.end.Format(time.RFC3339))
	fmt.Printf("# busiest device %s, quietest %s, %d devices known\n", b.laptop, b.iot, len(b.macs))

	for _, s := range strings.Split(*sections, ",") {
		switch s {
		case "api":
			b.apiSection()
		case "alerts":
			b.alertsSection()
		case "contention":
			b.contentionSection(*contention)
		case "write":
			b.writeSection()
		default:
			log.Fatalf("unknown section %q", s)
		}
	}

	fmt.Printf("\n# peak RSS %.0f MB, Go heap in use %.0f MB\n", procStatus("VmHWM"), heapMB())
	if *out != "" {
		data, _ := json.MarshalIndent(map[string]any{
			"db": *path, "devices": devices, "days": b.days, "hourly_rows": hourly, "minute_rows": minute,
			"db_mb": float64(info.Size()) / 1e6, "threads": threadsSetting, "memory_limit": memorySetting,
			"gomaxprocs": runtime.GOMAXPROCS(0), "results": b.results,
		}, "", "  ")
		if err := os.WriteFile(*out, data, 0o644); err != nil {
			log.Fatal(err)
		}
	}
}

func heapMB() float64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapInuse) / 1e6
}

func macString(raw string) string {
	b := []byte(raw)
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = fmt.Sprintf("%02x", x)
	}
	return strings.Join(parts, ":")
}

func must[T any](v T, err error) T {
	if err != nil {
		log.Fatal(err)
	}
	return v
}
