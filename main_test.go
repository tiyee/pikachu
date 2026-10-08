package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"pikachu/internal/dispatcher"
	"pikachu/internal/metrics"
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
