package domains

import (
	"context"
	"database/sql"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	migrate "github.com/rubenv/sql-migrate"
)

var t0 = time.Date(2026, 11, 27, 22, 0, 0, 0, time.UTC)

func TestNames(t *testing.T) {
	n := New()
	addr := netip.MustParseAddr("45.57.40.1")
	n.Observe(addr, "ipv4-c001.1.lhr004.ix.nflxvideo.net.", t0)
	n.Observe(addr, "WWW.Netflix.com", t0.Add(time.Second))
	for i := range 5 {
		n.Observe(netip.MustParseAddr("1.1.1.1"), string(rune('a'+i))+".example", t0.Add(time.Duration(i)*time.Second))
	}

	if got := n.Names(netip.MustParseAddr("::ffff:45.57.40.1")); !slices.Equal(got, []string{"www.netflix.com", "ipv4-c001.1.lhr004.ix.nflxvideo.net"}) {
		t.Errorf("names = %v, want the newest first, lowercase", got)
	}
	if got := n.Names(netip.MustParseAddr("1.1.1.1")); len(got) != maxNames || got[0] != "e.example" {
		t.Errorf("names = %v, want the 4 newest", got)
	}
	var none *Names
	if none.Name(addr) != "" {
		t.Error("a nil Names knows a name")
	}
}

func TestDnsmasqLog(t *testing.T) {
	n := New()
	p := &dnsmasqParser{names: n}
	for _, line := range []string{
		"Nov 27 22:00:01 dnsmasq[1234]: query[A] www.netflix.com from 192.168.1.23",
		"Nov 27 22:00:01 dnsmasq[1234]: forwarded www.netflix.com to 1.1.1.1",
		"Nov 27 22:00:01 dnsmasq[1234]: reply www.netflix.com is <CNAME>",
		"Nov 27 22:00:01 dnsmasq[1234]: reply www.dradis.netflix.com is <CNAME>",
		"Nov 27 22:00:01 dnsmasq[1234]: reply ipv4-c001.ix.nflxvideo.net is 45.57.40.1",
		"Nov 27 22:00:01 dnsmasq[1234]: reply ipv4-c001.ix.nflxvideo.net is 45.57.40.2",
		"Fri Nov 27 22:00:02 2026 daemon.info dnsmasq[1234]: query[A] www.google.com from 192.168.1.23",
		"Fri Nov 27 22:00:02 2026 daemon.info dnsmasq[1234]: cached www.google.com is 142.250.186.78",
		"Fri Nov 27 22:00:02 2026 daemon.info dnsmasq[1234]: reply ads.example is NXDOMAIN",
		"Fri Nov 27 22:00:02 2026 daemon.info dnsmasq[1234]: reply one.one.one.one is 2606:4700:4700::1111",
		"Nov 27 22:00:03 kernel: something else",
	} {
		p.line(line, t0)
	}

	for addr, want := range map[string]string{
		"45.57.40.1":           "www.netflix.com",
		"45.57.40.2":           "www.netflix.com",
		"142.250.186.78":       "www.google.com",
		"2606:4700:4700::1111": "one.one.one.one",
	} {
		if got := n.Name(netip.MustParseAddr(addr)); got != want {
			t.Errorf("name of %s = %q, want %q", addr, got, want)
		}
	}
	if names := n.Names(netip.MustParseAddr("45.57.40.1")); !slices.Contains(names, "ipv4-c001.ix.nflxvideo.net") {
		t.Errorf("names = %v, want the record's own name too", names)
	}
}

func TestFollowFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dnsmasq.log")
	if err := os.WriteFile(path, []byte("Nov 27 21:00:00 dnsmasq[1]: reply old.example is 9.9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Follow(ctx, path, n)

	waitFor := func(addr, name string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for n.Name(netip.MustParseAddr(addr)) != name {
			if time.Now().After(deadline) {
				t.Fatalf("never learned %s for %s", name, addr)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	appendLine := func(line string) {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString(line + "\n")
		f.Close()
	}

	time.Sleep(200 * time.Millisecond)
	appendLine("Nov 27 22:00:00 dnsmasq[1]: reply new.example is 8.8.8.8")
	waitFor("8.8.8.8", "new.example")
	if n.Name(netip.MustParseAddr("9.9.9.9")) != "" {
		t.Error("learned from what was in the log before it was followed")
	}

	// Rotated: the new file is read from its start.
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	appendLine("Nov 27 22:01:00 dnsmasq[1]: reply rotated.example is 7.7.7.7")
	waitFor("7.7.7.7", "rotated.example")
}

func TestObserveDNS(t *testing.T) {
	dns := &layers.DNS{
		ID: 1, QR: true, ResponseCode: layers.DNSResponseCodeNoErr,
		Questions: []layers.DNSQuestion{{Name: []byte("www.netflix.com"), Type: layers.DNSTypeA, Class: layers.DNSClassIN}},
		Answers: []layers.DNSResourceRecord{
			{Name: []byte("www.netflix.com"), Type: layers.DNSTypeCNAME, Class: layers.DNSClassIN, TTL: 60, CNAME: []byte("edge.nflxvideo.net")},
			{Name: []byte("edge.nflxvideo.net"), Type: layers.DNSTypeA, Class: layers.DNSClassIN, TTL: 60, IP: net.ParseIP("45.57.40.1").To4()},
		},
	}
	buf := gopacket.NewSerializeBuffer()
	if err := dns.SerializeTo(buf, gopacket.SerializeOptions{FixLengths: true}); err != nil {
		t.Fatal(err)
	}

	n := New()
	n.ObserveDNS(buf.Bytes(), t0)
	if got := n.Names(netip.MustParseAddr("45.57.40.1")); !slices.Equal(got, []string{"www.netflix.com", "edge.nflxvideo.net"}) {
		t.Errorf("names = %v, want the name asked for, then the record's", got)
	}
	n.ObserveDNS([]byte("not dns"), t0)
}

func TestLoadAndFlush(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := migrate.Exec(db, "sqlite3", &migrate.FileMigrationSource{Dir: "../migrations"}, migrate.Up); err != nil {
		t.Fatal(err)
	}

	n := New()
	n.Observe(netip.MustParseAddr("45.57.40.1"), "www.netflix.com", t0)
	n.Observe(netip.MustParseAddr("45.57.40.1"), "www.netflix.com", t0.Add(time.Minute))
	n.Observe(netip.MustParseAddr("9.9.9.9"), "old.example", t0.Add(-40*24*time.Hour))
	if err := n.Flush(db, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n.Name(netip.MustParseAddr("9.9.9.9")) != "" {
		t.Error("kept a name not seen for 40 days")
	}

	again := New()
	if err := again.Load(db, t0.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if again.Name(netip.MustParseAddr("45.57.40.1")) != "www.netflix.com" || again.Len() != 1 {
		t.Errorf("loaded %d addresses, want netflix's", again.Len())
	}
}
