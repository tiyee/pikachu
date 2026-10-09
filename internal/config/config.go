package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"pikachu/internal/types"
	"pikachu/internal/utils"
)

// LoadConfig 加载YAML配置文件
// tasksFile 指定任务配置文件路径，为空时默认使用 "tasks.yaml"
func LoadConfig(filename, tasksFile string) (*types.Config, error) {

	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	config := types.Config{Dispatcher: types.DispatcherConfig{MaxRetries: 3}}
	err = decodeYAML(data, &config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if tasksFile == "" {
		tasksFile = "tasks.yaml"
	}
	// 仅文件不存在时兼容内联任务，解析和权限错误必须返回。
	tasks, err := LoadTasks(tasksFile)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) || len(config.Tasks) == 0 {
			return nil, fmt.Errorf("failed to load tasks file %q: %w", tasksFile, err)
		}
		// 如果原配置文件中有tasks，则使用原配置（向后兼容）
	} else {
		// 使用从tasks.yaml加载的任务配置
		config.Tasks = tasks
	}

	return &config, nil
}

// LoadTasks 加载任务配置文件
func LoadTasks(filename string) ([]types.Task, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read tasks file: %w", err)
	}

	var tasksConfig struct {
		Tasks []types.Task `yaml:"tasks"`
	}

	err = decodeYAML(data, &tasksConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to parse tasks file: %w", err)
	}

	return tasksConfig.Tasks, nil
}

// decodeYAML 拒绝未知字段和多个文档，避免配置拼写错误被忽略。
func decodeYAML(data []byte, target interface{}) error {
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(target); err != nil {
		return err
	}
	var extra interface{}
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return fmt.Errorf("multiple YAML documents are not supported")
	}
	return nil
}

// ValidateConfig 验证配置文件
func ValidateConfig(config *types.Config) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}
	if len(config.Tasks) == 0 {
		return fmt.Errorf("no tasks configured")
	}

	// 验证数据库配置
	if err := validateDatabaseConfig(&config.Database); err != nil {
		return fmt.Errorf("database config validation failed: %w", err)
	}

	// 验证任务配置
	ids := make(map[string]int)
	if config.CallbackHost != "" {
		if err := validateURL(config.CallbackHost); err != nil {
			return fmt.Errorf("invalid callback_host: %w", err)
		}
		u, _ := url.Parse(config.CallbackHost)
		if u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("callback_host cannot contain query or fragment")
		}
	}
	for i, task := range config.Tasks {
		if previous, exists := ids[task.TaskID]; exists {
			return fmt.Errorf("task[%d]: duplicate task_id %q (first at task[%d])", i, task.TaskID, previous)
		}
		ids[task.TaskID] = i
		if err := validateTaskConfig(&task, i); err != nil {
			return err
		}
		if err := validateURL(utils.BuildCallbackURL(config.CallbackHost, task.CallbackURL)); err != nil {
			return fmt.Errorf("task[%d]: invalid final callback URL (relative paths require callback_host): %w", i, err)
		}
		seen := make(map[types.EventType]bool)
		events := make([]types.EventType, 0, len(task.Events))
		for _, event := range task.Events {
			if !seen[event] {
				seen[event] = true
				events = append(events, event)
			}
		}
		config.Tasks[i].Events = events
	}

	if config.Dispatcher.ShutdownTimeout < 0 {
		return fmt.Errorf("shutdown_timeout cannot be negative")
	}
	if config.Dispatcher.MaxRetries < 0 {
		return fmt.Errorf("max_retries cannot be negative")
	}
	if err := validateLegacyBatch(config); err != nil {
		return err
	}
	if err := validateNonnegative(config); err != nil {
		return err
	}

	// 设置默认值
	setDefaultValues(config)
	if config.Log.Format != "text" && config.Log.Format != "json" {
		return fmt.Errorf("log format must be text or json")
	}
	switch config.Log.Level {
	case types.LogLevelDebug, types.LogLevelInfo, types.LogLevelWarn, types.LogLevelError, types.LogLevelFatal, types.LogLevelPanic:
	default:
		return fmt.Errorf("invalid log level %q", config.Log.Level)
	}
	if config.Server.Enabled {
		if config.Server.Port == 0 {
			config.Server.Port = 8080
		}
		if config.Server.Port < 1 || config.Server.Port > 65535 {
			return fmt.Errorf("server port must be between 1 and 65535")
		}
		if config.Server.Path == "" {
			config.Server.Path = "/health"
		}
		if !strings.HasPrefix(config.Server.Path, "/") || config.Server.Path == "/metrics-json" || strings.ContainsAny(config.Server.Path, " {}\t\r\n?#") {
			return fmt.Errorf("invalid health path %q", config.Server.Path)
		}
	}

	// 验证分发器配置的逻辑约束
	if err := validateDispatcherConstraints(&config.Dispatcher); err != nil {
		return err
	}

	return nil
}

// validateDatabaseConfig 验证数据库配置
func validateDatabaseConfig(config *types.DatabaseConfig) error {
	if config.Host == "" {
		return fmt.Errorf("database host cannot be empty")
	}
	if config.Port <= 0 || config.Port > 65535 {
		return fmt.Errorf("database port must be between 1 and 65535")
	}
	if config.User == "" {
		return fmt.Errorf("database user cannot be empty")
	}
	if config.Database == "" {
		return fmt.Errorf("database name cannot be empty")
	}
	if config.ServerID == 0 {
		return fmt.Errorf("database server_id cannot be zero")
	}
	return nil
}

// validateTaskConfig 验证任务配置
func validateTaskConfig(task *types.Task, index int) error {
	if task.TaskID == "" {
		return fmt.Errorf("task[%d]: task_id cannot be empty", index)
	}
	if task.TableName == "" {
		return fmt.Errorf("task[%d]: table_name cannot be empty", index)
	}
	if task.CallbackURL == "" {
		return fmt.Errorf("task[%d]: callback_url cannot be empty", index)
	}
	if len(task.Events) == 0 {
		return fmt.Errorf("task[%d]: events cannot be empty", index)
	}

	// 验证事件类型
	for _, event := range task.Events {
		if event != types.EventInsert && event != types.EventUpdate && event != types.EventDelete {
			return fmt.Errorf("task[%d]: invalid event type '%s'", index, event)
		}
	}

	// 验证回调URL格式 - 支持相对路径和绝对路径
	if err := validateCallbackURL(task.CallbackURL); err != nil {
		return fmt.Errorf("task[%d]: invalid callback_url: %w", index, err)
	}

	return nil
}

// validateURL 验证URL格式
func validateURL(urlStr string) error {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https, got: %s", parsedURL.Scheme)
	}

	if parsedURL.Host == "" {
		return fmt.Errorf("URL host cannot be empty")
	}
	if port := parsedURL.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return fmt.Errorf("URL port must be between 1 and 65535")
		}
	}
	if parsedURL.Hostname() == "" || parsedURL.User != nil || parsedURL.Fragment != "" {
		return fmt.Errorf("URL must have a hostname and cannot contain userinfo or fragment")
	}

	return nil
}

// validateCallbackURL 验证回调URL格式 - 支持相对路径和绝对路径
func validateCallbackURL(urlStr string) error {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("invalid callback URL format: %w", err)
	}

	// 如果是绝对URL，需要验证scheme和host
	if parsedURL.IsAbs() {
		if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
			return fmt.Errorf("callback URL scheme must be http or https, got: %s", parsedURL.Scheme)
		}
		if parsedURL.Host == "" {
			return fmt.Errorf("callback URL host cannot be empty for absolute URLs")
		}
	} else {
		// 如果是相对路径，需要以/开头
		if !strings.HasPrefix(urlStr, "/") || parsedURL.Host != "" {
			return fmt.Errorf("relative callback URL must start with '/', got: %s", urlStr)
		}
	}

	return nil
}

// setDefaultValues 设置默认值
func setDefaultValues(config *types.Config) {
	if config.Dispatcher.ShutdownTimeout == 0 {
		config.Dispatcher.ShutdownTimeout = 30 * time.Second
	}
	// 设置分发器默认值
	if config.Dispatcher.WorkerCount <= 0 {
		config.Dispatcher.WorkerCount = 20 // 增加默认工作协程数量以提高并发性能
	}
	if config.Dispatcher.QueueSize <= 0 {
		config.Dispatcher.QueueSize = 1000 // 增加默认队列大小
	}
	if config.Dispatcher.Timeout <= 0 {
		config.Dispatcher.Timeout = 30 * time.Second
	}
	if config.Dispatcher.RetryBaseDelay <= 0 {
		config.Dispatcher.RetryBaseDelay = 5 * time.Second // 减少基础延迟以加快恢复
	}
	if config.Dispatcher.RetryMaxDelay <= 0 {
		config.Dispatcher.RetryMaxDelay = 60 * time.Second // 设置最大延迟
	}
	if config.Dispatcher.MaxConnections <= 0 {
		config.Dispatcher.MaxConnections = 100 // 增加最大连接数
	}
	if config.Dispatcher.MaxIdleConns <= 0 {
		config.Dispatcher.MaxIdleConns = 20 // 设置空闲连接池大小
	}
	if config.Dispatcher.IdleConnTimeout <= 0 {
		config.Dispatcher.IdleConnTimeout = 90 * time.Second // 空闲连接超时
	}

	// 设置监控器默认值
	if config.Monitor.EventQueueSize <= 0 {
		config.Monitor.EventQueueSize = 10000 // 增加事件队列大小
	}
	if config.Monitor.EventQueueTimeout <= 0 {
		config.Monitor.EventQueueTimeout = 2 * time.Second // 减少超时时间以加快响应
	}

	// 设置日志默认值
	if config.Log.Level == "" {
		config.Log.Level = types.LogLevelInfo
	}
	if config.Log.Format == "" {
		config.Log.Format = "text"
	}

	// 设置数据库默认charset
	if config.Database.Charset == "" {
		config.Database.Charset = "utf8mb4"
	}
	if config.Database.ConnectTimeout == 0 {
		config.Database.ConnectTimeout = 10 * time.Second
	}
	if config.Database.ReadTimeout == 0 {
		config.Database.ReadTimeout = 30 * time.Second
	}
	if config.Log.Directory == "" {
		config.Log.Directory = "logs"
	}
	if config.Log.MaxSize == 0 {
		config.Log.MaxSize = 100
	}
	if config.Log.MaxBackups == 0 {
		config.Log.MaxBackups = 5
	}
	if config.Log.MaxAge == 0 {
		config.Log.MaxAge = 7
	}
}

// validateDispatcherConstraints 验证分发器配置的逻辑约束
func validateDispatcherConstraints(config *types.DispatcherConfig) error {
	// 如果设置了重试次数，基础重试延迟不能低于1秒
	if config.MaxRetries > 0 && config.RetryBaseDelay < 1*time.Second {
		return fmt.Errorf("retry_base_delay cannot be less than 1 second when max_retries is set (current: %v, minimum: 1s)", config.RetryBaseDelay)
	}

	// 最大重试延迟应该大于基础延迟
	if config.RetryMaxDelay > 0 && config.RetryBaseDelay >= config.RetryMaxDelay {
		return fmt.Errorf("retry_max_delay (%v) must be greater than retry_base_delay (%v)", config.RetryMaxDelay, config.RetryBaseDelay)
	}

	// 工作协程数量应该在合理范围内
	if config.WorkerCount > 1000 {
		return fmt.Errorf("worker_count (%d) is too high, maximum recommended is 1000", config.WorkerCount)
	}

	// 队列大小应该在合理范围内
	if config.QueueSize > 100000 {
		return fmt.Errorf("queue_size (%d) is too large, maximum recommended is 100000", config.QueueSize)
	}

	// HTTP客户端配置验证
	if config.MaxConnections > 0 && config.MaxIdleConns > config.MaxConnections {
		return fmt.Errorf("max_idle_conns (%d) cannot be greater than max_connections (%d)", config.MaxIdleConns, config.MaxConnections)
	}

	return nil
}

// validateLegacyBatch 仅接受旧示例的兼容值，拒绝承诺尚未实现的批处理行为。
func validateLegacyBatch(c *types.Config) error {
	if (c.Dispatcher.BatchSize != 0 && c.Dispatcher.BatchSize != 1) || (c.Dispatcher.BatchTimeout != 0 && c.Dispatcher.BatchTimeout != 100*time.Millisecond) || (c.Monitor.BatchSize != 0 && c.Monitor.BatchSize != 1) || (c.Monitor.BatchTimeout != 0 && c.Monitor.BatchTimeout != 50*time.Millisecond) || (c.Monitor.FlushInterval != 0 && c.Monitor.FlushInterval != time.Second) {
		return fmt.Errorf("batch_size, batch_timeout and flush_interval are deprecated; batching is not supported")
	}
	return nil
}

// validateNonnegative 显式负值不能被默认值静默覆盖。
func validateNonnegative(c *types.Config) error {
	values := map[string]int64{
		"worker_count": int64(c.Dispatcher.WorkerCount), "queue_size": int64(c.Dispatcher.QueueSize), "timeout": int64(c.Dispatcher.Timeout), "retry_base_delay": int64(c.Dispatcher.RetryBaseDelay), "retry_max_delay": int64(c.Dispatcher.RetryMaxDelay), "max_connections": int64(c.Dispatcher.MaxConnections), "max_idle_conns": int64(c.Dispatcher.MaxIdleConns), "idle_conn_timeout": int64(c.Dispatcher.IdleConnTimeout), "event_queue_size": int64(c.Monitor.EventQueueSize), "event_queue_timeout": int64(c.Monitor.EventQueueTimeout), "database.connect_timeout": int64(c.Database.ConnectTimeout), "database.read_timeout": int64(c.Database.ReadTimeout), "log.max_size": int64(c.Log.MaxSize), "log.max_backups": int64(c.Log.MaxBackups), "log.max_age": int64(c.Log.MaxAge),
	}
	for name, value := range values {
		if value < 0 {
			return fmt.Errorf("%s cannot be negative", name)
		}
	}
	return nil
}
