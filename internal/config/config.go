package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"pikachu/internal/types"
)

// LoadConfig 加载YAML配置文件
func LoadConfig(filename string) (*types.Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config types.Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// 尝试加载tasks配置文件
	tasks, err := LoadTasks("tasks.yaml")
	if err != nil {
		// 如果tasks.yaml不存在，尝试从原配置文件中加载tasks（向后兼容）
		if len(config.Tasks) == 0 {
			return nil, fmt.Errorf("no tasks configured and failed to load tasks.yaml: %w", err)
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

	err = yaml.Unmarshal(data, &tasksConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to parse tasks file: %w", err)
	}

	return tasksConfig.Tasks, nil
}

// ValidateConfig 验证配置文件
func ValidateConfig(config *types.Config) error {
	if len(config.Tasks) == 0 {
		return fmt.Errorf("no tasks configured")
	}

	// 验证数据库配置
	if err := validateDatabaseConfig(&config.Database); err != nil {
		return fmt.Errorf("database config validation failed: %w", err)
	}

	// 验证任务配置
	for i, task := range config.Tasks {
		if err := validateTaskConfig(&task, i); err != nil {
			return err
		}
	}

	// 设置默认值
	setDefaultValues(config)

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
		if !strings.HasPrefix(urlStr, "/") {
			return fmt.Errorf("relative callback URL must start with '/', got: %s", urlStr)
		}
	}

	return nil
}

// setDefaultValues 设置默认值
func setDefaultValues(config *types.Config) {
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
	if config.Dispatcher.MaxRetries <= 0 {
		config.Dispatcher.MaxRetries = 3
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
	if config.Dispatcher.BatchSize <= 0 {
		config.Dispatcher.BatchSize = 1 // 默认不批处理，保持实时性
	}
	if config.Dispatcher.BatchTimeout <= 0 {
		config.Dispatcher.BatchTimeout = 100 * time.Millisecond // 批处理超时
	}

	// 设置监控器默认值
	if config.Monitor.EventQueueSize <= 0 {
		config.Monitor.EventQueueSize = 10000 // 增加事件队列大小
	}
	if config.Monitor.EventQueueTimeout <= 0 {
		config.Monitor.EventQueueTimeout = 2 * time.Second // 减少超时时间以加快响应
	}
	if config.Monitor.BatchSize <= 0 {
		config.Monitor.BatchSize = 1 // 默认不批处理
	}
	if config.Monitor.BatchTimeout <= 0 {
		config.Monitor.BatchTimeout = 50 * time.Millisecond
	}
	if config.Monitor.FlushInterval <= 0 {
		config.Monitor.FlushInterval = 1 * time.Second // 刷新间隔
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

	// 批处理大小验证
	if config.BatchSize < 1 {
		return fmt.Errorf("batch_size (%d) must be at least 1", config.BatchSize)
	}

	if config.BatchSize > 1000 {
		return fmt.Errorf("batch_size (%d) is too large, maximum recommended is 1000", config.BatchSize)
	}

	return nil
}
