package dispatcher

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"pikachu/internal/log"
	"pikachu/internal/metrics"
	"pikachu/internal/types"
	"pikachu/internal/utils"
)

// jsonCacheEntry JSON 缓存条目
type jsonCacheEntry struct {
	data      []byte
	timestamp time.Time
}

// Dispatcher 回调分发器
type Dispatcher struct {
	config       *types.Config
	eventQueue   chan *types.ChangeEvent
	httpClient   *http.Client
	taskMap      map[string]*types.Task
	taskQueues   []chan *types.CallbackTask
	workersMux   sync.RWMutex
	workersReady int32 // 使用原子操作跟踪工作协程是否准备就绪
	ctx          context.Context
	cancel       context.CancelFunc

	// 对象池优化内存分配
	payloadPool      sync.Pool // WebhookPayload 对象池
	bufferPool       sync.Pool // bytes.Buffer 对象池
	callbackTaskPool sync.Pool // CallbackTask 对象池

	// JSON 缓存优化重试性能
	jsonCache    sync.Map      // 用于缓存重试任务的 JSON 数据
	jsonCacheTTL time.Duration // JSON 缓存TTL

	// 指标收集器
	metrics *metrics.Metrics
}

// New 创建新的分发器
func New(cfg *types.Config, eventQueue chan *types.ChangeEvent) *Dispatcher {
	ctx, cancel := context.WithCancel(context.Background())

	// 创建优化的HTTP传输配置
	transport := &http.Transport{
		MaxIdleConns:        cfg.Dispatcher.MaxIdleConns,
		MaxIdleConnsPerHost: cfg.Dispatcher.MaxIdleConns / 2, // 分配一半给单个主机
		IdleConnTimeout:     cfg.Dispatcher.IdleConnTimeout,
		DisableCompression:  false, // 启用压缩
		MaxConnsPerHost:     cfg.Dispatcher.MaxConnections,
		// 移除 ForceAttemptHTTP2: true，保持协议兼容性
		// 让Go自动协商协议版本，确保与各种回调服务端兼容
	}

	// 创建HTTP客户端，设置超时和传输配置
	httpClient := &http.Client{
		Timeout:   cfg.Dispatcher.Timeout,
		Transport: transport,
	}

	dispatcher := &Dispatcher{
		config:     cfg,
		eventQueue: eventQueue,
		httpClient: httpClient,
		taskMap:    make(map[string]*types.Task),
		ctx:        ctx,
		cancel:     cancel,
	}

	// 初始化对象池
	dispatcher.payloadPool = sync.Pool{
		New: func() interface{} {
			return &types.WebhookPayload{}
		},
	}

	dispatcher.bufferPool = sync.Pool{
		New: func() interface{} {
			return new(bytes.Buffer)
		},
	}

	dispatcher.callbackTaskPool = sync.Pool{
		New: func() interface{} {
			return &types.CallbackTask{}
		},
	}

	dispatcher.jsonCacheTTL = 5 * time.Minute // 默认5分钟TTL
	dispatcher.metrics = metrics.NewMetrics() // 初始化指标收集器

	// 建立任务映射并预构建回调URL
	for i := range cfg.Tasks {
		task := &cfg.Tasks[i]
		// 预构建完整的回调URL，避免运行时重复计算
		task.PrebuiltCallbackURL = utils.BuildCallbackURL(cfg.CallbackHost, task.CallbackURL)
		dispatcher.taskMap[task.TaskID] = task
	}

	return dispatcher
}

// Start 启动分发器
func (d *Dispatcher) Start() {
	log.Info("Starting webhook dispatcher")

	// 启动多个工作协程处理回调
	for i := 0; i < d.config.Dispatcher.WorkerCount; i++ {
		go d.worker(i)
	}

	// 等待所有工作协程准备就绪
	for {
		if atomic.LoadInt32(&d.workersReady) == int32(d.config.Dispatcher.WorkerCount) {
			break
		}
		time.Sleep(10 * time.Millisecond) // 短暂等待避免CPU空转
	}

	// 监听事件队列
	go d.eventLoop()
}

// Stop 停止分发器
func (d *Dispatcher) Stop() {
	log.Info("Stopping webhook dispatcher")
	d.cancel()

	// 清理JSON缓存
	d.jsonCache.Range(func(key, value interface{}) bool {
		d.jsonCache.Delete(key)
		return true
	})
}

// eventLoop 事件循环
func (d *Dispatcher) eventLoop() {
	for {
		select {
		case event := <-d.eventQueue:
			d.processEvent(event)
		case <-d.ctx.Done():
			return
		}
	}
}

// worker 工作协程
func (d *Dispatcher) worker(id int) {
	log.Info("Webhook worker started", log.Int("worker_id", id))

	// 创建工作任务队列
	taskQueue := make(chan *types.CallbackTask, d.config.Dispatcher.QueueSize)
	d.workersMux.Lock()
	d.taskQueues = append(d.taskQueues, taskQueue)
	d.workersMux.Unlock()

	// 标记此工作协程为准备就绪
	atomic.AddInt32(&d.workersReady, 1)

	defer func() {
		// 工作协程退出时减少准备就绪计数
		atomic.AddInt32(&d.workersReady, -1)
		log.Info("Webhook worker stopped", log.Int("worker_id", id))
	}()

	for {
		select {
		case <-d.ctx.Done():
			return
		case task := <-taskQueue:
			// 确保在使用完后归还对象到池中
			defer func() {
				// 重置对象状态后归还到池中
				task.Event = nil
				task.CallbackURL = ""
				task.RetryCount = 0
				task.MaxRetries = 0
				d.callbackTaskPool.Put(task)
			}()

			// 构建webhook载荷
			payload := d.buildWebhookPayload(task.Event)
			// 执行回调
			d.executeCallback(task, payload)
		}
	}
}

// 工作协程索引，用于轮询分配任务
var workerIndex int32 = 0

// processEvent 处理事件
func (d *Dispatcher) processEvent(event *types.ChangeEvent) {
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime).Seconds()
		d.metrics.RecordEventProcessingDuration(event.TaskID, string(event.Event), duration)
	}()

	task, exists := d.taskMap[event.TaskID]
	if !exists {
		log.Error("Task not found for event", log.String("task_id", event.TaskID))
		d.metrics.RecordError("task_not_found", "dispatcher")
		d.metrics.RecordEventProcessed(event.TaskID, event.Table, string(event.Event), "failed")
		return
	}

	// 检查是否有可用的工作协程
	d.workersMux.RLock()
	readyWorkers := atomic.LoadInt32(&d.workersReady)
	queueCount := len(d.taskQueues)
	d.workersMux.RUnlock()

	if readyWorkers == 0 || queueCount == 0 {
		log.Error("No workers available, dropping event",
			log.String("task_id", event.TaskID),
			log.Int32("ready_workers", readyWorkers),
			log.Int("queue_count", queueCount))
		d.metrics.RecordError("no_workers", "dispatcher")
		d.metrics.IncrementEventsDropped()
		d.metrics.RecordEventProcessed(event.TaskID, event.Table, string(event.Event), "dropped")
		return
	}

	// 从对象池获取回调任务，使用预构建的回调URL提高性能
	callbackTask := d.callbackTaskPool.Get().(*types.CallbackTask)
	*callbackTask = types.CallbackTask{
		Event:       event,
		CallbackURL: task.PrebuiltCallbackURL,
		RetryCount:  0,
		MaxRetries:  d.config.Dispatcher.MaxRetries,
	}

	// 使用轮询算法选择工作协程
	d.workersMux.RLock()
	index := atomic.AddInt32(&workerIndex, 1) % int32(queueCount)
	targetQueue := d.taskQueues[index]
	workerID := fmt.Sprintf("worker_%d", index)
	queueLen := len(targetQueue)
	d.workersMux.RUnlock()

	// 更新队列大小指标
	d.metrics.UpdateQueueSize("task_queue", workerID, float64(queueLen))

	// 将任务发送到选定的工作协程队列，使用非阻塞方式
	select {
	case targetQueue <- callbackTask:
		// 任务成功发送
		d.metrics.IncrementEventsQueued()
		d.metrics.RecordEventProcessed(event.TaskID, event.Table, string(event.Event), "queued")
	default:
		log.Warn("Worker queue full, dropping event",
			log.String("task_id", event.TaskID),
			log.Int32("worker_index", index))
		d.metrics.RecordError("queue_full", "dispatcher")
		d.metrics.IncrementEventsDropped()
		d.metrics.RecordEventProcessed(event.TaskID, event.Table, string(event.Event), "dropped")
	}
}

// buildWebhookPayload 构建webhook载荷，使用对象池优化
func (d *Dispatcher) buildWebhookPayload(event *types.ChangeEvent) *types.WebhookPayload {
	// 从对象池获取 payload
	payload := d.payloadPool.Get().(*types.WebhookPayload)

	// 重置对象状态
	*payload = types.WebhookPayload{
		PrimaryID: event.PrimaryID,
		Event:     event.Event,
		Table:     event.Table,
		Timestamp: event.Timestamp,
	}

	switch event.Event {
	case types.EventInsert:
		payload.Data = event.NewData
	case types.EventUpdate:
		payload.OldData = event.OldData
		payload.NewData = event.NewData
	case types.EventDelete:
		payload.Data = event.NewData
	}

	return payload
}

// executeCallback 执行回调
func (d *Dispatcher) executeCallback(callbackTask *types.CallbackTask, payload *types.WebhookPayload) {
	startTime := time.Now()
	taskID := callbackTask.Event.TaskID

	// 确保在使用完后归还对象到池中
	defer d.payloadPool.Put(payload)

	// 生成缓存键
	cacheKey := d.generateCacheKey(callbackTask, payload)

	// 尝试从缓存获取 JSON 数据
	var jsonData []byte
	var err error

	if cachedEntry, ok := d.jsonCache.Load(cacheKey); ok {
		entry := cachedEntry.(*jsonCacheEntry)
		// 检查缓存是否过期
		if time.Since(entry.timestamp) < d.jsonCacheTTL {
			jsonData = entry.data
			log.Debug("Using cached JSON data", log.String("task_id", taskID))
			d.metrics.RecordCacheHit()
		} else {
			// 缓存过期，删除
			d.jsonCache.Delete(cacheKey)
			d.metrics.RecordCacheMiss()
		}
	} else {
		d.metrics.RecordCacheMiss()
	}

	if jsonData == nil {
		// 缓存未命中或过期，重新序列化
		buffer := d.bufferPool.Get().(*bytes.Buffer)
		buffer.Reset()
		err := json.NewEncoder(buffer).Encode(payload)
		if err != nil {
			log.Error("Failed to marshal webhook payload",
				log.String("task_id", taskID),
				zap.Error(err))
			d.metrics.RecordError("json_marshal", "dispatcher")
			d.bufferPool.Put(buffer)
			return
		}

		jsonData = make([]byte, buffer.Len())
		copy(jsonData, buffer.Bytes())
		d.bufferPool.Put(buffer)

		// 缓存 JSON 数据（仅在第一次尝试时缓存）
		if callbackTask.RetryCount == 0 {
			d.jsonCache.Store(cacheKey, &jsonCacheEntry{
				data:      jsonData,
				timestamp: time.Now(),
			})
		}
	}

	// 创建HTTP请求
	req, err := http.NewRequestWithContext(d.ctx, "POST", callbackTask.CallbackURL, bytes.NewReader(jsonData))
	if err != nil {
		log.Error("Failed to create webhook request",
			log.String("task_id", taskID),
			zap.Error(err))
		d.metrics.RecordError("request_creation", "dispatcher")
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", utils.GetUserAgent())

	// 发送请求
	resp, err := d.httpClient.Do(req)
	if err != nil {
		d.handleCallbackError(callbackTask, payload, err)
		return
	}
	defer resp.Body.Close()

	duration := time.Since(startTime).Seconds()
	statusCode := fmt.Sprintf("%d", resp.StatusCode)

	// 记录请求日志
	log.Info("Webhook request sent",
		log.String("task_id", taskID),
		log.String("url", callbackTask.CallbackURL),
		log.Int("status_code", resp.StatusCode),
		log.Any("payload", payload))

	// 记录请求指标
	d.metrics.RecordWebhookRequest(taskID, statusCode)
	d.metrics.RecordWebhookRequestDuration(taskID, statusCode, duration)

	// 检查响应状态
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("webhook returned status code: %d", resp.StatusCode)
		d.handleCallbackError(callbackTask, payload, err)
		d.metrics.RecordError("http_error", "dispatcher")
		return
	}

	log.Info("Webhook callback successful", log.String("task_id", taskID))

	// 请求成功后清除缓存（避免缓存过多）
	d.jsonCache.Delete(cacheKey)
}

// handleCallbackError 处理回调错误
func (d *Dispatcher) handleCallbackError(callbackTask *types.CallbackTask, payload *types.WebhookPayload, err error) {
	taskID := callbackTask.Event.TaskID

	log.Info("Webhook retry attempt",
		log.String("task_id", taskID),
		log.String("url", callbackTask.CallbackURL),
		log.Int("retry_count", callbackTask.RetryCount),
		zap.Error(err))

	// 记录重试指标
	d.metrics.RecordWebhookRetry(taskID)

	// 检查是否需要重试
	if callbackTask.RetryCount >= callbackTask.MaxRetries {
		log.Error("Webhook failed after max retries",
			log.String("task_id", taskID),
			log.Int("max_retries", callbackTask.MaxRetries),
			zap.Error(err))
		d.metrics.RecordError("max_retries_exceeded", "dispatcher")
		return
	}

	// 延迟重试
	callbackTask.RetryCount++
	go func(task *types.CallbackTask) {
		// 指数退避重试策略，使用配置的基础延迟和最大延迟限制
		retryDelay := time.Duration(math.Pow(2, float64(task.RetryCount))) * d.config.Dispatcher.RetryBaseDelay
		// 限制重试延迟不超过最大值
		if retryDelay > d.config.Dispatcher.RetryMaxDelay {
			retryDelay = d.config.Dispatcher.RetryMaxDelay
		}
		time.Sleep(retryDelay)

		// 检查是否有可用的工作协程
		d.workersMux.RLock()
		readyWorkers := atomic.LoadInt32(&d.workersReady)
		queueCount := len(d.taskQueues)
		d.workersMux.RUnlock()

		if readyWorkers == 0 || queueCount == 0 {
			log.Warn("No workers available for retry, dropping task",
				log.String("task_id", task.Event.TaskID),
				log.Int("retry_count", task.RetryCount))
			return
		}

		// 使用轮询算法选择工作协程
		d.workersMux.RLock()
		index := atomic.AddInt32(&workerIndex, 1) % int32(queueCount)
		targetQueue := d.taskQueues[index]
		d.workersMux.RUnlock()

		// 将重试任务发送到工作协程队列，使用非阻塞方式
		select {
		case targetQueue <- task:
			log.Info("Retry task queued successfully",
				log.String("task_id", task.Event.TaskID),
				log.Int("retry_count", task.RetryCount),
				log.Int32("worker_index", index))
		default:
			log.Warn("Worker queue full, dropping retry task",
				log.String("task_id", task.Event.TaskID),
				log.Int("retry_count", task.RetryCount),
				log.Int32("worker_index", index))
		}
	}(callbackTask)
}

// generateCacheKey 生成缓存键
func (d *Dispatcher) generateCacheKey(callbackTask *types.CallbackTask, payload *types.WebhookPayload) string {
	// 使用任务的唯一标识符和载荷的关键信息生成缓存键
	keyData := fmt.Sprintf("%s:%s:%s:%v:%s",
		callbackTask.Event.TaskID,
		callbackTask.Event.Table,
		callbackTask.Event.Event,
		callbackTask.Event.PrimaryID,
		callbackTask.CallbackURL)

	// 使用MD5哈希生成固定长度的缓存键
	hasher := md5.New()
	hasher.Write([]byte(keyData))
	return hex.EncodeToString(hasher.Sum(nil))
}
