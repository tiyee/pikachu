package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Dispatcher 回调分发器
type Dispatcher struct {
	config     *Config
	eventQueue chan *ChangeEvent
	httpClient *http.Client
	taskMap    map[string]*Task
	ctx        context.Context
	cancel     context.CancelFunc
}

// NewDispatcher 创建新的分发器
func NewDispatcher(config *Config, eventQueue chan *ChangeEvent) *Dispatcher {
	ctx, cancel := context.WithCancel(context.Background())

	// 创建HTTP客户端，设置超时
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	dispatcher := &Dispatcher{
		config:     config,
		eventQueue: eventQueue,
		httpClient: httpClient,
		taskMap:    make(map[string]*Task),
		ctx:        ctx,
		cancel:     cancel,
	}

	// 建立任务映射
	for i := range config.Tasks {
		task := &config.Tasks[i]
		dispatcher.taskMap[task.TaskID] = task
	}

	return dispatcher
}

// Start 启动分发器
func (d *Dispatcher) Start() {
	Logger.Info("Starting webhook dispatcher")

	// 启动多个工作协程处理回调
	for i := 0; i < 5; i++ {
		go d.worker(i)
	}

	// 监听事件队列
	go d.eventLoop()
}

// Stop 停止分发器
func (d *Dispatcher) Stop() {
	Logger.Info("Stopping webhook dispatcher")
	d.cancel()
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
	Logger.WithField("worker_id", id).Info("Webhook worker started")

	for {
		select {
		case <-d.ctx.Done():
			Logger.WithField("worker_id", id).Info("Webhook worker stopped")
			return
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// processEvent 处理事件
func (d *Dispatcher) processEvent(event *ChangeEvent) {
	task, exists := d.taskMap[event.TaskID]
	if !exists {
		Logger.WithField("task_id", event.TaskID).Error("Task not found for event")
		return
	}

	// 构建webhook载荷
	payload := d.buildWebhookPayload(event)

	// 创建回调任务
	callbackTask := &CallbackTask{
		Event:       event,
		CallbackURL: task.CallbackURL,
		RetryCount:  0,
		MaxRetries:  3,
	}

	// 执行回调
	d.executeCallback(callbackTask, payload)
}

// buildWebhookPayload 构建webhook载荷
func (d *Dispatcher) buildWebhookPayload(event *ChangeEvent) *WebhookPayload {
	payload := &WebhookPayload{
		PrimaryID: event.PrimaryID,
		Event:     event.Event,
		Table:     event.Table,
		Timestamp: event.Timestamp,
	}

	switch event.Event {
	case EventInsert:
		payload.Data = event.NewData
	case EventUpdate:
		payload.OldData = event.OldData
		payload.NewData = event.NewData
	case EventDelete:
		payload.Data = event.NewData
	}

	return payload
}

// executeCallback 执行回调
func (d *Dispatcher) executeCallback(callbackTask *CallbackTask, payload *WebhookPayload) {
	// 序列化载荷
	jsonData, err := json.Marshal(payload)
	if err != nil {
		LogError(callbackTask.Event.TaskID, err, "marshal webhook payload")
		return
	}

	// 创建HTTP请求
	req, err := http.NewRequestWithContext(d.ctx, "POST", callbackTask.CallbackURL, bytes.NewBuffer(jsonData))
	if err != nil {
		LogError(callbackTask.Event.TaskID, err, "create webhook request")
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Pikachu/1.0")

	// 发送请求
	resp, err := d.httpClient.Do(req)
	if err != nil {
		d.handleCallbackError(callbackTask, payload, err)
		return
	}
	defer resp.Body.Close()

	// 记录请求日志
	LogWebhookRequest(callbackTask.Event.TaskID, callbackTask.CallbackURL, resp.StatusCode, payload)

	// 检查响应状态
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("webhook returned status code: %d", resp.StatusCode)
		d.handleCallbackError(callbackTask, payload, err)
		return
	}

	Logger.WithField("task_id", callbackTask.Event.TaskID).Info("Webhook callback successful")
}

// handleCallbackError 处理回调错误
func (d *Dispatcher) handleCallbackError(callbackTask *CallbackTask, payload *WebhookPayload, err error) {
	LogWebhookRetry(callbackTask.Event.TaskID, callbackTask.CallbackURL, callbackTask.RetryCount, err)

	// 检查是否需要重试
	if callbackTask.RetryCount >= callbackTask.MaxRetries {
		LogError(callbackTask.Event.TaskID, err, fmt.Sprintf("webhook failed after %d retries", callbackTask.MaxRetries))
		return
	}

	// 延迟重试
	callbackTask.RetryCount++
	go func() {
		time.Sleep(60 * time.Second) // 60秒后重试
		d.executeCallback(callbackTask, payload)
	}()
}
