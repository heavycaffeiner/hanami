package log

import (
	"io"
	"log/slog"
	"os"

	"go.uber.org/fx/fxevent"
)

type Config struct {
	ServiceName string
	Development bool
	AddSource   bool
	Level       slog.Leveler
	Writer      io.Writer
	Attributes  []slog.Attr
}

func New(config Config) *slog.Logger {
	writer := config.Writer
	if writer == nil {
		writer = os.Stderr
	}
	level := config.Level
	if level == nil {
		level = slog.LevelInfo
	}
	options := &slog.HandlerOptions{
		AddSource: config.AddSource,
		Level:     level,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) == 0 && attr.Key == slog.TimeKey {
				return slog.Time(slog.TimeKey, attr.Value.Time().UTC())
			}
			return attr
		},
	}
	var handler slog.Handler
	if config.Development {
		handler = slog.NewTextHandler(writer, options)
	} else {
		handler = slog.NewJSONHandler(writer, options)
	}
	logger := slog.New(handler)
	attributes := make([]any, 0, len(config.Attributes)+1)
	if config.ServiceName != "" {
		attributes = append(attributes, slog.String("service.name", config.ServiceName))
	}
	for _, attribute := range config.Attributes {
		attributes = append(attributes, attribute)
	}
	if len(attributes) > 0 {
		logger = logger.With(attributes...)
	}
	return logger
}

func FxEventLogger(logger *slog.Logger) fxevent.Logger {
	eventLogger := &fxevent.SlogLogger{Logger: logger}
	eventLogger.UseLogLevel(slog.LevelDebug)
	return eventLogger
}
