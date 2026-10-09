# Pikachu

Go 编写的 MySQL CDC 工具：读取 binlog 行变更，通过固定数量的 worker 将 JSON 发送到 webhook。当前应用版本为 1.1.0，源码构建需要 Go 1.27；CI 使用 Go 1.27.1。

## 投递边界

保持尽力投递，不保存或恢复位点，不做初始全量快照。每次启动从当前 master position 开始，停机及异常退出期间的事件不会补发。没有至少一次或恰好一次保证，接收端需处理丢失、重复与乱序。

Monitor 输出有界队列 → Dispatcher 共享有界队列 → 固定 worker。队列满时等待空位形成背压，`monitor.event_queue_timeout` 只控制拥塞告警间隔。每个 worker 独占一次变更及其 JSON，重试复用当前事件的字节，任务结束即释放；等待重试也占用 worker。

HTTP 2xx 表示成功。所有重定向均不跟随，3xx 和其他失败响应按配置重试，避免 POST 被转换成 GET 或将数据库数据发送到未配置的目标。单次投递耗尽重试后计入失败及丢弃，继续处理其他事件。

收到 SIGINT / SIGTERM 后先停止 Monitor，等待生产者退出，再关闭队列并限时排空 Dispatcher。默认排空期限为 30s，到期取消剩余 HTTP 和重试。该期限只覆盖 Dispatcher；数据库读写现在响应取消，也有独立超时。Compose 的停止宽限期为 45s，调整排空期限时应同步留出组件停止余量。

性能取决于行大小、网络与回调延迟。仓库没有可复用的吞吐量或延迟基准，因此不承诺固定性能指标、成功率或内存占用。

## 快速开始

```sh
cp config-example.yaml config.yaml
cp tasks-example.yaml tasks.yaml
# 编辑数据库、唯一 server_id、任务表名和 callback_host
make build
./pikachu -version
./pikachu -config config.yaml -tasks tasks.yaml
```

配置文件含数据库凭据，`config.yaml` 和 `tasks.yaml` 均不再受 Git 跟踪，也不进入 Docker 构建上下文。取消跟踪保留本地文件，不清除 Git 历史。主配置示例与任务示例现在可以一起通过配置校验，实际运行仍需对应数据库表及回调服务。

路径优先级：命令行 `-config` / `-tasks` → `CONFIG_PATH` / `TASKS_PATH` 环境变量 → 工作目录的 `config.yaml` / `tasks.yaml`。两个相对路径分别基于工作目录，不相互推导。

独立任务文件读取成功后完全覆盖内联 `tasks`。仅文件不存在且内联任务非空时兼容回退；权限错误、语法错误和未知字段都返回错误。空 tasks 路径按默认 `tasks.yaml` 处理。YAML 严格校验字段，只允许一个文档，不支持配置热重载。

## MySQL 要求

- 开启 `log_bin`，使用 `binlog_format=ROW` 和 `binlog_row_image=FULL`。
- 所有写入连接也必须使用 FULL 行镜像，不应在会话中改为 MINIMAL / NOBLOB。启动校验读取全局设置；canal 行事件不提供省略列标识，无法区分省略值与 SQL NULL。
- 配置与其他复制客户端不冲突的 `server_id`。多副本独立消费，会产生重复回调。
- 账号需要任务表的 SELECT，以及 REPLICATION CLIENT、REPLICATION SLAVE 权限。

权限通过实际 SELECT、master status 查询和复制握手验证，支持账号的实际生效权限，不解析 SHOW GRANTS 字符串或推测角色权限。表元数据由 canal 管理，DDL 后按需刷新，没有额外的跨事件 schema 缓存。

复制 Flavor 固定为 mysql，MariaDB 不在当前支持范围。真实 MySQL 版本兼容性应在部署环境验证，协议模拟测试不能代替数据库集成测试。

## 配置

完整主配置见 `config-example.yaml`，环境示例见 `config.prod.yaml`、`config.test.yaml`，任务格式见 `tasks-example.yaml`。

### 数据库

| 配置 | 默认值 / 含义 |
| --- | --- |
| host / port / user / database / server_id | 必填；port 为 1–65535，server_id 非零 |
| password | 数据库密码 |
| charset | utf8mb4 |
| connect_timeout | 10s，连接建立超时 |
| read_timeout | 30s，每次数据库读写等待超时；复制心跳周期为其一半 |

### Dispatcher

| 配置 | 默认值 / 含义 |
| --- | --- |
| worker_count | 20；最大 1000 |
| queue_size | 1000；共享队列总容量为 worker_count × queue_size，单项最大 100000 |
| timeout | 30s，单次 HTTP 请求超时 |
| max_retries | YAML 中省略时为 3，显式 0 禁用重试，负值拒绝；总尝试次数为 1 + max_retries |
| retry_base_delay | 5s；启用重试时至少 1s，首次实际等待 2 倍 base |
| retry_max_delay | 60s，必须严格大于 base |
| shutdown_timeout | 30s；零使用默认值，负值拒绝 |
| max_connections | 100，每个回调主机的连接上限 |
| max_idle_conns | 20，全局空闲连接上限 |
| idle_conn_timeout | 90s |

队列限制按事件数量计，不是按字节计。大行或慢回调会增加内存使用和积压，应按实际负载调节容量及并发。

### Monitor

| 配置 | 默认值 / 含义 |
| --- | --- |
| event_queue_size | 10000 |
| event_queue_timeout | 2s；队列拥塞告警间隔，告警后继续等待 |

批处理没有实现。`dispatcher.batch_size/batch_timeout` 和 `monitor.batch_size/batch_timeout/flush_interval` 已废弃并从示例移除；只接受旧示例默认值作为兼容设置，其他值直接报错，不再静默忽略用户的批处理要求。

### 回调与任务

```yaml
callback_host: "https://api.example.com/base"
tasks:
  - task_id: user_monitor
    name: 用户变更
    table_name: users
    events: [insert, update, delete]
    callback_url: /webhook/users
```

最终地址为 `https://api.example.com/base/webhook/users`。绝对 HTTP(S) 回调直接使用；相对路径必须配置有效的 `callback_host`。主机地址不接受 query、fragment 或 userinfo；`//other-host/path` 形式拒绝。最终 URL 和端口在启动时校验。

`task_id` 必须唯一，不同任务可监控同一张表；重复事件类型自动去重。表名使用原始名称，无需手动添加反引号，内部反引号会转义。

### 日志与健康服务

| 配置 | 默认值 / 含义 |
| --- | --- |
| log.level | info；debug / info / warn / error / fatal / panic |
| log.format | text；text 写 stdout，json 写文件 |
| log.directory | logs，JSON 日志目录，自动创建 |
| log.max_size | 100，单个日志文件最大 MiB |
| log.max_backups | 5，轮转备份数 |
| log.max_age | 7，备份保留天数，备份 gzip 压缩 |
| server.enabled | false；同时控制健康及指标接口 |
| server.port | 启用时为 8080 |
| server.path | /health；不能与 /metrics-json 冲突 |

JSON 业务日志写入 `output.log`，`error.log` 仅记录 zap 内部错误，不是业务 error 分流。目录或文件不可写时返回启动错误。使用目录挂载时，目录必须可被容器 UID/GID 10001 写入。

## Webhook 载荷

```json
{
  "event": "insert",
  "table": "users",
  "primary_id": 1,
  "data": {"id": 1, "name": "Alice"},
  "timestamp": "2026-10-09T12:00:00+08:00"
}
```

INSERT / DELETE 使用 `data`；UPDATE 使用 `old_data`、`new_data`。复合主键的 `primary_id` 为对象；没有 PRIMARY 时尝试 `id`，否则为 null。时间戳是程序处理行事件时的时间，不是事务提交时间。没有事件 ID 或事务聚合载荷。

## 健康与指标

启用 `server.enabled` 后：

- `GET /health`（或配置路径）：`status`、`monitor_running`、`dispatcher_running`、`event_queue_size`、`last_event_time`。组件未运行时返回 503。
- `GET /metrics-json`：上述组件状态及 `task_count`、`events_queued`、`events_succeeded`、`events_failed`、`events_dropped`、`webhook_retries`、`cache_size`。

running 表示本地循环存活，不代表复制连接新鲜度或端到端实时性。单个回调失败不会令健康接口变为 DOWN，应观察失败与积压指标。

所有计数只在当前进程有效。queued 表示进入 worker 队列的累计数；succeeded 为成功数；failed/dropped 为最终失败或取消后放弃数；retries 为实际发起的重试次数。`cache_size` 保留兼容名称，表示 worker 当前持有的独立 JSON 数量，结束后减回。`event_queue_size` 只统计 Monitor 输出队列，不包含 Dispatcher 队列。没有 Prometheus `/metrics`、uptime、version 或位点接口。

## Docker 部署

镜像使用已固定摘要的 Go 1.27.1 Alpine 构建阶段与 Alpine 3.24 运行阶段。运行用户为 UID/GID 10001，包含 CA 证书及 tzdata；`.dockerignore` 使用允许列表排除实际配置、凭据、日志和本地辅助文件。

```sh
# 示例配置只需复制一次，随后填入实际连接及任务设置
cp config-example.yaml config.yaml
cp tasks-example.yaml tasks.yaml
sudo install -d -o 10001 -g 10001 -m 0750 /data/logs/pikachu
# 让容器组只读配置，其他用户不能读取凭据
sudo chgrp 10001 config.yaml tasks.yaml
chmod 0640 config.yaml tasks.yaml
docker compose up -d --build
```

Compose 挂载配置只读及日志目录，不提供位点数据卷。若使用其他运行 UID，需同步宿主机目录及文件权限。命令行参数会覆盖环境变量路径，例如：

```sh
docker run --rm pikachu -version
```

## 检查与发布

```sh
go build ./...
go vet ./...
go test ./...
go test -race ./...
make ci
# 额外静态检查，需要单独安装 golangci-lint
make lint
```

`make ci` 验证格式及模块、构建、vet、race 和覆盖率，不运行 tidy 或改写格式。`cmd/test-runner` 汇总失败后非零退出，无交互询问。测试位于对应包旁；Monitor 使用合成行事件及本地 MySQL 协议服务器，Dispatcher 使用 httptest，不连接业务数据库或外部 webhook。真实 MySQL、容器运行和负载测试需独立验证。

PR / 分支提交运行 `.github/workflows/ci.yml`，构建、vet、race 全部通过后才允许发布 workflow 构建及推送 ACR 镜像。版本输入通过环境变量传入 shell 后校验，镜像创建时间由 UTC 实际时间生成。镜像标签不自动修改二进制版本，版本变更需更新 `internal/utils/utils.go` 的 Version。

项目采用 MIT 许可证，见 LICENSE。
