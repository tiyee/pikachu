package monitor

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
	"github.com/go-mysql-org/go-mysql/schema"
	_ "github.com/go-sql-driver/mysql"
	"go.uber.org/zap"

	"pikachu/internal/log"
	"pikachu/internal/types"
	"pikachu/internal/utils"
)

// EventCallback 事件回调函数类型
type EventCallback func()

// Monitor MySQL监控器
type Monitor struct {
	config        *types.Config
	canal         *canal.Canal
	eventQueue    chan *types.ChangeEvent
	tasksByTable  map[string][]*types.Task // 按表名分组的任务
	eventTaskMap  map[string][]*types.Task // 按事件类型分组的任务
	schemaCache   map[string]*types.TableSchema
	ctx           context.Context
	cancel        context.CancelFunc
	eventCallback EventCallback
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
func New(config *types.Config, eventQueue chan *types.ChangeEvent, eventCallback EventCallback) (*Monitor, error) {
	ctx, cancel := context.WithCancel(context.Background())

	monitor := &Monitor{
		config:        config,
		eventQueue:    eventQueue,
		tasksByTable:  make(map[string][]*types.Task),
		eventTaskMap:  make(map[string][]*types.Task),
		schemaCache:   make(map[string]*types.TableSchema),
		ctx:           ctx,
		cancel:        cancel,
		eventCallback: eventCallback,
	}

	// 建立任务映射 - 优化后的版本
	tasksByTable := make(map[string][]*types.Task)
	eventTaskMap := make(map[string][]*types.Task)

	for i := range config.Tasks {
		task := &config.Tasks[i]

		// 按表名分组
		tasksByTable[task.TableName] = append(tasksByTable[task.TableName], task)

		// 按事件类型分组
		for _, event := range task.Events {
			eventTaskId := utils.GetEventTaskId(task.TableName, string(event))
			eventTaskMap[eventTaskId] = append(eventTaskMap[eventTaskId], task)
		}
	}

	monitor.tasksByTable = tasksByTable
	monitor.eventTaskMap = eventTaskMap

	// 初始化canal
	cfg := canal.NewDefaultConfig()
	cfg.Addr = fmt.Sprintf("%s:%d", config.Database.Host, config.Database.Port)
	cfg.User = config.Database.User
	cfg.Password = config.Database.Password
	cfg.Charset = config.Database.Charset // 从配置文件读取charset
	cfg.ServerID = config.Database.ServerID
	cfg.Flavor = "mysql"
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
	log.Info("Starting MySQL monitor")

	// 加载表结构
	if err := m.loadTableSchemas(); err != nil {
		return fmt.Errorf("failed to load table schemas: %w", err)
	}

	// 启动canal
	pos, err := m.canal.GetMasterPos()
	if err != nil {
		return fmt.Errorf("failed to get master position: %w", err)
	}

	log.Info("Starting from master position", log.Any("position", pos))

	// 记录任务启动日志
	for _, task := range m.config.Tasks {
		log.Info("Task started",
			log.String("task_id", task.TaskID),
			log.String("task_name", task.Name),
			log.String("table_name", task.TableName))
	}

	return m.canal.RunFrom(pos)
}

// Stop 停止监控
func (m *Monitor) Stop() {
	log.Info("Stopping MySQL monitor")

	for _, task := range m.config.Tasks {
		log.Info("Task stopped", log.String("task_id", task.TaskID))
	}

	if m.canal != nil {
		m.canal.Close()
	}
	m.cancel()
}

// loadTableSchemas 加载表结构
func (m *Monitor) loadTableSchemas() error {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s",
		m.config.Database.User, m.config.Database.Password,
		m.config.Database.Host, m.config.Database.Port,
		m.config.Database.Database, m.config.Database.Charset)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	// 设置连接池参数
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

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
func (m *Monitor) getTableSchema(db *sql.DB, tableName string) (*types.TableSchema, error) {
	// 使用简化的引用函数处理表名，确保被反引号包围
	quotedTableName := utils.EnsureQuoted(tableName)
	query := fmt.Sprintf("SELECT * FROM %s LIMIT 0", quotedTableName)

	log.Debug("Loading table schema",
		log.String("table_name", tableName),
		log.String("quoted_table_name", quotedTableName),
		log.String("query", query))

	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query table %s: %w", tableName, err)
	}
	defer rows.Close()

	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("failed to get column types for table %s: %w", tableName, err)
	}

	schema_ := &types.TableSchema{
		Columns: make(map[string]*sql.ColumnType),
	}

	for _, ct := range columnTypes {
		schema_.Columns[ct.Name()] = ct
	}

	return schema_, nil
}

// OnRow 处理行变更事件 - 实现canal.EventHandler接口
func (m *Monitor) OnRow(e *canal.RowsEvent) error {
	eventTaskId := utils.GetEventTaskId(e.Table.Name, string(e.Action))
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

		select {
		case m.eventQueue <- event:
			// 如果有事件回调函数，则调用它
			if m.eventCallback != nil {
				m.eventCallback()
			}
		case <-m.ctx.Done():
			return m.ctx.Err()
		case <-time.After(m.config.Monitor.EventQueueTimeout):
			log.Error("Event queue timeout, event dropped",
				log.String("task_id", task.TaskID),
				log.String("event_type", string(event.Event)),
				log.String("table", event.Table))
			return fmt.Errorf("event queue timeout")
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

		select {
		case m.eventQueue <- event:
			// 如果有事件回调函数，则调用它
			if m.eventCallback != nil {
				m.eventCallback()
			}
		case <-m.ctx.Done():
			return m.ctx.Err()
		case <-time.After(m.config.Monitor.EventQueueTimeout):
			log.Error("Event queue timeout, event dropped",
				log.String("task_id", task.TaskID),
				log.String("event_type", string(event.Event)),
				log.String("table", event.Table))
			return fmt.Errorf("event queue timeout")
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

		select {
		case m.eventQueue <- event:
			// 如果有事件回调函数，则调用它
			if m.eventCallback != nil {
				m.eventCallback()
			}
		case <-m.ctx.Done():
			return m.ctx.Err()
		case <-time.After(m.config.Monitor.EventQueueTimeout):
			log.Error("Event queue timeout, event dropped",
				log.String("task_id", task.TaskID),
				log.String("event_type", string(event.Event)),
				log.String("table", event.Table))
			return fmt.Errorf("event queue timeout")
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
	log.Info("Binary log rotated", log.String("next_log_name", string(rotateEvent.NextLogName)))
	return nil
}

// OnTableChanged 处理表结构变更事件 - 实现canal.EventHandler接口
func (m *Monitor) OnTableChanged(header *replication.EventHeader, schema string, table string) error {
	log.Info("Table schema changed", log.String("schema", schema), log.String("table", table))

	// 重新加载表结构
	if tasks, exists := m.tasksByTable[table]; exists && len(tasks) > 0 {
		if err := m.loadTableSchemas(); err != nil {
			log.Error("Failed to reload table schema",
				log.String("task_id", tasks[0].TaskID),
				zap.Error(err))
		}
	}

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

// String 返回处理器名称 - 实现canal.EventHandler接口
func (m *Monitor) String() string {
	return "pikachuMonitor"
}

func (m *Monitor) OnPosSynced(header *replication.EventHeader, pos mysql.Position, set mysql.GTIDSet, force bool) error {
	// 记录位置同步信息，用于监控和调试
	log.Debug("Position synced",
		log.Any("position", pos),
		log.Bool("force", force),
		log.Any("gtid_set", set))
	return nil
}

// CheckDatabasePermissions 检查数据库权限
func CheckDatabasePermissions(config *types.DatabaseConfig) error {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s",
		config.User, config.Password, config.Host, config.Port, config.Database, config.Charset)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	// 检查连接
	if err := db.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	// 检查权限 - 最小化权限集
	// 仅包含应用程序实际需要的权限
	requiredPrivileges := []string{
		"SELECT",             // 用于获取表结构信息
		"REPLICATION SLAVE",  // 用于连接二进制日志并接收变更事件
		"REPLICATION CLIENT", // 用于获取主服务器的位置信息
	}

	rows, err := db.Query("SHOW GRANTS FOR CURRENT_USER()")
	if err != nil {
		return fmt.Errorf("failed to check privileges: %w", err)
	}
	defer rows.Close()

	var grants []string
	for rows.Next() {
		var grant string
		if err := rows.Scan(&grant); err != nil {
			continue
		}
		grants = append(grants, grant)
	}

	// 简化权限检查 - 在实际环境中需要更精确的解析
	hasAllPrivileges := false
	for _, grant := range grants {
		if utils.Contains(grant, "ALL PRIVILEGES") || utils.Contains(grant, "GRANT ALL") {
			hasAllPrivileges = true
			break
		}
	}

	if !hasAllPrivileges {
		// 检查是否有必要的权限
		for _, privilege := range requiredPrivileges {
			hasPrivilege := false
			for _, grant := range grants {
				if utils.Contains(grant, privilege) {
					hasPrivilege = true
					break
				}
			}
			if !hasPrivilege {
				return fmt.Errorf("missing required privilege: %s", privilege)
			}
		}
	}

	return nil
}
