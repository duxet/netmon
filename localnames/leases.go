package localnames

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

// Lease is a DHCP lease: the address a DHCP server gave a device, and the
// name the device asked for.
type Lease struct {
	MAC  net.HardwareAddr
	Addr netip.Addr
	Name string
	// Expires is when the lease ends, zero for never. A lease that ended,
	// or that the server took back, has a time in the past.
	Expires time.Time
}

// Auto is the dhcp_leases setting that reads the first of DefaultLeaseFiles
// there is.
const Auto = "auto"

// DefaultLeaseFiles are where DHCP servers keep their leases, in the order
// they are looked for: dnsmasq's on OpenWrt, Debian and Pi-hole, ISC dhcpd's
// on Linux and BSD, Kea's, and macOS's (for Internet Sharing).
var DefaultLeaseFiles = []string{
	"/tmp/dhcp.leases",
	"/var/lib/misc/dnsmasq.leases",
	"/etc/pihole/dhcp.leases",
	"/var/lib/dhcp/dhcpd.leases",
	"/var/db/dhcpd.leases",
	"/var/lib/kea/kea-leases4.csv",
	"/var/db/dhcpd_leases",
}

// LeaseFiles returns the files a dhcp_leases setting names: DefaultLeaseFiles
// for Auto, none for "" or "off", or else the paths, separated by commas.
func LeaseFiles(setting string) []string {
	switch strings.TrimSpace(setting) {
	case Auto:
		return DefaultLeaseFiles
	case "", "off":
		return nil
	}
	var paths []string
	for _, path := range strings.Split(setting, ",") {
		if path = strings.TrimSpace(path); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

// maxLeaseFile bounds how much of a lease file is read. ISC dhcpd's grows
// until it rewrites it, about every hour.
const maxLeaseFile = 32 << 20

// ParseLeases reads the leases of a lease file of dnsmasq, ISC dhcpd, Kea
// (its CSV file) or macOS's bootpd, whichever it is. Where a file has several
// leases for the same address, as ISC dhcpd's and Kea's do, the last counts.
func ParseLeases(r io.Reader) ([]Lease, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxLeaseFile))
	if err != nil {
		return nil, err
	}
	first := firstLine(data)
	switch {
	case strings.HasPrefix(first, "address,"):
		return parseKea(data)
	case strings.HasPrefix(first, "{"):
		return parseBootpd(data), nil
	case bytes.Contains(data, []byte("\nlease ")) || strings.HasPrefix(first, "lease ") || strings.HasPrefix(first, "#"):
		return parseISC(data), nil
	}
	return parseDnsmasq(data), nil
}

func firstLine(data []byte) string {
	for line := range strings.Lines(string(data)) {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// lastPerAddress keeps the last lease of each address, in the order of the
// first.
func lastPerAddress(leases []Lease) []Lease {
	index := map[netip.Addr]int{}
	var result []Lease
	for _, lease := range leases {
		if i, ok := index[lease.Addr]; ok {
			result[i] = lease
			continue
		}
		index[lease.Addr] = len(result)
		result = append(result, lease)
	}
	return result
}

// parseDnsmasq reads dnsmasq's lease file, one lease per line:
//
//	1732838400 aa:bb:cc:dd:ee:ff 192.168.1.10 Johns-iPhone 01:aa:bb:cc:dd:ee:ff
//
// with 0 for a lease that never expires and * for no name. DHCPv6 leases
// have an IAID in place of the MAC address, so are skipped.
func parseDnsmasq(data []byte) []Lease {
	var leases []Lease
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		expiry, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		mac, err := net.ParseMAC(fields[1])
		if err != nil {
			continue
		}
		addr, err := netip.ParseAddr(fields[2])
		if err != nil {
			continue
		}
		lease := Lease{MAC: mac, Addr: addr.Unmap(), Name: fields[3]}
		if expiry > 0 {
			lease.Expires = time.Unix(expiry, 0)
		}
		leases = append(leases, lease)
	}
	return leases
}

// ended is the Expires of a lease that is over.
var ended = time.Unix(1, 0)

// parseISC reads ISC dhcpd's lease file, a block per lease:
//
//	lease 192.168.1.10 {
//	  ends 4 2026/11/28 12:00:00;
//	  binding state active;
//	  hardware ethernet aa:bb:cc:dd:ee:ff;
//	  client-hostname "Johns-iPhone";
//	}
func parseISC(data []byte) []Lease {
	var leases []Lease
	var current *Lease
	active := false
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if current == nil {
			if rest, ok := strings.CutPrefix(line, "lease "); ok {
				if addr, err := netip.ParseAddr(strings.TrimSpace(strings.TrimSuffix(rest, "{"))); err == nil {
					current, active = &Lease{Addr: addr.Unmap()}, true
				}
			}
			continue
		}
		if line == "}" {
			if !active {
				current.Expires = ended
			}
			if len(current.MAC) > 0 {
				leases = append(leases, *current)
			}
			current = nil
			continue
		}

		statement := strings.TrimSuffix(line, ";")
		if i := strings.Index(statement, ";"); i >= 0 {
			statement = statement[:i]
		}
		fields := strings.Fields(statement)
		switch {
		case len(fields) == 3 && fields[0] == "hardware":
			if mac, err := net.ParseMAC(fields[2]); err == nil {
				current.MAC = mac
			}
		case len(fields) >= 2 && fields[0] == "client-hostname":
			current.Name = unquote(strings.TrimSpace(strings.TrimPrefix(statement, "client-hostname")))
		case len(fields) >= 3 && fields[0] == "binding" && fields[1] == "state":
			active = fields[2] == "active"
		case len(fields) >= 2 && fields[0] == "ends":
			current.Expires = parseISCTime(fields[1:])
		}
	}
	return lastPerAddress(leases)
}

// parseISCTime reads the time of an ends statement: "never", "epoch
// 1732838400" or a weekday and a UTC time, "4 2026/11/28 12:00:00".
func parseISCTime(fields []string) time.Time {
	switch {
	case fields[0] == "never":
		return time.Time{}
	case fields[0] == "epoch" && len(fields) >= 2:
		if seconds, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
			return time.Unix(seconds, 0)
		}
	case len(fields) >= 3:
		if t, err := time.Parse("2006/01/02 15:04:05", fields[1]+" "+fields[2]); err == nil {
			return t
		}
	}
	// What can't be read is taken to be over, rather than kept forever.
	return ended
}

func unquote(s string) string {
	if unquoted, err := strconv.Unquote(s); err == nil {
		return unquoted
	}
	return strings.Trim(s, `"`)
}

// parseKea reads the CSV lease file of Kea's memfile backend. Its first line
// names the columns; a state other than 0 is a declined or expired lease.
func parseKea(data []byte) ([]Lease, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read kea leases: %w", err)
	}
	column := map[string]int{}
	for i, name := range header {
		column[strings.TrimSpace(name)] = i
	}
	for _, name := range []string{"address", "hwaddr", "expire", "hostname"} {
		if _, ok := column[name]; !ok {
			return nil, fmt.Errorf("read kea leases: no %s column", name)
		}
	}

	var leases []Lease
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			continue
		}
		get := func(name string) string {
			if i, ok := column[name]; ok && i < len(record) {
				return strings.TrimSpace(record[i])
			}
			return ""
		}
		addr, err := netip.ParseAddr(get("address"))
		if err != nil {
			continue
		}
		mac, err := net.ParseMAC(get("hwaddr"))
		if err != nil {
			continue
		}
		lease := Lease{MAC: mac, Addr: addr.Unmap(), Name: get("hostname"), Expires: ended}
		expire, err := strconv.ParseInt(get("expire"), 10, 64)
		if state := get("state"); err == nil && (state == "0" || state == "") {
			lease.Expires = time.Unix(expire, 0)
		}
		leases = append(leases, lease)
	}
	return lastPerAddress(leases), nil
}

// parseBootpd reads macOS's /var/db/dhcpd_leases, a block per lease:
//
//	{
//		name=Johns-iPhone
//		ip_address=192.168.2.3
//		hw_address=1,a2:3:4:5:6:7
//		lease=0x6748a0f0
//	}
//
// with the MAC address's leading zeros left out, and the lease's end in hex.
func parseBootpd(data []byte) []Lease {
	var leases []Lease
	var current *Lease
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == "{":
			current = &Lease{}
		case line == "}":
			if current != nil && current.Addr.IsValid() && len(current.MAC) > 0 {
				leases = append(leases, *current)
			}
			current = nil
		case current != nil:
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			switch key {
			case "name":
				current.Name = value
			case "ip_address":
				if addr, err := netip.ParseAddr(value); err == nil {
					current.Addr = addr.Unmap()
				}
			case "hw_address":
				current.MAC = parsePaddedMAC(value)
			case "lease":
				if seconds, err := strconv.ParseInt(strings.TrimPrefix(value, "0x"), 16, 64); err == nil {
					current.Expires = time.Unix(seconds, 0)
				}
			}
		}
	}
	return lastPerAddress(leases)
}

// parsePaddedMAC reads a MAC address as bootpd writes it, "1,a2:3:4:5:6:7":
// the hardware type (1 for Ethernet), then the bytes without leading zeros.
func parsePaddedMAC(value string) net.HardwareAddr {
	kind, hex, ok := strings.Cut(value, ",")
	if !ok || kind != "1" {
		return nil
	}
	parts := strings.Split(hex, ":")
	for i, part := range parts {
		if len(part) == 1 {
			parts[i] = "0" + part
		}
	}
	mac, err := net.ParseMAC(strings.Join(parts, ":"))
	if err != nil {
		return nil
	}
	return mac
}

const (
	// leasePoll is how often lease files are checked for changes.
	leasePoll = 30 * time.Second
	// leaseReread is how often a lease file is read even when it didn't
	// change, so that a name forgotten in the database comes back.
	leaseReread = 10 * time.Minute
)

// WatchLeases learns the names in the lease files until ctx is done,
// reading each again when it changes. Files that aren't there are skipped,
// and looked for again later; with auto, only the first there is is read,
// and none being there isn't worth a warning.
func WatchLeases(ctx context.Context, paths []string, auto bool, names *Names) {
	type state struct {
		modified time.Time
		size     int64
		read     time.Time
		warned   bool
	}
	states := map[string]*state{}
	poll := func() {
		for _, path := range paths {
			s := states[path]
			if s == nil {
				s = &state{}
				states[path] = s
			}
			info, err := os.Stat(path)
			if err != nil {
				if !auto && !s.warned {
					s.warned = true
					slog.Warn("Can't read DHCP leases", "path", path, "err", err)
				}
				continue
			}
			now := time.Now()
			if info.ModTime().Equal(s.modified) && info.Size() == s.size && now.Sub(s.read) < leaseReread {
				if auto {
					return
				}
				continue
			}
			leases, err := readLeases(path)
			if err != nil {
				if !s.warned {
					s.warned = true
					slog.Warn("Can't read DHCP leases", "path", path, "err", err)
				}
				continue
			}
			if s.read.IsZero() {
				slog.Info("Reading device names from DHCP leases", "path", path, "leases", len(leases))
			}
			s.modified, s.size, s.read, s.warned = info.ModTime(), info.Size(), now, false
			for _, lease := range leases {
				names.Learn(Observation{MAC: lease.MAC, Addr: lease.Addr, Name: lease.Name, Source: DHCP, Until: lease.Expires})
			}
			if auto {
				return
			}
		}
	}

	poll()
	ticker := time.NewTicker(leasePoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll()
		}
	}
}

func readLeases(path string) ([]Lease, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info, err := file.Stat(); err == nil && info.IsDir() {
		return nil, &fs.PathError{Op: "read", Path: path, Err: errors.New("is a directory")}
	}
	return ParseLeases(file)
}
