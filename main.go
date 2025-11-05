package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"

	"pikachu/internal/config"
	"pikachu/internal/dispatcher"
	"pikachu/internal/log"
	"pikachu/internal/metrics"
	"pikachu/internal/monitor"
	"pikachu/internal/types"
	"pikachu/internal/utils"
)

// 全局变量
var eventQueue chan *types.ChangeEvent

// 系统状态信息
var systemStatus = struct {
	mutex             sync.RWMutex
	MonitorRunning    bool
	DispatcherRunning bool
	EventQueueSize    int
	LastEventTime     time.Time
}{}

// 全局指标收集器
var globalMetrics *metrics.Metrics

func main() {
	// 解析命令行参数
	configFile := flag.String("config", "config.yaml", "配置文件路径")
	showVersion := flag.Bool("version", false, "显示版本信息")
	flag.Parse()

	// 如果请求显示版本，则显示并退出
	if *showVersion {
		fmt.Printf("pikachu version %s\n", utils.Version)
		return
	}

	// 加载配置
	cfg, err := config.LoadConfig(*configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		return
	}

	// 验证配置
	if err := config.ValidateConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid config: %v\n", err)
		return
	}

	// 初始化日志系统（根据配置）
	if err := log.Init(&cfg.Log); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return
	}

	log.Info("Starting pikachu MySQL monitor tool",
		zap.String("version", utils.Version))
	log.Info("LoadConfig success")
	log.Info("ValidateConfig success")

	// 如果启用了HTTP服务器，则启动健康检查
	if cfg.Server.Enabled {
		go startHealthCheckServer(cfg)
	}

	// 检查数据库权限
	if err := monitor.CheckDatabasePermissions(&cfg.Database); err != nil {
		log.Fatal("Database permission check failed", zap.Error(err))
	}
	log.Info("Database permission check passed")
	// 创建事件队列 (使用配置的缓冲大小)
	eventQueue = make(chan *types.ChangeEvent, cfg.Monitor.EventQueueSize)

	// 创建事件回调函数，用于更新系统状态
	eventCallback := func() {
		systemStatus.mutex.Lock()
		systemStatus.LastEventTime = time.Now()
		systemStatus.mutex.Unlock()
	}

	// 创建监控器
	mon, err := monitor.New(cfg, eventQueue, eventCallback)
	if err != nil {
		log.Fatal("Failed to create monitor", zap.Error(err))
	}
	log.Info("Create monitor success")
	// 初始化全局指标收集器
	globalMetrics = metrics.NewMetrics()

	// 创建分发器
	dispatch := dispatcher.New(cfg, eventQueue)

	// 启动分发器
	dispatch.Start()
	systemStatus.mutex.Lock()
	systemStatus.DispatcherRunning = true
	systemStatus.mutex.Unlock()

	// 启动监控器 (在单独的goroutine中)
	go func() {
		if err := mon.Start(); err != nil {
			log.Fatal("Monitor failed", zap.Error(err))
		}
	}()
	systemStatus.mutex.Lock()
	systemStatus.MonitorRunning = true
	systemStatus.mutex.Unlock()

	// 等待一小段时间确保监控器正常启动
	time.Sleep(2 * time.Second)
	log.Info("pikachu started successfully")

	// 等待中断信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	log.Info("Received shutdown signal")

	// 更新系统状态
	systemStatus.mutex.Lock()
	systemStatus.MonitorRunning = false
	systemStatus.DispatcherRunning = false
	systemStatus.mutex.Unlock()

	// 优雅关闭
	mon.Stop()
	dispatch.Stop()

	log.Info("pikachu stopped")
	// 关闭日志器，确保所有日志都被刷新
	log.Close()
}

// startHealthCheckServer 启动健康检查HTTP服务器
func startHealthCheckServer(cfg *types.Config) {
	// 设置默认值
	port := cfg.Server.Port
	if port == 0 {
		port = 8080
	}

	path := cfg.Server.Path
	if path == "" {
		path = "/health"
	}

	// 健康检查处理器
	http.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		systemStatus.mutex.RLock()
		queueSize := len(eventQueue)
		monitorRunning := systemStatus.MonitorRunning
		dispatcherRunning := systemStatus.DispatcherRunning
		lastEventTime := systemStatus.LastEventTime
		systemStatus.mutex.RUnlock()

		status := map[string]interface{}{
			"status":             "UP",
			"monitor_running":    monitorRunning,
			"dispatcher_running": dispatcherRunning,
			"event_queue_size":   queueSize,
			"last_event_time":    lastEventTime,
		}

		// 检查是否所有关键组件都正常运行
		healthy := monitorRunning && dispatcherRunning
		if !healthy {
			status["status"] = "DOWN"
			w.WriteHeader(http.StatusServiceUnavailable)
		}

		// 返回JSON响应
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(status)
	})

	// Prometheus metrics端点已移除，仅保留基本的健康检查

	// 自定义指标端点（返回基本JSON格式的指标）
	http.HandleFunc("/metrics-json", func(w http.ResponseWriter, r *http.Request) {
		systemStatus.mutex.RLock()
		defer systemStatus.mutex.RUnlock()

		// 返回基本的metrics信息
		metricsData := map[string]interface{}{
			"task_count":         len(cfg.Tasks),
			"monitor_running":    systemStatus.MonitorRunning,
			"dispatcher_running": systemStatus.DispatcherRunning,
			"event_queue_size":   len(eventQueue),
			"last_event_time":    systemStatus.LastEventTime,
			"events_queued":      globalMetrics.GetEventsQueued(),
			"events_dropped":     globalMetrics.GetEventsDropped(),
			"cache_size":         globalMetrics.GetCacheSize(),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(metricsData)
	})

	// 启动HTTP服务器
	serverAddr := fmt.Sprintf(":%d", port)
	log.Info("Starting health check server", zap.String("address", serverAddr))

	// 创建HTTP服务器对象以便可以优雅关闭
	server := &http.Server{
		Addr: serverAddr,
	}

	// 在单独的goroutine中启动服务器
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("Failed to start health check server", zap.Error(err))
		}
	}()

	// 创建信号通道用于关闭服务器
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 等待中断信号以优雅地关闭服务器
	go func() {
		<-sigChan
		log.Info("Shutting down health check server")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Error("Health check server shutdown failed", zap.Error(err))
		}
	}()
}
