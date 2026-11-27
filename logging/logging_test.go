package logging

import (
	"log/slog"
	"testing"
)

func TestSetLevel(t *testing.T) {
	for name, want := range map[string]slog.Level{
		"debug": slog.LevelDebug, "INFO": slog.LevelInfo, " warn ": slog.LevelWarn, "error": slog.LevelError, "loud": slog.LevelInfo,
	} {
		SetLevel(name)
		if got := level.Level(); got != want {
			t.Errorf("SetLevel(%q) = %v, want %v", name, got, want)
		}
	}
}
