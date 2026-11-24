package api

import (
	"testing"

	"github.com/duxet/netmon/apps"
	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/storage"
)

func ip(t *testing.T, s string) common.IPAddress {
	t.Helper()
	addr, err := common.ParseIPAddress(s)
	if err != nil {
		t.Fatal(err)
	}
	return *addr
}

func pair(t *testing.T, src, dst string, inBytes, inPackets, outBytes, outPackets uint64) storage.EndpointPairTrafficRecord {
	return storage.EndpointPairTrafficRecord{
		SourceIPAddress:      ip(t, src),
		DestinationIPAddress: ip(t, dst),
		InBytes:              inBytes,
		InPackets:            inPackets,
		OutBytes:             outBytes,
		OutPackets:           outPackets,
	}
}

func TestAggregateAppTraffic(t *testing.T) {
	classifier := apps.NewClassifier(apps.Known, "")

	records := []storage.EndpointPairTrafficRecord{
		// Outbound to Telegram.
		pair(t, "192.168.1.10", "149.154.167.51", 1000, 10, 100, 1),
		pair(t, "192.168.1.11", "91.108.56.1", 500, 5, 50, 1),
		// Inbound from Telegram: directions are swapped.
		pair(t, "149.154.167.51", "192.168.1.10", 7, 1, 70, 2),
		// Outbound to YouTube.
		pair(t, "192.168.1.10", "142.250.186.78", 5000, 50, 200, 2),
		// Unknown remote host.
		pair(t, "192.168.1.10", "1.1.1.1", 9000, 90, 900, 9),
		// Local only: ignored.
		pair(t, "192.168.1.10", "192.168.1.1", 100000, 1, 1, 1),
	}

	got := aggregateAppTraffic(classifier, records)

	want := []AppTrafficDTO{
		{"YouTube / Google", TrafficDTO{5000, 50, 200, 2}},
		{"Telegram", TrafficDTO{1570, 17, 157, 3}},
		{apps.Other, TrafficDTO{9000, 90, 900, 9}},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d apps (%+v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("app %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
