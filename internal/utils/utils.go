package utils

import (
	"fmt"
	"regexp"
	"strings"
)

// Contains 检查字符串是否包含子字符串，使用标准库实现
func Contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// EnsureQuoted 确保标识符被反引号包围
// 如果已经有反引号就不处理，否则添加反引号
func EnsureQuoted(identifier string) string {
	if identifier == "" {
		return identifier
	}

	// 检查是否已经有反引号包围
	if strings.HasPrefix(identifier, "`") && strings.HasSuffix(identifier, "`") {
		return identifier
	}

	// 添加反引号
	return "`" + identifier + "`"
}

// GetEventTaskId 生成事件任务ID
func GetEventTaskId(tableName, eventType string) string {
	return tableName + "." + eventType
}

// EscapeRegexForTable 对表名进行正则表达式转义
func EscapeRegexForTable(database, table string) string {
	escapedDatabase := regexp.QuoteMeta(database)
	escapedTable := regexp.QuoteMeta(table)
	return escapedDatabase + "\\." + escapedTable
}

// Errorf 创建格式化错误
func Errorf(format string, args ...interface{}) error {
	return fmt.Errorf("ERROR: "+format, args...)
}

// Version 应用版本信息
const Version = "1.0.0"

// GetUserAgent 获取用户代理字符串
func GetUserAgent() string {
	return fmt.Sprintf("pikachu/%s", Version)
}

// BuildCallbackURL 构建完整的回调URL
func BuildCallbackURL(callbackHost, callbackURL string) string {
	// 如果callbackURL已经是绝对URL，直接返回
	if strings.HasPrefix(callbackURL, "http://") || strings.HasPrefix(callbackURL, "https://") {
		return callbackURL
	}

	// 如果是相对路径，与callbackHost组合
	if callbackHost == "" {
		return callbackURL
	}

	// 确保callbackHost以/结尾，callbackURL以/开头
	if !strings.HasSuffix(callbackHost, "/") {
		callbackHost += "/"
	}
	if !strings.HasPrefix(callbackURL, "/") {
		callbackURL = "/" + callbackURL
	}

	return callbackHost + strings.TrimPrefix(callbackURL, "/")
}
