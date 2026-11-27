// Package version tells which version of netmon is running.
package version

import (
	"runtime/debug"
	"strings"
)

// Version is the release, such as 1.2.3. Releases set it with
// -ldflags "-X github.com/duxet/netmon/version.Version=1.2.3"; other builds
// are "dev".
var Version = "dev"

// String returns the release, or for other builds "dev" with the commit they
// were built from, when Go recorded it: "dev (7f84ad9)", or
// "dev (7f84ad9, modified)" with changes that weren't committed.
func String() string {
	if Version != "dev" {
		return Version
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Version
	}
	return dev(info.Settings)
}

func dev(settings []debug.BuildSetting) string {
	var revision string
	var modified bool
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		return Version
	}

	details := []string{revision[:min(len(revision), 7)]}
	if modified {
		details = append(details, "modified")
	}
	return Version + " (" + strings.Join(details, ", ") + ")"
}
