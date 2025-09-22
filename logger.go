package main

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Logger *zap.Logger
var logLevel zap.AtomicLevel

// InitLogger 初始化日志系统，使用zap的高效模式
func InitLogger() {
	// 创建可动态调整的日志级别
	logLevel = zap.NewAtomicLevel()
	logLevel.SetLevel(zap.InfoLevel)

	// 创建zap配置
	config := zap.Config{
		Level:            logLevel,
		Development:      false, // 生产模式，更高性能
		Encoding:         "text",
		EncoderConfig:    zap.NewProductionEncoderConfig(),
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	// 自定义时间格式
	config.EncoderConfig.TimeKey = "time"
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	// 构建logger
	var err error
	Logger, err = config.Build()
	if err != nil {
		// 如果初始化失败，使用默认的生产logger
		Logger, _ = zap.NewProduction()
	}

}

// ConfigureLogger 根据配置调整日志级别和格式
func ConfigureLogger(config *LogConfig) {
	// 设置日志级别
	switch config.Level {
	case LogLevelDebug:
		logLevel.SetLevel(zap.DebugLevel)
	case LogLevelInfo:
		logLevel.SetLevel(zap.InfoLevel)
	case LogLevelWarn:
		logLevel.SetLevel(zap.WarnLevel)
	case LogLevelError:
		logLevel.SetLevel(zap.ErrorLevel)
	case LogLevelFatal:
		logLevel.SetLevel(zap.FatalLevel)
	case LogLevelPanic:
		logLevel.SetLevel(zap.PanicLevel)
	default:
		logLevel.SetLevel(zap.InfoLevel)
	}

	// 设置日志格式
	if config.Format == "json" {
		// 重建logger以应用新格式
		config := zap.Config{
			Level:            logLevel,
			Development:      false,
			Encoding:         "json",
			EncoderConfig:    zap.NewProductionEncoderConfig(),
			OutputPaths:      []string{"stdout"},
			ErrorOutputPaths: []string{"stderr"},
		}

		config.EncoderConfig.TimeKey = "time"
		config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

		var err error
		Logger, err = config.Build()
		if err != nil {
			Logger, _ = zap.NewProduction()
		}

	}

	// 记录配置信息
	Logger.Info("Logger configured",
		zap.String("level", string(config.Level)),
		zap.String("format", config.Format),
	)
}

// CloseLogger 关闭日志器，确保所有日志都被刷新
func CloseLogger() {
	_ = Logger.Sync()
}

func LogTaskStart(taskID, name, table string) {
	Logger.Info("Task started",
		zap.String("task_id", taskID),
		zap.String("name", name),
		zap.String("table", table),
	)
}

func LogTaskStop(taskID string) {
	Logger.Info("Task stopped",
		zap.String("task_id", taskID),
	)
}

func LogChangeEvent(event *ChangeEvent) {
	Logger.Info("Database change captured",
		zap.String("task_id", event.TaskID),
		zap.String("event", string(event.Event)),
		zap.String("table", event.Table),
		zap.Time("timestamp", event.Timestamp),
	)
}

func LogWebhookRequest(taskID, url string, statusCode int, payload interface{}) {
	Logger.Info("Webhook request sent",
		zap.String("task_id", taskID),
		zap.String("url", url),
		zap.Int("status_code", statusCode),
		zap.Any("payload", payload),
	)
}

func LogWebhookRetry(taskID, url string, retryCount int, err error) {
	Logger.Warn("Webhook retry",
		zap.String("task_id", taskID),
		zap.String("url", url),
		zap.Int("retry_count", retryCount),
		zap.Error(err),
	)
}

func LogError(taskID string, err error, context string) {
	Logger.Error("Error occurred",
		zap.String("task_id", taskID),
		zap.String("context", context),
		zap.Error(err),
	)
}
