# pikachu - MySQL 变更监控工具

pikachu 是一个基于 Go 语言开发的高效 MySQL 数据库变更捕获(CDC)工具。它通过解析 MySQL 的 binlog 日志来实时捕获数据库表的变更事件（插入、更新、删除），并将这些变更通过 webhook 的方式发送到指定的回调地址。

## 功能特性

- **实时监控**: 实时监控 MySQL 数据库表的变更事件
- **事件类型支持**: 支持 INSERT、UPDATE、DELETE 事件监控
- **灵活配置**: 通过 YAML 配置文件灵活配置监控任务
- **Webhook 通知**: 将数据库变更事件通过 webhook 发送到指定地址
- **失败重试**: 支持 webhook 失败重试机制，默认重试3次
- **并发处理**: 基于工作协程池的并发 webhook 分发
- **健康检查**: 提供 HTTP 健康检查和监控指标端点
- **优雅关闭**: 支持优雅关闭，确保事件处理完成
- **结构化日志**: 支持可配置的结构化日志记录
- **Docker 部署**: 支持 Docker 和 Docker Compose 部署

## 快速开始

### 配置要求

- Go 1.25+ (如果直接从源码编译)
- MySQL 5.6+ 或 MariaDB 10.0+（需要开启二进制日志）
- Docker 和 Docker Compose（如果使用容器部署）

### 直接运行

#### 安装

```bash
go build -o pikachu .
```

#### 配置文件

创建或修改 `config.yaml` 配置文件：

```yaml
database:
  host: "localhost"
  port: 3306
  user: "root"
  password: "password"
  database: "test_db"
  server_id: 100

log:
  level: "info"  # debug, info, warn, error, fatal, panic
  format: "text" # text, json

server:
  enabled: true  # 是否启用健康检查服务器
  port: 8080     # 服务器端口
  path: "/health" # 健康检查路径

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

#### 运行

```bash
./pikachu -config config.yaml
```

### 使用 Docker 运行

#### 准备配置文件

确保 `config.yaml` 文件已正确配置（特别是数据库连接信息）。

#### 启动服务

```bash
docker-compose up -d
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

### 日志配置

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| level | string | 否 | 日志级别：debug, info, warn, error, fatal, panic (默认: info) |
| format | string | 否 | 日志格式：text, json (默认: text) |

### 服务器配置

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| enabled | bool | 否 | 是否启用健康检查服务器 (默认: false) |
| port | int | 否 | 服务器端口 (默认: 8080) |
| path | string | 否 | 健康检查路径 (默认: /health) |

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
3. **初始化组件**: 初始化监控器、分发器和事件队列
4. **事件监听**: 监控器通过 canal 监听 MySQL binlog 事件
5. **事件处理**: 捕获的变更事件通过事件队列传递给分发器
6. **Webhook 发送**: 分发器将事件以 webhook 形式发送到指定地址
7. **健康检查**: 提供 HTTP 健康检查和系统状态监控
8. **优雅关闭**: 支持优雅关闭，确保事件处理完成

## 权限要求

MySQL 用户需要以下权限：
- SELECT - 用于查询表结构
- REPLICATION SLAVE - 用于读取二进制日志
- REPLICATION CLIENT - 用于获取复制状态信息

## MySQL 配置要求

确保 MySQL 服务器已正确配置：
- 开启二进制日志：`log_bin=ON`
- 设置二进制日志格式为 ROW：`binlog_format=ROW`
- 确保 `server_id` 已设置（全局唯一）

## Webhook 数据格式

发送到回调地址的数据格式如下：

```json
{
  "primary_id": 1,
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

## 健康检查与监控

pikachu 提供了 HTTP 健康检查和监控指标端点：

- **健康检查端点**: `http://<host>:<port>/health` - 返回系统健康状态
- **监控指标端点**: `http://<host>:<port>/metrics` - 返回系统运行指标

健康检查响应示例：
```json
{
  "status": "UP",
  "monitor_running": true,
  "dispatcher_running": true,
  "event_queue_size": 0,
  "last_event_time": "2023-05-15T10:30:45Z"
}
```

## 日志说明

程序使用结构化日志记录关键操作和错误信息：

- 支持多种日志级别，可根据需要调整详细程度
- 支持文本和 JSON 两种日志格式
- 日志记录包含时间戳、日志级别、消息和相关字段信息
- 使用 Docker 部署时，日志默认存储在宿主机的 `/data/logs/pikachu` 目录

## Docker 部署说明

### Dockerfile 特性

- **多阶段构建**: 使用 Go 1.25-alpine 作为编译阶段，alpine:3.19 作为运行阶段
- **非 root 用户**: 使用 appuser 用户运行应用，提高安全性
- **证书支持**: 预装 ca-certificates 以支持 HTTPS 连接
- **日志持久化**: 配置日志目录卷挂载，确保日志持久化

### docker-compose.yml 说明

- **服务配置**: 仅包含 pikachu 应用服务（MySQL 由外部提供）
- **端口映射**: 映射健康检查端口 8080
- **卷挂载**: 日志目录和配置文件的持久化挂载
- **网络配置**: 使用自定义网络隔离服务通信

## 常见问题与排查

### 连接 MySQL 失败
- 检查数据库连接配置是否正确
- 验证 MySQL 用户权限是否满足要求
- 确认 MySQL 服务器是否开启了二进制日志
- 检查 MySQL 服务器网络连接是否正常

### 事件未触发
- 检查监控的表名是否正确
- 确认配置的事件类型（insert/update/delete）是否正确
- 验证 MySQL 二进制日志格式是否为 ROW
- 检查是否有数据变更发生

### Webhook 回调失败
- 检查回调 URL 是否可访问
- 查看日志中的错误信息
- 确认网络连接和防火墙设置
- 检查回调服务是否正常运行

## 贡献

欢迎提交 Issue 和 Pull Request 来改进 pikachu。

## 许可证

MIT License