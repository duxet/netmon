package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/duckdb/duckdb-go/v2"
	"github.com/duxet/netmon/apps"
	migrate "github.com/rubenv/sql-migrate"
)

// A kind of device, with how much and when it talks. Flow counts are
// distinct (peer, protocol, port) per hour, which is what an hourly row is.
type kind struct {
	name  string
	share float64
	// oui is the vendor prefix of its MAC address.
	oui [3]byte
	// Mean flows per hour when in use and when idle.
	activeFlows, idleFlows float64
	// homePeers are endpoints it talks to all the time (push, telemetry).
	homePeers int
	// active is the chance it is in use at an hour of the day.
	active func(hour int) float64
	// Median bytes per flow and the spread (log-normal sigma).
	medianBytes, sigma float64
	// downShare is the fraction of bytes it downloads.
	downShare float64
	// randomMAC is the chance it uses a private (locally administered) MAC.
	randomMAC float64
	// appBias is the share of its new flows that go to streaming services.
	appBias float64
}

func hours(from, to int, in, out float64) func(int) float64 {
	return func(h int) float64 {
		if h >= from && h < to {
			return in
		}
		return out
	}
}

var kinds = []kind{
	{name: "laptop", share: 0.12, oui: [3]byte{0xa4, 0x83, 0xe7}, activeFlows: 220, idleFlows: 25, homePeers: 10,
		active: hours(8, 24, 0.7, 0.05), medianBytes: 60e3, sigma: 2.2, downShare: 0.85},
	{name: "phone", share: 0.25, oui: [3]byte{0xf0, 0x99, 0xb6}, activeFlows: 110, idleFlows: 30, homePeers: 6,
		active: hours(7, 24, 0.6, 0.1), medianBytes: 40e3, sigma: 2.2, downShare: 0.85, randomMAC: 0.6},
	{name: "tablet", share: 0.05, oui: [3]byte{0x8c, 0x85, 0x90}, activeFlows: 80, idleFlows: 10, homePeers: 4,
		active: hours(17, 23, 0.5, 0.03), medianBytes: 50e3, sigma: 2.3, downShare: 0.9, randomMAC: 0.5},
	{name: "tv", share: 0.08, oui: [3]byte{0x3c, 0x22, 0xfb}, activeFlows: 50, idleFlows: 8, homePeers: 5,
		active: hours(18, 24, 0.6, 0.05), medianBytes: 200e3, sigma: 2.6, downShare: 0.97, appBias: 0.5},
	{name: "console", share: 0.04, oui: [3]byte{0x7c, 0xbb, 0x8a}, activeFlows: 60, idleFlows: 6, homePeers: 4,
		active: hours(18, 24, 0.3, 0.02), medianBytes: 100e3, sigma: 2.8, downShare: 0.9, appBias: 0.2},
	{name: "iot", share: 0.38, oui: [3]byte{0x50, 0x02, 0x91}, activeFlows: 8, idleFlows: 8, homePeers: 3,
		active: hours(0, 24, 1, 1), medianBytes: 4e3, sigma: 1.5, downShare: 0.4},
	{name: "nas", share: 0.03, oui: [3]byte{0x00, 0x11, 0x32}, activeFlows: 40, idleFlows: 40, homePeers: 8,
		active: hours(0, 24, 1, 1), medianBytes: 30e3, sigma: 2.5, downShare: 0.3},
}

type device struct {
	kind      *kind
	mac       net.HardwareAddr
	ip        netip.Addr
	homePeers []int
	// Hours since the start of the data when it is on the network.
	from, to int
	name     string
	// telnet makes it connect to port 23 now and then, for port rules.
	telnet bool
}

type row struct {
	srcMAC, dstMAC       []byte
	srcIP, dstIP         []byte
	proto                uint8
	port                 uint16
	inBytes, inPackets   uint64
	outBytes, outPackets uint64
	createdAt            time.Time
}

type seeder struct {
	rng      *rand.Rand
	zipf     *rand.Zipf
	peers    []netip.Addr
	appPeers []netip.Addr
	devices  []*device
	router   netip.Addr
	wanIP    netip.Addr
	start    time.Time
	nHours   int
}

func randomPublic(rng *rand.Rand) netip.Addr {
	for {
		var b [4]byte
		for i := range b {
			b[i] = byte(rng.IntN(256))
		}
		a := netip.AddrFrom4(b)
		if b[0] == 0 || b[0] >= 224 || b[0] == 100 && b[1]&0xc0 == 64 || !apps.IsPublic(a) {
			continue
		}
		return a
	}
}

// appAddresses returns addresses inside the prefixes of known apps.
func appAddresses(rng *rand.Rand, n int) []netip.Addr {
	var prefixes []netip.Prefix
	for _, app := range apps.Known {
		for _, p := range app.Prefixes {
			if prefix := netip.MustParsePrefix(p); prefix.Addr().Is4() {
				prefixes = append(prefixes, prefix)
			}
		}
	}
	addrs := make([]netip.Addr, n)
	for i := range addrs {
		p := prefixes[rng.IntN(len(prefixes))]
		b := p.Addr().As4()
		host := rng.Uint32() & (1<<(32-p.Bits()) - 1)
		v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]) | host
		addrs[i] = netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
	}
	return addrs
}

func (s *seeder) macFor(k *kind, i int) net.HardwareAddr {
	mac := net.HardwareAddr{k.oui[0], k.oui[1], k.oui[2], byte(i >> 8), byte(i), byte(s.rng.IntN(256))}
	if s.rng.Float64() < k.randomMAC {
		mac[0] = byte(s.rng.IntN(256))&0xfc | 0x02
	}
	return mac
}

func (s *seeder) addDevice(k *kind, from, to int) *device {
	i := len(s.devices)
	d := &device{
		kind: k, mac: s.macFor(k, i), ip: netip.AddrFrom4([4]byte{192, 168, byte(1 + (i+2)/250), byte(2 + (i+2)%250)}),
		from: from, to: to,
	}
	for range k.homePeers {
		d.homePeers = append(d.homePeers, s.rng.IntN(min(2000, len(s.peers))))
	}
	if s.rng.Float64() < 0.4 {
		d.name = fmt.Sprintf("%s %d", k.name, i)
	}
	s.devices = append(s.devices, d)
	return d
}

func poisson(rng *rand.Rand, mean float64) int {
	if mean <= 0 {
		return 0
	}
	if mean > 30 {
		return max(0, int(math.Round(rng.NormFloat64()*math.Sqrt(mean)+mean)))
	}
	l, k, p := math.Exp(-mean), 0, 1.0
	for {
		p *= rng.Float64()
		if p <= l {
			return k
		}
		k++
	}
}

type flowKey struct {
	peer  netip.Addr
	proto uint8
	port  uint16
}

type plannedFlow struct {
	key        flowKey
	persistent bool
	bytes      float64
}

func (s *seeder) port() (uint8, uint16) {
	switch r := s.rng.Float64(); {
	case r < 0.72:
		return 6, 443
	case r < 0.82:
		return 17, 443
	case r < 0.89:
		return 6, 80
	case r < 0.905:
		return 6, 5228
	case r < 0.92:
		return 17, 123
	case r < 0.93:
		return 6, 853
	case r < 0.945:
		return 17, 3478
	case r < 0.955:
		return 6, 993
	case r < 0.96:
		return 6, 8883
	default:
		return uint8([]int{6, 17}[s.rng.IntN(2)]), uint16(1024 + s.rng.IntN(64000))
	}
}

func (s *seeder) bytes(k *kind, scale float64) float64 {
	return k.medianBytes * scale * math.Exp(s.rng.NormFloat64()*k.sigma)
}

// plan returns the remote flows of a device in one hour.
func (s *seeder) plan(d *device, hour time.Time) []plannedFlow {
	k := d.kind
	active := s.rng.Float64() < k.active(hour.Hour())
	mean := k.idleFlows
	if active {
		mean = k.activeFlows
	}
	if hour.Weekday() == time.Saturday || hour.Weekday() == time.Sunday {
		mean *= 1.2
	}

	seen := map[flowKey]int{}
	var flows []plannedFlow
	add := func(key flowKey, persistent bool, b float64) {
		if i, ok := seen[key]; ok {
			flows[i].bytes += b
			return
		}
		seen[key] = len(flows)
		flows = append(flows, plannedFlow{key: key, persistent: persistent, bytes: b})
	}

	for _, p := range d.homePeers {
		add(flowKey{s.peers[p], 6, 443}, true, s.bytes(k, 0.3))
	}
	for range poisson(s.rng, mean) {
		var peer netip.Addr
		if s.rng.Float64() < k.appBias {
			peer = s.appPeers[s.rng.IntN(len(s.appPeers))]
		} else {
			peer = s.peers[s.zipf.Uint64()]
		}
		proto, port := s.port()
		scale := 1.0
		if !active {
			scale = 0.1
		}
		add(flowKey{peer, proto, port}, false, s.bytes(k, scale))
	}
	if d.telnet && s.rng.Float64() < 0.3 {
		add(flowKey{s.peers[len(s.peers)-1-s.rng.IntN(50)], 6, []uint16{23, 2323}[s.rng.IntN(2)]}, false, 300)
	}
	return flows
}

func (s *seeder) packets(b uint64, size uint64) uint64 {
	return b/size + 1
}

// rowsFor turns the flows of a device's hour into rows: one per flow for
// hourly data, or one per flow and minute it was active for per-minute data.
func (s *seeder) rowsFor(d *device, hour time.Time, perMinute bool, end time.Time, emit func(row)) {
	mac := []byte(d.mac)
	ip := d.ip.AsSlice()
	out := func(dstMAC []byte, dst netip.Addr, proto uint8, port uint16, total float64, at time.Time, down float64) {
		in := uint64(total * down)
		o := uint64(total) - in
		emit(row{srcMAC: mac, dstMAC: dstMAC, srcIP: ip, dstIP: dst.AsSlice(), proto: proto, port: port,
			inBytes: in, inPackets: s.packets(in, 1200), outBytes: o, outPackets: s.packets(o, 300), createdAt: at})
	}

	down := min(0.99, max(0.05, d.kind.downShare+s.rng.NormFloat64()*0.08))
	flows := s.plan(d, hour)
	// DNS through the router, whose own MAC address isn't in its neighbor table.
	flows = append(flows, plannedFlow{key: flowKey{s.router, 17, 53}, persistent: true, bytes: float64(len(flows)) * 400})
	local := map[flowKey][]byte{}
	// Local traffic: laptops copy to the NAS, phones cast to the TV.
	for _, other := range s.devices {
		if other == d || other.from > s.hourIndex(hour) || other.to <= s.hourIndex(hour) {
			continue
		}
		switch {
		case d.kind.name == "laptop" && other.kind.name == "nas" && s.rng.Float64() < 0.3:
			key := flowKey{other.ip, 6, 445}
			local[key] = other.mac
			flows = append(flows, plannedFlow{key: key, bytes: s.bytes(d.kind, 50)})
		case d.kind.name == "phone" && other.kind.name == "tv" && s.rng.Float64() < 0.05:
			key := flowKey{other.ip, 6, 8009}
			local[key] = other.mac
			flows = append(flows, plannedFlow{key: key, bytes: 20e3})
		}
	}

	for _, f := range flows {
		dstMAC := local[f.key]
		if !perMinute {
			out(dstMAC, f.key.peer, f.key.proto, f.key.port, f.bytes, hour, down)
			continue
		}
		var minutes []int
		if f.persistent {
			for m := range 60 {
				if s.rng.Float64() < 0.8 {
					minutes = append(minutes, m)
				}
			}
		} else {
			first := s.rng.IntN(60)
			for m := first; m < min(60, first+1+int(s.rng.ExpFloat64()*1.5)); m++ {
				minutes = append(minutes, m)
			}
		}
		for _, m := range minutes {
			at := hour.Add(time.Duration(m)*time.Minute + time.Duration(s.rng.IntN(900))*time.Millisecond)
			if !at.Before(end) {
				break
			}
			out(dstMAC, f.key.peer, f.key.proto, f.key.port, f.bytes/float64(len(minutes)), at, down)
		}
	}
}

func (s *seeder) hourIndex(hour time.Time) int {
	return int(hour.Sub(s.start) / time.Hour)
}

// inbound is traffic from the internet to the NAS (port forwarding) and the
// router's own traffic, neither of which has a source MAC address.
func (s *seeder) other(hour time.Time, emit func(row)) {
	for _, d := range s.devices {
		if d.kind.name != "nas" || d.from > s.hourIndex(hour) || d.to <= s.hourIndex(hour) {
			continue
		}
		for range poisson(s.rng, 4) {
			b := uint64(s.bytes(d.kind, 1))
			emit(row{srcIP: randomPublic(s.rng).AsSlice(), dstMAC: d.mac, dstIP: d.ip.AsSlice(), proto: 6, port: 443,
				inBytes: b, inPackets: s.packets(b, 1200), outBytes: b / 10, outPackets: s.packets(b/10, 300), createdAt: hour})
		}
	}
	for _, upstream := range []string{"8.8.8.8", "1.1.1.1", "9.9.9.9"} {
		emit(row{srcIP: s.wanIP.AsSlice(), dstIP: netip.MustParseAddr(upstream).AsSlice(), proto: 17, port: 53,
			inBytes: 200e3, inPackets: 1500, outBytes: 80e3, outPackets: 1500, createdAt: hour})
	}
}

const stagingTable = `CREATE TABLE staging(
	src_mac BLOB, dst_mac BLOB, src_ip BLOB, dst_ip BLOB, ip_proto UTINYINT, port USMALLINT,
	in_bytes UBIGINT, in_packets UBIGINT, out_bytes UBIGINT, out_packets UBIGINT, created_at TIMESTAMPTZ)`

func blob(b []byte) driver.Value {
	if b == nil {
		return nil
	}
	return b
}

func seedCommand(args []string) {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	out := fs.String("out", "flows.db", "database file to create")
	nDevices := fs.Int("devices", 40, "devices on the network")
	days := fs.Int("days", 30, "days of history")
	nPeers := fs.Int("peers", 0, "distinct remote addresses (default 2500 per device)")
	alerts := fs.Int("alerts", 5000, "resolved alerts in the history")
	seed := fs.Uint64("seed", 1, "random seed")
	_ = fs.Parse(args)

	if _, err := os.Stat(*out); err == nil {
		log.Fatalf("%s exists", *out)
	}
	if *nPeers == 0 {
		*nPeers = 2500 * *nDevices
	}

	began := time.Now()
	end := time.Now().UTC().Truncate(time.Minute)
	// Like after compaction: the previous hour and the current one are per
	// minute, everything before is hourly.
	rawFrom := end.Add(-time.Hour).Truncate(time.Hour)
	start := end.Truncate(time.Hour).Add(-time.Duration(*days) * 24 * time.Hour)

	rng := rand.New(rand.NewPCG(*seed, 42))
	s := &seeder{rng: rng, start: start, nHours: int(end.Sub(start) / time.Hour),
		router: netip.MustParseAddr("192.168.1.1"), wanIP: netip.MustParseAddr("100.70.12.34")}
	s.peers = make([]netip.Addr, *nPeers)
	popularApps := appAddresses(rng, 2000)
	for i := range s.peers {
		if (i < 3000 && rng.Float64() < 0.4) || rng.Float64() < 0.03 {
			s.peers[i] = popularApps[rng.IntN(len(popularApps))]
		} else {
			s.peers[i] = randomPublic(rng)
		}
	}
	s.appPeers = appAddresses(rng, 300)
	s.zipf = rand.NewZipf(rng, 1.07, 1, uint64(*nPeers-1))

	// The household, there all along, then guests and phones that rotate
	// their private address, which join for a while.
	for i := range kinds {
		k := &kinds[i]
		n := int(math.Round(k.share * float64(*nDevices)))
		if k.name == "nas" {
			n = max(n, 1)
		}
		for range n {
			s.addDevice(k, 0, s.nHours+1)
		}
	}
	for _, d := range s.devices {
		if d.kind.name == "iot" {
			d.telnet = true
			break
		}
	}
	// A camera that went quiet three hours ago, for inactivity rules.
	for i := len(s.devices) - 1; i >= 0; i-- {
		if s.devices[i].kind.name == "iot" {
			s.devices[i].to = s.nHours - 3
			s.devices[i].name = "Garden camera"
			break
		}
	}
	for h := 0; h < s.nHours; h += 48 + rng.IntN(48) {
		s.addDevice(&kinds[1], h, h+1+rng.IntN(48))
	}
	// Two guests who just joined, for new device rules.
	s.addDevice(&kinds[1], s.nHours-1, s.nHours+1)
	s.addDevice(&kinds[1], s.nHours-1, s.nHours+1)

	connector, err := duckdb.NewConnector(*out, nil)
	if err != nil {
		log.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	if _, err := migrate.Exec(db, "sqlite3", migrate.FileMigrationSource{Dir: migrationsDir()}, migrate.Up); err != nil {
		log.Fatal(err)
	}

	conn, err := db.Conn(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	exec := func(q string, args ...any) {
		if _, err := conn.ExecContext(context.Background(), q, args...); err != nil {
			log.Fatalf("%s: %v", q, err)
		}
	}
	exec(stagingTable)

	var hourlyRows, minuteRows int64
	load := func(table string, fill func(emit func(row))) {
		var app *duckdb.Appender
		err := conn.Raw(func(dc any) error {
			var err error
			app, err = duckdb.NewAppenderFromConn(dc.(driver.Conn), "", "staging")
			return err
		})
		if err != nil {
			log.Fatal(err)
		}
		fill(func(r row) {
			if err := app.AppendRow(blob(r.srcMAC), blob(r.dstMAC), r.srcIP, r.dstIP, r.proto, r.port,
				r.inBytes, r.inPackets, r.outBytes, r.outPackets, r.createdAt); err != nil {
				log.Fatal(err)
			}
		})
		if err := app.Close(); err != nil {
			log.Fatal(err)
		}
		exec(`INSERT INTO ` + table + ` SELECT * FROM staging ORDER BY created_at`)
		exec(`DELETE FROM staging`)
	}

	// Hourly rows a day at a time, oldest first, as compaction writes them.
	for day := start; day.Before(rawFrom); day = day.Add(24 * time.Hour) {
		load("flows_hourly", func(emit func(row)) {
			count := func(r row) { hourlyRows++; emit(r) }
			for hour := day; hour.Before(day.Add(24*time.Hour)) && hour.Before(rawFrom); hour = hour.Add(time.Hour) {
				h := s.hourIndex(hour)
				for _, d := range s.devices {
					if d.from <= h && h < d.to {
						s.rowsFor(d, hour, false, end, count)
					}
				}
				s.other(hour, count)
			}
		})
	}
	load("flows", func(emit func(row)) {
		count := func(r row) { minuteRows++; emit(r) }
		for hour := rawFrom; hour.Before(end); hour = hour.Add(time.Hour) {
			h := s.hourIndex(hour)
			for _, d := range s.devices {
				if d.from <= h && h < d.to {
					// The guests joined 20 and 45 minutes ago.
					from := hour
					if d.from == s.nHours-1 && d.to == s.nHours+1 {
						from = end.Add(-time.Duration(20+25*(len(s.devices)-1-indexOf(s.devices, d))) * time.Minute)
					}
					s.rowsFor(d, hour, true, end, func(r row) {
						if !r.createdAt.Before(from) {
							count(r)
						}
					})
				}
			}
			var other []row
			s.other(hour, func(r row) { other = append(other, r) })
			for m := range 60 {
				at := hour.Add(time.Duration(m) * time.Minute)
				if !at.Before(end) {
					break
				}
				for _, r := range other {
					r.createdAt = at
					r.inBytes, r.outBytes = r.inBytes/60, r.outBytes/60
					count(r)
				}
			}
		}
	})

	// Devices as migration 004 seeds them, names, and a history of alerts.
	exec(`DELETE FROM devices`)
	exec(`INSERT INTO devices
		SELECT mac, arg_max(ip, created_at), min(created_at), max(created_at)
		FROM (
			SELECT src_mac AS mac, src_ip AS ip, created_at FROM all_flows WHERE octet_length(src_mac) > 0
			UNION ALL
			SELECT dst_mac, dst_ip, created_at FROM all_flows WHERE octet_length(dst_mac) > 0
		)
		GROUP BY mac`)
	for _, d := range s.devices {
		if d.name != "" {
			exec(`INSERT INTO device_names VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, []byte(d.mac), d.name, start)
		}
	}
	// Resolved alerts of rules that no longer exist, spread over the 30 days
	// alerts are kept by default.
	step := 30 * 24 * 3600 / max(1, *alerts)
	exec(`INSERT INTO alerts (rule_id, rule_name, subject, state, severity, value, title, body, labels,
			started_at, last_evaluated_at, last_notified_at, resolved_at)
		SELECT 9000 + i % 5, 'Old rule', 'subject ' || (i % 300), 'resolved', 'warning', i, 'Title', 'Body', '{}',
			?::TIMESTAMPTZ - to_seconds(i * ?), ?::TIMESTAMPTZ - to_seconds(i * ?), NULL, ?::TIMESTAMPTZ - to_seconds(i * ?)
		FROM range(?) t(i)`, end, step, end, step, end, step, *alerts)
	exec(`INSERT INTO notification_deliveries (alert_id, channel_id, kind, status, attempts, error, created_at)
		SELECT id, 1, 'firing', 'sent', 1, NULL, started_at FROM alerts`)
	exec(`CREATE TABLE bench_meta(end_time TIMESTAMPTZ, devices INTEGER, days INTEGER)`)
	exec(`INSERT INTO bench_meta VALUES (?, ?, ?)`, end, *nDevices, *days)
	exec(`DROP TABLE staging`)
	exec(`CHECKPOINT`)

	var devices int
	_ = db.QueryRow(`SELECT count(*) FROM devices`).Scan(&devices)
	info, _ := os.Stat(*out)
	log.Printf("Seeded %s in %s: %d devices seen, %d hourly rows, %d per-minute rows, %.0f MB, data ends at %s",
		*out, time.Since(began).Round(time.Second), devices, hourlyRows, minuteRows, float64(info.Size())/1e6, end.Format(time.RFC3339))
}

func indexOf(devices []*device, d *device) int {
	for i, x := range devices {
		if x == d {
			return i
		}
	}
	return -1
}
