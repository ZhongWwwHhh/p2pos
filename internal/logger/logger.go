package logger

import (
	"io"
	"log/slog"
	"p2pos/internal/config"
	"sync"

	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	Logger      *slog.Logger
	handlerOpts = &slog.HandlerOptions{Level: slog.LevelInfo}
	mu          sync.Mutex
	logWriter   io.Writer
)

func InitWithWriter(w io.Writer) {
	logWriter = w
	Logger = slog.New(slog.NewTextHandler(logWriter, handlerOpts))
}
func Init() {
	logWriter = &lumberjack.Logger{
		Filename:   config.LogPath,
		MaxSize:    config.LogMaxSize,
		MaxBackups: 5,
		MaxAge:     30,
		Compress:   false,
	}
	Logger = slog.New(slog.NewTextHandler(logWriter, handlerOpts))
}

func SetLevel(level slog.Level) {
	mu.Lock()
	defer mu.Unlock()
	handlerOpts.Level = level
	if Logger != nil && logWriter != nil {
		Logger = slog.New(slog.NewTextHandler(logWriter, handlerOpts))
	}
}

func Debug(msg string, detail any) {
	Logger.Debug(msg, "detail", detail)
}

func Info(msg string, detail any) {
	Logger.Info(msg, "detail", detail)
}

func Warn(msg string, detail any) {
	Logger.Warn(msg, "detail", detail)
}

func Error(msg string, detail any) {
	Logger.Error(msg, "detail", detail)
}
