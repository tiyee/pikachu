package metrics

import (
	"sync/atomic"
)

// Metrics 指标收集器
type Metrics struct {
	eventsSucceeded int64
	eventsFailed    int64
	retries         int64
	// 内部计数器
	eventsQueued  int64
	eventsDropped int64
	cacheSize     int64
}

// NewMetrics 创建新的指标收集器
func NewMetrics() *Metrics {
	return &Metrics{}
}

// IncrementEventsQueued 增加排队事件数
func (m *Metrics) IncrementEventsQueued() {
	atomic.AddInt64(&m.eventsQueued, 1)
}

// IncrementEventsDropped 增加丢弃事件数
func (m *Metrics) IncrementEventsDropped() {
	atomic.AddInt64(&m.eventsDropped, 1)
}

// GetEventsQueued 获取排队事件数
func (m *Metrics) GetEventsQueued() int64 {
	return atomic.LoadInt64(&m.eventsQueued)
}

// GetEventsDropped 获取丢弃事件数
func (m *Metrics) GetEventsDropped() int64 {
	return atomic.LoadInt64(&m.eventsDropped)
}

// GetCacheSize 获取缓存大小
func (m *Metrics) GetCacheSize() int64 {
	return atomic.LoadInt64(&m.cacheSize)
}

// AddCacheSize 更新当前 worker 持有的独立 JSON 载荷数量。
func (m *Metrics) AddCacheSize(delta int64) { atomic.AddInt64(&m.cacheSize, delta) }

// IncrementEventsSucceeded 记录完成投递的事件。
func (m *Metrics) IncrementEventsSucceeded() { atomic.AddInt64(&m.eventsSucceeded, 1) }

// IncrementEventsFailed 记录本次运行未确认投递并已放弃的事件。
func (m *Metrics) IncrementEventsFailed() { atomic.AddInt64(&m.eventsFailed, 1) }

// IncrementRetries 记录实际发起的重试。
func (m *Metrics) IncrementRetries() { atomic.AddInt64(&m.retries, 1) }

// GetEventsSucceeded 返回成功数。
func (m *Metrics) GetEventsSucceeded() int64 { return atomic.LoadInt64(&m.eventsSucceeded) }

// GetEventsFailed 返回未确认投递数。
func (m *Metrics) GetEventsFailed() int64 { return atomic.LoadInt64(&m.eventsFailed) }

// GetRetries 返回重试数。
func (m *Metrics) GetRetries() int64 { return atomic.LoadInt64(&m.retries) }
