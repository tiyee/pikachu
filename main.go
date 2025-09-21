package main

import (
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// 初始化日志
	InitLogger()

	// 解析命令行参数
	configFile := flag.String("config", "config.yaml", "配置文件路径")
	flag.Parse()

	Logger.Info("Starting Pikachu MySQL monitor tool")

	// 加载配置
	config, err := LoadConfig(*configFile)
	if err != nil {
		Logger.WithError(err).Fatal("Failed to load config")
	}

	// 验证配置
	if err := ValidateConfig(config); err != nil {
		Logger.WithError(err).Fatal("Invalid config")
	}

	// 检查数据库权限
	if err := CheckDatabasePermissions(&config.Database); err != nil {
		Logger.WithError(err).Fatal("Database permission check failed")
	}

	// 创建事件队列 (缓冲大小为1000)
	eventQueue := make(chan *ChangeEvent, 1000)

	// 创建监控器
	monitor, err := NewMonitor(config, eventQueue)
	if err != nil {
		Logger.WithError(err).Fatal("Failed to create monitor")
	}

	// 创建分发器
	dispatcher := NewDispatcher(config, eventQueue)

	// 启动分发器
	dispatcher.Start()

	// 启动监控器 (在单独的goroutine中)
	go func() {
		if err := monitor.Start(); err != nil {
			Logger.WithError(err).Fatal("Monitor failed")
		}
	}()

	// 等待一小段时间确保监控器正常启动
	time.Sleep(2 * time.Second)
	Logger.Info("Pikachu started successfully")

	// 等待中断信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	Logger.Info("Received shutdown signal")

	// 优雅关闭
	monitor.Stop()
	dispatcher.Stop()

	Logger.Info("Pikachu stopped")
}
