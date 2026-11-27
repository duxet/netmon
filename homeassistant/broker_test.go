package homeassistant

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/duxet/netmon/collector"
	"github.com/duxet/netmon/storage"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
)

// Against a real broker: netmon says it is online, publishes retained
// states that a client subscribing later gets, and says it is offline when
// it closes.
func TestBroker(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()

	server := mochi.New(&mochi.Options{InlineClient: true})
	if err := server.AddHook(new(auth.AllowHook), nil); err != nil {
		t.Fatal(err)
	}
	if err := server.AddListener(listeners.NewTCP(listeners.Config{ID: "tcp", Address: address})); err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve() }()
	defer server.Close()

	db := openTestDB(t)
	now := time.Now()
	laptop := addDevice(t, db, laptopMAC, "192.168.1.23", now)
	if err := storage.SetDeviceName(db, laptop, "Laptop"); err != nil {
		t.Fatal(err)
	}

	p := Connect(Config{Broker: "tcp://" + address}, db, collector.NewTotals())
	deadline := time.Now().Add(5 * time.Second)
	for !p.client.(mqtt.Client).IsConnected() {
		if time.Now().After(deadline) {
			t.Fatal("netmon didn't connect to the broker")
		}
		time.Sleep(20 * time.Millisecond)
	}
	p.Publish(now)

	got := make(chan string, 10)
	err = server.Subscribe("netmon/#", 1, func(_ *mochi.Client, _ packets.Subscription, pk packets.Packet) {
		got <- pk.TopicName + " " + string(pk.Payload)
	})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{"netmon/status": "online", "netmon/device/a483e71c2f90/state": `"presence":"home"`}
	timeout := time.After(5 * time.Second)
	for len(want) > 0 {
		select {
		case m := <-got:
			for topic, text := range want {
				if strings.HasPrefix(m, topic+" ") && strings.Contains(m, text) {
					delete(want, topic)
				}
			}
		case <-timeout:
			t.Fatalf("never got %v", want)
		}
	}

	p.Close()
	timeout = time.After(5 * time.Second)
	for {
		select {
		case m := <-got:
			if m == "netmon/status offline" {
				return
			}
		case <-timeout:
			t.Fatal("nothing offline after closing")
		}
	}
}
