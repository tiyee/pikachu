package log

import (
	"errors"
	"fmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
	"io"
	"os"
	"path/filepath"
	"pikachu/internal/types"
)

var logger = &Logger{logger: zap.NewNop()}

// 全局log变量，方便外部直接使用
var Log = logger

type Logger struct {
	logger  *zap.Logger
	closers []io.Closer
}

func GetLogger() *zap.Logger {
	return logger.logger
}
func (l *Logger) Info(key string, value ...zap.Field) {
	l.logger.Info(key, value...)
}
func (l *Logger) Debug(key string, value ...zap.Field) {
	l.logger.Debug(key, value...)
}
func (l *Logger) Error(key string, value ...zap.Field) {
	l.logger.Error(key, value...)
}
func (l *Logger) Warn(key string, value ...zap.Field) {
	l.logger.Warn(key, value...)
}
func (l *Logger) Fatal(key string, value ...zap.Field) {
	l.logger.Fatal(key, value...)
}
func (l *Logger) Panic(key string, value ...zap.Field) {
	l.logger.Panic(key, value...)
}

// Init 初始化日志系统，文件打开失败向入口返回错误。
func Init(config *types.LogConfig) error {
	if config == nil {
		return errors.New("logger config cannot be nil")
	}
	level := zap.NewAtomicLevelAt(zap.InfoLevel)
	if config.Level != "" {
		if err := level.UnmarshalText([]byte(config.Level)); err != nil {
			return fmt.Errorf("invalid log level: %w", err)
		}
	}
	encoder := zap.NewProductionEncoderConfig()
	encoder.TimeKey = "time"
	encoder.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05.000 -0700")
	var zl *zap.Logger
	var closers []io.Closer
	switch config.Format {
	case "", "text":
		cfg := zap.Config{Level: level, Encoding: "console", EncoderConfig: encoder, OutputPaths: []string{"stdout"}, ErrorOutputPaths: []string{"stderr"}}
		var err error
		zl, err = cfg.Build(zap.AddCallerSkip(1))
		if err != nil {
			return fmt.Errorf("build logger: %w", err)
		}
	case "json":
		directory := config.Directory
		if directory == "" {
			directory = "logs"
		}
		if err := os.MkdirAll(directory, 0750); err != nil {
			return fmt.Errorf("create log directory: %w", err)
		}
		size, backups, age := config.MaxSize, config.MaxBackups, config.MaxAge
		if size == 0 {
			size = 100
		}
		if backups == 0 {
			backups = 5
		}
		if age == 0 {
			age = 7
		}
		if size < 0 || backups < 0 || age < 0 {
			return fmt.Errorf("log rotation limits cannot be negative")
		}
		outputs := make([]*lumberjack.Logger, 0, 2)
		for _, name := range []string{"output.log", "error.log"} {
			filename := filepath.Join(directory, name)
			f, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				return fmt.Errorf("open log file: %w", err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("close log file: %w", err)
			}
			outputs = append(outputs, &lumberjack.Logger{Filename: filename, MaxSize: size, MaxBackups: backups, MaxAge: age, Compress: true})
		}
		zl = zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(encoder), zapcore.AddSync(outputs[0]), level), zap.AddCaller(), zap.AddCallerSkip(1), zap.ErrorOutput(zapcore.AddSync(outputs[1])))
		for _, output := range outputs {
			closers = append(closers, output)
		}
	default:
		return fmt.Errorf("invalid log format %q", config.Format)
	}
	Close()
	logger = &Logger{logger: zl, closers: closers}
	Log = logger
	logger.Info("Logger initialized successfully", zap.String("level", string(config.Level)), zap.String("format", config.Format))
	return nil
}

// Close 同步日志并关闭轮转文件，可重复调用。
func Close() {
	_ = logger.logger.Sync()
	for _, closer := range logger.closers {
		_ = closer.Close()
	}
}
