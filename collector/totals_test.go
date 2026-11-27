package collector

import (
	"net"
	"testing"
)

func TestTotals(t *testing.T) {
	laptop := net.HardwareAddr{0xa4, 0x83, 0xe7, 0x1c, 0x2f, 0x90}
	nas := net.HardwareAddr{0x00, 0x11, 0x32, 0x9b, 0xc4, 0x01}

	totals := NewTotals()
	totals.AddFlow(laptop, nil, 1000, 100) // to the internet
	totals.AddFlow(laptop, nas, 50, 9000)  // a backup to the NAS
	totals.AddFlow(nil, nas, 20, 300)      // from the internet to the NAS

	devices := totals.Devices()
	if got := devices[laptop.String()]; got != (DeviceTotal{Received: 1050, Sent: 9100}) {
		t.Errorf("laptop = %+v, want 1050 received and 9100 sent", got)
	}
	if got := devices[nas.String()]; got != (DeviceTotal{Received: 9300, Sent: 70}) {
		t.Errorf("NAS = %+v, want 9300 received and 70 sent", got)
	}

	var none *Totals
	none.AddFlow(laptop, nil, 1, 1)
	if none.Devices() != nil {
		t.Error("a nil Totals has devices")
	}
}
