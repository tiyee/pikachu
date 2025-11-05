package log

import "go.uber.org/zap"

func Info(key string, value ...zap.Field) {
	logger.Info(key, value...)
}
func Warn(key string, value ...zap.Field) {
	logger.Warn(key, value...)
}
func Debug(key string, value ...zap.Field) {
	logger.Debug(key, value...)
}
func Error(key string, value ...zap.Field) {
	logger.Error(key, value...)
}
func Fatal(key string, value ...zap.Field) {
	logger.Fatal(key, value...)
}
func Panic(key string, value ...zap.Field) {
	logger.Panic(key, value...)
}
