package log

import (
	"go.uber.org/zap"
	"time"
)

func String(key string, value string) zap.Field {
	return zap.String(key, value)
}
func Bool(key string, value bool) zap.Field {
	return zap.Bool(key, value)
}
func Time(key string, value time.Time) zap.Field {
	return zap.Time(key, value)
}
func Duration(key string, value time.Duration) zap.Field {
	return zap.Duration(key, value)
}
func Int(key string, value int) zap.Field {
	return zap.Int(key, value)
}
func Int32(key string, value int32) zap.Field {
	return zap.Int32(key, value)
}
func Int64(key string, value int64) zap.Field {
	return zap.Int64(key, value)
}
func Uint(key string, value uint) zap.Field {
	return zap.Uint(key, value)
}
func Uint32(key string, value uint32) zap.Field {
	return zap.Uint32(key, value)
}
func Uint64(key string, value uint64) zap.Field {
	return zap.Uint64(key, value)
}
func Float32(key string, value float32) zap.Field {
	return zap.Float32(key, value)
}
func Float64(key string, value float64) zap.Field {
	return zap.Float64(key, value)
}
func Any(key string, value interface{}) zap.Field {
	return zap.Any(key, value)
}
