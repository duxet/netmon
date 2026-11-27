package api

import (
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// HostRules say which addresses netmon answers to, by the Host header of
// requests.
type HostRules struct {
	// PublicURL is where people reach netmon, whose host is allowed.
	PublicURL string
	// Allowed are more host names to answer to, or "*" for any.
	Allowed []string
}

// privateSuffixes are domains that nobody can register, so that no web page
// can make a name in them point at netmon (DNS rebinding): names a home
// network's DNS server gives out.
var privateSuffixes = []string{
	".lan", ".local", ".home", ".home.arpa", ".internal", ".intranet", ".localdomain", ".localhost", ".private", ".corp",
}

// allows reports whether netmon answers to requests for host, a Host header's
// name without its port.
func (r HostRules) allows(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if slices.Contains(r.Allowed, "*") {
		return true
	}
	// Addresses and names without a domain can't be pointed at netmon from
	// the outside.
	if _, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return true
	}
	if !strings.Contains(host, ".") {
		return true
	}
	for _, suffix := range privateSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	if public, err := url.Parse(r.PublicURL); err == nil && public.Hostname() != "" && strings.EqualFold(public.Hostname(), host) {
		return true
	}
	return slices.ContainsFunc(r.Allowed, func(allowed string) bool { return strings.EqualFold(allowed, host) })
}

// hostname returns the name in a Host header, without its port.
func hostname(host string) string {
	if name, _, err := net.SplitHostPort(host); err == nil {
		return name
	}
	return host
}

// safeMethod reports whether a request with method changes nothing.
func safeMethod(method string) bool {
	return method == fiber.MethodGet || method == fiber.MethodHead || method == fiber.MethodOptions
}

// crossOrigin reports whether a browser sent the request from another site's
// page. Browsers say so in Sec-Fetch-Site, and older ones in Origin; requests
// with neither, such as from scripts, aren't from a page at all.
func crossOrigin(c fiber.Ctx) bool {
	switch c.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return false
	case "":
	default:
		return true
	}

	origin := c.Get(fiber.HeaderOrigin)
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return true
	}
	return !strings.EqualFold(u.Host, c.Host())
}

// protect refuses requests for hosts the rules don't allow, which a page on
// another site would make after pointing its own name at netmon, and changes
// that pages on other sites ask for. rules is asked for every request, as the
// configuration page can change them.
func protect(rules func() HostRules) fiber.Handler {
	return func(c fiber.Ctx) error {
		if host := hostname(c.Host()); !rules().allows(host) {
			return fiber.NewError(fiber.StatusForbidden,
				"netmon doesn't answer to "+host+": open it at its address or a name on your network, or add the name to allowed_hosts")
		}
		if !safeMethod(c.Method()) && crossOrigin(c) {
			return fiber.NewError(fiber.StatusForbidden, "netmon doesn't take changes from other sites' pages")
		}
		return c.Next()
	}
}
