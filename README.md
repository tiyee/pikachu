# Pikachu - MySQL 变更监控工具

Pikachu 是一个基于 Go 语言开发的 MySQL 数据库变更监控工具。它通过解析 MySQL 的 binlog 日志来实时捕获数据库表的变更事件（插入、更新、删除），并将这些变更通过 webhook 的方式发送到指定的回调地址。

## 功能特性

- **实时监控**: 实时监控 MySQL 数据库表的变更事件
- **事件类型支持**: 支持 INSERT、UPDATE、DELETE 事件监控
- **灵活配置**: 通过 YAML 配置文件灵活配置监控任务
- **Webhook 通知**: 将数据库变更事件通过 webhook 发送到指定地址
- **失败重试**: 支持 webhook 失败重试机制
- **优雅关闭**: 支持优雅关闭，确保事件处理完成
- **日志记录**: 完整的日志记录，便于问题排查

## 快速开始

### 配置要求

- Go 1.16+
- MySQL 5.7+

### 安装

```bash
go build -o pikachu main.go
```


### 配置文件

创建 `config.yaml` 配置文件：

```yaml
database:
  host: "localhost"
  port: 3306
  user: "your_username"
  password: "your_password"
  database: "your_database"
  server_id: 100

tasks:
- task_id: "user_monitor"
  name: "用户表变更监控"
  table_name: "users"
  events: ["insert", "update", "delete"]
  callback_url: "https://api.example.com/webhook/user"

- task_id: "order_monitor"
  name: "订单表变更监控"
  table_name: "orders"
  events: ["insert", "update"]
  callback_url: "https://api.example.com/webhook/order"
```


### 运行

```bash
./pikachu -config config.yaml
```


## 配置说明

### 数据库配置

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| host | string | 是 | MySQL 主机地址 |
| port | int | 是 | MySQL 端口 |
| user | string | 是 | MySQL 用户名 |
| password | string | 是 | MySQL 密码 |
| database | string | 是 | 数据库名称 |
| server_id | uint32 | 是 | 用于 binlog 同步的唯一 server ID |

### 任务配置

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| task_id | string | 是 | 任务唯一标识 |
| name | string | 是 | 任务名称 |
| table_name | string | 是 | 要监控的表名 |
| events | []string | 是 | 要监控的事件类型 (insert/update/delete) |
| callback_url | string | 是 | webhook 回调地址 |

## 工作原理

1. **配置加载**: 启动时加载并验证配置文件
2. **权限检查**: 检查数据库连接和必要权限
3. **初始化组件**: 初始化监控器和分发器
4. **事件监听**: 监控器通过 canal 监听 MySQL binlog 事件
5. **事件处理**: 捕获的变更事件通过事件队列传递给分发器
6. **Webhook 发送**: 分发器将事件以 webhook 形式发送到指定地址
7. **失败重试**: 支持失败重试机制
8. **优雅关闭**: 支持优雅关闭，确保事件处理完成

## 权限要求

MySQL 用户需要以下权限：

- SELECT
- RELOAD
- SHOW DATABASES
- REPLICATION SLAVE
- REPLICATION CLIENT

## Webhook 数据格式

发送到回调地址的数据格式如下：

```json
{
  "event": "insert",
  "table": "users",
  "data": {
    "id": 1,
    "name": "John Doe",
    "email": "john@example.com"
  },
  "timestamp": "2023-01-01T12:00:00Z"
}
```


根据不同事件类型，数据格式略有不同：

- **INSERT**: 包含 `data` 字段，表示新插入的数据
- **UPDATE**: 包含 `old_data` 和 `new_data` 字段，分别表示更新前后的数据
- **DELETE**: 包含 `data` 字段，表示被删除的数据

## 日志说明

程序使用结构化日志记录关键操作和错误信息，日志包含时间戳和相关字段信息，便于问题排查和监控。

## 贡献

欢迎提交 Issue 和 Pull Request 来改进 Pikachu。

## 许可证

MIT License