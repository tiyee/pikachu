package types

import (
	"database/sql"
	"time"
)

// EventType 定义监控事件类型
type EventType string

const (
	EventInsert EventType = "insert"
	EventUpdate EventType = "update"
	EventDelete EventType = "delete"
)

// Task 任务配置结构
type Task struct {
	TaskID              string      `yaml:"task_id"`
	Name                string      `yaml:"name"`
	TableName           string      `yaml:"table_name"`
	Events              []EventType `yaml:"events"`
	CallbackURL         string      `yaml:"callback_url"`
	PrebuiltCallbackURL string      `yaml:"-"` // 预构建的完整回调URL，不序列化到YAML
}
type EventTask struct {
	TableName string
	Event     EventType
	Tasks     []*Task
}

// LogLevel 日志级别类型
type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
	LogLevelFatal LogLevel = "fatal"
	LogLevelPanic LogLevel = "panic"
)

// ServerConfig HTTP服务器配置
type ServerConfig struct {
	Enabled bool   `yaml:"enabled"`
	Port    int    `yaml:"port"`
	Path    string `yaml:"path"`
}

// DispatcherConfig 分发器配置
type DispatcherConfig struct {
	WorkerCount     int           `yaml:"worker_count"`      // 工作协程数量
	QueueSize       int           `yaml:"queue_size"`        // 队列大小
	Timeout         time.Duration `yaml:"timeout"`           // HTTP请求超时
	MaxRetries      int           `yaml:"max_retries"`       // 最大重试次数
	RetryBaseDelay  time.Duration `yaml:"retry_base_delay"`  // 重试基础延迟
	RetryMaxDelay   time.Duration `yaml:"retry_max_delay"`   // 最大重试延迟
	MaxConnections  int           `yaml:"max_connections"`   // 最大并发连接数
	MaxIdleConns    int           `yaml:"max_idle_conns"`    // 最大空闲连接数
	IdleConnTimeout time.Duration `yaml:"idle_conn_timeout"` // 空闲连接超时
	BatchSize       int           `yaml:"batch_size"`        // 批处理大小
	BatchTimeout    time.Duration `yaml:"batch_timeout"`     // 批处理超时
}

// MonitorConfig 监控器配置
type MonitorConfig struct {
	EventQueueSize    int           `yaml:"event_queue_size"`    // 事件队列大小
	EventQueueTimeout time.Duration `yaml:"event_queue_timeout"` // 事件队列超时时间
	BatchSize         int           `yaml:"batch_size"`          // 批处理大小
	BatchTimeout      time.Duration `yaml:"batch_timeout"`       // 批处理超时
	FlushInterval     time.Duration `yaml:"flush_interval"`      // 刷新间隔
}

// Config 配置文件结构
type Config struct {
	Database     DatabaseConfig   `yaml:"database"`
	Tasks        []Task           `yaml:"tasks"`
	Log          LogConfig        `yaml:"log"`
	Server       ServerConfig     `yaml:"server"`
	Dispatcher   DispatcherConfig `yaml:"dispatcher"`
	Monitor      MonitorConfig    `yaml:"monitor"`
	CallbackHost string           `yaml:"callback_host"` // 回调主机地址，用于不同环境配置
}

// LogConfig 日志配置
type LogConfig struct {
	Level  LogLevel `yaml:"level"`
	Format string   `yaml:"format"` // text, json
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Database string `yaml:"database"`
	ServerID uint32 `yaml:"server_id"`
	Charset  string `yaml:"charset"` // 数据库字符集，可选，默认为utf8mb4
}

// ChangeEvent 数据变更事件
type ChangeEvent struct {
	TaskID    string
	Event     EventType
	Table     string
	PrimaryID interface{}
	OldData   map[string]interface{}
	NewData   map[string]interface{}
	Timestamp time.Time
}

// WebhookPayload webhook载荷结构
type WebhookPayload struct {
	Event     EventType              `json:"event"`
	Table     string                 `json:"table"`
	PrimaryID interface{}            `json:"primary_id"`
	Data      map[string]interface{} `json:"data,omitempty"`
	OldData   map[string]interface{} `json:"old_data,omitempty"`
	NewData   map[string]interface{} `json:"new_data,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

// CallbackTask 回调任务
type CallbackTask struct {
	Event       *ChangeEvent
	CallbackURL string
	RetryCount  int
	MaxRetries  int
}

// TableSchema 表结构信息
type TableSchema struct {
	Columns map[string]*sql.ColumnType
}
