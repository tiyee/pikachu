// Package testutil 提供仅供回归测试使用的本地 MySQL 协议服务器。
package testutil

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/server"
	"pikachu/internal/types"
)

// QueryHandler 在测试中模拟特定 SQL 的结果或故障，其余查询使用基础回复。
type QueryHandler func(string) (*mysql.Result, error)

type handler struct {
	server.EmptyHandler
	query QueryHandler
}

func (h handler) UseDB(string) error { return nil }
func (h handler) HandleQuery(query string) (*mysql.Result, error) {
	if h.query != nil {
		return h.query(query)
	}
	return DefaultQuery(query)
}

// Result 构造文本协议结果集，不依赖真实数据库。
func Result(names []string, rows [][]interface{}) (*mysql.Result, error) {
	rs, err := mysql.BuildSimpleResultset(names, rows, false)
	return &mysql.Result{Resultset: rs}, err
}

// DefaultQuery 提供连接、表权限和 canal 初始化所需的最小结果。
func DefaultQuery(query string) (*mysql.Result, error) {
	q := strings.ToLower(query)
	switch {
	case strings.Contains(q, "binlog_format"):
		return Result([]string{"Variable_name", "Value"}, [][]interface{}{{"binlog_format", "ROW"}})
	case strings.Contains(q, "binlog_row_image"):
		return Result([]string{"Variable_name", "Value"}, [][]interface{}{{"binlog_row_image", "FULL"}})
	case strings.Contains(q, "show master status") || strings.Contains(q, "show binary log status"):
		return Result([]string{"File", "Position"}, [][]interface{}{{"binlog.000001", uint64(4)}})
	case strings.HasPrefix(q, "select *"):
		return Result([]string{"id"}, [][]interface{}{})
	default:
		return &mysql.Result{}, nil
	}
}

// StartMySQL 启动回环协议服务器并在测试结束时关闭所有连接。
func StartMySQL(t *testing.T, query QueryHandler) types.DatabaseConfig {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := server.NewDefaultServer()
	var mu sync.Mutex
	connections := make(map[net.Conn]bool)
	var handlers sync.WaitGroup
	acceptedDone := make(chan struct{})
	go func() {
		defer close(acceptedDone)
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections[conn] = true
			mu.Unlock()
			handlers.Add(1)
			go func() {
				defer handlers.Done()
				defer func() { conn.Close(); mu.Lock(); delete(connections, conn); mu.Unlock() }()
				c, err := s.NewConn(conn, "review", "synthetic", handler{query: query})
				if err != nil {
					return
				}
				for !c.Closed() {
					if err := c.HandleCommand(); err != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		l.Close()
		<-acceptedDone
		mu.Lock()
		for c := range connections {
			c.Close()
		}
		mu.Unlock()
		handlers.Wait()
	})
	host, port, _ := net.SplitHostPort(l.Addr().String())
	p, _ := strconv.Atoi(port)
	return types.DatabaseConfig{Host: host, Port: p, User: "review", Password: "synthetic", Database: "review", ServerID: 99, Charset: "utf8mb4", ConnectTimeout: time.Second, ReadTimeout: time.Second}
}
