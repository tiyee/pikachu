package monitor

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/schema"
	"pikachu/internal/log"
	"pikachu/internal/testutil"
	"pikachu/internal/types"
)

func TestMain(m *testing.M) {
	if err := log.Init(&types.LogConfig{Level: types.LogLevelFatal, Format: "text"}); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func TestNewRequiresFullRowImagesAndActualSelectAccess(t *testing.T) {
	for _, tc := range []struct {
		name, image string
		deny        bool
	}{
		{"full", "FULL", false}, {"minimal", "MINIMAL", false}, {"noblob", "NOBLOB", false}, {"denied", "FULL", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &types.Config{Database: testutil.StartMySQL(t, func(q string) (*mysql.Result, error) {
				if strings.Contains(q, "SHOW GRANTS") {
					t.Error("grant text must not be used")
				}
				if tc.deny && strings.HasPrefix(q, "SELECT *") {
					return nil, mysql.NewError(mysql.ER_TABLEACCESS_DENIED_ERROR, "SELECT denied")
				}
				if strings.Contains(q, "binlog_row_image") {
					return testutil.Result([]string{"Variable_name", "Value"}, [][]interface{}{{"binlog_row_image", tc.image}})
				}
				return testutil.DefaultQuery(q)
			}), Tasks: []types.Task{{TaskID: "t", TableName: "users", Events: []types.EventType{types.EventInsert}}}}
			m, err := New(context.Background(), c, make(chan *types.ChangeEvent, 1), nil)
			if tc.image != "FULL" || tc.deny {
				if err == nil {
					m.Stop()
					t.Fatal("unsupported configuration accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if m.Running() {
				t.Fatal("running before Start")
			}
			m.Stop()
			m.Stop()
		})
	}
}

func TestCanalInitializationCancellationClosesSQLConnection(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	db := testutil.StartMySQL(t, func(q string) (*mysql.Result, error) {
		if strings.Contains(q, "binlog_format") {
			once.Do(func() { close(entered) })
			<-release
		}
		return testutil.DefaultQuery(q)
	})
	t.Cleanup(func() { close(release) })
	c := &types.Config{Database: db, Tasks: []types.Task{{TableName: "users"}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		m, err := New(ctx, c, make(chan *types.ChangeEvent, 1), nil)
		if m != nil {
			m.Stop()
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("query not reached")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected error %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canal SQL did not cancel")
	}
}

func TestCanalInitializationErrorIsNotMaskedByCleanup(t *testing.T) {
	db := testutil.StartMySQL(t, func(q string) (*mysql.Result, error) {
		if strings.Contains(q, "binlog_format") {
			return testutil.Result([]string{"Variable_name", "Value"}, [][]interface{}{{"binlog_format", "STATEMENT"}})
		}
		return testutil.DefaultQuery(q)
	})
	m, err := New(context.Background(), &types.Config{Database: db, Tasks: []types.Task{{TableName: "users"}}}, make(chan *types.ChangeEvent, 1), nil)
	if m != nil {
		m.Stop()
	}
	if err == nil || errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "STATEMENT") {
		t.Fatalf("initialization error masked: %v", err)
	}
}

func TestDatabaseReadTimeout(t *testing.T) {
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := databaseDialer(ctx, types.DatabaseConfig{ConnectTimeout: time.Second, ReadTimeout: 20 * time.Millisecond})(ctx, "tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	serverConn := <-accepted
	defer serverConn.Close()
	_, err = conn.Read(make([]byte, 1))
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("missing read timeout: %v", err)
	}
}

func TestUpdateDeletePayloadAndPrimaryKeys(t *testing.T) {
	m := testMonitor(t)
	m.eventQueue = make(chan *types.ChangeEvent, 2)
	m.eventTaskMap = map[string][]*types.Task{"users.insert": {{TaskID: "t"}}, "users.update": {{TaskID: "t"}}, "users.delete": {{TaskID: "t"}}}
	table := &schema.Table{Schema: "prod", Name: "users", Columns: []schema.TableColumn{{Name: "tenant"}, {Name: "id"}, {Name: "value"}}, Indexes: []*schema.Index{{Name: "PRIMARY", Columns: []string{"tenant", "id"}}}}
	if err := m.OnRow(&canal.RowsEvent{Table: table, Action: canal.UpdateAction, Rows: [][]interface{}{{"a", 1, "old"}, {"a", 2, nil}}}); err != nil {
		t.Fatal(err)
	}
	e := <-m.eventQueue
	pk := e.PrimaryID.(map[string]interface{})
	if pk["id"] != 2 || pk["tenant"] != "a" || e.OldData["value"] != "old" || e.NewData["value"] != nil {
		t.Fatal("wrong update or composite key")
	}
	if err := m.OnRow(&canal.RowsEvent{Table: table, Action: canal.DeleteAction, Rows: [][]interface{}{{"a", 2, "old"}}}); err != nil {
		t.Fatal(err)
	}
	e = <-m.eventQueue
	if e.Event != types.EventDelete || e.NewData["value"] != "old" {
		t.Fatal("wrong delete image")
	}
	if err := m.OnRow(&canal.RowsEvent{Table: table, Action: canal.UpdateAction, Rows: [][]interface{}{{"a", 1, "old"}}}); err == nil {
		t.Fatal("odd row pairs accepted")
	}
	if err := m.OnRow(&canal.RowsEvent{Table: table, Action: canal.InsertAction, Rows: [][]interface{}{{"a", 1}}}); err == nil {
		t.Fatal("short row image accepted")
	}
}

type fakeReplication struct {
	started chan struct{}
	stopped chan struct{}
	once    sync.Once
}

func (f *fakeReplication) GetMasterPos() (mysql.Position, error) {
	return mysql.Position{Name: "binlog.000001", Pos: 4}, nil
}
func (f *fakeReplication) RunFrom(mysql.Position) error { close(f.started); <-f.stopped; return nil }
func (f *fakeReplication) Close()                       { f.once.Do(func() { close(f.stopped) }) }

func TestMonitorStartStopLifecycle(t *testing.T) {
	m := testMonitor(t)
	f := &fakeReplication{started: make(chan struct{}), stopped: make(chan struct{})}
	m.canal = f
	done := make(chan error, 1)
	go func() { done <- m.Start() }()
	<-f.started
	if !m.Running() {
		t.Fatal("not running")
	}
	m.Stop()
	m.Stop()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if m.Running() {
		t.Fatal("still running")
	}
}

func TestStopCancelsMasterPositionQuery(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	db := testutil.StartMySQL(t, func(q string) (*mysql.Result, error) {
		if strings.Contains(strings.ToLower(q), "show master status") || strings.Contains(strings.ToLower(q), "show binary log status") {
			once.Do(func() { close(entered) })
			<-release
		}
		return testutil.DefaultQuery(q)
	})
	t.Cleanup(func() { close(release) })
	c := &types.Config{Database: db, Tasks: []types.Task{{TableName: "users"}}}
	m, err := New(context.Background(), c, make(chan *types.ChangeEvent, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- m.Start() }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("master position query not reached")
	}
	stopped := make(chan struct{})
	go func() { m.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop blocked on canal SQL lock")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected error %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start did not exit")
	}
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
		row := []interface{}{1}
		if db != "prod" {
			row = nil
		}
		err := m.OnRow(&canal.RowsEvent{Action: canal.InsertAction, Table: &schema.Table{Schema: db, Name: "users", Columns: []schema.TableColumn{{Name: "id"}}}, Rows: [][]interface{}{row}})
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
