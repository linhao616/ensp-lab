package logging

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	logger    *zap.Logger
	logLevel  = "info"
	logFormat = "console"
)

func SetLogLevel(level string) {
	logLevel = level
}

func SetLogFormat(format string) {
	logFormat = format
}

func InitLogger() *zap.Logger {
	if logger != nil {
		return logger
	}

	var config zap.Config
	if logFormat == "json" {
		config = zap.NewProductionConfig()
	} else {
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	level, err := zapcore.ParseLevel(logLevel)
	if err != nil {
		level = zapcore.InfoLevel
	}
	config.Level = zap.NewAtomicLevelAt(level)

	logger, err = config.Build()
	if err != nil {
		panic("failed to initialize logger: " + err.Error())
	}

	logger.Info("Logger initialized",
		zap.String("level", level.String()),
		zap.String("format", logFormat),
	)

	return logger
}

func Logger() *zap.Logger {
	if logger == nil {
		return InitLogger()
	}
	return logger
}

func Sync() {
	if logger != nil {
		_ = logger.Sync()
	}
}

func Debug(msg string, fields ...zap.Field) {
	Logger().Debug(msg, fields...)
}

func Info(msg string, fields ...zap.Field) {
	Logger().Info(msg, fields...)
}

func Warn(msg string, fields ...zap.Field) {
	Logger().Warn(msg, fields...)
}

func Error(msg string, fields ...zap.Field) {
	Logger().Error(msg, fields...)
}

func Fatal(msg string, fields ...zap.Field) {
	Logger().Fatal(msg, fields...)
	os.Exit(1)
}

func Panic(msg string, fields ...zap.Field) {
	Logger().Panic(msg, fields...)
}
