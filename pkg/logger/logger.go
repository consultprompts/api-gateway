package logger

import (
	"log/slog"
	"os"
)

// Init configures the process-wide slog default logger to emit structured
// JSON to stdout and returns it for callers that want an explicit reference.
func Init() *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	log := slog.New(handler)
	slog.SetDefault(log)
	return log
}
