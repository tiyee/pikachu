package dispatcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"pikachu/internal/log"
	"pikachu/internal/metrics"
	"pikachu/internal/types"
	"pikachu/internal/utils"
)

// Dispatcher 使用共享有界队列分发回调，每个任务由一个 worker 独占到最终完成。
type Dispatcher struct {
	config     *types.Config
	eventQueue <-chan *types.ChangeEvent
	jobs       chan *types.ChangeEvent
	httpClient *http.Client
	taskMap    map[string]string
	metrics    *metrics.Metrics
	ctx        context.Context
	cancel     context.CancelFunc
	workers    sync.WaitGroup
	startOnce  sync.Once
	done       chan struct{}
	running    atomic.Bool
}

// New 创建分发器，指标实例由入口与 HTTP handler 共享。
func New(cfg *types.Config, eventQueue <-chan *types.ChangeEvent, collector *metrics.Metrics) *Dispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Dispatcher{
		config: cfg, eventQueue: eventQueue, ctx: ctx, cancel: cancel,
		jobs:    make(chan *types.ChangeEvent, cfg.Dispatcher.QueueSize*cfg.Dispatcher.WorkerCount),
		taskMap: make(map[string]string), metrics: collector, done: make(chan struct{}),
		httpClient: &http.Client{Timeout: cfg.Dispatcher.Timeout, Transport: &http.Transport{
			MaxIdleConns: cfg.Dispatcher.MaxIdleConns, MaxIdleConnsPerHost: cfg.Dispatcher.MaxIdleConns / 2,
			IdleConnTimeout: cfg.Dispatcher.IdleConnTimeout, MaxConnsPerHost: cfg.Dispatcher.MaxConnections,
		}},
	}
	for _, task := range cfg.Tasks {
		d.taskMap[task.TaskID] = utils.BuildCallbackURL(cfg.CallbackHost, task.CallbackURL)
	}
	return d
}

// Start 启动固定数量的 worker 和事件循环。
func (d *Dispatcher) Start() {
	d.startOnce.Do(func() {
		d.running.Store(true)
		for i := 0; i < d.config.Dispatcher.WorkerCount; i++ {
			d.workers.Add(1)
			go d.worker()
		}
		go d.eventLoop()
	})
}

// Running 返回分发器是否仍可接收并处理事件。
func (d *Dispatcher) Running() bool { return d.running.Load() }

func (d *Dispatcher) recordFailure() {
	d.metrics.IncrementEventsFailed()
	d.metrics.IncrementEventsDropped()
}

// Stop 等待排空；调用者必须先停止生产者并关闭 eventQueue。超时后取消在途请求和重试。
func (d *Dispatcher) Stop(ctx context.Context) error {
	defer d.httpClient.CloseIdleConnections()
	defer d.cancel()
	select {
	case <-d.done:
		return nil
	case <-ctx.Done():
		d.cancel()
		<-d.done
		return ctx.Err()
	}
}

func (d *Dispatcher) eventLoop() {
	defer close(d.done)
	defer d.running.Store(false)
	for event := range d.eventQueue {
		if event == nil {
			d.recordFailure()
			continue
		}
		select {
		case d.jobs <- event:
			d.metrics.IncrementEventsQueued()
		case <-d.ctx.Done():
			d.recordFailure()
		}
	}
	close(d.jobs)
	d.workers.Wait()
}

func (d *Dispatcher) worker() {
	defer d.workers.Done()
	for event := range d.jobs {
		err := d.deliver(event)
		if err != nil {
			d.recordFailure()
			log.Error("Webhook delivery failed; event abandoned", log.String("task_id", event.TaskID), log.Any("error", err))
		} else {
			d.metrics.IncrementEventsSucceeded()
		}
	}
}

func buildWebhookPayload(event *types.ChangeEvent) *types.WebhookPayload {
	payload := &types.WebhookPayload{PrimaryID: event.PrimaryID, Event: event.Event, Table: event.Table, Timestamp: event.Timestamp}
	switch event.Event {
	case types.EventInsert, types.EventDelete:
		payload.Data = event.NewData
	case types.EventUpdate:
		payload.OldData = event.OldData
		payload.NewData = event.NewData
	}
	return payload
}

func (d *Dispatcher) deliver(event *types.ChangeEvent) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	callbackURL, ok := d.taskMap[event.TaskID]
	if !ok {
		return fmt.Errorf("task not found: %s", event.TaskID)
	}
	// JSON 仅属于当前变更，重试复用同一份字节，不跨事件共享，也不进入对象池。
	data, err := json.Marshal(buildWebhookPayload(event))
	if err != nil {
		return fmt.Errorf("marshal webhook: %w", err)
	}
	d.metrics.AddCacheSize(1)
	defer d.metrics.AddCacheSize(-1)
	delay := d.config.Dispatcher.RetryBaseDelay
	for attempt := 0; ; attempt++ {
		err = d.send(callbackURL, data)
		if err == nil {
			return nil
		}
		if d.ctx.Err() != nil {
			return d.ctx.Err()
		}
		if attempt >= d.config.Dispatcher.MaxRetries {
			return fmt.Errorf("task %s exhausted retries: %w", event.TaskID, err)
		}
		// 保持原有首次等待 2 * base 的行为，并在乘法前限制溢出。
		if delay >= d.config.Dispatcher.RetryMaxDelay/2 {
			delay = d.config.Dispatcher.RetryMaxDelay
		} else {
			delay *= 2
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-d.ctx.Done():
			timer.Stop()
			return d.ctx.Err()
		}
		d.metrics.IncrementRetries()
	}
}

func (d *Dispatcher) send(callbackURL string, data []byte) error {
	req, err := http.NewRequestWithContext(d.ctx, http.MethodPost, callbackURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", utils.GetUserAgent())
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send webhook: %w", err)
	}
	// 限制忽略的响应数据量；小响应读至 EOF 以复用连接，大响应直接关闭。
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status code: %d", resp.StatusCode)
	}
	return nil
}
