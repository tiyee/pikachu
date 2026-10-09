package dispatcher

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"pikachu/internal/log"
	"pikachu/internal/metrics"
	"pikachu/internal/types"
)

func TestMain(m *testing.M) {
	if err := log.Init(&types.LogConfig{Level: types.LogLevelFatal, Format: "text"}); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func startDispatcher(t *testing.T, url string, workers, retries int) (*Dispatcher, chan *types.ChangeEvent, *metrics.Metrics) {
	t.Helper()
	cfg := &types.Config{Tasks: []types.Task{{TaskID: "t", CallbackURL: url}}, Dispatcher: types.DispatcherConfig{
		WorkerCount: workers, QueueSize: 1, Timeout: time.Second, MaxRetries: retries, RetryBaseDelay: time.Millisecond, RetryMaxDelay: 4 * time.Millisecond,
	}}
	queue := make(chan *types.ChangeEvent, 1)
	collector := metrics.NewMetrics()
	d := New(cfg, queue, collector)
	d.Start()
	return d, queue, collector
}
func event(value string) *types.ChangeEvent {
	return &types.ChangeEvent{TaskID: "t", Table: "users", Event: types.EventUpdate, PrimaryID: 1, NewData: map[string]interface{}{"value": value}}
}
func stopDispatcher(t *testing.T, d *Dispatcher, q chan *types.ChangeEvent) error {
	t.Helper()
	close(q)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return d.Stop(ctx)
}
func TestDistinctChangesAndRetriesKeepOwnJSON(t *testing.T) {
	var mu sync.Mutex
	bodies := make(map[string][]string)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var payload types.WebhookPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Error(err)
		}
		value, _ := payload.NewData["value"].(string)
		mu.Lock()
		bodies[value] = append(bodies[value], string(raw))
		attempt := len(bodies[value])
		mu.Unlock()
		if attempt == 1 {
			w.WriteHeader(500)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	d, q, m := startDispatcher(t, server.URL, 2, 1)
	q <- event("first")
	q <- event("second")
	if err := stopDispatcher(t, d, q); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"first", "second"} {
		if len(bodies[value]) != 2 {
			t.Fatalf("%s requests: %v", value, bodies)
		}
		if bodies[value][0] != bodies[value][1] {
			t.Fatal("retry changed payload")
		}
	}
	if m.GetEventsSucceeded() != 2 || m.GetRetries() != 2 || m.GetCacheSize() != 0 {
		t.Fatal("incorrect terminal metrics")
	}
}

func TestSaturatedQueueAppliesBackpressureAndDrains(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { entered <- struct{}{}; <-release })
		w.WriteHeader(204)
	}))
	defer server.Close()
	d, q, m := startDispatcher(t, server.URL, 1, 0)
	producerDone := make(chan struct{})
	go func() {
		for i := 0; i < 30; i++ {
			q <- event("value")
		}
		close(q)
		close(producerDone)
	}()
	<-entered
	select {
	case <-producerDone:
		t.Error("producer did not encounter backpressure")
	default:
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := d.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	<-producerDone
	if m.GetEventsQueued() != 30 || m.GetEventsSucceeded() != 30 || m.GetEventsDropped() != 0 || m.GetCacheSize() != 0 {
		t.Fatal("events were lost or payloads retained")
	}
}

func TestStopWaitsForAllWorkers(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(204)
	}))
	defer server.Close()
	d, q, _ := startDispatcher(t, server.URL, 2, 0)
	q <- event("pending")
	<-entered
	close(q)
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { done <- d.Stop(ctx) }()
	select {
	case <-done:
		t.Error("shutdown returned before HTTP completion")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestFailedDeliveryCountedAndOtherEventsContinue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload types.WebhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.NewData["value"] == "failed" {
			w.WriteHeader(500)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	d, q, m := startDispatcher(t, server.URL, 1, 1)
	q <- event("failed")
	q <- event("successful")
	if err := stopDispatcher(t, d, q); err != nil {
		t.Fatal(err)
	}
	if m.GetEventsFailed() != 1 || m.GetEventsDropped() != 1 || m.GetEventsSucceeded() != 1 || m.GetCacheSize() != 0 || m.GetRetries() != 1 {
		t.Fatal("failed callback stopped processing or metrics are incorrect")
	}
}

func TestShutdownCancelsRetryAndReleasesPayloads(t *testing.T) {
	entered := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- struct{}{}; w.WriteHeader(500) }))
	defer server.Close()
	d, q, m := startDispatcher(t, server.URL, 1, 2)
	// worker 尚未收到事件，修改测试配置不会与读取并发。
	d.config.Dispatcher.RetryBaseDelay = time.Hour
	d.config.Dispatcher.RetryMaxDelay = 3 * time.Hour
	q <- event("pending")
	<-entered
	close(q)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := d.Stop(ctx); err != context.DeadlineExceeded {
		t.Fatalf("shutdown error: %v", err)
	}
	if m.GetCacheSize() != 0 || m.GetEventsFailed() != 1 || d.Running() {
		t.Fatal("shutdown left active tasks")
	}
}

func TestShutdownCancelsInFlightRequest(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()
	d, q, m := startDispatcher(t, server.URL, 1, 0)
	q <- event("pending")
	<-entered
	close(q)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := d.Stop(ctx); err != context.DeadlineExceeded {
		t.Fatalf("shutdown error: %v", err)
	}
	if m.GetCacheSize() != 0 || m.GetEventsFailed() != 1 {
		t.Fatal("in-flight payload was retained")
	}
}

func TestRedirectsCannotCountAsSuccessfulDelivery(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var targetCalls int
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/start" {
					http.Redirect(w, r, "/target", status)
					return
				}
				targetCalls++
				w.WriteHeader(204)
			}))
			defer s.Close()
			d, q, m := startDispatcher(t, s.URL+"/start", 1, 1)
			q <- event("redirect")
			if err := stopDispatcher(t, d, q); err != nil {
				t.Fatal(err)
			}
			if targetCalls != 0 || m.GetEventsSucceeded() != 0 || m.GetEventsFailed() != 1 || m.GetRetries() != 1 {
				t.Fatal("redirect delivered or counted as success")
			}
		})
	}
}
