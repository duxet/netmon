package notify

import (
	"reflect"
	"slices"
	"strings"

	"github.com/nicholas-fedor/shoutrrr/pkg/router"
)

// docsBaseURL is where shoutrrr documents its services. A service's page is
// at its package path below pkg/services, e.g. chat/telegram/.
const docsBaseURL = "https://shoutrrr.nickfedor.com/latest/services/"

// ServiceInfo describes a supported service, for people setting up a
// channel.
type ServiceInfo struct {
	Scheme string
	// Name is how people know the service.
	Name string
	// ExampleURL shows the URL format, with placeholders in capitals. It is
	// only set for the common services.
	ExampleURL string
	// DocsURL is the service's documentation, which explains where to find
	// the values its URL needs.
	DocsURL string
}

// commonServices are listed first, with an example URL each. The formats
// are those of shoutrrr's services overview.
var commonServices = []ServiceInfo{
	{Scheme: "telegram", Name: "Telegram", ExampleURL: "telegram://TOKEN@telegram?chats=CHAT_ID"},
	{Scheme: "ntfy", Name: "ntfy", ExampleURL: "ntfy://ntfy.sh/TOPIC"},
	{Scheme: "discord", Name: "Discord", ExampleURL: "discord://TOKEN@WEBHOOK_ID"},
	{Scheme: "smtp", Name: "Email (SMTP)", ExampleURL: "smtp://USERNAME:PASSWORD@HOST:587/?fromaddress=FROM_ADDRESS&toaddresses=TO_ADDRESS"},
	{Scheme: "gotify", Name: "Gotify", ExampleURL: "gotify://HOST/TOKEN"},
	{Scheme: "pushover", Name: "Pushover", ExampleURL: "pushover://shoutrrr:API_TOKEN@USER_KEY/"},
	{Scheme: "slack", Name: "Slack", ExampleURL: "slack://hook:TOKEN@webhook"},
	{Scheme: "matrix", Name: "Matrix", ExampleURL: "matrix://USER:PASSWORD@HOST/?rooms=ROOM"},
	{Scheme: "homeassistant", Name: "Home Assistant", ExampleURL: "homeassistant://LONG_LIVED_TOKEN@HOST:8123"},
	{Scheme: webhookScheme, Name: "Webhook (JSON)", ExampleURL: "generic://HOST/PATH?template=json"},
}

// hiddenServices are supported but of no use for netmon: the logger
// service writes to a Go logger, which here discards everything.
var hiddenServices = []string{"logger"}

// Services lists the supported services: the common ones first, then the
// others by scheme.
func Services() []ServiceInfo {
	services := slices.Clone(commonServices)
	for i := range services {
		services[i].DocsURL = DocsURL(services[i].Scheme)
	}

	schemes := router.SupportedSchemas()
	slices.Sort(schemes)
	for _, scheme := range schemes {
		common := slices.ContainsFunc(commonServices, func(s ServiceInfo) bool { return s.Scheme == scheme })
		if common || slices.Contains(hiddenServices, scheme) {
			continue
		}
		services = append(services, ServiceInfo{Scheme: scheme, Name: scheme, DocsURL: DocsURL(scheme)})
	}
	return services
}

// DocsURL returns the documentation page of the service with the given
// scheme, or the overview of all services for an unknown one.
func DocsURL(scheme string) string {
	// NewService only creates the service, so a zero router does.
	var r router.ServiceRouter
	service, err := r.NewService(scheme)
	if err != nil {
		return docsBaseURL + "overview/"
	}

	pkgPath := reflect.TypeOf(service).Elem().PkgPath()
	_, page, found := strings.Cut(pkgPath, "/pkg/services/")
	if !found {
		return docsBaseURL + "overview/"
	}
	return docsBaseURL + page + "/"
}
