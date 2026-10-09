package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
	"github.com/go-mysql-org/go-mysql/schema"

	"pikachu/internal/log"
	"pikachu/internal/types"
	"pikachu/internal/utils"
)

// EventCallback 事件回调函数类型
type EventCallback func()

// Monitor MySQL监控器
type Monitor struct {
	running       atomic.Bool
	config        *types.Config
	canal         replicationClient
	eventQueue    chan *types.ChangeEvent
	eventTaskMap  map[string][]*types.Task // 按事件类型分组的任务
	ctx           context.Context
	cancel        context.CancelFunc
	eventCallback EventCallback
	stopOnce      sync.Once
}

// replicationClient 便于隔离测试复制循环的生命周期。
type replicationClient interface {
	GetMasterPos() (mysql.Position, error)
	RunFrom(mysql.Position) error
	Close()
}

// GetPrimaryKey 获取主键值，支持复合主键
func GetPrimaryKey(table *schema.Table, newData, oldData map[string]interface{}) interface{} {
	for _, index := range table.Indexes {
		if index.Name == "PRIMARY" {
			if len(index.Columns) == 1 {
				// 单个主键
				pkColumn := index.Columns[0]
				if newData != nil {
					return newData[pkColumn]
				}
				if oldData != nil {
					return oldData[pkColumn]
				}
			} else if len(index.Columns) > 1 {
				// 复合主键，返回map
				pkData := make(map[string]interface{})
				dataSource := newData
				if dataSource == nil {
					dataSource = oldData
				}
				if dataSource != nil {
					for _, col := range index.Columns {
						pkData[col] = dataSource[col]
					}
					return pkData
				}
			}
			break
		}
	}

	// 如果没有找到主键，尝试使用id字段
	dataSource := newData
	if dataSource == nil {
		dataSource = oldData
	}
	if dataSource != nil {
		if id, exists := dataSource["id"]; exists {
			return id
		}
	}

	return nil
}

// New 创建新的监控器
func New(parent context.Context, config *types.Config, eventQueue chan *types.ChangeEvent, eventCallback EventCallback) (*Monitor, error) {
	ctx, cancel := context.WithCancel(parent)

	monitor := &Monitor{
		config:        config,
		eventQueue:    eventQueue,
		eventTaskMap:  make(map[string][]*types.Task),
		ctx:           ctx,
		cancel:        cancel,
		eventCallback: eventCallback,
	}

	// 建立任务映射 - 优化后的版本
	eventTaskMap := make(map[string][]*types.Task)

	for i := range config.Tasks {
		task := &config.Tasks[i]

		// 按事件类型分组
		for _, event := range task.Events {
			eventTaskId := utils.GetEventTaskId(task.TableName, string(event))
			eventTaskMap[eventTaskId] = append(eventTaskMap[eventTaskId], task)
		}
	}

	monitor.eventTaskMap = eventTaskMap

	// 直接验证实际 SELECT 能力，兼容生效角色与表级授权。
	if err := monitor.validateTables(); err != nil {
		cancel()
		return nil, fmt.Errorf("validate database access: %w", err)
	}

	// 初始化canal
	cfg := canal.NewDefaultConfig()
	cfg.Addr = net.JoinHostPort(config.Database.Host, strconv.Itoa(config.Database.Port))
	cfg.User = config.Database.User
	cfg.Password = config.Database.Password
	cfg.Charset = config.Database.Charset // 从配置文件读取charset
	cfg.ServerID = config.Database.ServerID
	cfg.Flavor = "mysql"
	cfg.Dialer = databaseDialer(ctx, config.Database)
	cfg.ReadTimeout = config.Database.ReadTimeout
	cfg.HeartbeatPeriod = config.Database.ReadTimeout / 2
	cfg.Dump.SkipMasterData = true
	cfg.Dump.ExecutionPath = ""

	// 注入项目的zap logger到canal配置中
	// 创建zap到slog的适配器，让canal使用项目的zap logger进行日志输出
	zapLogger := log.GetLogger()
	if zapLogger != nil {
		slogHandler := log.NewZapSlogAdapter(zapLogger)
		canalLogger := slog.New(slogHandler)
		cfg.Logger = canalLogger
		log.Info("Successfully injected zap logger into canal config")
	}

	cfg.IncludeTableRegex = monitor.buildTableRegex()

	c, err := canal.NewCanal(cfg)
	if err != nil {
		contextErr := ctx.Err()
		cancel()
		if contextErr != nil {
			return nil, contextErr
		}
		return nil, fmt.Errorf("failed to create canal: %w", err)
	}

	if err := c.CheckBinlogRowImage("FULL"); err != nil {
		cancel()
		c.Close()
		return nil, fmt.Errorf("binlog_row_image must be FULL: %w", err)
	}
	monitor.canal = c
	c.SetEventHandler(monitor)

	return monitor, nil
}

// buildTableRegex 构建表名正则表达式
func (m *Monitor) buildTableRegex() []string {
	var tables []string
	for _, task := range m.config.Tasks {
		// 使用utils.go中的函数进行正则表达式转义
		regex := utils.EscapeRegexForTable(m.config.Database.Database, task.TableName)
		tables = append(tables, regex)

		log.Debug("Building table regex",
			log.String("database", m.config.Database.Database),
			log.String("table_name", task.TableName),
			log.String("regex", regex))
	}
	return tables
}

// Start 启动监控
func (m *Monitor) Start() error {
	defer m.running.Store(false)
	log.Info("Starting MySQL monitor")

	if err := m.ctx.Err(); err != nil {
		return err
	}

	// 保持尽力投递语义，每次启动从当前 master position 开始。
	pos, err := m.canal.GetMasterPos()
	if err != nil {
		if m.ctx.Err() != nil {
			return m.ctx.Err()
		}
		return fmt.Errorf("failed to get master position: %w", err)
	}
	if err := m.ctx.Err(); err != nil {
		return err
	}
	log.Info("Starting from master position", log.Any("position", pos))

	// 记录任务启动日志
	for _, task := range m.config.Tasks {
		log.Info("Task started",
			log.String("task_id", task.TaskID),
			log.String("task_name", task.Name),
			log.String("table_name", task.TableName))
	}

	m.running.Store(true)
	err = m.canal.RunFrom(pos)
	if m.ctx.Err() != nil {
		return m.ctx.Err()
	}
	return err
}

// Running 表示初始化完成并进入复制循环，不代表实时连接健康。
func (m *Monitor) Running() bool { return m.running.Load() }

// Stop 停止生产并中断队列等待。
func (m *Monitor) Stop() {
	m.stopOnce.Do(func() {
		m.cancel()
		m.running.Store(false)
		log.Info("Stopping MySQL monitor")
		if m.canal != nil {
			m.canal.Close()
		}
	})
}

// OnRow 处理行变更事件 - 实现canal.EventHandler接口
func (m *Monitor) OnRow(e *canal.RowsEvent) error {
	if e == nil || e.Table == nil {
		return fmt.Errorf("row event has no table metadata")
	}

	if e.Table.Schema != m.config.Database.Database {
		return nil
	}
	eventTaskId := utils.GetEventTaskId(e.Table.Name, string(e.Action))
	tasks, exists := m.eventTaskMap[eventTaskId]
	if !exists {
		return nil
	}

	if e.Action == canal.UpdateAction && len(e.Rows)%2 != 0 {
		return fmt.Errorf("update event must contain paired row images")
	}
	for _, row := range e.Rows {
		if len(row) != len(e.Table.Columns) {
			return fmt.Errorf("row image column count does not match table metadata")
		}
	}

	switch e.Action {
	case canal.InsertAction:
		for _, task := range tasks {
			if err := m.handleInsert(e, task); err != nil {
				return err
			}
		}
	case canal.UpdateAction:
		for _, task := range tasks {
			if err := m.handleUpdate(e, task); err != nil {
				return err
			}
		}
	case canal.DeleteAction:
		for _, task := range tasks {
			if err := m.handleDelete(e, task); err != nil {
				return err
			}
		}
	}

	return nil
}

// handleInsert 处理插入事件
func (m *Monitor) handleInsert(e *canal.RowsEvent, task *types.Task) error {
	for _, row := range e.Rows {
		data := m.buildRowData(e.Table, row)
		primaryID := GetPrimaryKey(e.Table, data, map[string]interface{}{})
		event := &types.ChangeEvent{
			TaskID:    task.TaskID,
			PrimaryID: primaryID,
			Event:     types.EventInsert,
			Table:     e.Table.Name,
			NewData:   data,
			Timestamp: time.Now(),
		}

		log.Info("Change event detected",
			log.String("task_id", event.TaskID),
			log.String("event_type", string(event.Event)),
			log.String("table", event.Table),
			log.Any("primary_id", event.PrimaryID))

		if err := m.enqueue(event); err != nil {
			return err
		}
	}
	return nil
}

// handleUpdate 处理更新事件
func (m *Monitor) handleUpdate(e *canal.RowsEvent, task *types.Task) error {
	for i := 0; i < len(e.Rows); i += 2 {
		oldRow := e.Rows[i]
		newRow := e.Rows[i+1]

		oldData := m.buildRowData(e.Table, oldRow)
		newData := m.buildRowData(e.Table, newRow)
		primaryID := GetPrimaryKey(e.Table, newData, oldData)
		event := &types.ChangeEvent{
			TaskID:    task.TaskID,
			Event:     types.EventUpdate,
			PrimaryID: primaryID,
			Table:     e.Table.Name,
			OldData:   oldData,
			NewData:   newData,
			Timestamp: time.Now(),
		}

		log.Info("Change event detected",
			log.String("task_id", event.TaskID),
			log.String("event_type", string(event.Event)),
			log.String("table", event.Table),
			log.Any("primary_id", event.PrimaryID))

		if err := m.enqueue(event); err != nil {
			return err
		}
	}
	return nil
}

// handleDelete 处理删除事件
func (m *Monitor) handleDelete(e *canal.RowsEvent, task *types.Task) error {
	for _, row := range e.Rows {
		data := m.buildRowData(e.Table, row)
		primaryID := GetPrimaryKey(e.Table, data, map[string]interface{}{})
		event := &types.ChangeEvent{
			TaskID:    task.TaskID,
			PrimaryID: primaryID,
			Event:     types.EventDelete,
			Table:     e.Table.Name,
			NewData:   data,
			Timestamp: time.Now(),
		}

		log.Info("Change event detected",
			log.String("task_id", event.TaskID),
			log.String("event_type", string(event.Event)),
			log.String("table", event.Table),
			log.Any("primary_id", event.PrimaryID))

		if err := m.enqueue(event); err != nil {
			return err
		}
	}
	return nil
}

// enqueue 在拥塞时保持背压；超时时间仅控制告警频率，不丢弃事件。
func (m *Monitor) enqueue(event *types.ChangeEvent) error {
	ticker := time.NewTicker(m.config.Monitor.EventQueueTimeout)
	defer ticker.Stop()
	for {
		select {
		case m.eventQueue <- event:
			if m.eventCallback != nil {
				m.eventCallback()
			}
			return nil
		case <-m.ctx.Done():
			return m.ctx.Err()
		case <-ticker.C:
			log.Warn("Event queue is full; waiting for capacity", log.String("task_id", event.TaskID))
		}
	}
}

// buildRowData 构建行数据
func (m *Monitor) buildRowData(table *schema.Table, row []interface{}) map[string]interface{} {
	data := make(map[string]interface{})

	for i, col := range table.Columns {
		if i < len(row) {
			data[col.Name] = row[i]
		}
	}

	return data
}

// OnRotate 处理日志轮转事件 - 实现canal.EventHandler接口
func (m *Monitor) OnRotate(header *replication.EventHeader, rotateEvent *replication.RotateEvent) error {
	log.Info("Binary log rotated", log.String("next_log_name", string(rotateEvent.NextLogName)))
	return nil
}

// OnTableChanged 处理表结构变更事件 - 实现canal.EventHandler接口
func (m *Monitor) OnTableChanged(header *replication.EventHeader, schema string, table string) error {
	if schema != m.config.Database.Database {
		return nil
	}
	log.Info("Table schema changed", log.String("schema", schema), log.String("table", table))

	// canal 已清理表元数据缓存，下一个行事件按需获取新结构。

	return nil
}

// OnDDL 处理DDL事件 - 实现canal.EventHandler接口
func (m *Monitor) OnDDL(rh *replication.EventHeader, nextPos mysql.Position, queryEvent *replication.QueryEvent) error {
	log.Info("DDL executed", log.String("query", string(queryEvent.Query)))
	return nil
}

// OnXID 处理事务提交事件 - 实现canal.EventHandler接口
func (m *Monitor) OnXID(eventHeader *replication.EventHeader, nextPos mysql.Position) error {
	// 通常不需要处理
	return nil
}

// OnGTID 处理GTID事件 - 实现canal.EventHandler接口
func (m *Monitor) OnGTID(eventHeader *replication.EventHeader, nextPos mysql.BinlogGTIDEvent) error {
	// 通常不需要处理
	return nil
}

func (m *Monitor) OnRowsQueryEvent(e *replication.RowsQueryEvent) error {
	return nil
}

// OnTableNotFound 对缺失表元数据告警并跳过，保持尽力投递语义。
func (m *Monitor) OnTableNotFound(header *replication.EventHeader, e *replication.RowsEvent) error {
	log.Warn("Rows event references a table that no longer exists, skipping",
		log.String("schema", string(e.Table.Schema)), log.String("table", string(e.Table.Table)))
	return nil
}

// String 返回处理器名称 - 实现canal.EventHandler接口
func (m *Monitor) String() string {
	return "pikachuMonitor"
}

// OnPosSynced 仅记录读取进度，不持久化或恢复位点。
func (m *Monitor) OnPosSynced(header *replication.EventHeader, pos mysql.Position, set mysql.GTIDSet, force bool) error {
	log.Debug("Position synced", log.Any("position", pos), log.Bool("force", force), log.Any("gtid_set", set))
	return nil
}
