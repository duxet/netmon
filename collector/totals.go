package collector

import (
	"net"
	"sync"
)

// Totals adds up the traffic each device sent and received in what the
// collector recorded since netmon started, for the metrics. A nil Totals
// ignores everything. It is safe for concurrent use.
type Totals struct {
	mu      sync.Mutex
	devices map[string]DeviceTotal
}

// DeviceTotal is the traffic of one device, in bytes, from its own side.
type DeviceTotal struct {
	Received uint64
	Sent     uint64
}

func NewTotals() *Totals {
	return &Totals{devices: map[string]DeviceTotal{}}
}

// Devices returns the traffic of every device, by MAC address.
func (t *Totals) Devices() map[string]DeviceTotal {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	devices := make(map[string]DeviceTotal, len(t.devices))
	for mac, total := range t.devices {
		devices[mac] = total
	}
	return devices
}

// AddFlow adds a recorded flow: what its source sent and received, which the
// destination received and sent. The ends without a MAC address aren't local
// devices.
func (t *Totals) AddFlow(srcMAC, dstMAC net.HardwareAddr, srcReceived, srcSent uint64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(srcMAC) > 0 {
		t.add(srcMAC.String(), srcReceived, srcSent)
	}
	if len(dstMAC) > 0 {
		t.add(dstMAC.String(), srcSent, srcReceived)
	}
}

func (t *Totals) add(mac string, received, sent uint64) {
	total := t.devices[mac]
	total.Received += received
	total.Sent += sent
	t.devices[mac] = total
}
