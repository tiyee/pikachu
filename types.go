package main

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
	TaskID      string      `yaml:"task_id"`
	Name        string      `yaml:"name"`
	TableName   string      `yaml:"table_name"`
	Events      []EventType `yaml:"events"`
	CallbackURL string      `yaml:"callback_url"`
}
type EventTask struct {
	TableName string
	Event     EventType
	Tasks     []*Task
}

// LogLevel 日志级别类型
type LogLevel string

const (
	LogLevelDebug   LogLevel = "debug"
	LogLevelInfo    LogLevel = "info"
	LogLevelWarn    LogLevel = "warn"
	LogLevelError   LogLevel = "error"
	LogLevelFatal   LogLevel = "fatal"
	LogLevelPanic   LogLevel = "panic"
)

// ServerConfig HTTP服务器配置
type ServerConfig struct {
	Enabled bool   `yaml:"enabled"`
	Port    int    `yaml:"port"`
	Path    string `yaml:"path"`
}

// Config 配置文件结构
type Config struct {
	Database DatabaseConfig `yaml:"database"`
	Tasks    []Task         `yaml:"tasks"`
	Log      LogConfig      `yaml:"log"`
	Server   ServerConfig   `yaml:"server"`
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
