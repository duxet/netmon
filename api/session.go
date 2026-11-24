package api

import (
	"github.com/duxet/netmon/auth"
	"github.com/gofiber/fiber/v3"
)

// registerSessionRoutes adds the endpoint that tells the web UI whether people
// sign in, and who they are, so that it can show who is signed in and offer
// to sign out. signIn is whether netmon asks people to sign in.
func registerSessionRoutes(app *fiber.App, signIn bool) {
	app.Get("/api/session", func(c fiber.Ctx) error {
		return c.JSON(newSessionDTO(signIn, auth.UserFrom(c)))
	})
}

func newSessionDTO(signIn bool, user *auth.User) SessionDTO {
	dto := SessionDTO{AuthEnabled: signIn}
	if signIn && user != nil {
		dto.User = &UserDTO{Name: nonEmpty(user.Name), Email: nonEmpty(user.Email)}
	}
	return dto
}

// nonEmpty returns a pointer to s, or nil when it is empty.
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
