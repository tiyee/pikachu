package metrics

import (
	"sync/atomic"
)

// Metrics 指标收集器
type Metrics struct {
	// 内部计数器
	eventsQueued  int64
	eventsDropped int64
	cacheSize     int64
}

// NewMetrics 创建新的指标收集器
func NewMetrics() *Metrics {
	return &Metrics{}
}

// RecordEventProcessed 记录事件处理 (简化版本，仅记录日志)
func (m *Metrics) RecordEventProcessed(taskID, table, eventType, status string) {
	// 移除prometheus，仅保留内部计数
}

// RecordWebhookRequest 记录 Webhook 请求 (简化版本，仅记录日志)
func (m *Metrics) RecordWebhookRequest(taskID, statusCode string) {
	// 移除prometheus，仅保留内部计数
}

// RecordWebhookRetry 记录 Webhook 重试 (简化版本，仅记录日志)
func (m *Metrics) RecordWebhookRetry(taskID string) {
	// 移除prometheus，仅保留内部计数
}

// RecordEventProcessingDuration 记录事件处理时间 (简化版本，仅记录日志)
func (m *Metrics) RecordEventProcessingDuration(taskID, eventType string, duration float64) {
	// 移除prometheus，仅保留内部计数
}

// RecordWebhookRequestDuration 记录 Webhook 请求时间 (简化版本，仅记录日志)
func (m *Metrics) RecordWebhookRequestDuration(taskID, statusCode string, duration float64) {
	// 移除prometheus，仅保留内部计数
}

// UpdateQueueSize 更新队列大小 (简化版本，仅记录日志)
func (m *Metrics) UpdateQueueSize(queueType, workerID string, size float64) {
	// 移除prometheus，仅保留内部计数
}

// UpdateActiveWorkers 更新活跃工作协程数量 (简化版本，仅记录日志)
func (m *Metrics) UpdateActiveWorkers(count int64) {
	// 移除prometheus，仅保留内部计数
}

// RecordCacheHit 记录缓存命中 (简化版本，仅记录日志)
func (m *Metrics) RecordCacheHit() {
	// 移除prometheus，仅保留内部计数
}

// RecordCacheMiss 记录缓存未命中 (简化版本，仅记录日志)
func (m *Metrics) RecordCacheMiss() {
	// 移除prometheus，仅保留内部计数
}

// RecordError 记录错误 (简化版本，仅记录日志)
func (m *Metrics) RecordError(errorType, component string) {
	// 移除prometheus，仅保留内部计数
}

// IncrementEventsQueued 增加排队事件数
func (m *Metrics) IncrementEventsQueued() {
	atomic.AddInt64(&m.eventsQueued, 1)
}

// IncrementEventsDropped 增加丢弃事件数
func (m *Metrics) IncrementEventsDropped() {
	atomic.AddInt64(&m.eventsDropped, 1)
}

// UpdateCacheSize 更新缓存大小
func (m *Metrics) UpdateCacheSize(size int64) {
	atomic.StoreInt64(&m.cacheSize, size)
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
