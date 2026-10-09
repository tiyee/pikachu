package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-mysql-org/go-mysql/mysql"
	"pikachu/internal/dispatcher"
	"pikachu/internal/metrics"
	"pikachu/internal/testutil"
	"pikachu/internal/types"
)

func TestMetricsHandlerUsesDispatcherCollector(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer target.Close()
	cfg := &types.Config{Tasks: []types.Task{{TaskID: "task", CallbackURL: target.URL}}, Dispatcher: types.DispatcherConfig{WorkerCount: 1, QueueSize: 1, Timeout: time.Second}}
	queue := make(chan *types.ChangeEvent, 1)
	collector := metrics.NewMetrics()
	state := &healthState{}
	d := dispatcher.New(cfg, queue, collector)
	handler, err := newHealthHandler(cfg, queue, collector, state, func() bool { return true }, d.Running)
	if err != nil {
		t.Fatal(err)
	}
	// 启动前也可安全查询，无全局指标的 nil 窗口。
	check := func(want float64) {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/metrics-json", nil))
		var data map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		if data["events_queued"] != want || data["events_succeeded"] != want || data["cache_size"] != float64(0) {
			t.Fatalf("metrics: %v", data)
		}
	}
	check(0)
	d.Start()
	queue <- &types.ChangeEvent{TaskID: "task", Event: types.EventInsert, NewData: map[string]interface{}{"id": 1}}
	close(queue)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := d.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	check(1)
}

func TestRunCancelsDatabaseHandshake(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := l.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	d := t.TempDir()
	file := filepath.Join(d, "config.yaml")
	_, port, _ := net.SplitHostPort(l.Addr().String())
	data := fmt.Sprintf("database:\n  host: 127.0.0.1\n  port: %s\n  user: review\n  database: review\n  server_id: 99\nlog:\n  level: fatal\ntasks:\n  - task_id: t\n    table_name: users\n    events: [insert]\n    callback_url: http://localhost/webhook\n", port)
	if err := os.WriteFile(file, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, file, filepath.Join(d, "missing.yaml")) }()
	select {
	case c := <-accepted:
		defer c.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("not connected")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("startup did not cancel")
	}
}

func TestRunConfigurationFailureAndEnvironmentPaths(t *testing.T) {
	if err := run(context.Background(), filepath.Join(t.TempDir(), "missing.yaml"), ""); err == nil {
		t.Fatal("missing config accepted")
	}
	t.Setenv("CONFIG_PATH", "/custom/config.yaml")
	if envPath("CONFIG_PATH", "config.yaml") != "/custom/config.yaml" {
		t.Fatal("environment ignored")
	}
	t.Setenv("CONFIG_PATH", "")
	if envPath("CONFIG_PATH", "config.yaml") != "config.yaml" {
		t.Fatal("default path changed")
	}
}

func TestRunReleasesHealthListenerAfterMonitorError(t *testing.T) {
	db := testutil.StartMySQL(t, func(q string) (*mysql.Result, error) {
		if strings.Contains(strings.ToLower(q), "show master status") || strings.Contains(strings.ToLower(q), "show binary log status") {
			return nil, mysql.NewError(mysql.ER_SPECIFIC_ACCESS_DENIED_ERROR, "replication client denied")
		}
		return testutil.DefaultQuery(q)
	})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	_, port, _ := net.SplitHostPort(address)
	l.Close()
	d := t.TempDir()
	file := filepath.Join(d, "config.yaml")
	data := fmt.Sprintf("database:\n  host: %s\n  port: %d\n  user: %s\n  password: %s\n  database: review\n  server_id: 99\nlog:\n  level: fatal\nserver:\n  enabled: true\n  port: %s\ntasks:\n  - task_id: t\n    table_name: users\n    events: [insert]\n    callback_url: http://localhost/webhook\n", db.Host, db.Port, db.User, db.Password, port)
	if err := os.WriteFile(file, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := run(ctx, file, filepath.Join(d, "missing.yaml")); err == nil || !strings.Contains(err.Error(), "replication client denied") {
		t.Fatalf("monitor error not propagated: %v", err)
	}
	reopened, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("health listener leaked: %v", err)
	}
	reopened.Close()
}

func TestHealthStartupAndConcurrentMetrics(t *testing.T) {
	cfg := &types.Config{}
	queue := make(chan *types.ChangeEvent, 1)
	collector := metrics.NewMetrics()
	state := &healthState{}
	handler, err := newHealthHandler(cfg, queue, collector, state, func() bool { return false }, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
	if w.Code != 503 || w.Header().Get("Content-Type") != "application/json" {
		t.Fatal("invalid unhealthy response")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				state.eventReceived()
				collector.IncrementEventsQueued()
				handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/metrics-json", nil))
			}
		}()
	}
	wg.Wait()
	if collector.GetEventsQueued() != 400 {
		t.Fatal("counter lost updates")
	}
}

func TestHealthRouteConflictReturnsError(t *testing.T) {
	cfg := &types.Config{Server: types.ServerConfig{Path: "/metrics-json"}}
	if _, err := newHealthHandler(cfg, nil, metrics.NewMetrics(), &healthState{}, func() bool { return false }, func() bool { return false }); err == nil {
		t.Fatal("duplicate route accepted")
	}
}
