package live

import (
	"context"
	"net/netip"
	"testing"
	"time"
)

var (
	laptop = MAC{0x00, 0x11, 0x32, 0x9b, 0xc4, 0x01}
	phone  = MAC{0xa4, 0x83, 0xe7, 0x1c, 0x2f, 0x90}

	t0 = time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
)

func find(t *testing.T, sample Sample, mac MAC) DeviceRate {
	t.Helper()
	for _, d := range sample.Devices {
		if d.MAC == mac {
			return d
		}
	}
	t.Fatalf("sample at %s has no device %s: %+v", sample.Time, mac, sample.Devices)
	return DeviceRate{}
}

func TestMACString(t *testing.T) {
	if got := laptop.String(); got != "00:11:32:9b:c4:01" {
		t.Errorf("MAC = %q", got)
	}
}

func TestTickTurnsBytesIntoRates(t *testing.T) {
	m := NewMeter()
	m.Tick(t0) // starts the first interval

	m.Record(laptop, netip.MustParseAddr("192.168.1.10"), 5000, 1000)
	m.Record(laptop, netip.MustParseAddr("192.168.1.10"), 3000, 0)
	m.Record(phone, netip.Addr{}, 0, 400)
	m.Tick(t0.Add(2 * time.Second))

	samples := m.Samples(time.Time{})
	if len(samples) != 1 {
		t.Fatalf("got %d samples, want 1", len(samples))
	}

	s := samples[0]
	if !s.Time.Equal(t0.Add(2 * time.Second)) {
		t.Errorf("sample time = %s, want the end of the interval", s.Time)
	}

	if got := find(t, s, laptop); got.Download != 4000 || got.Upload != 500 {
		t.Errorf("laptop = %v down / %v up per second, want 4000/500 (8000 and 1000 bytes over 2s)", got.Download, got.Upload)
	}
	if got := find(t, s, phone); got.Download != 0 || got.Upload != 200 {
		t.Errorf("phone = %v down / %v up per second, want 0/200", got.Download, got.Upload)
	}
}

func TestDevicesAreSortedByMAC(t *testing.T) {
	m := NewMeter()
	m.Tick(t0)
	m.Record(phone, netip.Addr{}, 1, 1)
	m.Record(laptop, netip.Addr{}, 1, 1)
	m.Tick(t0.Add(time.Second))

	devices := m.Samples(time.Time{})[0].Devices
	if len(devices) != 2 || devices[0].MAC != laptop || devices[1].MAC != phone {
		t.Errorf("devices = %+v, want laptop then phone", devices)
	}
}

func TestFirstTickOnlyStartsAnInterval(t *testing.T) {
	m := NewMeter()
	m.Record(laptop, netip.Addr{}, 1_000_000, 0) // from who knows how long ago
	m.Tick(t0)

	if samples := m.Samples(time.Time{}); len(samples) != 0 {
		t.Fatalf("first tick made a sample: %+v", samples)
	}

	m.Record(laptop, netip.Addr{}, 100, 0)
	m.Tick(t0.Add(time.Second))
	if got := find(t, m.Samples(time.Time{})[0], laptop); got.Download != 100 {
		t.Errorf("download = %v, want only what was recorded after the first tick", got.Download)
	}
}

func TestIdleIntervalsAreSamplesWithoutDevices(t *testing.T) {
	m := NewMeter()
	m.Tick(t0)
	m.Tick(t0.Add(time.Second))

	samples := m.Samples(time.Time{})
	if len(samples) != 1 || len(samples[0].Devices) != 0 {
		t.Errorf("samples = %+v, want one without devices", samples)
	}
}

func TestRecordingNothingDoesNotListTheDevice(t *testing.T) {
	m := NewMeter()
	m.Tick(t0)
	m.Record(laptop, netip.Addr{}, 0, 0)
	m.Tick(t0.Add(time.Second))

	if devices := m.Samples(time.Time{})[0].Devices; len(devices) != 0 {
		t.Errorf("devices = %+v, want none", devices)
	}
}

func TestClockGoingBackwardsMakesNoSample(t *testing.T) {
	m := NewMeter()
	m.Tick(t0)
	m.Record(laptop, netip.Addr{}, 100, 0)
	m.Tick(t0.Add(-time.Second))
	m.Tick(t0)

	if samples := m.Samples(time.Time{}); len(samples) != 1 || len(samples[0].Devices) != 0 {
		t.Errorf("samples = %+v, want only the empty interval after the clock was corrected", samples)
	}
}

func TestSampleTimesAreWholeMilliseconds(t *testing.T) {
	m := NewMeter()
	m.Tick(t0)
	m.Tick(t0.Add(time.Second + 123_456_789*time.Nanosecond))

	got := m.Samples(time.Time{})[0].Time
	if want := t0.Add(time.Second + 123*time.Millisecond); !got.Equal(want) {
		t.Errorf("time = %s, want %s", got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}

func TestPrefersIPv4AndKeepsItsChoice(t *testing.T) {
	v4 := netip.MustParseAddr("192.168.1.10")
	otherV4 := netip.MustParseAddr("192.168.1.11")
	v6 := netip.MustParseAddr("2001:db8::10")

	m := NewMeter()
	m.Tick(t0)
	m.Record(laptop, v6, 1, 0)
	m.Record(laptop, netip.MustParseAddr("::ffff:192.168.1.10"), 1, 0) // an IPv4 address in IPv6 form
	m.Record(laptop, otherV4, 1, 0)
	m.Record(phone, v6, 1, 0)
	m.Record(phone, netip.MustParseAddr("2001:db8::11"), 1, 0)
	m.Tick(t0.Add(time.Second))

	s := m.Samples(time.Time{})[0]
	if got := find(t, s, laptop).IP; got != v4 {
		t.Errorf("laptop IP = %s, want the first IPv4 address %s", got, v4)
	}
	if got := find(t, s, phone).IP; got != v6 {
		t.Errorf("phone IP = %s, want the first address %s", got, v6)
	}
}

func TestSamplesSinceReturnsOnlyNewerOnes(t *testing.T) {
	m := NewMeter()
	m.Tick(t0)
	for i := 1; i <= 5; i++ {
		m.Record(laptop, netip.Addr{}, uint64(i), 0)
		m.Tick(t0.Add(time.Duration(i) * time.Second))
	}

	if got := len(m.Samples(time.Time{})); got != 5 {
		t.Fatalf("all samples = %d, want 5", got)
	}

	// since is exclusive, so a client that has the sample at +3s gets +4s and +5s.
	since := t0.Add(3 * time.Second)
	got := m.Samples(since)
	if len(got) != 2 || !got[0].Time.Equal(since.Add(time.Second)) {
		t.Errorf("samples since +3s = %+v, want the two after it", got)
	}

	if got := m.Samples(t0.Add(5 * time.Second)); len(got) != 0 {
		t.Errorf("samples since the newest = %+v, want none", got)
	}
}

func TestSamplesAreACopy(t *testing.T) {
	m := NewMeter()
	m.Tick(t0)
	m.Tick(t0.Add(time.Second))

	got := m.Samples(time.Time{})
	got[0].Time = time.Time{}

	if m.Samples(time.Time{})[0].Time.IsZero() {
		t.Error("modifying the result changed the meter")
	}
}

func TestOldSamplesAreDropped(t *testing.T) {
	m := NewMeter()
	m.Tick(t0)
	for i := 1; i <= int(History/time.Second)+30; i++ {
		m.Tick(t0.Add(time.Duration(i) * time.Second))
	}

	samples := m.Samples(time.Time{})
	if len(samples) == 0 {
		t.Fatal("no samples")
	}

	newest := samples[len(samples)-1].Time
	if age := newest.Sub(samples[0].Time); age > History {
		t.Errorf("oldest sample is %s older than the newest, want at most %s", age, History)
	}
	if got, want := len(samples), int(History/time.Second)+1; got != want {
		t.Errorf("kept %d samples, want %d (History at one per second, bounds included)", got, want)
	}
}

func TestResetForgetsEverything(t *testing.T) {
	m := NewMeter()
	m.Tick(t0)
	m.Record(laptop, netip.Addr{}, 100, 0)
	m.Tick(t0.Add(time.Second))
	m.Record(laptop, netip.Addr{}, 100, 0)

	m.Reset()

	m.Tick(t0.Add(time.Hour)) // starts over, so this is not an hour long interval
	if samples := m.Samples(time.Time{}); len(samples) != 0 {
		t.Fatalf("samples after reset = %+v, want none", samples)
	}

	m.Tick(t0.Add(time.Hour + time.Second))
	if devices := m.Samples(time.Time{})[0].Devices; len(devices) != 0 {
		t.Errorf("devices = %+v, want the traffic recorded before the reset to be gone", devices)
	}
}

func TestWatchedExpires(t *testing.T) {
	m := NewMeter()
	if m.Watched(t0) {
		t.Error("a new meter is watched")
	}

	m.Watch(t0)
	if !m.Watched(t0.Add(IdleAfter - time.Millisecond)) {
		t.Error("not watched shortly after Watch")
	}
	if m.Watched(t0.Add(IdleAfter)) {
		t.Error("still watched after IdleAfter")
	}

	m.Watch(t0.Add(time.Minute))
	if !m.Watched(t0.Add(time.Minute + time.Second)) {
		t.Error("not watched after watching again")
	}
}

func TestRunTicksUntilCancelled(t *testing.T) {
	m := NewMeter()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.Run(ctx, 5*time.Millisecond)
	}()

	deadline := time.After(5 * time.Second)
	for {
		m.Record(laptop, netip.Addr{}, 100, 0)
		if len(m.Samples(time.Time{})) >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Run made no samples")
		case <-time.After(time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}

func TestConcurrentUse(t *testing.T) {
	m := NewMeter()
	m.Tick(t0)

	stop := make(chan struct{})
	finished := make(chan struct{}, 3)
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				finished <- struct{}{}
				return
			default:
				m.Record(laptop, netip.Addr{}, 1, 1)
			}
		}
	}()
	go func() {
		for i := 1; ; i++ {
			select {
			case <-stop:
				finished <- struct{}{}
				return
			default:
				m.Tick(t0.Add(time.Duration(i) * time.Millisecond * 10))
			}
		}
	}()
	go func() {
		for {
			select {
			case <-stop:
				finished <- struct{}{}
				return
			default:
				m.Samples(time.Time{})
				m.Watch(t0)
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)
	close(stop)
	for range 3 {
		<-finished
	}
}
