package main

import (
	"github.com/sirupsen/logrus"
	"os"
)

var Logger *logrus.Logger

func InitLogger() {
	Logger = logrus.New()
	Logger.SetOutput(os.Stdout)
	Logger.SetLevel(logrus.InfoLevel)

	// 设置日志格式
	Logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})
}

func LogTaskStart(taskID, name, table string) {
	Logger.WithFields(logrus.Fields{
		"task_id": taskID,
		"name":    name,
		"table":   table,
	}).Info("Task started")
}

func LogTaskStop(taskID string) {
	Logger.WithFields(logrus.Fields{
		"task_id": taskID,
	}).Info("Task stopped")
}

func LogChangeEvent(event *ChangeEvent) {
	Logger.WithFields(logrus.Fields{
		"task_id":   event.TaskID,
		"event":     event.Event,
		"table":     event.Table,
		"timestamp": event.Timestamp,
	}).Info("Database change captured")
}

func LogWebhookRequest(taskID, url string, statusCode int, payload interface{}) {
	Logger.WithFields(logrus.Fields{
		"task_id":     taskID,
		"url":         url,
		"status_code": statusCode,
		"payload":     payload,
	}).Info("Webhook request sent")
}

func LogWebhookRetry(taskID, url string, retryCount int, err error) {
	Logger.WithFields(logrus.Fields{
		"task_id":     taskID,
		"url":         url,
		"retry_count": retryCount,
		"error":       err.Error(),
	}).Warn("Webhook retry")
}

func LogError(taskID string, err error, context string) {
	Logger.WithFields(logrus.Fields{
		"task_id": taskID,
		"context": context,
		"error":   err.Error(),
	}).Error("Error occurred")
}
