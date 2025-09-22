package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
	"github.com/go-mysql-org/go-mysql/schema"
	_ "github.com/go-sql-driver/mysql"
	"go.uber.org/zap"
)

// EventCallback 事件回调函数类型
type EventCallback func()

// Monitor MySQL监控器
type Monitor struct {
	config        *Config
	canal         *canal.Canal
	eventQueue    chan *ChangeEvent
	taskMap       map[string]*Task
	eventTaskMap  map[string][]*Task
	schemaCache   map[string]*TableSchema
	ctx           context.Context
	cancel        context.CancelFunc
	eventCallback EventCallback
}

func getEventTaskId(tableName, eventType string) string {
	return fmt.Sprintf("%s.%s", tableName, eventType)
}
func getPrimaryKey(table *schema.Table, newData, oldData map[string]interface{}) interface{} {
	for _, index := range table.Indexes {
		if index.Name == "PRIMARY" {
			if len(index.Columns) > 0 {
				pkColumn := index.Columns[0]
				if newData != nil {
					return newData[pkColumn]
				}
				if oldData != nil {
					return oldData[pkColumn]
				}
			}
		}
	}
	return nil
}

// NewMonitor 创建新的监控器
func NewMonitor(config *Config, eventQueue chan *ChangeEvent, eventCallback EventCallback) (*Monitor, error) {
	ctx, cancel := context.WithCancel(context.Background())

	monitor := &Monitor{
		config:        config,
		eventQueue:    eventQueue,
		taskMap:       make(map[string]*Task),
		eventTaskMap:  make(map[string][]*Task),
		schemaCache:   make(map[string]*TableSchema),
		ctx:           ctx,
		cancel:        cancel,
		eventCallback: eventCallback,
	}

	// 建立任务映射
	eventTaskMap := make(map[string][]*Task)
	for i := range config.Tasks {
		task := &config.Tasks[i]
		monitor.taskMap[task.TableName] = task
		for _, event := range task.Events {
			eventTaskId := getEventTaskId(task.TableName, string(event))
			if _, exists := eventTaskMap[eventTaskId]; !exists {
				eventTaskMap[eventTaskId] = make([]*Task, 0)
			}
			eventTaskMap[eventTaskId] = append(eventTaskMap[eventTaskId], task)
		}
	}
	monitor.eventTaskMap = eventTaskMap

	// 初始化canal
	cfg := canal.NewDefaultConfig()
	cfg.Addr = fmt.Sprintf("%s:%d", config.Database.Host, config.Database.Port)
	cfg.User = config.Database.User
	cfg.Password = config.Database.Password
	cfg.Charset = "utf8mb4"
	cfg.ServerID = config.Database.ServerID
	cfg.Flavor = "mysql"
	//cfg.DataDir = "./data"
	//cfg.DumpExec = "mysqldump"

	// 设置要监听的数据库和表
	//cfg.Dump.Schema = config.Database.Database
	cfg.IncludeTableRegex = monitor.buildTableRegex()

	c, err := canal.NewCanal(cfg)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create canal: %w", err)
	}

	monitor.canal = c
	monitor.canal.SetEventHandler(monitor)

	return monitor, nil
}

// buildTableRegex 构建表名正则表达式
func (m *Monitor) buildTableRegex() []string {
	var tables []string
	for _, task := range m.config.Tasks {
		tables = append(tables, fmt.Sprintf("%s\\.%s", m.config.Database.Database, task.TableName))
	}
	return tables
}

// Start 启动监控
func (m *Monitor) Start() error {
	Logger.Info("Starting MySQL monitor")

	// 加载表结构
	if err := m.loadTableSchemas(); err != nil {
		return fmt.Errorf("failed to load table schemas: %w", err)
	}

	// 启动canal
	pos, err := m.canal.GetMasterPos()
	if err != nil {
		return fmt.Errorf("failed to get master position: %w", err)
	}

	Logger.With(zap.Any("position", pos)).Info("Starting from master position")

	// 记录任务启动日志
	for _, task := range m.config.Tasks {
		LogTaskStart(task.TaskID, task.Name, task.TableName)
	}

	return m.canal.RunFrom(pos)
}

// Stop 停止监控
func (m *Monitor) Stop() {
	Logger.Info("Stopping MySQL monitor")

	for _, task := range m.config.Tasks {
		LogTaskStop(task.TaskID)
	}

	if m.canal != nil {
		m.canal.Close()
	}
	m.cancel()
}

// loadTableSchemas 加载表结构
func (m *Monitor) loadTableSchemas() error {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s",
		m.config.Database.User, m.config.Database.Password,
		m.config.Database.Host, m.config.Database.Port, m.config.Database.Database)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	for _, task := range m.config.Tasks {
		schema_, err := m.getTableSchema(db, task.TableName)
		if err != nil {
			return fmt.Errorf("failed to load schema for table %s: %w", task.TableName, err)
		}
		m.schemaCache[task.TableName] = schema_
	}

	return nil
}

// getTableSchema 获取表结构
func (m *Monitor) getTableSchema(db *sql.DB, tableName string) (*TableSchema, error) {
	rows, err := db.Query(fmt.Sprintf("SELECT * FROM %s LIMIT 0", tableName))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}

	schema_ := &TableSchema{
		Columns: make(map[string]*sql.ColumnType),
	}

	for _, ct := range columnTypes {
		schema_.Columns[ct.Name()] = ct
	}

	return schema_, nil
}

// OnRow 处理行变更事件 - 实现canal.EventHandler接口
func (m *Monitor) OnRow(e *canal.RowsEvent) error {
	eventTaskId := getEventTaskId(e.Table.Name, string(e.Action))
	tasks, exists := m.eventTaskMap[eventTaskId]
	if !exists {
		return nil
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
func (m *Monitor) handleInsert(e *canal.RowsEvent, task *Task) error {
	for _, row := range e.Rows {
		data := m.buildRowData(e.Table, row)
		primaryID := getPrimaryKey(e.Table, data, map[string]interface{}{})
		event := &ChangeEvent{
			TaskID:    task.TaskID,
			PrimaryID: primaryID,
			Event:     EventInsert,
			Table:     e.Table.Name,
			NewData:   data,
			Timestamp: time.Now(),
		}

		LogChangeEvent(event)

		select {
		case m.eventQueue <- event:
			// 如果有事件回调函数，则调用它
			if m.eventCallback != nil {
				m.eventCallback()
			}
		case <-m.ctx.Done():
			return m.ctx.Err()
		default:
			Logger.Warn("Event queue is full, dropping event", zap.String("task_id", task.TaskID))
		}
	}
	return nil
}

// handleUpdate 处理更新事件
func (m *Monitor) handleUpdate(e *canal.RowsEvent, task *Task) error {
	for i := 0; i < len(e.Rows); i += 2 {
		oldRow := e.Rows[i]
		newRow := e.Rows[i+1]

		oldData := m.buildRowData(e.Table, oldRow)
		newData := m.buildRowData(e.Table, newRow)
		primaryID := getPrimaryKey(e.Table, newData, oldData)
		event := &ChangeEvent{
			TaskID:    task.TaskID,
			Event:     EventUpdate,
			PrimaryID: primaryID,
			Table:     e.Table.Name,
			OldData:   oldData,
			NewData:   newData,
			Timestamp: time.Now(),
		}

		LogChangeEvent(event)

		select {
		case m.eventQueue <- event:
			// 如果有事件回调函数，则调用它
			if m.eventCallback != nil {
				m.eventCallback()
			}
		case <-m.ctx.Done():
			return m.ctx.Err()
		default:
			Logger.Warn("Event queue is full, dropping event", zap.String("task_id", task.TaskID))
		}
	}
	return nil
}

// handleDelete 处理删除事件
func (m *Monitor) handleDelete(e *canal.RowsEvent, task *Task) error {
	for _, row := range e.Rows {
		data := m.buildRowData(e.Table, row)
		primaryID := getPrimaryKey(e.Table, data, map[string]interface{}{})
		event := &ChangeEvent{
			TaskID:    task.TaskID,
			PrimaryID: primaryID,
			Event:     EventDelete,
			Table:     e.Table.Name,
			NewData:   data,
			Timestamp: time.Now(),
		}

		LogChangeEvent(event)

		select {
		case m.eventQueue <- event:
			// 如果有事件回调函数，则调用它
			if m.eventCallback != nil {
				m.eventCallback()
			}
		case <-m.ctx.Done():
			return m.ctx.Err()
		default:
			Logger.Warn("Event queue is full, dropping event", zap.String("task_id", task.TaskID))
		}
	}
	return nil
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
	Logger.Info("Binary log rotated", zap.String("next_log_name", string(rotateEvent.NextLogName)))
	return nil
}

// OnTableChanged 处理表结构变更事件 - 实现canal.EventHandler接口
func (m *Monitor) OnTableChanged(header *replication.EventHeader, schema string, table string) error {
	Logger.Info("Table schema changed", zap.String("schema", schema), zap.String("table", table))

	// 重新加载表结构
	if task, exists := m.taskMap[table]; exists {
		if err := m.loadTableSchemas(); err != nil {
			LogError(task.TaskID, err, "reload table schema")
		}
	}

	return nil
}

// OnDDL 处理DDL事件 - 实现canal.EventHandler接口
func (m *Monitor) OnDDL(rh *replication.EventHeader, nextPos mysql.Position, queryEvent *replication.QueryEvent) error {
	Logger.Info("DDL executed", zap.String("query", string(queryEvent.Query)))
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

// String 返回处理器名称 - 实现canal.EventHandler接口
func (m *Monitor) String() string {
	return "pikachuMonitor"
}
func (m *Monitor) OnPosSynced(header *replication.EventHeader, pos mysql.Position, set mysql.GTIDSet, force bool) error {
	// 可以记录位置同步信息，或保持为空实现
	Logger.Debug("Position synced", zap.Any("position", pos))
	return nil
}
