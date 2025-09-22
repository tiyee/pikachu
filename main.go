package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"go.uber.org/zap"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// 全局变量
var sigChan chan os.Signal
var eventQueue chan *ChangeEvent

// 系统状态信息
var systemStatus = struct {
	mutex             sync.RWMutex
	MonitorRunning    bool
	DispatcherRunning bool
	EventQueueSize    int
	LastEventTime     time.Time
}{}

func main() {
	// 初始化日志
	InitLogger()

	// 解析命令行参数
	configFile := flag.String("config", "config.yaml", "配置文件路径")
	flag.Parse()

	Logger.Info("Starting pikachu MySQL monitor tool")

	// 加载配置
	config, err := LoadConfig(*configFile)
	if err != nil {
		Logger.Fatal("Failed to load config", zap.Error(err))
	}

	// 验证配置
	if err := ValidateConfig(config); err != nil {
		Logger.Fatal("Invalid config", zap.Error(err))
	}

	// 配置日志系统
	ConfigureLogger(&config.Log)

	// 如果启用了HTTP服务器，则启动健康检查
	if config.Server.Enabled {
		go startHealthCheckServer(config)
	}

	// 检查数据库权限
	if err := CheckDatabasePermissions(&config.Database); err != nil {
		Logger.Fatal("Database permission check failed", zap.Error(err))
	}

	// 创建事件队列 (缓冲大小为1000)
	eventQueue = make(chan *ChangeEvent, 1000)

	// 创建事件回调函数，用于更新系统状态
	eventCallback := func() {
		systemStatus.mutex.Lock()
		systemStatus.LastEventTime = time.Now()
		systemStatus.mutex.Unlock()
	}

	// 创建监控器
	monitor, err := NewMonitor(config, eventQueue, eventCallback)
	if err != nil {
		Logger.Fatal("Failed to create monitor", zap.Error(err))
	}

	// 创建分发器
	dispatcher := NewDispatcher(config, eventQueue)

	// 启动分发器
	dispatcher.Start()
	systemStatus.mutex.Lock()
	systemStatus.DispatcherRunning = true
	systemStatus.mutex.Unlock()

	// 启动监控器 (在单独的goroutine中)
	go func() {
		if err := monitor.Start(); err != nil {
			Logger.Fatal("Monitor failed", zap.Error(err))
		}
	}()
	systemStatus.mutex.Lock()
	systemStatus.MonitorRunning = true
	systemStatus.mutex.Unlock()

	// 等待一小段时间确保监控器正常启动
	time.Sleep(2 * time.Second)
	Logger.Info("pikachu started successfully")

	// 等待中断信号
	sigChan = make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	Logger.Info("Received shutdown signal")

	// 更新系统状态
	systemStatus.mutex.Lock()
	systemStatus.MonitorRunning = false
	systemStatus.DispatcherRunning = false
	systemStatus.mutex.Unlock()

	// 优雅关闭
	monitor.Stop()
	dispatcher.Stop()

	Logger.Info("pikachu stopped")
	// 关闭日志器，确保所有日志都被刷新
	CloseLogger()
}

// startHealthCheckServer 启动健康检查HTTP服务器
func startHealthCheckServer(config *Config) {
	// 设置默认值
	port := config.Server.Port
	if port == 0 {
		port = 8080
	}

	path := config.Server.Path
	if path == "" {
		path = "/health"
	}

	// 健康检查处理器
	http.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		systemStatus.mutex.RLock()
		status := map[string]interface{}{
			"status":             "UP",
			"monitor_running":    systemStatus.MonitorRunning,
			"dispatcher_running": systemStatus.DispatcherRunning,
			"event_queue_size":   len(eventQueue),
			"last_event_time":    systemStatus.LastEventTime,
		}
		systemStatus.mutex.RUnlock()

		// 检查是否所有关键组件都正常运行
		healthy := systemStatus.MonitorRunning && systemStatus.DispatcherRunning
		if !healthy {
			status["status"] = "DOWN"
			w.WriteHeader(http.StatusServiceUnavailable)
		}

		// 返回JSON响应
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(status)
	})

	//  metrics处理器
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		systemStatus.mutex.RLock()
		defer systemStatus.mutex.RUnlock()

		// 返回基本的metrics信息
		metrics := map[string]interface{}{
			"task_count":         len(config.Tasks),
			"monitor_running":    systemStatus.MonitorRunning,
			"dispatcher_running": systemStatus.DispatcherRunning,
			"event_queue_size":   len(eventQueue),
			"last_event_time":    systemStatus.LastEventTime,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(metrics)
	})

	// 启动HTTP服务器
	serverAddr := fmt.Sprintf(":%d", port)
	Logger.Info("Starting health check server on %s", zap.String("address", serverAddr))

	// 创建HTTP服务器对象以便可以优雅关闭
	server := &http.Server{
		Addr: serverAddr,
	}

	// 在单独的goroutine中启动服务器
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			Logger.Error("Failed to start health check server", zap.Error(err))
		}
	}()

	// 等待中断信号以优雅地关闭服务器
	go func() {
		<-sigChan
		Logger.Info("Shutting down health check server")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			Logger.Error("Health check server shutdown failed", zap.Error(err))
		}
	}()
}
