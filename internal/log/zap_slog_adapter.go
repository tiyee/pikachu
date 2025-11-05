package log

import (
	"context"
	"log/slog"
	"runtime"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// ZapSlogAdapter 实现slog.Handler接口，将slog日志转换为zap日志
// 这样canal就可以使用项目的zap logger进行日志输出
type ZapSlogAdapter struct {
	logger *zap.Logger
	// 缓存级别映射，避免重复计算
	levelCache map[slog.Level]zapcore.Level
}

// NewZapSlogAdapter 创建新的适配器
// 参数logger: 项目的zap logger实例
// 返回: 实现了slog.Handler接口的适配器
func NewZapSlogAdapter(logger *zap.Logger) *ZapSlogAdapter {
	// 初始化级别映射缓存
	levelCache := map[slog.Level]zapcore.Level{
		slog.LevelDebug: zapcore.DebugLevel,
		slog.LevelInfo:  zapcore.InfoLevel,
		slog.LevelWarn:  zapcore.WarnLevel,
		slog.LevelError: zapcore.ErrorLevel,
	}

	return &ZapSlogAdapter{
		logger:     logger,
		levelCache: levelCache,
	}
}

// convertSlogLevelToZapLevel 将slog级别转换为zap级别
// 使用缓存避免重复计算
func (z *ZapSlogAdapter) convertSlogLevelToZapLevel(level slog.Level) zapcore.Level {
	if zapLevel, exists := z.levelCache[level]; exists {
		return zapLevel
	}
	// 未知级别默认为Info级别
	return zapcore.InfoLevel
}

// Enabled 实现slog.Handler接口，检查指定级别的日志是否启用
// 参数ctx: 上下文
// 参数level: slog日志级别
// 返回: 是否启用该级别的日志
func (z *ZapSlogAdapter) Enabled(ctx context.Context, level slog.Level) bool {
	// 使用缓存的方法转换级别
	zapLevel := z.convertSlogLevelToZapLevel(level)
	return z.logger.Core().Enabled(zapLevel)
}

// Handle 实现slog.Handler接口，处理日志记录
// 参数ctx: 上下文
// 参数r: slog日志记录
// 返回: 错误信息
func (z *ZapSlogAdapter) Handle(ctx context.Context, r slog.Record) error {
	// 获取调用者信息，跳过适配器和slog包的调用栈
	pcs := make([]uintptr, 1)
	runtime.Callers(6, pcs) // 跳过更多调用栈以找到真正的调用者
	frame, _ := runtime.CallersFrames(pcs).Next()

	// 构建zap字段
	fields := make([]zapcore.Field, 0, r.NumAttrs()+3) // 预分配空间，包含调用者信息
	r.Attrs(func(attr slog.Attr) bool {
		fields = append(fields, zap.Any(attr.Key, attr.Value.Any()))
		return true
	})

	// 使用缓存的方法转换级别
	zapLevel := z.convertSlogLevelToZapLevel(r.Level)

	// 获取日志消息
	msg := r.Message
	if msg == "" {
		msg = "log message"
	}

	// 添加调用者信息字段
	fields = append(fields,
		zap.String("source", frame.Function),
		zap.String("file", frame.File),
		zap.Int("line", frame.Line),
	)

	// 添加时间戳（如果slog记录中没有时间字段）
	hasTime := false
	r.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "time" {
			hasTime = true
			return false // 找到时间字段就停止
		}
		return true
	})
	if !hasTime {
		fields = append(fields, zap.Time("time", r.Time))
	}

	// 使用zap logger记录日志
	if ce := z.logger.Check(zapLevel, msg); ce != nil {
		ce.Write(fields...)
	}

	return nil
}

// WithAttrs 实现slog.Handler接口，返回带有额外属性的新处理器
// 参数attrs: 要添加的slog属性列表
// 返回: 新的slog.Handler
func (z *ZapSlogAdapter) WithAttrs(attrs []slog.Attr) slog.Handler {
	// 将slog属性转换为zap字段
	zapFields := z.convertAttrsToZapFields(attrs)
	// 创建带有额外字段的新logger
	newLogger := z.logger.With(zapFields...)
	// 返回新的适配器
	return NewZapSlogAdapter(newLogger)
}

// WithGroup 实现slog.Handler接口，返回带有组名的新处理器
// 参数name: 组名
// 返回: 新的slog.Handler
func (z *ZapSlogAdapter) WithGroup(name string) slog.Handler {
	// 创建带有组名的新logger
	newLogger := z.logger.With(zap.String("group", name))
	// 返回新的适配器
	return NewZapSlogAdapter(newLogger)
}

// convertAttrsToZapFields 将slog属性转换为zap字段
// 参数attrs: slog属性列表
// 返回: zap字段列表
func (z *ZapSlogAdapter) convertAttrsToZapFields(attrs []slog.Attr) []zapcore.Field {
	fields := make([]zapcore.Field, 0, len(attrs))
	for _, attr := range attrs {
		fields = append(fields, zap.Any(attr.Key, attr.Value.Any()))
	}
	return fields
}
