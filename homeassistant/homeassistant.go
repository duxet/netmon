// Package homeassistant publishes devices to Home Assistant over MQTT: for
// each, whether it is home, as presence detection by the router, and how
// much it downloads and uploads, set up through MQTT discovery.
package homeassistant

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/duxet/netmon/collector"
	"github.com/duxet/netmon/storage"
	"github.com/duxet/netmon/vendors"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// awayAfter is how long a device has to be without traffic to be away.
const awayAfter = 10 * time.Minute

// Config is where and what to publish.
type Config struct {
	// Broker is the MQTT broker, such as tcp://homeassistant.lan:1883 or
	// ssl://broker:8883; Username and Password log in to it.
	Broker   string
	Username string
	Password string
	// Topic is where netmon publishes, such as netmon/device/<mac>/state.
	Topic string
	// DiscoveryPrefix is where Home Assistant looks for devices to set up,
	// homeassistant by default.
	DiscoveryPrefix string
	// AllDevices publishes every device seen in the last day, instead of
	// only those that were given a name or an icon.
	AllDevices bool
}

// client is the part of an MQTT client a Publisher uses.
type client interface {
	Publish(topic string, qos byte, retained bool, payload any) mqtt.Token
	Disconnect(quiesce uint)
}

// Publisher publishes the devices after every round of recording.
type Publisher struct {
	cfg    Config
	db     *sql.DB
	totals *collector.Totals
	client client

	mu sync.Mutex
	// previous is what the totals were at the last publish, to tell rates.
	previous   map[string]collector.DeviceTotal
	previousAt time.Time
	// published are the devices set up in Home Assistant, by MAC address.
	published map[string]bool
}

// Connect starts publishing to the broker, which it connects to in the
// background, again and again when the connection is lost.
func Connect(cfg Config, db *sql.DB, totals *collector.Totals) *Publisher {
	p := newPublisher(cfg, db, totals)
	opts := mqtt.NewClientOptions().
		AddBroker(cfg.Broker).
		SetClientID("netmon").
		SetUsername(cfg.Username).
		SetPassword(cfg.Password).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetWill(p.availability(), "offline", 1, true).
		SetOnConnectHandler(func(c mqtt.Client) {
			slog.Info("Connected to the MQTT broker", "broker", cfg.Broker)
			c.Publish(p.availability(), 1, true, "online")
			// A broker that lost its retained messages has to learn the
			// devices again.
			p.mu.Lock()
			p.published = map[string]bool{}
			p.mu.Unlock()
		}).
		SetConnectionLostHandler(func(_ mqtt.Client, err error) {
			slog.Warn("Lost the connection to the MQTT broker", "err", err)
		})
	c := mqtt.NewClient(opts)
	c.Connect()
	p.client = c
	return p
}

func newPublisher(cfg Config, db *sql.DB, totals *collector.Totals) *Publisher {
	if cfg.Topic == "" {
		cfg.Topic = "netmon"
	}
	if cfg.DiscoveryPrefix == "" {
		cfg.DiscoveryPrefix = "homeassistant"
	}
	return &Publisher{cfg: cfg, db: db, totals: totals, published: map[string]bool{}}
}

// Close says netmon is offline, and disconnects.
func (p *Publisher) Close() {
	p.client.Publish(p.availability(), 1, true, "offline").WaitTimeout(2 * time.Second)
	p.client.Disconnect(250)
}

func (p *Publisher) availability() string { return p.cfg.Topic + "/status" }

// objectID is a device's ID in topics and Home Assistant: its MAC address
// without colons.
func objectID(mac string) string { return strings.ReplaceAll(mac, ":", "") }

func (p *Publisher) stateTopic(mac string) string {
	return p.cfg.Topic + "/device/" + objectID(mac) + "/state"
}

// state is what is published about a device every round.
type state struct {
	// Presence is home or not_home, as Home Assistant's device trackers say.
	Presence string `json:"presence"`
	// Download and Upload are bytes per second over the last round.
	Download float64 `json:"download"`
	Upload   float64 `json:"upload"`
	IP       string  `json:"ip"`
	LastSeen string  `json:"last_seen"`
}

// Publish publishes the devices, as of now: the last round's rates, and who
// is home. It sets up new devices in Home Assistant, and takes away those
// that aren't published any more.
func (p *Publisher) Publish(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	devices, err := storage.GetDevices(p.db)
	if err != nil {
		slog.Error("Failed to load devices to publish", "err", err)
		return
	}
	aliases, err := storage.GetAliases(p.db)
	if err != nil {
		slog.Error("Failed to load devices to publish", "err", err)
		return
	}
	totals := collector.MergeAliases(p.totals.Devices(), aliases)
	seconds := now.Sub(p.previousAt).Seconds()
	rate := func(mac string) (download, upload float64) {
		// Totals start at zero, so a device that wasn't in them before had
		// none then.
		previous := p.previous[mac]
		if p.previous == nil || seconds <= 0 {
			return 0, 0
		}
		return float64(totals[mac].Received-previous.Received) / seconds, float64(totals[mac].Sent-previous.Sent) / seconds
	}

	current := map[string]bool{}
	var networkDown, networkUp float64
	for _, d := range devices {
		mac := d.MACAddress.String()
		download, upload := rate(mac)
		networkDown += download
		networkUp += upload

		chosen := d.Name != nil || d.Icon != nil
		if !chosen && !(p.cfg.AllDevices && now.Sub(d.LastSeen) < 24*time.Hour) {
			continue
		}
		current[mac] = true
		if !p.published[mac] {
			p.discover(mac, d)
			p.published[mac] = true
		}

		presence := "not_home"
		if now.Sub(d.LastSeen) < awayAfter {
			presence = "home"
		}
		p.publishJSON(p.stateTopic(mac), state{
			Presence: presence, Download: download, Upload: upload,
			IP: d.IPAddress.String(), LastSeen: d.LastSeen.UTC().Format(time.RFC3339),
		})
	}

	for _, mac := range slices.Sorted(maps.Keys(p.published)) {
		if !current[mac] {
			p.forget(mac)
			delete(p.published, mac)
		}
	}

	if !p.published["network"] {
		p.discoverNetwork()
		p.published["network"] = true
	}
	p.publishJSON(p.cfg.Topic+"/network/state", map[string]float64{"download": networkDown, "upload": networkUp})

	p.previous, p.previousAt = totals, now
}

func (p *Publisher) publishJSON(topic string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	p.client.Publish(topic, 0, true, data)
}

// configTopic is where Home Assistant looks for one entity of a device.
func (p *Publisher) configTopic(component, object, entity string) string {
	return fmt.Sprintf("%s/%s/netmon_%s/%s/config", p.cfg.DiscoveryPrefix, component, object, entity)
}

// entities are what each device has in Home Assistant: its component, the
// end of its ID, and its configuration.
func (p *Publisher) entities(mac string, device map[string]any) [][3]any {
	topic := p.stateTopic(mac)
	id := "netmon_" + objectID(mac)
	common := func(extra map[string]any) map[string]any {
		config := map[string]any{"state_topic": topic, "availability_topic": p.availability(), "device": device}
		maps.Copy(config, extra)
		return config
	}
	return [][3]any{
		{"device_tracker", "presence", common(map[string]any{
			"name": nil, "unique_id": id + "_presence", "value_template": "{{ value_json.presence }}",
			"payload_home": "home", "payload_not_home": "not_home", "source_type": "router",
			"json_attributes_topic": topic,
		})},
		{"sensor", "download", common(map[string]any{
			"name": "Download", "unique_id": id + "_download", "value_template": "{{ value_json.download | round(0) }}",
			"device_class": "data_rate", "unit_of_measurement": "B/s", "state_class": "measurement",
		})},
		{"sensor", "upload", common(map[string]any{
			"name": "Upload", "unique_id": id + "_upload", "value_template": "{{ value_json.upload | round(0) }}",
			"device_class": "data_rate", "unit_of_measurement": "B/s", "state_class": "measurement",
		})},
	}
}

// discover sets a device up in Home Assistant.
func (p *Publisher) discover(mac string, d storage.DeviceRecord) {
	name := mac
	if d.Name != nil {
		name = *d.Name
	}
	device := map[string]any{
		"identifiers": []string{"netmon_" + objectID(mac)},
		"connections": [][2]string{{"mac", mac}},
		"name":        name,
		"via_device":  "netmon",
	}
	if vendor := vendors.Lookup(d.MACAddress.HardwareAddr); vendor != "" {
		device["manufacturer"] = vendor
	}
	for _, e := range p.entities(mac, device) {
		p.publishJSON(p.configTopic(e[0].(string), objectID(mac), e[1].(string)), e[2])
	}
}

// forget takes a device away from Home Assistant.
func (p *Publisher) forget(mac string) {
	for _, e := range p.entities(mac, nil) {
		p.client.Publish(p.configTopic(e[0].(string), objectID(mac), e[1].(string)), 0, true, []byte{})
	}
	p.client.Publish(p.stateTopic(mac), 0, true, []byte{})
}

// discoverNetwork sets up netmon itself, with the traffic of the whole
// network.
func (p *Publisher) discoverNetwork() {
	topic := p.cfg.Topic + "/network/state"
	device := map[string]any{"identifiers": []string{"netmon"}, "name": "netmon", "manufacturer": "netmon"}
	for _, direction := range []string{"download", "upload"} {
		p.publishJSON(p.configTopic("sensor", "network", direction), map[string]any{
			"name": "Network " + direction, "unique_id": "netmon_network_" + direction,
			"state_topic": topic, "availability_topic": p.availability(), "device": device,
			"value_template": "{{ value_json." + direction + " | round(0) }}",
			"device_class":   "data_rate", "unit_of_measurement": "B/s", "state_class": "measurement",
		})
	}
}
