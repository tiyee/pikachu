package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"pikachu/internal/config"
	"pikachu/internal/dispatcher"
	"pikachu/internal/log"
	"pikachu/internal/metrics"
	"pikachu/internal/monitor"
	"pikachu/internal/types"
	"pikachu/internal/utils"
)

// healthState 仅保护事件时间，组件状态由组件自己的原子状态读取。
type healthState struct {
	mutex     sync.RWMutex
	lastEvent time.Time
}

func (s *healthState) eventReceived() { s.mutex.Lock(); s.lastEvent = time.Now(); s.mutex.Unlock() }
func (s *healthState) lastEventTime() time.Time {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.lastEvent
}

func main() {
	configFile := flag.String("config", "config.yaml", "配置文件路径")
	tasksFile := flag.String("tasks", "tasks.yaml", "任务配置文件路径")
	showVersion := flag.Bool("version", false, "显示版本信息")
	flag.Parse()
	if *showVersion {
		fmt.Printf("Pikachu version %s\n", utils.Version)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *configFile, *tasksFile); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, configFile, tasksFile string) error {
	cfg, err := config.LoadConfig(configFile, tasksFile)
	if err != nil {
		return err
	}
	if err = config.ValidateConfig(cfg); err != nil {
		return err
	}
	if err = log.Init(&cfg.Log); err != nil {
		return err
	}
	defer log.Close()
	if err = monitor.CheckDatabasePermissions(&cfg.Database); err != nil {
		return err
	}
	queue := make(chan *types.ChangeEvent, cfg.Monitor.EventQueueSize)
	collector := metrics.NewMetrics()
	state := &healthState{}
	mon, err := monitor.New(cfg, queue, state.eventReceived)
	if err != nil {
		return err
	}
	dispatch := dispatcher.New(cfg, queue, collector)

	var server *http.Server
	serverErrors := make(chan error, 1)
	if cfg.Server.Enabled {
		handler, err := newHealthHandler(cfg, queue, collector, state, mon.Running, dispatch.Running)
		if err != nil {
			mon.Stop()
			return err
		}
		port := cfg.Server.Port
		if port == 0 {
			port = 8080
		}
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			mon.Stop()
			return fmt.Errorf("listen health server: %w", err)
		}
		server = &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				serverErrors <- err
			}
		}()
		defer func() {
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdown); err != nil {
				server.Close()
				log.Error("Health server shutdown failed", log.Any("error", err))
			}
		}()
	}
	dispatch.Start()
	monitorDone := make(chan error, 1)
	go func() { monitorDone <- mon.Start() }()
	log.Info("Pikachu started", log.String("version", utils.Version))
	monitorExited := false
	select {
	case <-ctx.Done():
	case err = <-monitorDone:
		monitorExited = true
	case err = <-serverErrors:
	}
	// 先取消生产并等待生产者完全退出，随后才关闭共享队列。
	mon.Stop()
	if !monitorExited {
		<-monitorDone
	}
	close(queue)
	drain, cancel := context.WithTimeout(context.Background(), cfg.Dispatcher.ShutdownTimeout)
	defer cancel()
	drainErr := dispatch.Stop(drain)
	return errors.Join(err, drainErr)
}

// newHealthHandler 在所有依赖初始化后构造独立 mux，避免全局变量的启动期竞争。
func newHealthHandler(cfg *types.Config, queue <-chan *types.ChangeEvent, collector *metrics.Metrics, state *healthState, monitorRunning, dispatcherRunning func() bool) (http.Handler, error) {
	path := cfg.Server.Path
	if path == "" {
		path = "/health"
	}
	if !strings.HasPrefix(path, "/") || path == "/metrics-json" || strings.ContainsAny(path, " {}\t\r\n?#") {
		return nil, fmt.Errorf("invalid health path: %q", path)
	}
	mux := http.NewServeMux()
	snapshot := func() map[string]interface{} {
		return map[string]interface{}{"monitor_running": monitorRunning(), "dispatcher_running": dispatcherRunning(), "event_queue_size": len(queue), "last_event_time": state.lastEventTime()}
	}
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		result := snapshot()
		result["status"] = "UP"
		w.Header().Set("Content-Type", "application/json")
		if !result["monitor_running"].(bool) || !result["dispatcher_running"].(bool) {
			result["status"] = "DOWN"
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("/metrics-json", func(w http.ResponseWriter, r *http.Request) {
		result := snapshot()
		result["task_count"] = len(cfg.Tasks)
		result["events_queued"] = collector.GetEventsQueued()
		result["events_dropped"] = collector.GetEventsDropped()
		result["events_succeeded"] = collector.GetEventsSucceeded()
		result["events_failed"] = collector.GetEventsFailed()
		result["webhook_retries"] = collector.GetRetries()
		result["cache_size"] = collector.GetCacheSize()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
	return mux, nil
}
