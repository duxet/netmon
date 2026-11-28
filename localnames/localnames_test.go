package localnames

import (
	"context"
	"database/sql"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	migrate "github.com/rubenv/sql-migrate"
)

var t0 = time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

func mac(t *testing.T, s string) net.HardwareAddr {
	t.Helper()
	m, err := net.ParseMAC(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func newNames() *Names {
	n := New()
	n.now = func() time.Time { return t0 }
	return n
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := migrate.Exec(db, "sqlite3", &migrate.FileMigrationSource{Dir: "../migrations"}, migrate.Up); err != nil {
		t.Fatal(err)
	}
	return db
}

func addDevice(t *testing.T, db *sql.DB, m net.HardwareAddr, ip string, seen time.Time) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO devices VALUES (?, ?, ?, ?)`, []byte(m), netip.MustParseAddr(ip).AsSlice(), seen, seen); err != nil {
		t.Fatal(err)
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		" Johns-iPhone ":         "Johns-iPhone",
		"nas.lan.":               "nas.lan",
		"*":                      "",
		"":                       "",
		"bad\x00name":            "",
		strings.Repeat("a", 254): "",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLearn(t *testing.T) {
	n := newNames()
	phone := mac(t, "aa:bb:cc:dd:ee:01")
	addr := netip.MustParseAddr("192.168.1.10")

	n.Learn(Observation{Addr: addr, Name: "Johns-iPhone.local", Source: MDNS, Until: t0.Add(time.Hour)})
	if got := n.ByIP(addr); got != "Johns-iPhone.local" {
		t.Errorf("ByIP = %q, want the mDNS name", got)
	}
	if got := n.ByMAC(phone); got != "" {
		t.Errorf("ByMAC = %q, want none: mDNS doesn't tell MAC addresses", got)
	}

	n.Learn(Observation{MAC: phone, Addr: addr, Name: "Johns-iPhone", Source: DHCP, Until: t0.Add(time.Hour)})
	if got := n.ByIP(netip.MustParseAddr("::ffff:192.168.1.10")); got != "Johns-iPhone" {
		t.Errorf("ByIP = %q, want the DHCP name, which ranks higher", got)
	}
	n.Learn(Observation{Addr: addr, Name: "Other.local", Source: MDNS, Until: t0.Add(time.Hour)})
	if got := n.ByIP(addr); got != "Johns-iPhone" {
		t.Errorf("ByIP = %q, want the DHCP name kept", got)
	}
	n.Learn(Observation{MAC: phone, Name: "Phone.local", Source: MDNS})
	if got := n.ByMAC(phone); got != "Johns-iPhone" {
		t.Errorf("ByMAC = %q, want the DHCP name kept", got)
	}

	// A lease that ended names the device, but not the address.
	laptop := mac(t, "aa:bb:cc:dd:ee:02")
	old := netip.MustParseAddr("192.168.1.20")
	n.Learn(Observation{MAC: laptop, Addr: old, Name: "laptop", Source: DHCP, Until: t0.Add(-time.Minute)})
	if got := n.ByMAC(laptop); got != "laptop" {
		t.Errorf("ByMAC = %q, want laptop", got)
	}
	if got := n.ByIP(old); got != "" {
		t.Errorf("ByIP = %q, want none for an ended lease", got)
	}

	// Leases expire.
	n.now = func() time.Time { return t0.Add(2 * time.Hour) }
	if got := n.ByIP(netip.MustParseAddr("192.168.1.10")); got != "" {
		t.Errorf("ByIP = %q after the lease expired, want none (no saved device has the address)", got)
	}

	var none *Names
	none.Learn(Observation{Name: "x", Source: DHCP})
	if none.ByIP(addr) != "" || none.ByMAC(phone) != "" || none.Flush(nil) != nil {
		t.Error("a nil Names knows something")
	}
}

func TestFlush(t *testing.T) {
	db := openTestDB(t)
	phone, tv, printer := mac(t, "aa:bb:cc:dd:ee:01"), mac(t, "aa:bb:cc:dd:ee:02"), mac(t, "aa:bb:cc:dd:ee:03")
	addDevice(t, db, phone, "192.168.1.10", t0)
	addDevice(t, db, tv, "192.168.1.11", t0)

	n := newNames()
	n.Learn(Observation{MAC: phone, Addr: netip.MustParseAddr("192.168.1.10"), Name: "Johns-iPhone", Source: DHCP})
	n.Learn(Observation{Addr: netip.MustParseAddr("192.168.1.11"), Name: "Living-Room.local", Source: MDNS, Until: t0.Add(time.Hour)})
	// No device has used this address, so it has no MAC address to save under.
	n.Learn(Observation{Addr: netip.MustParseAddr("192.168.1.99"), Name: "ghost.local", Source: MDNS, Until: t0.Add(time.Hour)})
	n.Learn(Observation{MAC: printer, Name: "printer", Source: DHCP})
	// An mDNS name for the phone's address doesn't replace its DHCP name.
	n.Learn(Observation{Addr: netip.MustParseAddr("192.168.1.10"), Name: "iPhone.local", Source: MDNS, Until: t0.Add(time.Hour)})
	if err := n.Flush(db); err != nil {
		t.Fatal(err)
	}

	rows, err := db.Query(`SELECT name || ' ' || source FROM device_hostnames ORDER BY mac`)
	if err != nil {
		t.Fatal(err)
	}
	var saved []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		saved = append(saved, s)
	}
	rows.Close()
	if want := []string{"Johns-iPhone dhcp", "Living-Room.local mdns", "printer dhcp"}; !slices.Equal(saved, want) {
		t.Errorf("saved %q, want %q", saved, want)
	}

	// A new Names reads them back, by MAC address and by the address each
	// device used last.
	loaded := newNames()
	if err := loaded.Load(db); err != nil {
		t.Fatal(err)
	}
	if got := loaded.ByMAC(tv); got != "Living-Room.local" {
		t.Errorf("ByMAC(tv) = %q", got)
	}
	if got := loaded.ByIP(netip.MustParseAddr("192.168.1.10")); got != "Johns-iPhone" {
		t.Errorf("ByIP(phone's address) = %q", got)
	}

	// A Names that doesn't know the phone's DHCP name yet doesn't replace
	// it with an mDNS one in the database either.
	fresh := newNames()
	fresh.Learn(Observation{Addr: netip.MustParseAddr("192.168.1.10"), Name: "iPhone.local", Source: MDNS, Until: t0.Add(time.Hour)})
	if err := fresh.Flush(db); err != nil {
		t.Fatal(err)
	}
	if got := fresh.ByMAC(phone); got != "Johns-iPhone" {
		t.Errorf("ByMAC(phone) = %q, want the saved DHCP name", got)
	}

	// Another device took the TV's address since, so the address isn't the
	// TV's name any more.
	addDevice(t, db, mac(t, "aa:bb:cc:dd:ee:04"), "192.168.1.11", t0.Add(time.Hour))
	// The phone was forgotten.
	if _, err := db.Exec(`DELETE FROM device_hostnames WHERE mac = ?`, []byte(phone)); err != nil {
		t.Fatal(err)
	}
	if err := loaded.Flush(db); err != nil {
		t.Fatal(err)
	}
	if got := loaded.ByIP(netip.MustParseAddr("192.168.1.11")); got != "" {
		t.Errorf("ByIP(TV's old address) = %q, want none", got)
	}
	if got := loaded.ByMAC(phone); got != "" {
		t.Errorf("ByMAC(phone) = %q after it was forgotten, want none", got)
	}
}

func TestParseDnsmasq(t *testing.T) {
	leases, err := ParseLeases(strings.NewReader(`1764331200 aa:bb:cc:dd:ee:01 192.168.1.10 Johns-iPhone 01:aa:bb:cc:dd:ee:01
0 aa:bb:cc:dd:ee:02 192.168.1.11 * *
duid 00:01:00:01:2c:3d:4e:5f:aa:bb:cc:dd:ee:ff
1764331200 1234567 fd00::10 laptop 00:01:00:01
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 2 {
		t.Fatalf("leases = %+v, want 2 (DHCPv6 has no MAC address)", leases)
	}
	if l := leases[0]; l.MAC.String() != "aa:bb:cc:dd:ee:01" || l.Addr.String() != "192.168.1.10" || l.Name != "Johns-iPhone" || !l.Expires.Equal(time.Unix(1764331200, 0)) {
		t.Errorf("lease = %+v", l)
	}
	if l := leases[1]; l.Name != "*" || !l.Expires.IsZero() {
		t.Errorf("lease = %+v, want no expiry", l)
	}
}

func TestParseISC(t *testing.T) {
	leases, err := ParseLeases(strings.NewReader(`# The format of this file is documented in the dhcpd.leases(5) manual page.
authoring-byte-order little-endian;

lease 192.168.1.10 {
  starts 4 2026/11/26 12:00:00;
  ends 4 2026/11/26 13:00:00;
  binding state free;
  hardware ethernet aa:bb:cc:dd:ee:01;
  client-hostname "old-name";
}
lease 192.168.1.10 {
  starts 5 2026/11/28 12:00:00;
  ends 5 2026/11/28 14:00:00;
  binding state active;
  next binding state free;
  rewind binding state free;
  hardware ethernet aa:bb:cc:dd:ee:01;
  uid "\001\252\273\314\335\356\001";
  client-hostname "Johns-iPhone";
}
lease 192.168.1.11 {
  ends never;
  binding state active;
  hardware ethernet aa:bb:cc:dd:ee:02;
}
lease 192.168.1.12 {
  ends epoch 1764331200; # Fri Nov 28 12:00:00 2026
  binding state expired;
  hardware ethernet aa:bb:cc:dd:ee:03;
  client-hostname "gone";
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 3 {
		t.Fatalf("leases = %+v, want 3", leases)
	}
	if l := leases[0]; l.Name != "Johns-iPhone" || l.MAC.String() != "aa:bb:cc:dd:ee:01" || !l.Expires.Equal(time.Date(2026, 11, 28, 14, 0, 0, 0, time.UTC)) {
		t.Errorf("lease = %+v, want the last one of the address", l)
	}
	if l := leases[1]; l.Name != "" || !l.Expires.IsZero() {
		t.Errorf("lease = %+v", l)
	}
	if l := leases[2]; l.Name != "gone" || !l.Expires.Before(t0) {
		t.Errorf("lease = %+v, want one that ended", l)
	}
}

func TestParseKea(t *testing.T) {
	leases, err := ParseLeases(strings.NewReader(`address,hwaddr,client_id,valid_lifetime,expire,subnet_id,fqdn_fwd,fqdn_rev,hostname,state,user_context,pool_id
192.168.1.10,aa:bb:cc:dd:ee:01,,3600,1764331200,1,0,0,johns-iphone.lan.,0,,0
192.168.1.11,aa:bb:cc:dd:ee:02,,3600,1764331200,1,0,0,tv,2,,0
192.168.1.10,aa:bb:cc:dd:ee:01,,3600,1764334800,1,0,0,johns-iphone.lan.,0,,0
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 2 {
		t.Fatalf("leases = %+v", leases)
	}
	if l := leases[0]; l.Name != "johns-iphone.lan." || !l.Expires.Equal(time.Unix(1764334800, 0)) {
		t.Errorf("lease = %+v, want the last one of the address", l)
	}
	if l := leases[1]; !l.Expires.Before(t0) {
		t.Errorf("lease = %+v, want an expired one", l)
	}
}

func TestParseBootpd(t *testing.T) {
	leases, err := ParseLeases(strings.NewReader(`{
	name=Johns-iPhone
	ip_address=192.168.2.3
	hw_address=1,a2:3:4:5:6:7
	identifier=1,a2:3:4:5:6:7
	lease=0x6929a2c0
}
{
	name=nomac
	ip_address=192.168.2.4
	hw_address=ff,0:1:2
	lease=0x6929a2c0
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 {
		t.Fatalf("leases = %+v", leases)
	}
	if l := leases[0]; l.Name != "Johns-iPhone" || l.MAC.String() != "a2:03:04:05:06:07" || l.Addr.String() != "192.168.2.3" || !l.Expires.Equal(time.Unix(0x6929a2c0, 0)) {
		t.Errorf("lease = %+v", l)
	}
}

func TestLeaseFiles(t *testing.T) {
	if got := LeaseFiles("auto"); !slices.Equal(got, DefaultLeaseFiles) {
		t.Errorf("auto = %v", got)
	}
	if got := LeaseFiles(" off "); got != nil {
		t.Errorf("off = %v", got)
	}
	if got := LeaseFiles("/a, /b ,"); !slices.Equal(got, []string{"/a", "/b"}) {
		t.Errorf("list = %v", got)
	}
}

func TestWatchLeases(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dhcp.leases")
	if err := os.WriteFile(path, []byte("0 aa:bb:cc:dd:ee:01 192.168.1.10 nas *\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n := New()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		WatchLeases(ctx, []string{filepath.Join(dir, "missing"), path}, true, n)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for n.ByIP(netip.MustParseAddr("192.168.1.10")) != "nas" {
		if time.Now().After(deadline) {
			t.Fatal("the lease wasn't read")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

func mdnsResponse(t *testing.T, records ...layers.DNSResourceRecord) []byte {
	t.Helper()
	dns := layers.DNS{QR: true, AA: true, Answers: records[:1], Additionals: records[1:]}
	buf := gopacket.NewSerializeBuffer()
	if err := dns.SerializeTo(buf, gopacket.SerializeOptions{FixLengths: true}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestObserveMDNS(t *testing.T) {
	n := newNames()
	a := func(name, ip string, ttl uint32) layers.DNSResourceRecord {
		addr := netip.MustParseAddr(ip)
		kind := layers.DNSTypeA
		if addr.Is6() {
			kind = layers.DNSTypeAAAA
		}
		return layers.DNSResourceRecord{Name: []byte(name), Type: kind, Class: layers.DNSClassIN, TTL: ttl, IP: addr.AsSlice()}
	}
	n.ObserveMDNS(mdnsResponse(t,
		layers.DNSResourceRecord{Name: []byte("_airplay._tcp.local"), Type: layers.DNSTypePTR, Class: layers.DNSClassIN, TTL: 4500, PTR: []byte("TV._airplay._tcp.local")},
		a("Living-Room-TV.local", "192.168.1.11", 120),
		a("Living-Room-TV.local", "fd00::11", 120),
		a("1b2c3d4e-5f60-7182-93a4-b5c6d7e8f901.local", "192.168.1.12", 120),
		a("example.com", "192.168.1.13", 120),
		a("Leaving.local", "192.168.1.14", 0),
	), t0)

	for ip, want := range map[string]string{
		"192.168.1.11": "Living-Room-TV.local",
		"fd00::11":     "Living-Room-TV.local",
		"192.168.1.12": "",
		"192.168.1.13": "",
		"192.168.1.14": "",
	} {
		if got := n.ByIP(netip.MustParseAddr(ip)); got != want {
			t.Errorf("ByIP(%s) = %q, want %q", ip, got, want)
		}
	}

	// Queries, ours included, teach nothing.
	query, err := mdnsQuery()
	if err != nil {
		t.Fatal(err)
	}
	var decoded layers.DNS
	if err := decoded.DecodeFromBytes(query, gopacket.NilDecodeFeedback); err != nil || decoded.QR || len(decoded.Questions) != len(mdnsServices) {
		t.Errorf("query = %+v, %v", decoded, err)
	}
	n.ObserveMDNS(query, t0)
	n.ObserveMDNS([]byte("garbage"), t0)
}
