package monitor

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/schema"
	"pikachu/internal/log"
	"pikachu/internal/types"
)

func TestMain(m *testing.M) {
	if err := log.Init(&types.LogConfig{Level: types.LogLevelFatal, Format: "text"}); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
func testMonitor(t *testing.T) *Monitor {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &Monitor{ctx: ctx, cancel: cancel, eventQueue: make(chan *types.ChangeEvent, 1),
		config:       &types.Config{Database: types.DatabaseConfig{Host: "localhost", Port: 3306, Database: "prod"}, Monitor: types.MonitorConfig{EventQueueTimeout: time.Millisecond}},
		eventTaskMap: map[string][]*types.Task{"users.insert": {{TaskID: "t"}}}}
}
func TestOnRowRejectsOtherDatabase(t *testing.T) {
	m := testMonitor(t)
	for _, db := range []string{"backup_prod", "prod", "prod_backup"} {
		err := m.OnRow(&canal.RowsEvent{Action: canal.InsertAction, Table: &schema.Table{Schema: db, Name: "users", Columns: []schema.TableColumn{{Name: "id"}}}, Rows: [][]interface{}{{1}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(m.eventQueue) != 1 {
		t.Fatal("unexpected event count")
	}
	if e := <-m.eventQueue; e.TaskID != "t" || e.NewData["id"] != 1 {
		t.Fatal("wrong event")
	}
}
func TestEnqueueWaitsBeyondWarningTimeout(t *testing.T) {
	m := testMonitor(t)
	first := &types.ChangeEvent{TaskID: "first"}
	second := &types.ChangeEvent{TaskID: "second"}
	m.eventQueue <- first
	done := make(chan error, 1)
	go func() { done <- m.enqueue(second) }()
	select {
	case err := <-done:
		t.Fatalf("queue timeout dropped event: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	<-m.eventQueue
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if <-m.eventQueue != second {
		t.Fatal("wrong queued event")
	}
}
func TestStopUnblocksFullQueue(t *testing.T) {
	m := testMonitor(t)
	m.eventQueue <- &types.ChangeEvent{}
	done := make(chan error, 1)
	go func() { done <- m.enqueue(&types.ChangeEvent{}) }()
	m.Stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("producer did not stop")
	}
}
