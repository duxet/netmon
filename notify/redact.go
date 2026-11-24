package notify

import (
	"cmp"
	"net/url"
	"slices"
	"strings"
)

// minSecretLength is the length from which parts of a URL are treated as
// secrets. Shorter ones, such as "telegram" in telegram://…@telegram or a
// port, are not worth hiding and would make errors unreadable.
const minSecretLength = 8

// redacted replaces secrets in errors, as in shoutrrr's masked URLs.
const redacted = "REDACTED"

// redact replaces the secrets of rawURL in s. Errors can contain them: a
// URL that doesn't parse is quoted whole, and an HTTP error names the
// request URL, which for Telegram has the bot token in its path. The URL
// itself, its user info, and every path segment and query value of at least
// minSecretLength characters are replaced, as written and decoded.
func redact(s, rawURL string) string {
	secrets := []string{rawURL}

	if u, err := url.Parse(rawURL); err == nil {
		if u.User != nil {
			secrets = append(secrets, u.User.Username())
			if password, ok := u.User.Password(); ok {
				secrets = append(secrets, password)
			}
		}
		secrets = append(secrets, strings.Split(u.EscapedPath(), "/")...)
		secrets = append(secrets, strings.Split(u.Path, "/")...)
		for _, pair := range strings.Split(u.RawQuery, "&") {
			_, value, _ := strings.Cut(pair, "=")
			secrets = append(secrets, value)
			if decoded, err := url.QueryUnescape(value); err == nil {
				secrets = append(secrets, decoded)
				// Header values such as "Bearer <token>" hold the secret
				// as one of their words, and lists separate items by commas.
				secrets = append(secrets, strings.FieldsFunc(decoded, func(r rune) bool { return r == ' ' || r == ',' })...)
			}
		}
	}

	// Longest first, so a secret containing another is replaced whole.
	slices.SortFunc(secrets, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	for _, secret := range secrets {
		if len(secret) >= minSecretLength {
			s = strings.ReplaceAll(s, secret, redacted)
		}
	}
	return s
}
