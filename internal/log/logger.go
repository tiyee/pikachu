package log

import (
	"errors"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"pikachu/internal/types"
	"time"
)

var logger *Logger

// 全局log变量，方便外部直接使用
var Log *Logger

type Logger struct {
	logger *zap.Logger
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

// Init 初始化日志系统
func Init(config *types.LogConfig) error {
	// 设置默认配置
	if config == nil {
		return errors.New("logger config cannot be nil")
	}

	// 创建可动态调整的日志级别
	logLevel := zap.NewAtomicLevelAt(zap.DebugLevel)
	encoderConfig := zap.NewProductionEncoderConfig()

	encoderConfig.TimeKey = "time"
	encoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout(time.DateTime)

	// 根据配置设置日志级别
	switch config.Level {
	case types.LogLevelDebug:
		logLevel.SetLevel(zap.DebugLevel)
	case types.LogLevelInfo:
		logLevel.SetLevel(zap.InfoLevel)
	case types.LogLevelWarn:
		logLevel.SetLevel(zap.WarnLevel)
	case types.LogLevelError:
		logLevel.SetLevel(zap.ErrorLevel)
	case types.LogLevelFatal:
		logLevel.SetLevel(zap.FatalLevel)
	case types.LogLevelPanic:
		logLevel.SetLevel(zap.PanicLevel)
	default:
		logLevel.SetLevel(zap.InfoLevel)
	}

	// 根据格式选择编码器配置
	var zapConfig zap.Config
	if config.Format == "text" {
		// 文本格式配置
		zapConfig = zap.Config{
			Level:            logLevel,
			Development:      false,
			Encoding:         "console",
			EncoderConfig:    encoderConfig,
			OutputPaths:      []string{"stdout"},
			ErrorOutputPaths: []string{"stderr"},
		}
	} else {
		// JSON格式配置（默认）
		zapConfig = zap.Config{
			Level:            logLevel,
			Development:      false,
			Encoding:         "json",
			EncoderConfig:    encoderConfig,
			OutputPaths:      []string{"./logs/output.log"},
			ErrorOutputPaths: []string{"./logs/error.log"},
		}
	}

	// 自定义时间格式
	zapConfig.EncoderConfig.TimeKey = "time"
	zapConfig.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05.000 -0700")

	// 构建logger
	zapLogger := zap.Must(zapConfig.Build(zap.AddCallerSkip(1)))
	logger = &Logger{logger: zapLogger}
	Log = logger // 设置全局log变量

	logger.Info("Logger initialized successfully",
		zap.String("level", string(config.Level)),
		zap.String("format", config.Format))

	return nil
}
func Close() {
	logger.logger.Sync()
}
