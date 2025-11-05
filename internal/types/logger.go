package types

import (
	"go.uber.org/zap"
	"time"
)

// 常用字段组合
var (
	// RequestID 请求相关字段
	RequestID = func(id string) zap.Field { return zap.String("request_id", id) }
	// TaskID 任务相关字段
	TaskID = func(id string) zap.Field { return zap.String("task_id", id) }
	// Table 数据库相关字段
	Table    = func(name string) zap.Field { return zap.String("table", name) }
	Database = func(name string) zap.Field { return zap.String("database", name) }
	// StatusCode HTTP相关字段
	StatusCode = func(code int) zap.Field { return zap.Int("status_code", code) }
	URL        = func(url string) zap.Field { return zap.String("url", url) }
	// Duration 性能相关字段
	Duration = func(d time.Duration) zap.Field { return zap.Duration("duration", d) }
	Size     = func(size int) zap.Field { return zap.Int("size", size) }
)
