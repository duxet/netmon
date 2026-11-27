package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/duxet/netmon/alerting"
	"github.com/duxet/netmon/networks"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v3"
)

// settingsVersion is the version of the settings export format.
const settingsVersion = 1

// SettingsDTO is what netmon was set up with, rather than what it recorded:
// its networks, the names and icons of devices, notification channels and
// alert rules. Each part has the fields of the request that creates one, so
// an export can be imported elsewhere.
type SettingsDTO struct {
	Version    int                `json:"version"`
	ExportedAt time.Time          `json:"exported_at"`
	Networks   []networkBody      `json:"networks"`
	Devices    []deviceSettings   `json:"devices"`
	Channels   []channelSettings  `json:"channels"`
	AlertRules []alertRuleSetting `json:"alert_rules"`
}

type deviceSettings struct {
	MAC  string  `json:"mac"`
	Name *string `json:"name"`
	Icon *string `json:"icon"`
}

type channelSettings struct {
	Name string `json:"name"`
	// URL holds the service's credentials. Exports have them replaced by
	// REDACTED unless asked for.
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

// alertRuleSetting is a rule as POST /api/alert-rules takes it, with the
// names of its channels rather than their IDs, which differ elsewhere.
type alertRuleSetting struct {
	ruleBody
	ChannelIDs []int64  `json:"-"`
	Channels   []string `json:"channels"`
}

// ImportCountDTO counts what an import created and changed of one kind.
type ImportCountDTO struct {
	Created int
	Updated int
}

// ImportResultDTO says what an import did.
type ImportResultDTO struct {
	Networks   ImportCountDTO
	Devices    ImportCountDTO
	Channels   ImportCountDTO
	AlertRules ImportCountDTO
	// Warnings are about what was left out, such as a channel without its URL.
	Warnings []string
}

// redactedURL tells a service URL that an export redacted.
func redactedURL(url string) bool {
	return strings.Contains(url, "REDACTED")
}

// registerBackupRoutes adds the endpoints that back up the database, and
// export and import the settings. databasePath is where the database is,
// next to which backups are written before they are sent; without one there
// are no backups.
func registerBackupRoutes(app *fiber.App, db *sql.DB, databasePath string) {
	app.Get("/api/backup", func(c fiber.Ctx) error {
		if databasePath == "" {
			return fiber.NewError(fiber.StatusNotFound, "backups aren't available")
		}

		// Next to the database, which is on storage meant for it, rather than
		// in a temporary directory that may be in memory, as on OpenWrt.
		suffix := make([]byte, 6)
		_, _ = rand.Read(suffix)
		path := databasePath + ".backup-" + hex.EncodeToString(suffix)
		if err := storage.Backup(c.Context(), db, path); err != nil {
			_ = os.Remove(path)
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			_ = os.Remove(path)
			return err
		}
		// Unlinked, the file lives on until it has been sent and closed.
		_ = os.Remove(path)
		_ = os.Remove(path + ".wal")
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return err
		}
		slog.Info("Backed up the database", "bytes", info.Size())

		name := "netmon-" + time.Now().Format("2006-01-02") + ".db"
		c.Set(fiber.HeaderContentDisposition, `attachment; filename="`+name+`"`)
		c.Set(fiber.HeaderContentType, "application/octet-stream")
		return c.SendStream(file, int(info.Size()))
	})

	app.Get("/api/settings-export", func(c fiber.Ctx) error {
		settings, err := exportSettings(db, c.Query("secrets") == "true")
		if err != nil {
			return err
		}
		name := "netmon-settings-" + time.Now().Format("2006-01-02") + ".json"
		c.Set(fiber.HeaderContentDisposition, `attachment; filename="`+name+`"`)
		return c.JSON(settings)
	})

	app.Post("/api/settings-import", func(c fiber.Ctx) error {
		var settings SettingsDTO
		if err := json.Unmarshal(c.Body(), &settings); err != nil {
			return badRequest("invalid settings: " + err.Error())
		}
		result, err := importSettings(db, settings)
		if err != nil {
			return err
		}
		slog.Info("Imported settings", "networks", result.Networks, "devices", result.Devices,
			"channels", result.Channels, "alert_rules", result.AlertRules)
		return c.JSON(result)
	})
}

func exportSettings(db *sql.DB, secrets bool) (SettingsDTO, error) {
	settings := SettingsDTO{
		Version:    settingsVersion,
		ExportedAt: time.Now().UTC(),
		Networks:   []networkBody{},
		Devices:    []deviceSettings{},
		Channels:   []channelSettings{},
		AlertRules: []alertRuleSetting{},
	}

	nets, err := storage.ListNetworks(db)
	if err != nil {
		return settings, err
	}
	for _, n := range nets {
		dto := toNetworkDTO(n)
		settings.Networks = append(settings.Networks, networkBody{Name: &dto.Name, VLAN: dto.VLAN, Color: &dto.Color, Prefixes: dto.Prefixes})
	}

	names, err := storage.GetDeviceNames(db)
	if err != nil {
		return settings, err
	}
	icons, err := storage.GetDeviceIcons(db)
	if err != nil {
		return settings, err
	}
	macs := map[string]bool{}
	for mac := range names {
		macs[mac] = true
	}
	for mac := range icons {
		macs[mac] = true
	}
	for _, mac := range slices.Sorted(keys(macs)) {
		device := deviceSettings{MAC: mac}
		if name, ok := names[mac]; ok {
			device.Name = &name
		}
		if icon, ok := icons[mac]; ok {
			device.Icon = &icon
		}
		settings.Devices = append(settings.Devices, device)
	}

	channels, err := storage.ListChannels(db)
	if err != nil {
		return settings, err
	}
	channelNames := map[int64]string{}
	for _, ch := range channels {
		channelNames[ch.ID] = ch.Name
		url := ch.URL
		if !secrets {
			url = toChannelDTO(ch).MaskedURL
		}
		settings.Channels = append(settings.Channels, channelSettings{Name: ch.Name, URL: url, Enabled: ch.Enabled})
	}

	rules, err := storage.ListRules(db)
	if err != nil {
		return settings, err
	}
	for _, r := range rules {
		notifyOnResolve, enabled := r.NotifyOnResolve, r.Enabled
		rule := alertRuleSetting{
			ruleBody: ruleBody{
				Name: r.Name, Type: r.Type, Params: json.RawMessage(r.Params), Severity: r.Severity,
				RepeatIntervalSeconds: r.RepeatIntervalSeconds, NotifyOnResolve: &notifyOnResolve, Enabled: &enabled,
			},
			Channels: []string{},
		}
		for _, id := range r.ChannelIDs {
			rule.Channels = append(rule.Channels, channelNames[id])
		}
		settings.AlertRules = append(settings.AlertRules, rule)
	}

	return settings, nil
}

func keys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// importError says which entry of an import is wrong.
func importError(kind string, i int, err error) error {
	message := err.Error()
	if fe, ok := err.(*fiber.Error); ok {
		message = fe.Message
	}
	return badRequest(fmt.Sprintf("%s[%d]: %s", kind, i, message))
}

// importSettings merges settings into netmon's: networks, channels and rules
// replace the ones with the same name and are added otherwise, and devices
// get the names and icons the settings give them. Everything is checked
// before anything changes.
func importSettings(db *sql.DB, settings SettingsDTO) (ImportResultDTO, error) {
	result := ImportResultDTO{Warnings: []string{}}
	if settings.Version != settingsVersion {
		return result, badRequest(fmt.Sprintf("version must be %d, the version of the export format", settingsVersion))
	}
	now := time.Now()

	// Networks are checked against each other as they will be, with the new
	// ones under made-up IDs, below zero.
	existingNets, err := storage.ListNetworks(db)
	if err != nil {
		return result, err
	}
	planned := slices.Clone(existingNets)
	type netChange struct {
		network networks.Network
		create  bool
	}
	var netChanges []netChange
	for i, body := range settings.Networks {
		var n networks.Network
		create := true
		if body.Name != nil {
			for _, existing := range planned {
				if strings.EqualFold(existing.Name, strings.TrimSpace(*body.Name)) {
					n, create = existing.Network, false
				}
			}
		}
		if create {
			n.ID = -int64(i) - 1
		}
		if err := parseNetwork(&body, &n, planned); err != nil {
			return result, importError("networks", i, err)
		}
		planned = slices.DeleteFunc(planned, func(r storage.NetworkRecord) bool { return r.ID == n.ID })
		planned = append(planned, storage.NetworkRecord{Network: n})
		netChanges = append(netChanges, netChange{n, create})
	}

	type deviceChange struct {
		mac        net.HardwareAddr
		name, icon *string
	}
	var deviceChanges []deviceChange
	for i, d := range settings.Devices {
		mac, err := net.ParseMAC(d.MAC)
		if err != nil {
			return result, importError("devices", i, badRequest("invalid mac address: "+d.MAC))
		}
		change := deviceChange{mac: mac}
		if d.Name != nil {
			name, err := parseDeviceName(*d.Name)
			if err != nil {
				return result, importError("devices", i, err)
			}
			change.name = &name
		}
		if d.Icon != nil {
			icon, err := parseDeviceIcon(*d.Icon)
			if err != nil {
				return result, importError("devices", i, err)
			}
			change.icon = &icon
		}
		deviceChanges = append(deviceChanges, change)
	}

	existingChannels, err := storage.ListChannels(db)
	if err != nil {
		return result, err
	}
	type channelChange struct {
		record storage.ChannelRecord
		create bool
	}
	var channelChanges []channelChange
	importedChannels := map[string]bool{}
	for i, ch := range settings.Channels {
		name, err := parseChannelName(ch.Name)
		if err != nil {
			return result, importError("channels", i, err)
		}
		record, create := storage.ChannelRecord{Name: name}, true
		for _, existing := range existingChannels {
			if strings.EqualFold(existing.Name, name) {
				record, create = existing, false
			}
		}
		record.Enabled = ch.Enabled
		if redactedURL(ch.URL) {
			// An export without secrets can't make a channel, only change one.
			if create {
				result.Warnings = append(result.Warnings, fmt.Sprintf("Left out the channel “%s”, as its URL was redacted: add it by hand", name))
				continue
			}
		} else {
			url, service, err := parseServiceURL(ch.URL)
			if err != nil {
				return result, importError("channels", i, err)
			}
			record.URL, record.Service = url, service.Scheme
		}
		importedChannels[strings.ToLower(name)] = true
		channelChanges = append(channelChanges, channelChange{record, create})
	}

	existingRules, err := storage.ListRules(db)
	if err != nil {
		return result, err
	}
	for i, rule := range settings.AlertRules {
		if _, err := parseName(rule.Name, "name must not be empty"); err != nil {
			return result, importError("alert_rules", i, err)
		}
		if _, err := alerting.ValidateParams(rule.Type, rule.Params); err != nil {
			return result, importError("alert_rules", i, badRequest(err.Error()))
		}
		if rule.Severity != "" {
			if err := alerting.ValidateSeverity(rule.Severity); err != nil {
				return result, importError("alert_rules", i, badRequest(err.Error()))
			}
		}
		if err := alerting.ValidateRepeat(rule.RepeatIntervalSeconds); err != nil {
			return result, importError("alert_rules", i, badRequest(err.Error()))
		}
		for _, channel := range rule.Channels {
			known := importedChannels[strings.ToLower(channel)] ||
				slices.ContainsFunc(existingChannels, func(c storage.ChannelRecord) bool { return strings.EqualFold(c.Name, channel) })
			if !known {
				result.Warnings = append(result.Warnings, fmt.Sprintf("The alert rule “%s” won't notify “%s”, which isn't a channel", rule.Name, channel))
			}
		}
	}

	// Everything checks out, so change it.
	for _, change := range netChanges {
		if change.create {
			if _, err := storage.CreateNetwork(db, change.network, now); err != nil {
				return result, err
			}
			result.Networks.Created++
		} else {
			if _, err := storage.UpdateNetwork(db, change.network, now); err != nil {
				return result, err
			}
			result.Networks.Updated++
		}
	}

	for _, change := range deviceChanges {
		if change.name != nil {
			if err := storage.SetDeviceName(db, change.mac, *change.name); err != nil {
				return result, err
			}
		}
		if change.icon != nil {
			if err := storage.SetDeviceIcon(db, change.mac, *change.icon); err != nil {
				return result, err
			}
		}
		result.Devices.Updated++
	}

	for _, change := range channelChanges {
		if change.create {
			if _, err := storage.CreateChannel(db, change.record, now); err != nil {
				return result, err
			}
			result.Channels.Created++
		} else {
			if _, err := storage.UpdateChannel(db, change.record, now); err != nil {
				return result, err
			}
			result.Channels.Updated++
		}
	}

	channels, err := storage.ListChannels(db)
	if err != nil {
		return result, err
	}
	for i, rule := range settings.AlertRules {
		body := rule.ruleBody
		body.ChannelIDs = nil
		for _, name := range rule.Channels {
			for _, ch := range channels {
				if strings.EqualFold(ch.Name, name) && !slices.Contains(body.ChannelIDs, ch.ID) {
					body.ChannelIDs = append(body.ChannelIDs, ch.ID)
				}
			}
		}
		record, err := parseRule(db, &body)
		if err != nil {
			return result, importError("alert_rules", i, err)
		}

		var old *storage.RuleRecord
		for _, existing := range existingRules {
			if strings.EqualFold(existing.Name, record.Name) {
				old = &existing
			}
		}
		if old == nil {
			if _, err := storage.CreateRule(db, record, now); err != nil {
				return result, err
			}
			result.AlertRules.Created++
			continue
		}
		record.ID = old.ID
		resolve := record.Type != old.Type || record.Params != old.Params || (old.Enabled && !record.Enabled)
		if _, err := storage.UpdateRule(db, record, now, resolve); err != nil {
			return result, err
		}
		result.AlertRules.Updated++
	}

	return result, nil
}
