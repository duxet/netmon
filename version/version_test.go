package version

import (
	"runtime/debug"
	"testing"
)

func TestString(t *testing.T) {
	t.Cleanup(func() { Version = "dev" })

	Version = "1.2.3"
	if got := String(); got != "1.2.3" {
		t.Errorf("release = %q, want 1.2.3", got)
	}
}

func TestDev(t *testing.T) {
	revision := debug.BuildSetting{Key: "vcs.revision", Value: "7f84ad9e1c0b2a3d4e5f60718293a4b5c6d7e8f9"}
	for _, test := range []struct {
		settings []debug.BuildSetting
		want     string
	}{
		{nil, "dev"},
		{[]debug.BuildSetting{revision}, "dev (7f84ad9)"},
		{[]debug.BuildSetting{revision, {Key: "vcs.modified", Value: "true"}}, "dev (7f84ad9, modified)"},
		{[]debug.BuildSetting{revision, {Key: "vcs.modified", Value: "false"}}, "dev (7f84ad9)"},
	} {
		if got := dev(test.settings); got != test.want {
			t.Errorf("dev(%v) = %q, want %q", test.settings, got, test.want)
		}
	}
}
