package logging

import (
	"fmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var appLogger Logger

type Logger interface {
	Debug(args ...interface{})
	Debugf(format string, args ...interface{})
	Info(args ...interface{})
	Infof(format string, args ...interface{})
	Warn(args ...interface{})
	Warnf(format string, args ...interface{})
	Error(args ...interface{})
	Errorf(format string, args ...interface{})
	Fatal(args ...interface{})
	Fatalf(format string, args ...interface{})
}

func GlobalLogger() (Logger, error) {
	if appLogger != nil {
		return appLogger, nil
	}
	return nil, fmt.Errorf("No logger configured")
}

func ZapLogger(level string) (Logger, error) {
	if appLogger != nil {
		return appLogger, nil
	}

	var cfg = zap.NewProductionConfig()

	lvl, err := zapcore.ParseLevel(level)
	if err != nil {
		return nil, err
	}
	cfg.Level.SetLevel(lvl)

	logger, err := cfg.Build()
	if err != nil {
		return nil, err
	}

	appLogger = logger.Sugar()
	return appLogger, nil
}

const (
	LevelDebug = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelFatal // Adding Fatal level
)
