package config

import (
	"pikachu/internal/types"
	"strings"
	"testing"
	"time"
)

func validConfig() *types.Config {
	return &types.Config{Database: types.DatabaseConfig{Host: "localhost", Port: 3306, User: "u", Database: "db", ServerID: 1}, Tasks: []types.Task{{TaskID: "one", TableName: "users", Events: []types.EventType{types.EventInsert}, CallbackURL: "http://localhost/webhook"}}}
}
func TestDuplicateTaskIDRejected(t *testing.T) {
	cfg := validConfig()
	cfg.Tasks = append(cfg.Tasks, cfg.Tasks[0])
	cfg.Tasks[1].CallbackURL = "http://localhost/other"
	if err := ValidateConfig(cfg); err == nil || !strings.Contains(err.Error(), "duplicate task_id") {
		t.Fatalf("expected duplicate ID error: %v", err)
	}
	cfg.Tasks[1].TaskID = "two"
	if err := ValidateConfig(cfg); err != nil {
		t.Fatal(err)
	}
}
func TestDeliveryDefaultsAndValidation(t *testing.T) {
	cfg := validConfig()
	if err := ValidateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Dispatcher.ShutdownTimeout != 30*time.Second {
		t.Fatal("missing delivery defaults")
	}
	cfg.Dispatcher.ShutdownTimeout = -time.Second
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("negative shutdown timeout accepted")
	}
}
