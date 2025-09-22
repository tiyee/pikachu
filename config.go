package main

import (
	"database/sql"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"gopkg.in/yaml.v3"
	"os"
)

// LoadConfig 加载YAML配置文件
func LoadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &config, nil
}

// ValidateConfig 验证配置文件
func ValidateConfig(config *Config) error {
	if len(config.Tasks) == 0 {
		return fmt.Errorf("no tasks configured")
	}

	for _, task := range config.Tasks {
		if task.TaskID == "" {
			return fmt.Errorf("task_id cannot be empty")
		}
		if task.TableName == "" {
			return fmt.Errorf("table_name cannot be empty for task %s", task.TaskID)
		}
		if task.CallbackURL == "" {
			return fmt.Errorf("callback_url cannot be empty for task %s", task.TaskID)
		}
		if len(task.Events) == 0 {
			return fmt.Errorf("events cannot be empty for task %s", task.TaskID)
		}
	}

	return nil
}

// CheckDatabasePermissions 检查数据库权限
func CheckDatabasePermissions(config *DatabaseConfig) error {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s",
		config.User, config.Password, config.Host, config.Port, config.Database)

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
		if contains(grant, "ALL PRIVILEGES") || contains(grant, "GRANT ALL") {
			hasAllPrivileges = true
			break
		}
	}

	if !hasAllPrivileges {
		// 检查是否有必要的权限
		for _, privilege := range requiredPrivileges {
			hasPrivilege := false
			for _, grant := range grants {
				if contains(grant, privilege) {
					hasPrivilege = true
					break
				}
			}
			if !hasPrivilege {
				return fmt.Errorf("missing required privilege: %s", privilege)
			}
		}
	}

	Logger.Info("Database permissions verified successfully")
	return nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > len(substr) &&
			(s[:len(substr)] == substr ||
				s[len(s)-len(substr):] == substr ||
				findInString(s, substr))))
}

func findInString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
