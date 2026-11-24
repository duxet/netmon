package auth

import (
	"bytes"
	"html/template"
	"log"

	"github.com/gofiber/fiber/v3"
)

// The sign-in pages are plain HTML rather than part of the web UI, which needs
// a session to load.
var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · netmon</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 16px/1.5 system-ui, sans-serif; display: grid; place-items: center; min-height: 100vh; margin: 0; padding: 1rem; box-sizing: border-box; }
  main { max-width: 26rem; }
  h1 { font-size: 1.25rem; margin: 0 0 .5rem; }
  p { margin: 0 0 1rem; }
  a.button { display: inline-block; padding: .5rem 1rem; border-radius: .5rem; background: CanvasText; color: Canvas; text-decoration: none; font-weight: 500; }
</style>
</head>
<body>
<main>
<h1>{{.Title}}</h1>
<p>{{.Message}}</p>
<a class="button" href="{{.Href}}">{{.Label}}</a>
</main>
</body>
</html>
`))

// page answers with a small page: a title, a sentence and a link. These pages
// show who is where in the sign-in, so they must not be kept.
func (a *Authenticator) page(c fiber.Ctx, status int, title, message, label, href string) error {
	var body bytes.Buffer
	err := pageTemplate.Execute(&body, struct{ Title, Message, Label, Href string }{title, message, label, href})
	if err != nil {
		log.Printf("Rendering the %q page failed: %v", title, err)
		return err
	}

	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set(fiber.HeaderContentSecurityPolicy, "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'")
	c.Type("html")
	return c.Status(status).Send(body.Bytes())
}
