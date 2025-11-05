# 第一阶段：编译阶段
FROM golang:latest AS builder

# 设置工作目录
WORKDIR /app
ENV GO111MODULE=on
ENV GOPROXY=https://goproxy.cn,direct
# 复制go.mod和go.sum文件并下载依赖
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY . .

# 编译应用程序
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o pikachu

# 第二阶段：运行阶段
FROM alpine:latest

# 创建日志目录
RUN mkdir -p /app/logs
# 安装证书以支持HTTPS
RUN apk --no-cache add ca-certificates

# 设置工作目录
WORKDIR /app

# 从编译阶段复制二进制文件
COPY --from=builder /app/pikachu .

# 复制配置文件
# COPY config.yaml .
# COPY tasks.yaml .


# 声明卷以持久化日志
VOLUME /app/logs

# 暴露健康检查端口
EXPOSE 8080

# 设置环境变量
ENV CONFIG_PATH=config.yaml

# 运行应用程序
CMD ["./pikachu", "-config", "config.yaml"]
