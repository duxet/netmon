//go:build linux

package collector

import (
	"errors"
	"os"
	"testing"

	"github.com/ti-mo/conntrack"
)

// The dump reads conntrack itself, so it is checked against the library's,
// where the test may read conntrack (as root).
func TestDumpConnectionsMatchesConntrack(t *testing.T) {
	c, err := conntrack.Dial(nil)
	if err != nil {
		t.Skipf("no conntrack: %v", err)
	}
	defer c.Close()

	before, err := c.Dump(nil)
	if errors.Is(err, os.ErrPermission) {
		t.Skip("reading conntrack needs CAP_NET_ADMIN")
	}
	if err != nil {
		t.Skipf("no conntrack: %v", err)
	}

	got, err := dumpConnections()
	if err != nil {
		t.Fatal(err)
	}

	after, err := c.Dump(nil)
	if err != nil {
		t.Fatal(err)
	}

	// Connections come and go between the dumps, so every connection found
	// must be one of those found before or after, with the same addresses.
	known := map[uint32]conntrack.Flow{}
	for _, flow := range append(before, after...) {
		known[flow.ID] = flow
	}
	for _, conn := range got {
		flow, ok := known[conn.id.id]
		if !ok {
			continue
		}
		want := track(flow)
		if conn.src != want.src || conn.dst != want.dst || conn.port != want.port || conn.proto != want.proto || conn.id.ports != want.id.ports {
			t.Errorf("connection %d = %+v, want %+v", conn.id.id, conn, want)
		}
	}
	if len(before) > 0 && len(got) == 0 {
		t.Errorf("found no connections, while conntrack has %d", len(before))
	}
	t.Logf("%d connections, %d before and %d after", len(got), len(before), len(after))
}
