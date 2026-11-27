// Package logging sets up netmon's log, whose level can change while netmon
// runs.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// Levels are the names of the levels, least severe first.
var Levels = []string{"debug", "info", "warn", "error"}

var level = new(slog.LevelVar)

// Setup makes the default logger, and the standard library's log package,
// write lines of text to standard error, leaving out messages less severe
// than the level named.
func Setup(name string) {
	SetLevel(name)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
}

// SetLevel changes the level of the log. Names that aren't one of Levels,
// in any case, leave it at info.
func SetLevel(name string) {
	l, ok := Parse(name)
	if !ok {
		l = slog.LevelInfo
	}
	level.Set(l)
}

// Parse returns the level named, one of Levels in any case.
func Parse(name string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	}
	return 0, false
}
