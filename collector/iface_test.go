package collector

import (
	"net"
	"net/netip"
	"testing"
)

func TestParseRouteInterface(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		want   string
	}{
		{
			name: "macOS",
			output: `   route to: default
destination: default
       mask: default
    gateway: 192.168.1.1
  interface: en0
      flags: <UP,GATEWAY,DONE,STATIC,PRIVSCOPE>
 recvpipe  sendpipe  ssthresh  rtt,msec    rttvar  hopcount      mtu     expire
       0         0         0         0         0         0      1500         0
`,
			want: "en0",
		},
		{
			name: "IPv6 default route over a VPN",
			output: `   route to: default
destination: default
       mask: default
    gateway: fe80::1%utun4
  interface: utun4
`,
			want: "utun4",
		},
		{
			name:   "no default route",
			output: "route: writing to routing socket: not in table\n",
			want:   "",
		},
		{
			name:   "empty",
			output: "",
			want:   "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRouteInterface(tc.output); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func loopbackName(t *testing.T) string {
	t.Helper()

	ifaces, err := net.Interfaces()
	if err != nil {
		t.Skip("cannot list interfaces:", err)
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 && iface.Flags&net.FlagUp != 0 {
			return iface.Name
		}
	}

	t.Skip("no loopback interface")

	return ""
}

// Packets carry plain 4 byte IPv4 addresses, so the prefixes must hold them in
// that form, even though the standard library may hand out the 16 byte one.
func TestLocalPrefixes(t *testing.T) {
	prefixes, err := localPrefixes(loopbackName(t))
	if err != nil {
		t.Fatal(err)
	}

	contains := func(ip netip.Addr) bool {
		for _, p := range prefixes {
			if p.Contains(ip) {
				return true
			}
		}

		return false
	}

	if !contains(netip.MustParseAddr("127.0.0.1")) {
		t.Errorf("127.0.0.1 is not in %v", prefixes)
	}
	if contains(netip.MustParseAddr("8.8.8.8")) {
		t.Errorf("8.8.8.8 is in %v", prefixes)
	}
}

func TestLocalPrefixesOfUnknownInterface(t *testing.T) {
	if _, err := localPrefixes("nosuchif0"); err == nil {
		t.Error("expected an error")
	}
}
