# 固定版本与多架构清单摘要，更新时同时运行 CI。
FROM golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -mod=readonly -trimpath -ldflags="-w -s" -o /pikachu .

FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
RUN apk --no-cache add ca-certificates tzdata \
    && addgroup -g 10001 -S pikachu \
    && adduser -u 10001 -S -G pikachu pikachu \
    && mkdir -p /app/logs \
    && chown -R pikachu:pikachu /app
WORKDIR /app
COPY --from=builder --chown=pikachu:pikachu /pikachu /app/pikachu
USER 10001:10001
VOLUME /app/logs
EXPOSE 8080
ENV CONFIG_PATH=/app/config.yaml TASKS_PATH=/app/tasks.yaml
ENTRYPOINT ["/app/pikachu"]
