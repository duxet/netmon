package api

import (
	"encoding/json"
	"errors"
	"log"
	"runtime"
	"slices"
	"strings"

	"github.com/duxet/netmon/config"
	"github.com/gofiber/fiber/v3"
)

// ConfigDTO is netmon's configuration, as the configuration page shows it.
type ConfigDTO struct {
	// File is the YAML file settings are saved to; Exists tells whether
	// there is one yet.
	File   string
	Exists bool
	// OS is the operating system netmon runs on, as some settings only
	// apply on one, e.g. "linux" or "darwin".
	OS string
	// DefaultPublicURL is where notifications link to without public_url.
	DefaultPublicURL string
	Settings         []ConfigSettingDTO
}

// ConfigSettingDTO is one setting. Values are strings, numbers or booleans.
type ConfigSettingDTO struct {
	// Key is the setting's key in the YAML file.
	Key string
	// Value is what netmon would start with now, from the environment, the
	// file or the default; Running is what it runs with.
	Value   any
	Running any
	Default any
	// File is the file's value, when InFile.
	File   any
	InFile bool
	// Env is the environment variable that sets the setting and overrides
	// the file; empty when none does.
	Env string
	// Live settings apply as soon as they are saved; the others when netmon
	// starts again.
	Live bool
	// Locked says why the setting can only be changed in the file, if so.
	Locked string
	// Secret settings have no values here; Set tells whether they have one.
	Secret bool
	Set    bool
	// Restart tells whether netmon has to start again to run with Value.
	Restart bool
}

// ConfigErrorDTO is a change that was refused, with the setting it is about.
type ConfigErrorDTO struct {
	Error string `json:"error"`
	// Setting is the key of the setting, or empty when it is about the
	// configuration as a whole.
	Setting string `json:"setting,omitempty"`
}

func toConfigDTO(state config.State) ConfigDTO {
	dto := ConfigDTO{
		File:             state.File,
		Exists:           state.Exists,
		OS:               runtime.GOOS,
		DefaultPublicURL: state.DefaultPublicURL,
		Settings:         make([]ConfigSettingDTO, 0, len(state.Settings)),
	}
	for _, s := range state.Settings {
		dto.Settings = append(dto.Settings, ConfigSettingDTO(s))
	}
	return dto
}

// registerConfigRoutes adds the endpoints of the configuration page.
func registerConfigRoutes(app *fiber.App, editor *config.Editor) {
	app.Get("/api/config", func(c fiber.Ctx) error {
		state, err := editor.State()
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "reading the configuration failed: "+err.Error())
		}
		return c.JSON(toConfigDTO(state))
	})

	// Saves settings to the file: a string, a number or a boolean sets one, null takes
	// it out of the file, back to its default, and settings left out stay.
	app.Put("/api/config", func(c fiber.Ctx) error {
		var changes map[string]any
		if err := json.Unmarshal(c.Body(), &changes); err != nil || changes == nil {
			return badRequest("the body must be an object of settings, such as {\"local_dns\": \"192.168.1.1\"}")
		}

		state, err := editor.Update(changes)
		var invalid *config.InvalidError
		switch {
		case errors.As(err, &invalid):
			return c.Status(fiber.StatusBadRequest).JSON(ConfigErrorDTO{Error: invalid.Error(), Setting: invalid.Key})
		case err != nil:
			log.Printf("Saving the configuration failed: %v", err)
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}

		keys := make([]string, 0, len(changes))
		for key := range changes {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		log.Printf("Saved %s to %s", strings.Join(keys, ", "), state.File)

		return c.JSON(toConfigDTO(state))
	})
}
