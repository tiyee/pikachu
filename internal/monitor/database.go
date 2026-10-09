package monitor

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"pikachu/internal/types"
	"pikachu/internal/utils"
)

// openDatabase 使用驱动构造 DSN，避免密码、数据库名及 IPv6 地址拼接错误。
func openDatabase(c types.DatabaseConfig) (*sql.DB, error) {
	d := driver.NewConfig()
	d.User, d.Passwd, d.DBName = c.User, c.Password, c.Database
	d.Net, d.Addr = "tcp", net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	d.Timeout, d.ReadTimeout, d.WriteTimeout = c.ConnectTimeout, c.ReadTimeout, c.ReadTimeout
	d.Params = map[string]string{"charset": c.Charset}
	connector, err := driver.NewConnector(d)
	if err != nil {
		return nil, fmt.Errorf("configure database: %w", err)
	}
	return sql.OpenDB(connector), nil
}

// validateTables 用实际查询验证连接和 SELECT 权限，不猜测 SHOW GRANTS 文本。
func (m *Monitor) validateTables() error {
	db, err := openDatabase(m.config.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(m.ctx, m.config.Database.ConnectTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	seen := make(map[string]bool)
	for _, task := range m.config.Tasks {
		if seen[task.TableName] {
			continue
		}
		seen[task.TableName] = true
		if err := checkTable(m.ctx, db, task.TableName, m.config.Database.ReadTimeout); err != nil {
			return err
		}
	}
	return nil
}

// checkTable 每张表单独限时；复制权限由 master status 查询和复制握手验证。
func checkTable(parent context.Context, db *sql.DB, table string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	rows, err := db.QueryContext(ctx, "SELECT * FROM "+utils.EnsureQuoted(table)+" LIMIT 0")
	if err != nil {
		return fmt.Errorf("check SELECT on table %q: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read table %q metadata: %w", table, err)
	}
	return nil
}

// contextConn 使 canal 的普通 SQL 和复制连接同时响应入口取消及读写超时。
type contextConn struct {
	net.Conn
	timeout time.Duration
	stop    func() bool
}

func (c *contextConn) Read(b []byte) (int, error) {
	if err := c.Conn.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.Conn.Read(b)
}
func (c *contextConn) Write(b []byte) (int, error) {
	if err := c.Conn.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.Conn.Write(b)
}
func (c *contextConn) Close() error { c.stop(); return c.Conn.Close() }

// databaseDialer 将库的连接 context 与 Monitor 的取消信号合并。
func databaseDialer(parent context.Context, c types.DatabaseConfig) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, c.ConnectTimeout)
		defer cancel()
		stop := context.AfterFunc(parent, cancel)
		defer stop()
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		wrapped := &contextConn{Conn: conn, timeout: c.ReadTimeout}
		wrapped.stop = context.AfterFunc(parent, func() { _ = conn.Close() })
		return wrapped, nil
	}
}
